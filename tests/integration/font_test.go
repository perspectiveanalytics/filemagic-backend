//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

func TestFontConvert_TTFtoWOFF2(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	sub := submitFile(t, server.URL, "/api/convert/font", "font.ttf", ttf, map[string]any{
		"targetFormat": "woff2",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("TTF->WOFF2 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// WOFF2 magic: "wOF2" (0x774F4632)
	if len(result) < 4 || string(result[:4]) != "wOF2" {
		t.Fatalf("output is not WOFF2 (first 4 bytes: %x)", result[:4])
	}
	t.Logf("TTF->WOFF2: %d bytes -> %d bytes", len(ttf), len(result))
}

func TestFontConvert_TTFtoWOFF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	sub := submitFile(t, server.URL, "/api/convert/font", "font.ttf", ttf, map[string]any{
		"targetFormat": "woff",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("TTF->WOFF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// WOFF magic: "wOFF" (0x774F4646)
	if len(result) < 4 || string(result[:4]) != "wOFF" {
		t.Fatalf("output is not WOFF (first 4 bytes: %x)", result[:4])
	}
	t.Logf("TTF->WOFF: %d bytes -> %d bytes", len(ttf), len(result))
}

func TestFontConvert_TTFtoOTF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	sub := submitFile(t, server.URL, "/api/convert/font", "font.ttf", ttf, map[string]any{
		"targetFormat": "otf",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("TTF->OTF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("OTF output is empty")
	}
	t.Logf("TTF->OTF: %d bytes -> %d bytes", len(ttf), len(result))
}

func TestFontConvert_OTFtoTTF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	otf := loadTestFile(t, "sample.otf")

	sub := submitFile(t, server.URL, "/api/convert/font", "font.otf", otf, map[string]any{
		"targetFormat": "ttf",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("OTF->TTF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("TTF output is empty")
	}
	t.Logf("OTF->TTF: %d bytes -> %d bytes", len(otf), len(result))
}

func TestFontConvert_OTFtoWOFF2(t *testing.T) {
	server, _, _ := setupTestServer(t)
	otf := loadTestFile(t, "sample.otf")

	sub := submitFile(t, server.URL, "/api/convert/font", "font.otf", otf, map[string]any{
		"targetFormat": "woff2",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("OTF->WOFF2 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "wOF2" {
		t.Fatalf("output is not WOFF2 (first 4 bytes: %x)", result[:4])
	}
	t.Logf("OTF->WOFF2: %d bytes -> %d bytes", len(otf), len(result))
}

func TestFontConvert_WOFF2toTTF_Roundtrip(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	// First: TTF -> WOFF2
	sub := submitFile(t, server.URL, "/api/convert/font", "font.ttf", ttf, map[string]any{
		"targetFormat": "woff2",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("TTF->WOFF2 (step 1) failed: %s", status.Error)
	}
	woff2 := downloadResult(t, server.URL, status.DownloadURL)
	if string(woff2[:4]) != "wOF2" {
		t.Fatal("step 1 output is not WOFF2")
	}

	// Then: WOFF2 -> TTF
	sub2 := submitFile(t, server.URL, "/api/convert/font", "font.woff2", woff2, map[string]any{
		"targetFormat": "ttf",
	})
	status2 := waitDone(t, server.URL, sub2.JobID)
	if status2.Status != "done" {
		t.Fatalf("WOFF2->TTF (step 2) failed: %s", status2.Error)
	}
	roundtrippedTTF := downloadResult(t, server.URL, status2.DownloadURL)
	if len(roundtrippedTTF) == 0 {
		t.Fatal("roundtrip TTF output is empty")
	}
	t.Logf("Roundtrip TTF->WOFF2->TTF: %d -> %d -> %d bytes", len(ttf), len(woff2), len(roundtrippedTTF))
}

func TestFontConvert_MissingFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "font.ttf")
	part.Write(ttf)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/font", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		t.Logf("Missing format: correctly rejected at submission with 400")
	} else if resp.StatusCode == http.StatusAccepted {
		var sub response.SubmitResponse
		json.NewDecoder(resp.Body).Decode(&sub)
		status := waitDone(t, server.URL, sub.JobID)
		if status.Status == "done" {
			t.Fatal("expected job to fail with no targetFormat, but it succeeded")
		}
		t.Logf("Missing format: correctly failed during processing: %s", status.Error)
	} else {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
}

func TestFontConvert_InvalidFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	ttf := loadTestFile(t, "sample.ttf")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "font.ttf")
	part.Write(ttf)
	optJSON, _ := json.Marshal(map[string]any{"targetFormat": "svg"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/font", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		var sub response.SubmitResponse
		json.NewDecoder(resp.Body).Decode(&sub)
		status := waitDone(t, server.URL, sub.JobID)
		if status.Status == "done" {
			t.Fatal("expected .svg format to be rejected, but conversion succeeded")
		}
		t.Logf("Invalid format: correctly failed during processing: %s", status.Error)
	} else {
		t.Logf("Invalid format: rejected at submission with status %d", resp.StatusCode)
	}
}

func TestFontConvert_RejectNonFont(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	optJSON, _ := json.Marshal(map[string]any{"targetFormat": "woff2"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/font", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
