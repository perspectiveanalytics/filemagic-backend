//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// createMinimalEPUB builds a valid EPUB3 in-memory (ZIP with mimetype stored
// first, META-INF/container.xml, content.opf, nav.xhtml, and chapter.xhtml).
func createMinimalEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype must be first and stored (no compression)
	fh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("application/epub+zip"))

	// META-INF/container.xml
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// OEBPS/content.opf
	w, _ = zw.Create("OEBPS/content.opf")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:12345678-1234-1234-1234-123456789abc</dc:identifier>
    <dc:title>Test EPUB Book</dc:title>
    <dc:language>en</dc:language>
    <dc:creator>Test Author</dc:creator>
    <meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="chapter2" href="chapter2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chapter1"/>
    <itemref idref="chapter2"/>
  </spine>
</package>`))

	// OEBPS/nav.xhtml — navigation document (required for EPUB3)
	w, _ = zw.Create("OEBPS/nav.xhtml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Table of Contents</title></head>
<body>
  <nav epub:type="toc" id="toc">
    <h1>Table of Contents</h1>
    <ol>
      <li><a href="chapter1.xhtml">Chapter 1: Introduction</a></li>
      <li><a href="chapter2.xhtml">Chapter 2: Details</a></li>
    </ol>
  </nav>
</body>
</html>`))

	// OEBPS/chapter1.xhtml
	w, _ = zw.Create("OEBPS/chapter1.xhtml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 1</title></head>
<body>
  <h1>Chapter 1: Introduction</h1>
  <p>This is the first chapter of the test EPUB document. It has been created for
  integration testing of the ebook conversion service.</p>
  <p>The document contains multiple chapters to ensure that the conversion process
  handles multi-section books correctly.</p>
  <p>Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor
  incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud
  exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.</p>
</body>
</html>`))

	// OEBPS/chapter2.xhtml
	w, _ = zw.Create("OEBPS/chapter2.xhtml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 2</title></head>
<body>
  <h1>Chapter 2: Details</h1>
  <p>This is the second chapter. It provides additional content to make the EPUB
  more substantial for testing various output formats.</p>
  <p>Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore
  eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in
  culpa qui officia deserunt mollit anim id est laborum.</p>
</body>
</html>`))

	zw.Close()
	return buf.Bytes()
}

func TestEbookConvert_RejectPDF(t *testing.T) {
	// PDF output requires Qt WebEngine + /proc mount which is incompatible
	// with systemd ProtectSystem=strict. PDF is not a supported ebook output.
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "book.epub")
	part.Write(epub)
	optJSON, _ := json.Marshal(map[string]any{"targetFormat": "pdf"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/ebook", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected PDF to be rejected with 400, got %d", resp.StatusCode)
	}
	t.Log("PDF format correctly rejected")
}

func TestEbookConvert_EPUBtoTXT(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "txt",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->TXT failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("TXT output is empty")
	}
	t.Logf("EPUB->TXT: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_EPUBtoHTMLZ(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "htmlz",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->HTMLZ failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// HTMLZ is a ZIP container
	if len(result) < 4 || result[0] != 0x50 || result[1] != 0x4B {
		t.Fatal("output is not a ZIP container (HTMLZ)")
	}
	t.Logf("EPUB->HTMLZ: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_EPUBtoDOCX(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "docx",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->DOCX failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// DOCX is a ZIP container
	if len(result) < 4 || result[0] != 0x50 || result[1] != 0x4B {
		t.Fatal("output is not a ZIP container (DOCX)")
	}
	t.Logf("EPUB->DOCX: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_EPUBtoMOBI(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "mobi",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->MOBI failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("MOBI output is empty")
	}
	t.Logf("EPUB->MOBI: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_EPUBtoAZW3(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "azw3",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->AZW3 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("AZW3 output is empty")
	}
	t.Logf("EPUB->AZW3: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_EPUBtoFB2(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	sub := submitFile(t, server.URL, "/api/convert/ebook", "book.epub", epub, map[string]any{
		"targetFormat": "fb2",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("EPUB->FB2 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if !bytes.Contains(result, []byte("<FictionBook")) && !bytes.Contains(result, []byte("FictionBook")) {
		t.Fatal("output does not contain FictionBook tag")
	}
	t.Logf("EPUB->FB2: %d bytes -> %d bytes", len(epub), len(result))
}

func TestEbookConvert_MissingFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "book.epub")
	part.Write(epub)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/ebook", writer.FormDataContentType(), &body)
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

func TestEbookConvert_InvalidFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)
	epub := createMinimalEPUB(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "book.epub")
	part.Write(epub)
	optJSON, _ := json.Marshal(map[string]any{"targetFormat": "exe"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/ebook", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		// Job was accepted — check if it fails during processing
		var sub response.SubmitResponse
		json.NewDecoder(resp.Body).Decode(&sub)
		status := waitDone(t, server.URL, sub.JobID)
		if status.Status == "done" {
			t.Fatal("expected .exe format to be rejected, but conversion succeeded")
		}
		t.Logf("Invalid format: correctly failed during processing: %s", status.Error)
	} else {
		t.Logf("Invalid format: rejected at submission with status %d", resp.StatusCode)
	}
}

func TestEbookConvert_RejectNonEbook(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(createTestPNG(t))
	optJSON, _ := json.Marshal(map[string]any{"targetFormat": "pdf"})
	writer.WriteField("options", string(optJSON))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/ebook", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

var _ time.Duration
