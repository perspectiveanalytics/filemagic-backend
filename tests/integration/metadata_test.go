//go:build integration

package integration

import (
	"bytes"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// exifTagCount runs exiftool and returns the number of EXIF/GPS/XMP tags found.
// Returns 0 if exiftool is not installed (skips test).
func exifTagCount(t *testing.T, data []byte, pattern string) int {
	t.Helper()

	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool not installed")
	}

	tmpFile, err := os.CreateTemp("", "exifcheck-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	out, _ := exec.Command("exiftool", tmpFile.Name()).CombinedOutput()
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(pattern)) {
			count++
		}
	}
	return count
}

// hasExifTags checks whether data contains any non-file tags via exiftool.
func hasExifTags(t *testing.T, data []byte) bool {
	t.Helper()

	if _, err := exec.LookPath("exiftool"); err != nil {
		t.Skip("exiftool not installed")
	}

	tmpFile, err := os.CreateTemp("", "exifcheck-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	out, _ := exec.Command("exiftool", "-s", "-G", tmpFile.Name()).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip file-level and exiftool-internal tags (always present regardless of metadata)
		if strings.HasPrefix(line, "[File]") || strings.HasPrefix(line, "[Composite]") || strings.HasPrefix(line, "[ExifTool]") {
			continue
		}
		// Any remaining tag means real metadata exists
		if strings.Contains(line, ":") {
			return true
		}
	}
	return false
}

// TestMetadataRemove_JPEG_GPS tests that GPS metadata is stripped from a real JPEG.
// Uses exif_gps.jpg from github.com/ianare/exif-samples (Nikon COOLPIX P6000
// with GPS coordinates: 43°28'2.81"N, 11°53'6.46"E).
func TestMetadataRemove_JPEG_GPS(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	// Verify input has GPS tags
	gpsBefore := exifTagCount(t, data, "GPS")
	if gpsBefore == 0 {
		t.Fatal("test file should have GPS tags before processing")
	}
	t.Logf("GPS tags before: %d", gpsBefore)

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)

	// Output should still be a valid JPEG
	if len(result) < 3 || result[0] != 0xFF || result[1] != 0xD8 || result[2] != 0xFF {
		t.Fatal("output is not a valid JPEG")
	}

	// GPS tags should be gone
	gpsAfter := exifTagCount(t, result, "GPS")
	if gpsAfter > 0 {
		t.Fatalf("GPS tags remaining after removal: %d", gpsAfter)
	}

	// Camera make/model should also be stripped
	makeAfter := exifTagCount(t, result, "Make")
	if makeAfter > 0 {
		t.Fatalf("Make tags remaining after removal: %d", makeAfter)
	}

	t.Logf("JPEG GPS metadata stripped: %d bytes -> %d bytes", len(data), len(result))
}

// TestMetadataRemove_JPEG_Camera tests that camera EXIF data is stripped.
// Uses exif_camera.jpg from github.com/ianare/exif-samples (Canon EOS 40D).
func TestMetadataRemove_JPEG_Camera(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_camera.jpg")

	// Verify input has camera tags
	cameraBefore := exifTagCount(t, data, "Canon")
	if cameraBefore == 0 {
		t.Fatal("test file should have Canon EXIF tags before processing")
	}

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_camera.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)

	// Valid JPEG
	if len(result) < 3 || result[0] != 0xFF || result[1] != 0xD8 || result[2] != 0xFF {
		t.Fatal("output is not a valid JPEG")
	}

	// Camera tags gone
	cameraAfter := exifTagCount(t, result, "Canon")
	if cameraAfter > 0 {
		t.Fatalf("Canon tags remaining: %d", cameraAfter)
	}

	t.Logf("JPEG camera metadata stripped: %d bytes -> %d bytes", len(data), len(result))
}

// TestMetadataRemove_PNG_GPS tests metadata removal from a PNG with EXIF+GPS.
// Uses exif_gps.png from github.com/MikeKovarik/exifr.
func TestMetadataRemove_PNG_GPS(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.png")

	// Verify input has GPS tags
	gpsBefore := exifTagCount(t, data, "GPS")
	if gpsBefore == 0 {
		t.Fatal("test PNG should have GPS tags before processing")
	}

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.png", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)

	// Valid PNG
	if len(result) < 8 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not a valid PNG")
	}

	gpsAfter := exifTagCount(t, result, "GPS")
	if gpsAfter > 0 {
		t.Fatalf("GPS tags remaining in PNG: %d", gpsAfter)
	}

	t.Logf("PNG GPS metadata stripped: %d bytes -> %d bytes", len(data), len(result))
}

// TestMetadataRemove_OutputSmallerOrEqual checks that stripped files are no larger
// than the originals (metadata removal should only reduce size).
func TestMetadataRemove_OutputSmallerOrEqual(t *testing.T) {
	server, _, _ := setupTestServer(t)

	files := []string{"exif_gps.jpg", "exif_camera.jpg", "exif_gps.png"}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data := loadTestFile(t, file)

			sub := submitFile(t, server.URL, "/api/convert/metadata/remove", file, data, nil)
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("failed for %s: %s", file, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)

			if len(result) > len(data) {
				t.Fatalf("output (%d bytes) is larger than input (%d bytes)", len(result), len(data))
			}
			t.Logf("%s: %d -> %d bytes (saved %d bytes)", file, len(data), len(result), len(data)-len(result))
		})
	}
}

// TestMetadataRemove_PreservesImageContent verifies the image is still decodable
// after stripping (not corrupted by the process).
func TestMetadataRemove_PreservesImageContent(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)

	// Should still be decodable as JPEG
	metadataCheckFormat(t, result, "jpeg")
}

// TestMetadataRemove_OneTimeDownload ensures the download is single-use.
func TestMetadataRemove_OneTimeDownload(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("failed: %s", status.Error)
	}

	// First download should succeed
	resp1, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != 200 {
		t.Fatalf("first download failed: %d", resp1.StatusCode)
	}

	// Second download should fail
	resp2, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("second download should not succeed")
	}
}

// TestMetadataRemove_DownloadFilename checks the Content-Disposition preserves
// the original filename with the same extension.
func TestMetadataRemove_DownloadFilename(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "my_vacation_photo.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("failed: %s", status.Error)
	}

	resp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "my_vacation_photo.jpg") {
		t.Fatalf("expected filename my_vacation_photo.jpg in Content-Disposition, got: %s", cd)
	}
}

// TestMetadataRemove_RejectBMP checks that BMP files are rejected
// (BMP doesn't carry EXIF metadata).
func TestMetadataRemove_RejectBMP(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample.bmp")

	var body bytes.Buffer
	writer := metadataNewMultipart(&body, "test.bmp", data)

	resp, err := http.Post(server.URL+"/api/convert/metadata/remove", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for BMP, got %d", resp.StatusCode)
	}
}

// TestMetadataRemove_RejectTextFile checks that non-image files are rejected.
func TestMetadataRemove_RejectTextFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	textData := []byte("this is not an image file")

	var body bytes.Buffer
	writer := metadataNewMultipart(&body, "document.txt", textData)

	resp, err := http.Post(server.URL+"/api/convert/metadata/remove", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for text file, got %d", resp.StatusCode)
	}
}

// TestMetadataRemove_CleanupAfterDownload verifies job files are deleted after download.
func TestMetadataRemove_CleanupAfterDownload(t *testing.T) {
	if isRemoteMode() {
		t.Skip("inspects local tmpDir — not applicable in remote mode")
	}
	server, _, tmpDir := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("failed: %s", status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	// Give cleanup goroutine a moment
	metadataWaitCleanup(t, tmpDir, sub.JobID)
}

// TestMetadataRemove_NoMetadataAfterStrip is a thorough check that runs exiftool
// on the output and verifies no non-file metadata remains.
func TestMetadataRemove_NoMetadataAfterStrip(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "exif_gps.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "exif_gps.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)

	if hasExifTags(t, result) {
		t.Fatal("output still contains EXIF/XMP/IPTC metadata after stripping")
	}
}

// helpers

func metadataCheckFormat(t *testing.T, data []byte, expectedFormat string) {
	t.Helper()
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("output image not decodable: %v", err)
	}
	if format != expectedFormat {
		t.Fatalf("expected %s format, got %s", expectedFormat, format)
	}
}

func metadataNewMultipart(body *bytes.Buffer, filename string, data []byte) *multipart.Writer {
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", filename)
	part.Write(data)
	writer.Close()
	return writer
}

func metadataWaitCleanup(t *testing.T, tmpDir, jobID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(tmpDir)
		found := false
		for _, e := range entries {
			if e.Name() == jobID {
				found = true
				break
			}
		}
		if !found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("job directory %s still exists after download", jobID)
}
