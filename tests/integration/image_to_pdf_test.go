//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

type mergeFile struct {
	name string
	data []byte
}

// submitMerge uploads multiple files to a merge endpoint (e.g. image-to-pdf, pdf merge).
func submitMerge(t *testing.T, serverURL, endpoint string, files []mergeFile, options map[string]any) response.SubmitResponse {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for _, f := range files {
		part, err := writer.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(f.data)
	}

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

// Image to PDF

func TestImageToPDF_SingleJPG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := createTestJPG(t)

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"photo.jpg", jpgData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("image-to-pdf (single JPG) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("single JPG -> PDF: %d bytes", len(result))
}

func TestImageToPDF_SinglePNG(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pngData := createTestPNG(t)

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"image.png", pngData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("image-to-pdf (single PNG) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("single PNG -> PDF: %d bytes", len(result))
}

func TestImageToPDF_MultipleImages(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := createTestJPG(t)
	pngData := createTestPNG(t)

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"photo1.jpg", jpgData},
		{"photo2.png", pngData},
		{"photo3.jpg", jpgData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("image-to-pdf (multiple) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("3 images -> PDF: %d bytes", len(result))
}

func TestImageToPDF_RealFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpg := loadTestFile(t, "sample.jpg")
	png := loadTestFile(t, "sample.png")

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"real_photo.jpg", jpg},
		{"real_image.png", png},
	}, nil)

	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("image-to-pdf (real files) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("real JPG+PNG -> PDF: %d bytes", len(result))
}

func TestImageToPDF_RejectInvalidFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	textData := []byte("this is not an image")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "bad.txt")
	part.Write(textData)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/merge/image-to-pdf", writer.FormDataContentType(), &body)
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

func TestImageToPDF_RejectNoFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/merge/image-to-pdf", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestImageToPDF_OneTimeDownload(t *testing.T) {
	server, _, _ := setupTestServer(t)
	jpgData := createTestJPG(t)

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"photo.jpg", jpgData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	// First download succeeds
	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first download failed: %d", resp.StatusCode)
	}

	// Second download should fail (one-time download)
	resp2, _ := http.Get(server.URL + status.DownloadURL)
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("second download should not succeed")
	}
}

// PDF Merge

func TestPDFMerge_TwoPDFs(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := createTestPDF(t)

	sub := submitMerge(t, server.URL, "/api/merge/pdf", []mergeFile{
		{"doc1.pdf", pdfData},
		{"doc2.pdf", pdfData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("pdf merge failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("2 PDFs merged: %d bytes", len(result))
}

func TestPDFMerge_RealFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdf := loadTestFile(t, "sample.pdf")

	sub := submitMerge(t, server.URL, "/api/merge/pdf", []mergeFile{
		{"doc1.pdf", pdf},
		{"doc2.pdf", pdf},
	}, nil)

	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("pdf merge (real) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not a valid PDF")
	}
	t.Logf("2 real PDFs merged: %d bytes -> %d bytes", len(pdf)*2, len(result))
}

func TestPDFMerge_RejectNoFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/merge/pdf", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPDFMerge_RejectInvalidFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	textData := []byte("this is not a pdf")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "bad.txt")
	part.Write(textData)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/merge/pdf", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPDFMerge_OneTimeDownload(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := createTestPDF(t)

	sub := submitMerge(t, server.URL, "/api/merge/pdf", []mergeFile{
		{"doc1.pdf", pdfData},
		{"doc2.pdf", pdfData},
	}, nil)

	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("expected done, got %s: %s", status.Status, status.Error)
	}

	resp, _ := http.Get(server.URL + status.DownloadURL)
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first download failed: %d", resp.StatusCode)
	}

	resp2, _ := http.Get(server.URL + status.DownloadURL)
	defer resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatal("second download should not succeed")
	}
}
