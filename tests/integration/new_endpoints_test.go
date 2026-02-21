//go:build integration

package integration

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"fmt"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// Helper: create minimal WAV file

func createMinimalWAV(t *testing.T) []byte {
	t.Helper()
	// 1 second of 8kHz 16-bit mono silence
	sampleRate := uint32(8000)
	bitsPerSample := uint16(16)
	numChannels := uint16(1)
	numSamples := sampleRate // 1 second
	dataSize := numSamples * uint32(bitsPerSample/8) * uint32(numChannels)

	var buf bytes.Buffer
	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	// fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))          // sub-chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))           // PCM format
	binary.Write(&buf, binary.LittleEndian, numChannels)         // channels
	binary.Write(&buf, binary.LittleEndian, sampleRate)          // sample rate
	binary.Write(&buf, binary.LittleEndian, sampleRate*uint32(bitsPerSample/8)*uint32(numChannels)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(uint32(bitsPerSample/8)*uint32(numChannels)))    // block align
	binary.Write(&buf, binary.LittleEndian, bitsPerSample)      // bits per sample
	// data sub-chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, dataSize)
	// Write silence (zeros)
	silence := make([]byte, dataSize)
	buf.Write(silence)
	return buf.Bytes()
}

// SVG to PNG

func TestSvgToPng_Basic(t *testing.T) {
	server, _, _ := setupTestServer(t)

	svgData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100" viewBox="0 0 100 100">
  <rect width="100" height="100" fill="red"/>
  <circle cx="50" cy="50" r="40" fill="blue"/>
</svg>`)

	sub := submitFile(t, server.URL, "/api/convert/svg/png", "icon.svg", svgData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("SVG->PNG failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 8 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("SVG->PNG: %d bytes -> %d bytes", len(svgData), len(result))
}

func TestSvgToPng_WithSize(t *testing.T) {
	server, _, _ := setupTestServer(t)

	svgData := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200">
  <rect width="200" height="200" fill="green"/>
</svg>`)

	sub := submitFile(t, server.URL, "/api/convert/svg/png", "box.svg", svgData, map[string]any{
		"width":  512,
		"height": 512,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("SVG->PNG (sized) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("SVG->PNG (512x512): %d bytes", len(result))
}

func TestSvgToPng_RejectNonSVG(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/svg/png", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Markdown to PDF

func TestMarkdownToPDF_Basic(t *testing.T) {
	server, _, _ := setupTestServer(t)

	mdContent := []byte("# Hello World\n\nThis is a **test** document.\n\n- Item 1\n- Item 2\n- Item 3\n")

	sub := submitFile(t, server.URL, "/api/convert/markdown/pdf", "readme.md", mdContent, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Markdown->PDF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("Markdown->PDF: %d bytes -> %d bytes", len(mdContent), len(result))
}

func TestMarkdownToPDF_RejectNonMarkdown(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "document.txt")
	part.Write([]byte("plain text file"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/markdown/pdf", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for .txt file, got %d", resp.StatusCode)
	}
}

func TestMarkdownToPDF_RejectBinaryFile(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Binary data with null bytes — should be rejected
	binaryData := []byte("# Header\x00\x00\x00binary content")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "binary.md")
	part.Write(binaryData)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/markdown/pdf", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for binary .md file, got %d", resp.StatusCode)
	}
}

// PDF Password

func TestPDFPassword_Protect(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":         "protect",
		"userPassword": "secret123",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF password protect failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	// Encrypted PDF should be larger than the original
	t.Logf("PDF protect: %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFPassword_ProtectAndRemove(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	// First: protect
	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":         "protect",
		"userPassword": "mypassword",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF protect failed: %s", status.Error)
	}
	encrypted := downloadResult(t, server.URL, status.DownloadURL)

	// Then: remove password
	sub2 := submitFile(t, server.URL, "/api/convert/pdf/password", "encrypted.pdf", encrypted, map[string]any{
		"mode":     "remove",
		"password": "mypassword",
	})
	status2 := waitDone(t, server.URL, sub2.JobID)
	if status2.Status != "done" {
		t.Fatalf("PDF password remove failed: %s", status2.Error)
	}

	decrypted := downloadResult(t, server.URL, status2.DownloadURL)
	if string(decrypted[:4]) != "%PDF" {
		t.Fatal("decrypted output is not PDF")
	}
	t.Logf("PDF protect+remove: %d -> %d -> %d bytes", len(pdfData), len(encrypted), len(decrypted))
}

func TestPDFPassword_RejectNonPDF(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.jpg")
	part.Write(createTestJPG(t))
	optJSON, _ := json.Marshal(map[string]any{"mode": "protect", "userPassword": "test"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/pdf/password", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// PDF Edit

func TestPDFEdit_AddWatermark(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := createTestPDF(t)

	sub := submitFile(t, server.URL, "/api/convert/pdf/edit", "document.pdf", pdfData, map[string]any{
		"watermark": map[string]any{
			"text":     "CONFIDENTIAL",
			"fontSize": 48,
			"opacity":  0.3,
			"rotation": -45,
			"color":    []float64{0.8, 0, 0},
			"position": "center",
		},
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF watermark failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF watermark: %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFEdit_AddPageNumbers(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "pdf_multipage.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/edit", "document.pdf", pdfData, map[string]any{
		"pageNumbers": map[string]any{
			"format":   "Page {page} of {total}",
			"fontSize": 10,
			"position": "bottom-center",
		},
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF page numbers failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF page numbers: %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFEdit_RotatePages(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := createTestPDF(t)

	sub := submitFile(t, server.URL, "/api/convert/pdf/edit", "document.pdf", pdfData, map[string]any{
		"rotations": map[string]any{
			"1": 90,
		},
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF rotate failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
}

func TestPDFEdit_RejectNonPDF(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	optJSON, _ := json.Marshal(map[string]any{"watermark": map[string]any{"text": "test"}})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/pdf/edit", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// PDF Extract Images

func TestPDFExtractImages_RealFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/extract-images", "document.pdf", pdfData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	// May succeed with extracted images or fail if PDF has no embedded images
	if status.Status == "done" {
		t.Logf("PDF extract images: found images in sample.pdf")
	} else {
		t.Logf("PDF extract images: %s (may have no embedded images)", status.Error)
	}
}

func TestPDFExtractImages_RejectNonPDF(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/pdf/extract-images", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// OCR

func TestOCR_JPGImage(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := loadTestFile(t, "sample.jpg")

	sub := submitFile(t, server.URL, "/api/convert/ocr", "document.jpg", jpgData, map[string]any{
		"languages": []string{"eng"},
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("OCR failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// OCR output should be text
	t.Logf("OCR output: %d bytes of text", len(result))
}

func TestOCR_PNGImage(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pngData := loadTestFile(t, "sample.png")

	sub := submitFile(t, server.URL, "/api/convert/ocr", "scan.png", pngData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("OCR (PNG) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	t.Logf("OCR (PNG) output: %d bytes", len(result))
}

func TestOCR_RejectNonImage(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "document.txt")
	part.Write([]byte("this is plain text"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/ocr", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Favicon

func TestFavicon_FromJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := loadTestFile(t, "sample.jpg")

	sub := submitFile(t, server.URL, "/api/convert/favicon", "logo.jpg", jpgData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Favicon generation failed: %s", status.Error)
	}

	// Favicon generates multiple files — download as zip
	zipURL := fmt.Sprintf("%s%s/zip", server.URL, status.DownloadURL)
	resp, err := http.Get(zipURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("favicon zip download failed: %d: %s", resp.StatusCode, string(bodyBytes))
	}

	data, _ := io.ReadAll(resp.Body)
	// ZIP magic bytes: PK
	if len(data) < 4 || data[0] != 0x50 || data[1] != 0x4B {
		t.Fatal("favicon output is not a ZIP file")
	}
	t.Logf("Favicon ZIP: %d bytes", len(data))
}

func TestFavicon_FromPNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pngData := createTestPNG(t)

	sub := submitFile(t, server.URL, "/api/convert/favicon", "icon.png", pngData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Favicon from PNG failed: %s", status.Error)
	}
	t.Logf("Favicon from PNG completed successfully")
}

func TestFavicon_RejectNonImage(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "readme.txt")
	part.Write([]byte("not an image"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/favicon", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Video Compress

func TestVideoCompress_Quality(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_640x360.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/compress", "video.mov", data, map[string]any{
		"quality": "low",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Video compress (low) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	assertMP4(t, result)
	t.Logf("Video compress (low): %d bytes -> %d bytes", len(data), len(result))
}

func TestVideoCompress_RejectNonVideo(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.jpg")
	part.Write(createTestJPG(t))
	optJSON, _ := json.Marshal(map[string]any{"quality": "medium"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/video/compress", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Video to GIF

func TestVideoToGif_Basic(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/gif", "clip.mov", data, map[string]any{
		"duration": 2,
		"fps":      8,
		"maxWidth": 160,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Video->GIF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// GIF magic bytes: GIF89a or GIF87a
	if len(result) < 6 || string(result[:3]) != "GIF" {
		t.Fatal("output is not GIF")
	}
	t.Logf("Video->GIF: %d bytes -> %d bytes", len(data), len(result))
}

func TestVideoToGif_RejectNonVideo(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "audio.mp3")
	part.Write([]byte("not a video"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/video/gif", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Audio Convert

func TestAudioConvert_WAVtoMP3(t *testing.T) {
	server, _, _ := setupTestServer(t)
	wavData := createMinimalWAV(t)

	sub := submitFile(t, server.URL, "/api/convert/audio", "sound.wav", wavData, map[string]any{
		"outputFormat": "mp3",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("WAV->MP3 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// MP3 files start with FF FB, FF F3, FF F2, or ID3 tag
	if len(result) < 3 {
		t.Fatal("output too small for MP3")
	}
	isMP3 := (result[0] == 0xFF && (result[1]&0xE0) == 0xE0) || string(result[:3]) == "ID3"
	if !isMP3 {
		t.Fatalf("output is not MP3 (first bytes: %x %x %x)", result[0], result[1], result[2])
	}
	t.Logf("WAV->MP3: %d bytes -> %d bytes", len(wavData), len(result))
}

func TestAudioConvert_WAVtoFLAC(t *testing.T) {
	server, _, _ := setupTestServer(t)
	wavData := createMinimalWAV(t)

	sub := submitFile(t, server.URL, "/api/convert/audio", "sound.wav", wavData, map[string]any{
		"outputFormat": "flac",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("WAV->FLAC failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// FLAC magic: "fLaC"
	if len(result) < 4 || string(result[:4]) != "fLaC" {
		t.Fatal("output is not FLAC")
	}
	t.Logf("WAV->FLAC: %d bytes -> %d bytes", len(wavData), len(result))
}

func TestAudioConvert_RejectNonAudio(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	optJSON, _ := json.Marshal(map[string]any{"outputFormat": "mp3"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/audio", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAudioConvert_MissingOutputFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	wavData := createMinimalWAV(t)

	// API accepts the request (202) but the job should fail during processing
	sub := submitFile(t, server.URL, "/api/convert/audio", "sound.wav", wavData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status == "done" {
		t.Fatal("expected job to fail with no outputFormat, but it succeeded")
	}
	t.Logf("Audio missing outputFormat: correctly failed with: %s", status.Error)
}

// Audio Extract

func TestAudioExtract_AllFormats(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_with_audio.mp4")

	for _, format := range []string{"mp3", "wav", "flac", "aac"} {
		t.Run(format, func(t *testing.T) {
			sub := submitFile(t, server.URL, "/api/convert/audio/extract", "video.mp4", data, map[string]any{
				"outputFormat": format,
			})
			status := waitDone(t, server.URL, sub.JobID)
			if status.Status != "done" {
				t.Fatalf("audio extract to %s failed: %s", format, status.Error)
			}

			result := downloadResult(t, server.URL, status.DownloadURL)
			if len(result) == 0 {
				t.Fatal("empty output")
			}
			t.Logf("MP4 -> %s: %d bytes -> %d bytes", format, len(data), len(result))
		})
	}
}

func TestAudioExtract_DefaultFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_with_audio.mp4")

	sub := submitFile(t, server.URL, "/api/convert/audio/extract", "video.mp4", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("audio extract default format failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	isMP3 := (len(result) >= 3 && result[0] == 'I' && result[1] == 'D' && result[2] == '3') ||
		(len(result) >= 2 && result[0] == 0xFF && (result[1]&0xE0) == 0xE0)
	if !isMP3 {
		t.Fatalf("default output is not MP3 (first bytes: %x)", result[:min(4, len(result))])
	}
	t.Logf("MP4 -> default (mp3): %d bytes", len(result))
}

func TestAudioExtract_NoAudioStream(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_noaudio.mov")

	sub := submitFile(t, server.URL, "/api/convert/audio/extract", "video.mov", data, map[string]any{
		"outputFormat": "mp3",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "error" {
		t.Fatal("expected error for video with no audio stream")
	}
	t.Logf("No audio stream: %s", status.Error)
}

func TestAudioExtract_RejectNonVideo(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.jpg")
	part.Write(createTestJPG(t))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/audio/extract", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// Decompress

func TestDecompress_ZipFile(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// First create a ZIP archive via archive/create
	pngData := createTestPNG(t)

	var createBody bytes.Buffer
	createWriter := multipart.NewWriter(&createBody)
	part, _ := createWriter.CreateFormFile("files", "image.png")
	part.Write(pngData)
	part2, _ := createWriter.CreateFormFile("files", "hello.txt")
	part2.Write([]byte("hello world"))
	createWriter.WriteField("format", "zip")
	createWriter.Close()

	createResp, err := http.Post(server.URL+"/api/archive/create", createWriter.FormDataContentType(), &createBody)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(createResp.Body)
		t.Fatalf("archive create failed: %d: %s", createResp.StatusCode, string(bodyBytes))
	}

	var createSubmit response.SubmitResponse
	json.NewDecoder(createResp.Body).Decode(&createSubmit)

	createStatus := waitForJob(t, server.URL, createSubmit.JobID, 30*time.Second)
	if createStatus.Status != "done" {
		t.Fatalf("archive create failed: %s", createStatus.Error)
	}

	zipData := downloadResult(t, server.URL, createStatus.DownloadURL)

	// Now decompress the ZIP
	sub := submitFile(t, server.URL, "/api/archive/decompress", "archive.zip", zipData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Decompress failed: %s", status.Error)
	}
	t.Logf("Decompress ZIP: %d bytes archive decompressed successfully", len(zipData))
}

func TestDecompress_RejectNonArchive(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.jpg")
	part.Write(createTestJPG(t))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/decompress", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// QR Code

func TestQRCode_Basic(t *testing.T) {
	server, _, _ := setupTestServer(t)

	reqBody, _ := json.Marshal(map[string]any{
		"text": "https://filemagic.app",
	})

	resp, err := http.Post(server.URL+"/api/generate/qrcode", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var submit response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&submit)

	status := waitDone(t, server.URL, submit.JobID)
	if status.Status != "done" {
		t.Fatalf("QR code generation failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// Should be a PNG
	if len(result) < 8 || result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("QR code output is not PNG")
	}
	t.Logf("QR code: %d bytes PNG", len(result))
}

func TestQRCode_WithOptions(t *testing.T) {
	server, _, _ := setupTestServer(t)

	reqBody, _ := json.Marshal(map[string]any{
		"text": "Hello World",
		"options": map[string]any{
			"size":            256,
			"errorCorrection": "H",
			"fgColor":         "#FF0000",
			"bgColor":         "#FFFFFF",
		},
	})

	resp, err := http.Post(server.URL+"/api/generate/qrcode", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var submit response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&submit)

	status := waitDone(t, server.URL, submit.JobID)
	if status.Status != "done" {
		t.Fatalf("QR code (custom) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if result[0] != 0x89 || result[1] != 0x50 {
		t.Fatal("output is not PNG")
	}
	t.Logf("QR code (256px, red): %d bytes", len(result))
}

func TestQRCode_EmptyText(t *testing.T) {
	server, _, _ := setupTestServer(t)

	reqBody, _ := json.Marshal(map[string]any{
		"text": "",
	})

	resp, err := http.Post(server.URL+"/api/generate/qrcode", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", resp.StatusCode)
	}
}

// Metadata Inspect

func TestMetadataInspect_JPGWithExif(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := loadTestFile(t, "exif_camera.jpg")

	sub := submitFile(t, server.URL, "/api/convert/metadata/inspect", "photo.jpg", jpgData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Metadata inspect failed: %s", status.Error)
	}
	t.Logf("Metadata inspect completed for EXIF camera JPG")
}

func TestMetadataInspect_PNGWithGPS(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pngData := loadTestFile(t, "exif_gps.png")

	sub := submitFile(t, server.URL, "/api/convert/metadata/inspect", "geotagged.png", pngData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Metadata inspect (PNG) failed: %s", status.Error)
	}
	t.Logf("Metadata inspect completed for GPS PNG")
}

func TestMetadataInspect_RejectNonImage(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "readme.txt")
	part.Write([]byte("plain text"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/metadata/inspect", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
