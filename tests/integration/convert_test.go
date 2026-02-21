//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/api"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/converter"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
)

// isRemoteMode returns true when tests target a deployed server via SERVER_URL.
func isRemoteMode() bool {
	return os.Getenv("SERVER_URL") != ""
}

func setupTestServer(t *testing.T) (*httptest.Server, *queue.Queue, string) {
	t.Helper()

	// Remote mode: target an already-running server.
	if serverURL := os.Getenv("SERVER_URL"); serverURL != "" {
		stub := &httptest.Server{URL: strings.TrimRight(serverURL, "/")}
		return stub, nil, ""
	}

	// Local mode: spin up an in-process test server.
	tmpDir := t.TempDir()

	cfg := &config.Config{
		ListenAddr:      ":0",
		TmpfsPath:       tmpDir,
		MaxFileSize:     20 * 1024 * 1024,
		MaxQueueSize:    10,
		JobTimeout:      30 * time.Second,
		CleanupInterval: 60 * time.Second,
		CleanupMaxAge:   600 * time.Second,
		NsjailPath:      "/usr/bin/nsjail",
		NsjailConfigDir: "../../configs/nsjail",
		RateLimitRPM:    100,
		RateLimitRPH:    1000,
	}

	registry, err := converter.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("failed to create converter registry: %v", err)
	}
	processFunc := func(ctx context.Context, job *queue.Job) error {
		return registry.Process(ctx, job)
	}

	q := queue.New(cfg.MaxQueueSize, processFunc)
	ctx := context.Background()
	q.Start(ctx)

	router := api.NewRouter(ctx, cfg, q, stats.New("", "", ""))
	server := httptest.NewServer(router)

	t.Cleanup(func() {
		server.Close()
	})

	return server, q, tmpDir
}

func createTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func createTestJPG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func createTestPDF(t *testing.T) []byte {
	t.Helper()
	pdf := `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>
endobj
4 0 obj
<< /Length 44 >>
stream
BT /F1 24 Tf 100 700 Td (Hello World) Tj ET
endstream
endobj
5 0 obj
<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>
endobj
xref
0 6
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
0000000266 00000 n
0000000360 00000 n
trailer
<< /Size 6 /Root 1 0 R >>
startxref
441
%%EOF`
	return []byte(pdf)
}

func submitConversion(t *testing.T, serverURL, endpoint, filename string, fileData []byte, options map[string]any) response.SubmitResponse {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(fileData)

	if options != nil {
		optJSON, _ := json.Marshal(options)
		writer.WriteField("options", string(optJSON))
	}

	writer.Close()

	resp, err := http.Post(serverURL+endpoint, writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func waitForJob(t *testing.T, serverURL, jobID string, timeout time.Duration) response.StatusResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("%s/api/queue/position/%s", serverURL, jobID))
		if err != nil {
			t.Fatal(err)
		}

		var status response.StatusResponse
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		if status.Status == "done" || status.Status == "error" {
			return status
		}

		time.Sleep(200 * time.Millisecond)
	}

	t.Fatal("job timed out")
	return response.StatusResponse{}
}

func TestHealthCheck(t *testing.T) {
	server, _, _ := setupTestServer(t)

	resp, err := http.Get(server.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var health response.HealthResponse
	json.NewDecoder(resp.Body).Decode(&health)
	if health.Status != "ok" {
		t.Fatalf("expected status ok, got %s", health.Status)
	}
}

func TestImageConvertPNGtoJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)

	pngData := createTestPNG(t)
	submit := submitConversion(t, server.URL, "/api/convert/image", "test.png", pngData, map[string]any{
		"outputFormat": "jpg",
	})

	if submit.JobID == "" {
		t.Fatal("empty job ID")
	}

	status := waitForJob(t, server.URL, submit.JobID, 10*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}
	if status.OutputSize <= 0 {
		t.Fatal("output size should be > 0")
	}

	// Download
	resp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("download failed: %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Fatal("output is not a valid JPEG")
	}

	// One-time download: second request should fail
	resp2, _ := http.Get(server.URL + status.DownloadURL)
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("second download should not succeed")
	}
}

func TestImageConvertJPGtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)

	jpgData := createTestJPG(t)
	submit := submitConversion(t, server.URL, "/api/convert/image", "test.jpg", jpgData, map[string]any{
		"outputFormat": "png",
	})

	status := waitForJob(t, server.URL, submit.JobID, 10*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	// PNG magic bytes
	if len(data) < 8 || data[0] != 0x89 || data[1] != 0x50 {
		t.Fatal("output is not a valid PNG")
	}
}

func TestPDFCompress(t *testing.T) {
	server, _, _ := setupTestServer(t)

	pdfData := createTestPDF(t)
	submit := submitConversion(t, server.URL, "/api/convert/pdf/compress", "test.pdf", pdfData, map[string]any{
		"level": "medium",
	})

	status := waitForJob(t, server.URL, submit.JobID, 15*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if len(data) < 4 || string(data[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
}

func TestImageCompress(t *testing.T) {
	server, _, _ := setupTestServer(t)

	pngData := createTestPNG(t)
	submit := submitConversion(t, server.URL, "/api/convert/image/compress", "test.png", pngData, map[string]any{
		"quality": 50,
	})

	status := waitForJob(t, server.URL, submit.JobID, 10*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if len(data) < 8 || data[0] != 0x89 || data[1] != 0x50 {
		t.Fatal("output is not a valid PNG")
	}
}

func TestRejectInvalidFileType(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Send a text file to image convert
	textData := []byte("this is not an image")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(textData)
	writer.WriteField("options", `{"outputFormat":"jpg"}`)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	var errResp response.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Code != response.CodeValidationError {
		t.Fatalf("expected VALIDATION_ERROR, got %s", errResp.Code)
	}
}

func TestRejectOversizedFile(t *testing.T) {
	if isRemoteMode() {
		t.Skip("uses custom MaxFileSize config — not applicable in remote mode")
	}
	tmpDir := t.TempDir()

	cfg := &config.Config{
		TmpfsPath:       tmpDir,
		MaxFileSize:     10, // 10 bytes max — any real image will exceed this
		MaxQueueSize:    10,
		JobTimeout:      30 * time.Second,
		NsjailPath:      "/usr/bin/nsjail",
		NsjailConfigDir: "../../configs/nsjail",
		RateLimitRPM:    100,
		RateLimitRPH:    1000,
	}

	registry, err := converter.NewRegistry(cfg)
	if err != nil {
		t.Fatalf("failed to create converter registry: %v", err)
	}
	q := queue.New(cfg.MaxQueueSize, func(ctx context.Context, job *queue.Job) error {
		return registry.Process(ctx, job)
	})
	bgCtx := context.Background()
	q.Start(bgCtx)

	router := api.NewRouter(bgCtx, cfg, q, stats.New("", "", ""))
	server := httptest.NewServer(router)
	defer server.Close()

	bigData := createTestPNG(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "big.png")
	part.Write(bigData)
	writer.WriteField("options", `{"outputFormat":"jpg"}`)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should reject - either FILE_TOO_LARGE or a 400
	if resp.StatusCode == http.StatusAccepted {
		t.Fatal("should have rejected oversized file")
	}
}

func TestQueueStatus(t *testing.T) {
	server, _, _ := setupTestServer(t)

	resp, err := http.Get(server.URL + "/api/queue/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status map[string]int
	json.NewDecoder(resp.Body).Decode(&status)
	if _, ok := status["queueLength"]; !ok {
		t.Fatal("missing queueLength field")
	}
}

func TestJobNotFound(t *testing.T) {
	server, _, _ := setupTestServer(t)

	resp, err := http.Get(server.URL + "/api/queue/position/nonexistent-id")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMissingFile(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("options", `{"outputFormat":"jpg"}`)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPDFCompressionLevels(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := createTestPDF(t)

	for _, level := range []string{"low", "medium", "high"} {
		t.Run(level, func(t *testing.T) {
			submit := submitConversion(t, server.URL, "/api/convert/pdf/compress", "test.pdf", pdfData, map[string]any{
				"level": level,
			})

			status := waitForJob(t, server.URL, submit.JobID, 15*time.Second)
			if status.Status != "done" {
				t.Fatalf("level %s: expected done, got %s: %s", level, status.Status, status.Error)
			}

			resp, _ := http.Get(server.URL + status.DownloadURL)
			resp.Body.Close()
		})
	}
}

func TestImageCompressWithResize(t *testing.T) {
	server, _, _ := setupTestServer(t)

	pngData := createTestPNG(t)
	submit := submitConversion(t, server.URL, "/api/convert/image/compress", "test.png", pngData, map[string]any{
		"quality":  80,
		"maxWidth": 5,
	})

	status := waitForJob(t, server.URL, submit.JobID, 10*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	// Decode and check dimensions
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	if bounds.Dx() > 5 {
		t.Fatalf("expected width <= 5, got %d", bounds.Dx())
	}
}

func TestCleanupAfterDownload(t *testing.T) {
	if isRemoteMode() {
		t.Skip("inspects local tmpDir — not applicable in remote mode")
	}
	server, _, tmpDir := setupTestServer(t)

	pngData := createTestPNG(t)
	submit := submitConversion(t, server.URL, "/api/convert/image", "test.png", pngData, map[string]any{
		"outputFormat": "jpg",
	})

	status := waitForJob(t, server.URL, submit.JobID, 10*time.Second)
	if status.Status != "done" {
		t.Fatal("job did not complete")
	}

	// Download (triggers cleanup)
	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Give cleanup goroutine a moment
	time.Sleep(200 * time.Millisecond)

	// Job directory should be gone
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.Name() == submit.JobID {
			t.Fatalf("job directory %s still exists after download", submit.JobID)
		}
	}
}
