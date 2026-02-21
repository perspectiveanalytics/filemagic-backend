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
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// heicFiles are real HEIC images downloaded from the internet.
// - real_photo.heic: 640x426, from filesamples.com
// - real_photo2.heic: from github.com/tigranbs/test-heic-images (iPhone photo)
// - sample.heic: original test fixture
var heicFiles = []string{"real_photo.heic", "real_photo2.heic", "sample.heic"}

// TestHEICtoJPG_AllFiles converts each real HEIC image to JPG and validates
// magic bytes, image decode, and non-zero dimensions.
func TestHEICtoJPG_AllFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)

	for _, file := range heicFiles {
		t.Run(file, func(t *testing.T) {
			data := loadTestFile(t, file)

			sub := submitFile(t, server.URL, "/api/convert/image", file, data, map[string]any{
				"outputFormat": "jpg",
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("HEIC->JPG failed for %s: %s", file, status.Error)
			}
			if status.OutputSize <= 0 {
				t.Fatal("output size should be > 0")
			}

			result := downloadResult(t, server.URL, status.DownloadURL)

			if len(result) < 3 || result[0] != 0xFF || result[1] != 0xD8 || result[2] != 0xFF {
				t.Fatal("output is not a valid JPEG")
			}

			img, format, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode output JPEG: %v", err)
			}
			if format != "jpeg" {
				t.Fatalf("expected format jpeg, got %s", format)
			}
			b := img.Bounds()
			if b.Dx() == 0 || b.Dy() == 0 {
				t.Fatal("output image has zero dimensions")
			}
			t.Logf("HEIC->JPG (%s): %dx%d, %d bytes -> %d bytes", file, b.Dx(), b.Dy(), len(data), len(result))
		})
	}
}

// TestHEICtoPNG_AllFiles converts each real HEIC image to PNG and validates
// magic bytes, image decode, and non-zero dimensions.
func TestHEICtoPNG_AllFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)

	for _, file := range heicFiles {
		t.Run(file, func(t *testing.T) {
			data := loadTestFile(t, file)

			sub := submitFile(t, server.URL, "/api/convert/image", file, data, map[string]any{
				"outputFormat": "png",
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("HEIC->PNG failed for %s: %s", file, status.Error)
			}
			if status.OutputSize <= 0 {
				t.Fatal("output size should be > 0")
			}

			result := downloadResult(t, server.URL, status.DownloadURL)

			if len(result) < 8 || result[0] != 0x89 || result[1] != 0x50 || result[2] != 0x4E || result[3] != 0x47 {
				t.Fatal("output is not a valid PNG")
			}

			img, format, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode output PNG: %v", err)
			}
			if format != "png" {
				t.Fatalf("expected format png, got %s", format)
			}
			b := img.Bounds()
			if b.Dx() == 0 || b.Dy() == 0 {
				t.Fatal("output image has zero dimensions")
			}
			t.Logf("HEIC->PNG (%s): %dx%d, %d bytes -> %d bytes", file, b.Dx(), b.Dy(), len(data), len(result))
		})
	}
}

// TestHEIC_DimensionsConsistent verifies that converting the same HEIC to JPG
// and PNG produces images with identical dimensions.
func TestHEIC_DimensionsConsistent(t *testing.T) {
	server, _, _ := setupTestServer(t)

	for _, file := range heicFiles {
		t.Run(file, func(t *testing.T) {
			data := loadTestFile(t, file)

			// Convert to JPG
			subJPG := submitFile(t, server.URL, "/api/convert/image", file, data, map[string]any{
				"outputFormat": "jpg",
			})
			statusJPG := waitDone(t, server.URL, subJPG.JobID)
			if statusJPG.Status != "done" {
				t.Fatalf("HEIC->JPG failed: %s", statusJPG.Error)
			}
			resultJPG := downloadResult(t, server.URL, statusJPG.DownloadURL)
			imgJPG, _, err := image.Decode(bytes.NewReader(resultJPG))
			if err != nil {
				t.Fatalf("cannot decode JPG: %v", err)
			}

			// Convert to PNG
			subPNG := submitFile(t, server.URL, "/api/convert/image", file, data, map[string]any{
				"outputFormat": "png",
			})
			statusPNG := waitDone(t, server.URL, subPNG.JobID)
			if statusPNG.Status != "done" {
				t.Fatalf("HEIC->PNG failed: %s", statusPNG.Error)
			}
			resultPNG := downloadResult(t, server.URL, statusPNG.DownloadURL)
			imgPNG, _, err := image.Decode(bytes.NewReader(resultPNG))
			if err != nil {
				t.Fatalf("cannot decode PNG: %v", err)
			}

			bJPG := imgJPG.Bounds()
			bPNG := imgPNG.Bounds()
			if bJPG.Dx() != bPNG.Dx() || bJPG.Dy() != bPNG.Dy() {
				t.Fatalf("dimension mismatch: JPG=%dx%d, PNG=%dx%d", bJPG.Dx(), bJPG.Dy(), bPNG.Dx(), bPNG.Dy())
			}
			t.Logf("HEIC dimensions consistent (%s): %dx%d", file, bJPG.Dx(), bJPG.Dy())
		})
	}
}

// TestHEICtoJPG_DownloadFilename verifies that the Content-Disposition header
// uses the original base name with .jpg extension.
func TestHEICtoJPG_DownloadFilename(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "vacation.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	resp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	cd := resp.Header.Get("Content-Disposition")
	if cd == "" {
		t.Fatal("missing Content-Disposition header")
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("invalid Content-Disposition header %q: %v", cd, err)
	}
	if params["filename"] != "vacation.jpg" {
		t.Fatalf("expected filename=vacation.jpg, got %q", params["filename"])
	}
}

// TestHEICtoPNG_DownloadFilename verifies the .png extension in the download.
func TestHEICtoPNG_DownloadFilename(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "vacation.heic", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	resp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	cd := resp.Header.Get("Content-Disposition")
	if cd == "" {
		t.Fatal("missing Content-Disposition header")
	}
	_, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("invalid Content-Disposition header %q: %v", cd, err)
	}
	if params["filename"] != "vacation.png" {
		t.Fatalf("expected filename=vacation.png, got %q", params["filename"])
	}
}

// TestHEIC_OneTimeDownload verifies that a converted HEIC file can only be
// downloaded once.
func TestHEIC_OneTimeDownload(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	// First download should succeed
	resp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first download failed: %d", resp.StatusCode)
	}

	time.Sleep(200 * time.Millisecond)

	// Second download should fail
	resp2, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("second download should not succeed")
	}
}

// TestHEIC_CleanupAfterDownload verifies that the job directory is removed
// after the converted file is downloaded.
func TestHEIC_CleanupAfterDownload(t *testing.T) {
	if isRemoteMode() {
		t.Skip("inspects local tmpDir — not applicable in remote mode")
	}
	server, _, tmpDir := setupTestServer(t)
	data := loadTestFile(t, "real_photo2.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	time.Sleep(200 * time.Millisecond)

	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.Name() == sub.JobID {
			t.Fatalf("job directory %s still exists after download", sub.JobID)
		}
	}
}

// TestHEIC_RejectForImageCompress verifies that HEIC files are rejected
// by the image compression endpoint (which only accepts JPEG and PNG).
func TestHEIC_RejectForImageCompress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "photo.heic")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	optJSON, _ := json.Marshal(map[string]any{"quality": 80})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image/compress", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for HEIC on compress endpoint, got %d", resp.StatusCode)
	}

	var errResp response.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&errResp)
	if errResp.Code != response.CodeValidationError {
		t.Fatalf("expected VALIDATION_ERROR, got %s", errResp.Code)
	}
}

// TestHEIC_InvalidOutputFormat verifies that requesting an unsupported
// output format returns an error.
func TestHEIC_InvalidOutputFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "gif",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "error" {
		t.Fatalf("expected error for unsupported format, got %s", status.Status)
	}
}

// TestHEIF_Extension verifies that .heif files (alternate extension) are
// accepted — detection is by magic bytes, not filename.
func TestHEIF_Extension(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heif", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("HEIF->JPG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 3 || result[0] != 0xFF || result[1] != 0xD8 || result[2] != 0xFF {
		t.Fatal("output is not a valid JPEG")
	}
	t.Logf("HEIF->JPG: %d bytes -> %d bytes", len(data), len(result))
}

// TestRealImages_CrossFormat tests converting the real downloaded PNG and JPG
// images through the conversion pipeline alongside the HEIC tests.
func TestRealImages_CrossFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []struct {
		name         string
		inputFile    string
		outputFormat string
		checkMagic   func([]byte) bool
	}{
		{"real_png_to_jpg", "real_photo.png", "jpg", func(d []byte) bool {
			return len(d) >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF
		}},
		{"real_jpg_to_png", "real_photo.jpg", "png", func(d []byte) bool {
			return len(d) >= 4 && d[0] == 0x89 && d[1] == 0x50 && d[2] == 0x4E && d[3] == 0x47
		}},
		{"real_heic_to_jpg", "real_photo.heic", "jpg", func(d []byte) bool {
			return len(d) >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF
		}},
		{"real_heic_to_png", "real_photo.heic", "png", func(d []byte) bool {
			return len(d) >= 4 && d[0] == 0x89 && d[1] == 0x50 && d[2] == 0x4E && d[3] == 0x47
		}},
		{"real_heic2_to_jpg", "real_photo2.heic", "jpg", func(d []byte) bool {
			return len(d) >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF
		}},
		{"real_heic2_to_png", "real_photo2.heic", "png", func(d []byte) bool {
			return len(d) >= 4 && d[0] == 0x89 && d[1] == 0x50 && d[2] == 0x4E && d[3] == 0x47
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := loadTestFile(t, tc.inputFile)

			sub := submitFile(t, server.URL, "/api/convert/image", tc.inputFile, data, map[string]any{
				"outputFormat": tc.outputFormat,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("%s failed: %s", tc.name, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if !tc.checkMagic(result) {
				t.Fatalf("output has wrong format (expected %s)", tc.outputFormat)
			}

			img, _, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode output: %v", err)
			}
			b := img.Bounds()
			if b.Dx() == 0 || b.Dy() == 0 {
				t.Fatal("output image has zero dimensions")
			}
			t.Logf("%s: %dx%d, %d bytes -> %d bytes (%.0f%%)",
				tc.name, b.Dx(), b.Dy(), len(data), len(result),
				float64(len(result))/float64(len(data))*100)
		})
	}
}

// TestRealImages_Compress tests compressing the real downloaded JPG and PNG.
func TestRealImages_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []struct {
		name    string
		file    string
		quality int
	}{
		{"real_jpg_q30", "real_photo.jpg", 30},
		{"real_jpg_q60", "real_photo.jpg", 60},
		{"real_jpg_q90", "real_photo.jpg", 90},
		{"real_png_q50", "real_photo.png", 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := loadTestFile(t, tc.file)

			sub := submitFile(t, server.URL, "/api/convert/image/compress", tc.file, data, map[string]any{
				"quality": tc.quality,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("%s failed: %s", tc.name, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			img, _, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode output: %v", err)
			}
			b := img.Bounds()
			t.Logf("%s: %dx%d, %d bytes -> %d bytes (%.0f%%)",
				tc.name, b.Dx(), b.Dy(), len(data), len(result),
				float64(len(result))/float64(len(data))*100)
		})
	}
}

// TestRealJPG_CompressAndResize tests compressing and resizing the real JPG.
func TestRealJPG_CompressAndResize(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "real_photo.jpg", data, map[string]any{
		"quality":   75,
		"maxWidth":  320,
		"maxHeight": 240,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("compress+resize failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 320 || b.Dy() > 240 {
		t.Fatalf("expected <= 320x240, got %dx%d", b.Dx(), b.Dy())
	}
	t.Logf("real JPG resize 320x240: %d bytes -> %d bytes, %dx%d",
		len(data), len(result), b.Dx(), b.Dy())
}

// TestRealPNG_CompressAndResize tests compressing and resizing the real PNG.
func TestRealPNG_CompressAndResize(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "real_photo.png", data, map[string]any{
		"quality":  60,
		"maxWidth": 200,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("compress+resize failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 200 {
		t.Fatalf("expected width <= 200, got %d", b.Dx())
	}
	t.Logf("real PNG resize 200w: %d bytes -> %d bytes, %dx%d",
		len(data), len(result), b.Dx(), b.Dy())
}

// TestHEIC_StatusReportsInputSize verifies that the job status response
// includes the correct input file size.
func TestHEIC_StatusReportsInputSize(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	if status.InputSize != int64(len(data)) {
		t.Fatalf("expected input size %d, got %d", len(data), status.InputSize)
	}
	if status.OutputSize <= 0 {
		t.Fatal("output size should be > 0")
	}

	// Download to clean up
	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	t.Logf("input=%d bytes, output=%d bytes, ratio=%.0f%%",
		status.InputSize, status.OutputSize,
		float64(status.OutputSize)/float64(status.InputSize)*100)
}

// TestHEIC_QueuePosition verifies that a submitted HEIC job reports
// a valid queue position.
func TestHEIC_QueuePosition(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "real_photo.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", data, map[string]any{
		"outputFormat": "jpg",
	})
	if sub.JobID == "" {
		t.Fatal("empty job ID")
	}
	if sub.Position < 1 {
		t.Fatalf("expected position >= 1, got %d", sub.Position)
	}

	// Check initial status
	resp, err := http.Get(fmt.Sprintf("%s/api/queue/position/%s", server.URL, sub.JobID))
	if err != nil {
		t.Fatal(err)
	}
	var status response.StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()

	if status.JobID != sub.JobID {
		t.Fatalf("expected jobId %s, got %s", sub.JobID, status.JobID)
	}

	// Wait for completion and clean up
	finalStatus := waitDone(t, server.URL, sub.JobID)
	if finalStatus.Status == "done" {
		r, _ := http.Get(server.URL + finalStatus.DownloadURL)
		io.ReadAll(r.Body)
		r.Body.Close()
	}
}
