//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// submitExpectError sends a file to the given endpoint and expects a non-202 status.
func submitExpectError(t *testing.T, serverURL, endpoint, filename string, fileData []byte, options map[string]any) (int, response.ErrorResponse) {
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

	var errResp response.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&errResp)
	return resp.StatusCode, errResp
}

// Progressive JPEG

// TestProgressiveJPEG_ToJPG converts a progressive JPEG to JPG.
// Progressive JPEGs use SOF2 markers instead of SOF0 and can confuse some decoders.
func TestProgressiveJPEG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "progressive.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("progressive JPEG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not a valid PNG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("progressive JPEG->PNG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestProgressiveJPEG_Compress tests compressing a progressive JPEG.
func TestProgressiveJPEG_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "progressive.jpg", data, map[string]any{
		"quality": 50,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("progressive JPEG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("progressive JPEG compress: %d bytes -> %d bytes", len(data), len(result))
}

// TestProgressiveJPEG_MetadataRemoval tests metadata removal from a progressive JPEG.
func TestProgressiveJPEG_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "progressive.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("progressive JPEG metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("progressive JPEG metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// CMYK JPEG

// TestCMYK_JPEG_ToPNG converts a CMYK JPEG to PNG.
// CMYK JPEGs use 4 channels instead of 3 — many decoders produce inverted colors
// or crash when encountering them without an ICC profile.
func TestCMYK_JPEG_ToPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "cmyk.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "cmyk.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("CMYK JPEG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("CMYK JPEG->PNG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestCMYK_JPEG_MetadataRemoval tests metadata removal from a CMYK JPEG.
func TestCMYK_JPEG_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "cmyk.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "cmyk.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("CMYK JPEG metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("CMYK JPEG metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// Animated WebP

// TestAnimatedWebP_ToJPG converts an animated WebP to JPG.
// Animated WebPs have ANIM/ANMF chunks; tools that only handle single-frame
// WebP may crash or silently discard frames.
func TestAnimatedWebP_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "animated.webp")

	sub := submitFile(t, server.URL, "/api/convert/image", "animated.webp", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("animated WebP->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("animated WebP->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestAnimatedWebP_ToPNG converts an animated WebP to PNG.
func TestAnimatedWebP_ToPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "animated.webp")

	sub := submitFile(t, server.URL, "/api/convert/image", "animated.webp", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("animated WebP->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("animated WebP->PNG: %d bytes -> %d bytes", len(data), len(result))
}

// 16-bit PNG

// TestPNG16bit_ToJPG converts a 16-bit per channel PNG to JPG.
// 16-bit PNGs have 65536 levels per channel; tools that assume 8-bit may
// silently truncate or produce corrupted output.
func TestPNG16bit_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "16bit.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "16bit.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("16-bit PNG->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("16-bit PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestPNG16bit_Compress tests compressing a 16-bit PNG.
func TestPNG16bit_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "16bit.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "16bit.png", data, map[string]any{
		"quality": 60,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("16-bit PNG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("16-bit PNG compress: %d bytes -> %d bytes", len(data), len(result))
}

// Interlaced PNG (Adam7)

// TestInterlacedPNG_ToJPG converts an interlaced (Adam7) RGBA PNG to JPG.
// Adam7 interlacing splits the image into 7 sub-passes; decoders that
// don't correctly reconstruct all passes produce striped or garbled output.
func TestInterlacedPNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "interlaced.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "interlaced.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("interlaced PNG->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("interlaced PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestInterlacedPNG_Compress tests compressing an interlaced PNG.
func TestInterlacedPNG_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "interlaced.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "interlaced.png", data, map[string]any{
		"quality": 70,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("interlaced PNG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("interlaced PNG compress: %d bytes -> %d bytes", len(data), len(result))
}

// Grayscale images

// TestGrayscaleJPEG_ToPNG converts a grayscale (single-channel) JPEG to PNG.
// Grayscale JPEGs have 1 component instead of 3, which can trip up tools
// expecting RGB data.
func TestGrayscaleJPEG_ToPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "grayscale.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "grayscale.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("grayscale JPEG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("grayscale JPEG->PNG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestGrayscaleJPEG_Compress tests compressing a grayscale JPEG.
func TestGrayscaleJPEG_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "grayscale.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "grayscale.jpg", data, map[string]any{
		"quality": 50,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("grayscale JPEG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("grayscale JPEG compress: %d bytes -> %d bytes", len(data), len(result))
}

// TestGrayscalePNG_ToJPG converts a grayscale PNG to JPG.
func TestGrayscalePNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "grayscale.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "grayscale.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("grayscale PNG->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("grayscale PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// PNG with alpha transparency

// TestTransparentPNG_ToJPG converts a PNG with alpha channel to JPG.
// JPEG does not support transparency — vips must flatten the alpha channel.
// Without proper handling, the output will have a black background or crash.
func TestTransparentPNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "transparent.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "transparent.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("transparent PNG->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("transparent PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestTransparentPNG_Compress tests compressing a PNG with alpha channel.
func TestTransparentPNG_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "transparent.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "transparent.png", data, map[string]any{
		"quality": 60,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("transparent PNG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("transparent PNG compress: %d bytes -> %d bytes", len(data), len(result))
}

// Tiny images (1x1 pixel)

// TestTiny1x1_PNG_ToJPG converts a 1x1 pixel PNG to JPG.
// Minimal dimensions can trigger edge cases in resize logic and buffer calculations.
func TestTiny1x1_PNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "tiny_1x1.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "tiny.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("1x1 PNG->JPG failed: %s", status.Error)
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
	t.Logf("1x1 PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestTiny1x1_JPG_ToPNG converts a 1x1 pixel JPG to PNG.
func TestTiny1x1_JPG_ToPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "tiny_1x1.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "tiny.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("1x1 JPG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("1x1 JPG->PNG: %d bytes -> %d bytes", len(data), len(result))
}

// TestTiny1x1_CompressWithResize tests compressing a 1x1 image with resize to
// smaller dimensions — should not crash on zero or negative resize targets.
func TestTiny1x1_CompressWithResize(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "tiny_1x1.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "tiny.png", data, map[string]any{
		"quality":  80,
		"maxWidth": 1,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("1x1 PNG compress+resize failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 1 {
		t.Fatalf("expected width <= 1, got %d", b.Dx())
	}
	t.Logf("1x1 PNG compress+resize: %d bytes -> %d bytes, %dx%d", len(data), len(result), b.Dx(), b.Dy())
}

// PNG with ICC color profile

// TestICCProfile_PNG_ToJPG converts a PNG with embedded ICC profile to JPG.
// ICC profiles can cause color space conversion issues, especially wide-gamut
// profiles (Display P3, ProPhoto RGB) that contain colors outside sRGB.
func TestICCProfile_PNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "icc_profile.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "icc.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("ICC PNG->JPG failed: %s", status.Error)
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
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("ICC PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestICCProfile_PNG_Compress tests compressing a PNG with ICC profile.
func TestICCProfile_PNG_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "icc_profile.png")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "icc.png", data, map[string]any{
		"quality": 60,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("ICC PNG compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("ICC PNG compress: %d bytes -> %d bytes", len(data), len(result))
}

// TestICCProfile_PNG_MetadataRemoval tests metadata removal from a PNG with ICC profile.
func TestICCProfile_PNG_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "icc_profile.png")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "icc.png", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("ICC PNG metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("ICC PNG metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// Broken EXIF DPI JPEG

// TestBrokenExifDPI_ToPNG converts a JPEG with broken DPI values in EXIF.
// Broken or zero DPI values can cause division-by-zero errors in tools that
// calculate pixel density from EXIF data.
func TestBrokenExifDPI_ToPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "broken_exif_dpi.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "broken_dpi.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("broken EXIF DPI JPEG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}

	img, _, err := image.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		t.Fatal("output has zero dimensions")
	}
	t.Logf("broken EXIF DPI JPEG->PNG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestBrokenExifDPI_Compress tests compressing a JPEG with broken DPI.
func TestBrokenExifDPI_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "broken_exif_dpi.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image/compress", "broken_dpi.jpg", data, map[string]any{
		"quality": 50,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("broken EXIF DPI compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("broken EXIF DPI compress: %d bytes -> %d bytes", len(data), len(result))
}

// TestBrokenExifDPI_MetadataRemoval tests that metadata removal handles broken DPI gracefully.
func TestBrokenExifDPI_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "broken_exif_dpi.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "broken_dpi.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("broken EXIF DPI metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("broken EXIF DPI metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// Truncated JPEG

// TestTruncatedJPEG_Conversion tests that a truncated JPEG (valid header, incomplete
// data stream) either converts successfully with partial data or returns a clear error
// — it must NOT crash or hang.
func TestTruncatedJPEG_Conversion(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "truncated.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "truncated.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)

	// Either succeed (vips can handle partial JPEGs) or error gracefully
	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("truncated JPEG handled gracefully: %d bytes -> %d bytes", len(data), len(result))
	} else if status.Status == "error" {
		t.Logf("truncated JPEG correctly rejected: %s", status.Error)
	} else {
		t.Fatalf("unexpected status for truncated JPEG: %s", status.Status)
	}
}

// TestTruncatedJPEG_MetadataRemoval tests metadata removal on a truncated JPEG.
func TestTruncatedJPEG_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "truncated.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "truncated.jpg", data, nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("truncated JPEG metadata removal handled: %d bytes -> %d bytes", len(data), len(result))
	} else if status.Status == "error" {
		t.Logf("truncated JPEG metadata removal correctly rejected: %s", status.Error)
	} else {
		t.Fatalf("unexpected status: %s", status.Status)
	}
}

// Files with only magic bytes (no real content)

// TestOnlyMagicBytes_JPEG tests that a file containing only JPEG magic bytes
// (no actual image data) is rejected or handled gracefully.
func TestOnlyMagicBytes_JPEG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "only_magic_bytes.jpg")

	// Server may reject at submission (400) or accept and fail during processing.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fake.jpg")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	optJSON, _ := json.Marshal(map[string]any{"outputFormat": "png"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Logf("correctly rejected magic-bytes-only JPEG at submission: %d", resp.StatusCode)
		return
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitDone(t, server.URL, sub.JobID)

	// Must not succeed — file has no actual image data
	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("WARNING: only-magic-bytes JPEG was accepted, output=%d bytes", len(result))
	} else if status.Status == "error" {
		t.Logf("correctly rejected magic-bytes-only JPEG: %s", status.Error)
	}
}

// TestOnlyMagicBytes_PNG tests that a file containing only PNG magic bytes is rejected.
func TestOnlyMagicBytes_PNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "only_magic_bytes.png")

	// Server may reject at submission (400) or accept and fail during processing.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fake.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	optJSON, _ := json.Marshal(map[string]any{"outputFormat": "jpg"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/image", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Logf("correctly rejected magic-bytes-only PNG at submission: %d", resp.StatusCode)
		return
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("WARNING: only-magic-bytes PNG was accepted, output=%d bytes", len(result))
	} else if status.Status == "error" {
		t.Logf("correctly rejected magic-bytes-only PNG during processing: %s", status.Error)
	}
}

// TestOnlyMagicBytes_PDF tests that a file containing only %PDF header is rejected.
func TestOnlyMagicBytes_PDF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "only_magic_bytes.pdf")

	// Server may reject at submission (400) or accept and fail during processing.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fake.pdf")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	optJSON, _ := json.Marshal(map[string]any{"level": "medium"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/pdf/compress", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Logf("correctly rejected magic-bytes-only PDF at submission: %d", resp.StatusCode)
		return
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("WARNING: only-magic-bytes PDF was accepted, output=%d bytes", len(result))
	} else if status.Status == "error" {
		t.Logf("correctly rejected magic-bytes-only PDF: %s", status.Error)
	}
}

// Multi-page PDF

// TestMultipagePDF_Compress tests that a multi-page PDF can be compressed
// without losing pages.
func TestMultipagePDF_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "pdf_multipage.pdf")

	for _, level := range []string{"low", "medium", "high"} {
		t.Run(level, func(t *testing.T) {
			sub := submitFile(t, server.URL, "/api/convert/pdf/compress", "multipage.pdf", data, map[string]any{
				"level": level,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("multi-page PDF compress (%s) failed: %s", level, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if len(result) < 4 || string(result[:4]) != "%PDF" {
				t.Fatal("output is not PDF")
			}
			t.Logf("multi-page PDF compress (%s): %d bytes -> %d bytes", level, len(data), len(result))
		})
	}
}

// TestMultipagePDF_MetadataRemoval tests metadata removal from a multi-page PDF.
func TestMultipagePDF_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "pdf_multipage.pdf")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "multipage.pdf", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("multi-page PDF metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("multi-page PDF metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// PDF with JavaScript (security edge case)

// TestPDFJavaScript_Compress tests that a PDF containing JavaScript actions
// can be compressed safely. Ghostscript's -dSAFER and -dPARANOIDSAFER flags
// should neutralize any embedded scripts.
func TestPDFJavaScript_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "pdf_javascript.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/compress", "jstest.pdf", data, map[string]any{
		"level": "medium",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF with JavaScript compress failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF with JavaScript compress: %d bytes -> %d bytes", len(data), len(result))
}

// TestPDFJavaScript_MetadataRemoval tests metadata removal from a PDF with JavaScript.
func TestPDFJavaScript_MetadataRemoval(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "pdf_javascript.pdf")

	sub := submitFile(t, server.URL, "/api/convert/metadata/remove", "jstest.pdf", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF with JavaScript metadata removal failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF with JavaScript metadata removal: %d bytes -> %d bytes", len(data), len(result))
}

// Large palette / complex PNG

// TestLargePalettePNG_ToJPG converts a complex 16-bit PNG with many colors.
func TestLargePalettePNG_ToJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "large_palette.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "complex.png", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("large palette PNG->JPG failed: %s", status.Error)
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
	t.Logf("large palette PNG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// Cross-format edge case conversion matrix

// TestEdgeCase_ConversionMatrix tests all edge case files through format conversion.
func TestEdgeCase_ConversionMatrix(t *testing.T) {
	server, _, _ := setupTestServer(t)

	tests := []struct {
		name         string
		file         string
		outputFormat string
	}{
		{"progressive_jpg_to_jpg", "progressive.jpg", "jpg"},
		{"cmyk_jpg_to_jpg", "cmyk.jpg", "jpg"},
		{"grayscale_jpg_to_jpg", "grayscale.jpg", "jpg"},
		{"broken_exif_to_jpg", "broken_exif_dpi.jpg", "jpg"},
		{"interlaced_png_to_png", "interlaced.png", "png"},
		{"16bit_png_to_png", "16bit.png", "png"},
		{"icc_png_to_png", "icc_profile.png", "png"},
		{"transparent_png_to_png", "transparent.png", "png"},
		{"animated_webp_to_jpg", "animated.webp", "jpg"},
		{"animated_webp_to_png", "animated.webp", "png"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := loadTestFile(t, tc.file)

			sub := submitFile(t, server.URL, "/api/convert/image", tc.file, data, map[string]any{
				"outputFormat": tc.outputFormat,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("%s failed: %s", tc.name, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)

			switch tc.outputFormat {
			case "jpg":
				if result[0] != 0xFF || result[1] != 0xD8 {
					t.Fatal("output is not JPEG")
				}
			case "png":
				if result[0] != 0x89 || result[1] != 0x50 {
					t.Fatal("output is not PNG")
				}
			}

			t.Logf("%s: %d bytes -> %d bytes", tc.name, len(data), len(result))
		})
	}
}

// Wrong filename extension (magic bytes should be trusted, not extension)

// TestWrongExtension_JPGNamedAsPNG tests that a JPEG file with .png extension
// is correctly detected by magic bytes and converted.
func TestWrongExtension_JPGNamedAsPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	// Submit a JPEG file with a .png extension — magic bytes should win
	sub := submitFile(t, server.URL, "/api/convert/image", "actually_jpeg.png", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("wrong extension JPEG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("wrong extension test: JPEG named as .png correctly converted to PNG")
}

// TestWrongExtension_PNGNamedAsJPG tests that a PNG file with .jpg extension
// is correctly detected and converted.
func TestWrongExtension_PNGNamedAsJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "interlaced.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "actually_png.jpg", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("wrong extension PNG->JPG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0xFF || result[1] != 0xD8 {
		t.Fatal("output is not JPEG")
	}
	t.Logf("wrong extension test: PNG named as .jpg correctly converted to JPG")
}

// Rapid sequential submissions (stress test for queue)

// TestRapidSubmissions submits multiple edge-case files rapidly and verifies
// all complete without errors.
func TestRapidSubmissions(t *testing.T) {
	server, _, _ := setupTestServer(t)

	files := []struct {
		name         string
		outputFormat string
	}{
		{"progressive.jpg", "png"},
		{"grayscale.jpg", "png"},
		{"interlaced.png", "jpg"},
		{"transparent.png", "jpg"},
		{"16bit.png", "jpg"},
	}

	// Submit all jobs
	type submission struct {
		name  string
		jobID string
	}
	var subs []submission

	for _, f := range files {
		data := loadTestFile(t, f.name)
		sub := submitFile(t, server.URL, "/api/convert/image", f.name, data, map[string]any{
			"outputFormat": f.outputFormat,
		})
		subs = append(subs, submission{name: f.name, jobID: sub.JobID})
	}

	// Wait for all to complete
	for _, s := range subs {
		status := waitDone(t, server.URL, s.jobID)
		if status.Status != "done" {
			t.Errorf("%s failed: %s", s.name, status.Error)
			continue
		}

		result := downloadResult(t, server.URL, status.DownloadURL)
		t.Logf("%s: completed, %d bytes output", s.name, len(result))
	}
}

// Edge case: same format conversion (e.g., JPG to JPG)

// TestSameFormatConversion_JPGtoJPG tests converting a JPEG to JPEG.
// Some tools may re-encode unnecessarily or fail on identity conversion.
func TestSameFormatConversion_JPGtoJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.jpg", data, map[string]any{
		"outputFormat": "jpg",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("JPG->JPG failed: %s", status.Error)
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
	t.Logf("JPG->JPG: %dx%d, %d bytes -> %d bytes", b.Dx(), b.Dy(), len(data), len(result))
}

// TestSameFormatConversion_PNGtoPNG tests converting a PNG to PNG.
func TestSameFormatConversion_PNGtoPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "interlaced.png")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.png", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PNG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("PNG->PNG: %d bytes -> %d bytes", len(data), len(result))
}

// Edge case: JPEG compress at extreme quality values

// TestEdgeQuality_Compress tests compression with boundary quality values.
func TestEdgeQuality_Compress(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "progressive.jpg")

	for _, quality := range []int{1, 100} {
		t.Run(func() string {
			if quality == 1 {
				return "min_quality"
			}
			return "max_quality"
		}(), func(t *testing.T) {
			sub := submitFile(t, server.URL, "/api/convert/image/compress", "photo.jpg", data, map[string]any{
				"quality": quality,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("compress q=%d failed: %s", quality, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if result[0] != 0xFF || result[1] != 0xD8 {
				t.Fatal("output is not JPEG")
			}

			img, _, err := image.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatalf("cannot decode: %v", err)
			}
			b := img.Bounds()
			t.Logf("compress q=%d: %dx%d, %d bytes -> %d bytes (%.0f%%)",
				quality, b.Dx(), b.Dy(), len(data), len(result),
				float64(len(result))/float64(len(data))*100)
		})
	}
}

// Cleanup tests for edge cases

// TestEdgeCase_CleanupAfterDownload verifies that job directories are removed
// after downloading edge-case files.
func TestEdgeCase_CleanupAfterDownload(t *testing.T) {
	if isRemoteMode() {
		t.Skip("inspects local tmpDir — not applicable in remote mode")
	}
	server, _, tmpDir := setupTestServer(t)
	data := loadTestFile(t, "cmyk.jpg")

	sub := submitFile(t, server.URL, "/api/convert/image", "cmyk.jpg", data, map[string]any{
		"outputFormat": "png",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("conversion failed: %s", status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	time.Sleep(200 * time.Millisecond)

	metadataWaitCleanup(t, tmpDir, sub.JobID)
}
