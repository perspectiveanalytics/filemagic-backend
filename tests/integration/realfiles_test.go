//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

func loadTestFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Skipf("test file %s not found: %v", name, err)
	}
	return data
}

func submitFile(t *testing.T, serverURL, endpoint, filename string, fileData []byte, options map[string]any) response.SubmitResponse {
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

func waitDone(t *testing.T, serverURL, jobID string) response.StatusResponse {
	t.Helper()
	timeout := 30 * time.Second
	if isRemoteMode() {
		timeout = 120 * time.Second
	}
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
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("timeout waiting for job")
	return response.StatusResponse{}
}

func downloadResult(t *testing.T, serverURL, downloadURL string) []byte {
	t.Helper()
	resp, err := http.Get(serverURL + downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("download returned %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	return data
}

// Image format conversion with real files

func TestRealHEICtoJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("HEIC->JPG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 3 || result[0] != 0xFF || result[1] != 0xD8 || result[2] != 0xFF {
		t.Fatal("output is not JPEG")
	}

	// Decode and check it's a real image
	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output JPEG: %v", err)
	}
	b := img.Bounds()
	t.Logf("HEIC->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

func TestRealHEICtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("HEIC->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("HEIC->PNG: %d bytes -> %d bytes", len(data), len(result))
}

func TestRealWebPtoJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.webp")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.webp", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("WebP->JPG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	t.Logf("WebP->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

func TestRealWebPtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.webp")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.webp", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("WebP->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("WebP->PNG: %d bytes -> %d bytes", len(data), len(result))
}

func TestRealBMPtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.bmp")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.bmp", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("BMP->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("BMP->PNG: %d bytes -> %d bytes", len(data), len(result))
}

func TestRealPNGtoJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "image.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PNG->JPG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	t.Logf("PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

func TestRealJPGtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("JPG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("JPG->PNG: %d bytes -> %d bytes", len(data), len(result))
}

// PDF compression with real file

func TestRealPDFCompressAllLevels(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.pdf")

	for _, level := range []string{"low", "medium", "high"} {
		t.Run(level, func(t *testing.T) {
			sub := submitFile(t, server.URL, "/api/convert/pdf/compress", "document.pdf", data, map[string]any{
				"level": level,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("PDF compress (%s) failed: %s", level, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if len(result) < 4 || string(result[:4]) != "%PDF" {
				t.Fatal("output is not PDF")
			}
			t.Logf("PDF compress (%s): %d bytes -> %d bytes (%.0f%%)",
				level, len(data), len(result),
				float64(len(result))/float64(len(data))*100)
		})
	}
}

// Image compression with real files

func TestRealJPGCompress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.jpg")

	for _, quality := range []int{20, 50, 80} {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			sub := submitFile(t, server.URL, "/api/convert/image/compress", "photo.jpg", data, map[string]any{
				"quality": quality,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("JPG compress (q=%d) failed: %s", quality, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if result[0] != 0xFF || result[1] != 0xD8 {
				t.Fatal("output is not JPEG")
			}

			img, _, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode output: %v", err)
			}
			b := img.Bounds()
			t.Logf("JPG compress (q=%d): %dx%d, %d bytes -> %d bytes (%.0f%%)",
				quality, b.Dx(), b.Dy(), len(data), len(result),
				float64(len(result))/float64(len(data))*100)
		})
	}
}

func TestRealPNGCompress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "image.png", data, map[string]any{
		"quality": 60,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PNG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("PNG compress (q=60): %d bytes -> %d bytes", len(data), len(result))
}

func TestRealJPGCompressAndResize(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "photo.jpg", data, map[string]any{
		"quality":  75,
		"maxWidth": 800,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("JPG compress+resize failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 800 {
		t.Fatalf("expected width <= 800, got %d", b.Dx())
	}
	t.Logf("JPG resize to 800w: %d bytes -> %d bytes, %dx%d", len(data), len(result), b.Dx(), b.Dy())
}

func TestRealJPGCompressAndResizeBoth(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "photo.jpg", data, map[string]any{
		"quality":   80,
		"maxWidth":  640,
		"maxHeight": 480,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("JPG resize both failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 640 || b.Dy() > 480 {
		t.Fatalf("expected <= 640x480, got %dx%d", b.Dx(), b.Dy())
	}
	t.Logf("JPG resize 640x480: %d bytes -> %d bytes, %dx%d", len(data), len(result), b.Dx(), b.Dy())
}
