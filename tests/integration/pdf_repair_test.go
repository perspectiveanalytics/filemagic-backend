//go:build integration

package integration

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

func TestPDFRepair_ValidPDF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "document.pdf", pdfData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF repair (valid PDF) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF repair (valid): %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFRepair_MultipagePDF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "pdf_multipage.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "multipage.pdf", pdfData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF repair (multipage) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF repair (multipage): %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFRepair_CorruptedXref(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// PDF with corrupted xref table — qpdf should attempt recovery
	malformed := buildMalformedPDF(t, "xref_corrupt")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "corrupted.pdf", malformed, nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		if len(result) < 4 || string(result[:4]) != "%PDF" {
			t.Fatal("repaired output is not PDF")
		}
		t.Logf("PDF repair (xref corrupt): recovered, %d bytes -> %d bytes", len(malformed), len(result))
	} else {
		t.Logf("PDF repair (xref corrupt): gracefully failed: %s", status.Error)
	}
}

func TestPDFRepair_CorruptedStream(t *testing.T) {
	server, _, _ := setupTestServer(t)

	malformed := buildMalformedPDF(t, "stream_corrupt")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "corrupted.pdf", malformed, nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		if len(result) < 4 || string(result[:4]) != "%PDF" {
			t.Fatal("repaired output is not PDF")
		}
		t.Logf("PDF repair (stream corrupt): recovered, %d bytes -> %d bytes", len(malformed), len(result))
	} else {
		t.Logf("PDF repair (stream corrupt): gracefully failed: %s", status.Error)
	}
}

func TestPDFRepair_PDFWithJavaScript(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "pdf_javascript.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "js_pdf.pdf", pdfData, nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		result := downloadResult(t, server.URL, status.DownloadURL)
		if len(result) < 4 || string(result[:4]) != "%PDF" {
			t.Fatal("output is not PDF")
		}
		t.Logf("PDF repair (JavaScript): %d bytes -> %d bytes", len(pdfData), len(result))
	} else {
		t.Logf("PDF repair (JavaScript): %s", status.Error)
	}
}

func TestPDFRepair_RejectNonPDF(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/pdf/repair", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPDFRepair_OnlyMagicBytes(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "only_magic_bytes.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/repair", "truncated.pdf", pdfData, nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		t.Log("PDF repair (only magic bytes): unexpectedly succeeded — qpdf may have generated minimal output")
	} else {
		t.Logf("PDF repair (only magic bytes): correctly failed: %s", status.Error)
	}
}
