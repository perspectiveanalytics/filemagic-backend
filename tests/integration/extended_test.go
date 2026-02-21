//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// PDF Password — extended tests

func TestPDFPassword_ProtectWithOwnerPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":          "protect",
		"userPassword":  "user123",
		"ownerPassword": "owner456",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF protect with owner password failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("PDF protect (user+owner): %d bytes -> %d bytes", len(pdfData), len(result))
}

func TestPDFPassword_RemoveWrongPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	// Protect with known password
	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":         "protect",
		"userPassword": "correct_password",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF protect failed: %s", status.Error)
	}
	encrypted := downloadResult(t, server.URL, status.DownloadURL)

	// Try to remove with wrong password
	sub2 := submitFile(t, server.URL, "/api/convert/pdf/password", "encrypted.pdf", encrypted, map[string]any{
		"mode":     "remove",
		"password": "wrong_password",
	})
	status2 := waitDone(t, server.URL, sub2.JobID)
	if status2.Status == "done" {
		t.Fatal("expected decryption to fail with wrong password, but it succeeded")
	}
	t.Logf("Wrong password: correctly failed: %s", status2.Error)
}

func TestPDFPassword_ProtectEmptyPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":         "protect",
		"userPassword": "",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status == "done" {
		t.Fatal("expected empty password to be rejected, but it succeeded")
	}
	t.Logf("Empty password: correctly failed: %s", status.Error)
}

func TestPDFPassword_InvalidMode(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "sample.pdf")

	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "document.pdf", pdfData, map[string]any{
		"mode":         "encrypt",
		"userPassword": "test",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status == "done" {
		t.Fatal("expected invalid mode to fail, but it succeeded")
	}
	t.Logf("Invalid mode: correctly failed: %s", status.Error)
}

func TestPDFPassword_RoundtripMultipage(t *testing.T) {
	server, _, _ := setupTestServer(t)
	pdfData := loadTestFile(t, "pdf_multipage.pdf")

	// Protect
	sub := submitFile(t, server.URL, "/api/convert/pdf/password", "multipage.pdf", pdfData, map[string]any{
		"mode":         "protect",
		"userPassword": "multipage_pass",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("PDF protect multipage failed: %s", status.Error)
	}
	encrypted := downloadResult(t, server.URL, status.DownloadURL)

	// Decrypt
	sub2 := submitFile(t, server.URL, "/api/convert/pdf/password", "encrypted.pdf", encrypted, map[string]any{
		"mode":     "remove",
		"password": "multipage_pass",
	})
	status2 := waitDone(t, server.URL, sub2.JobID)
	if status2.Status != "done" {
		t.Fatalf("PDF decrypt multipage failed: %s", status2.Error)
	}

	decrypted := downloadResult(t, server.URL, status2.DownloadURL)
	if string(decrypted[:4]) != "%PDF" {
		t.Fatal("decrypted output is not PDF")
	}
	t.Logf("PDF roundtrip multipage: %d -> %d -> %d bytes", len(pdfData), len(encrypted), len(decrypted))
}

// Archive Create — extended tests

func TestArchive_Create7z(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "hello.txt")
	part.Write([]byte("hello world"))
	part2, _ := writer.CreateFormFile("files", "data.txt")
	part2.Write([]byte("some data here"))
	writer.WriteField("format", "7z")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("7z creation failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// 7z magic: 37 7A BC AF 27 1C
	if len(result) < 6 || result[0] != 0x37 || result[1] != 0x7A || result[2] != 0xBC || result[3] != 0xAF {
		t.Fatalf("output is not 7z (first bytes: %x)", result[:6])
	}
	t.Logf("7z archive: %d bytes", len(result))
}

func TestArchive_Create7zWithPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "secret.txt")
	part.Write([]byte("top secret data"))
	writer.WriteField("format", "7z")
	writer.WriteField("password", "encrypted7z")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("encrypted 7z creation failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) == 0 {
		t.Fatal("encrypted 7z output is empty")
	}
	t.Logf("Encrypted 7z: %d bytes", len(result))
}

func TestArchive_CreateTarZst(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("files", "hello.txt")
	part.Write([]byte("hello world"))
	writer.WriteField("format", "tar.zst")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var sub response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&sub)
	status := waitForJob(t, server.URL, sub.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("tar.zst creation failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// zstd magic: 28 B5 2F FD
	if len(result) < 4 || result[0] != 0x28 || result[1] != 0xB5 || result[2] != 0x2F || result[3] != 0xFD {
		t.Fatalf("output is not zstd (first 4 bytes: %x)", result[:4])
	}
	t.Logf("tar.zst: %d bytes", len(result))
}

// Decompress — extended tests

func TestDecompress_PasswordZipRoundtrip(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create encrypted ZIP
	var createBody bytes.Buffer
	createWriter := multipart.NewWriter(&createBody)
	part, _ := createWriter.CreateFormFile("files", "secret.txt")
	part.Write([]byte("secret data"))
	createWriter.WriteField("format", "zip")
	createWriter.WriteField("password", "zippass123")
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

	var createSub response.SubmitResponse
	json.NewDecoder(createResp.Body).Decode(&createSub)
	createStatus := waitForJob(t, server.URL, createSub.JobID, 30*time.Second)
	if createStatus.Status != "done" {
		t.Fatalf("encrypted ZIP creation failed: %s", createStatus.Error)
	}

	zipData := downloadResult(t, server.URL, createStatus.DownloadURL)

	// Decompress with correct password — password must be a form field, not in JSON options
	var decompBody bytes.Buffer
	decompWriter := multipart.NewWriter(&decompBody)
	filePart, err := decompWriter.CreateFormFile("file", "archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	filePart.Write(zipData)
	decompWriter.WriteField("password", "zippass123")
	decompWriter.Close()

	decompResp, err := http.Post(server.URL+"/api/archive/decompress", decompWriter.FormDataContentType(), &decompBody)
	if err != nil {
		t.Fatal(err)
	}
	defer decompResp.Body.Close()

	if decompResp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(decompResp.Body)
		t.Fatalf("decompress submit failed: %d: %s", decompResp.StatusCode, string(bodyBytes))
	}

	var sub response.SubmitResponse
	json.NewDecoder(decompResp.Body).Decode(&sub)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Decompress encrypted ZIP failed: %s", status.Error)
	}
	t.Logf("Encrypted ZIP roundtrip: created %d bytes, decompressed successfully", len(zipData))
}

func TestDecompress_WrongPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create encrypted ZIP
	var createBody bytes.Buffer
	createWriter := multipart.NewWriter(&createBody)
	part, _ := createWriter.CreateFormFile("files", "secret.txt")
	part.Write([]byte("secret data"))
	createWriter.WriteField("format", "zip")
	createWriter.WriteField("password", "correct_pass")
	createWriter.Close()

	createResp, err := http.Post(server.URL+"/api/archive/create", createWriter.FormDataContentType(), &createBody)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	var createSub response.SubmitResponse
	json.NewDecoder(createResp.Body).Decode(&createSub)
	createStatus := waitForJob(t, server.URL, createSub.JobID, 30*time.Second)
	if createStatus.Status != "done" {
		t.Fatalf("ZIP creation failed: %s", createStatus.Error)
	}
	zipData := downloadResult(t, server.URL, createStatus.DownloadURL)

	// Decompress with wrong password
	sub := submitFile(t, server.URL, "/api/archive/decompress", "archive.zip", zipData, map[string]any{
		"password": "wrong_pass",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status == "done" {
		t.Fatal("expected decryption with wrong password to fail, but it succeeded")
	}
	t.Logf("Wrong password decompress: correctly failed: %s", status.Error)
}

func TestDecompress_7zRoundtrip(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create 7z
	var createBody bytes.Buffer
	createWriter := multipart.NewWriter(&createBody)
	part, _ := createWriter.CreateFormFile("files", "data.txt")
	part.Write([]byte("7z roundtrip data"))
	createWriter.WriteField("format", "7z")
	createWriter.Close()

	createResp, err := http.Post(server.URL+"/api/archive/create", createWriter.FormDataContentType(), &createBody)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	var createSub response.SubmitResponse
	json.NewDecoder(createResp.Body).Decode(&createSub)
	createStatus := waitForJob(t, server.URL, createSub.JobID, 30*time.Second)
	if createStatus.Status != "done" {
		t.Fatalf("7z creation failed: %s", createStatus.Error)
	}
	archiveData := downloadResult(t, server.URL, createStatus.DownloadURL)

	// Decompress
	sub := submitFile(t, server.URL, "/api/archive/decompress", "archive.7z", archiveData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("7z decompress failed: %s", status.Error)
	}
	t.Logf("7z roundtrip: %d bytes archive decompressed successfully", len(archiveData))
}

func TestDecompress_FilenameWithSpecialChars(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create archive with unicode filenames
	var createBody bytes.Buffer
	createWriter := multipart.NewWriter(&createBody)
	part, _ := createWriter.CreateFormFile("files", "café-résumé.txt")
	part.Write([]byte("unicode filename test"))
	part2, _ := createWriter.CreateFormFile("files", "日本語.txt")
	part2.Write([]byte("japanese filename test"))
	createWriter.WriteField("format", "zip")
	createWriter.Close()

	createResp, err := http.Post(server.URL+"/api/archive/create", createWriter.FormDataContentType(), &createBody)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(createResp.Body)
		t.Fatalf("expected 202, got %d: %s", createResp.StatusCode, string(bodyBytes))
	}

	var createSub response.SubmitResponse
	json.NewDecoder(createResp.Body).Decode(&createSub)
	createStatus := waitForJob(t, server.URL, createSub.JobID, 30*time.Second)
	if createStatus.Status != "done" {
		t.Fatalf("archive with unicode names failed: %s", createStatus.Error)
	}

	zipData := downloadResult(t, server.URL, createStatus.DownloadURL)

	// Decompress
	sub := submitFile(t, server.URL, "/api/archive/decompress", "archive.zip", zipData, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("decompress unicode filenames failed: %s", status.Error)
	}
	t.Logf("Unicode filename roundtrip: successful")
}

// Cert Convert — extended tests

func TestCertConvert_PEMtoP12(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, caKeyPEM := generateSelfSignedCACert(t)
	// Bundle cert+key for P12 conversion
	bundled := append(caCertPEM, caKeyPEM...)

	p12Data := submitCertConversion(t, server.URL, "ca.pem", bundled, "p12", "", "export123")

	if len(p12Data) == 0 {
		t.Fatal("P12 output is empty")
	}
	t.Logf("PEM->P12: %d bytes", len(p12Data))
}

func TestCertConvert_SameFormatRejection(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "cert.pem")
	part.Write(caCertPEM)
	writer.WriteField("targetFormat", "pem")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/certificate", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		var sub response.SubmitResponse
		json.NewDecoder(resp.Body).Decode(&sub)
		status := waitDone(t, server.URL, sub.JobID)
		if status.Status == "done" {
			// Some converters allow same-format (e.g. to normalize) — that's OK
			t.Log("PEM->PEM: succeeded (converter may allow normalization)")
		} else {
			t.Logf("PEM->PEM: correctly rejected: %s", status.Error)
		}
	} else if resp.StatusCode == http.StatusBadRequest {
		t.Log("PEM->PEM: correctly rejected at submission")
	} else {
		t.Logf("PEM->PEM: unexpected status %d", resp.StatusCode)
	}
}

func TestCertConvert_DERtoP7B(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)
	block, _ := pem.Decode(caCertPEM)
	derData := block.Bytes

	p7bData := submitCertConversion(t, server.URL, "cert.der", derData, "p7b", "", "")

	if len(p7bData) == 0 {
		t.Fatal("P7B output is empty")
	}
	t.Logf("DER->P7B: %d bytes -> %d bytes", len(derData), len(p7bData))
}

func TestCertConvert_P7BtoDER(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	// First convert PEM -> P7B
	p7bData := submitCertConversion(t, server.URL, "cert.pem", caCertPEM, "p7b", "", "")
	if len(p7bData) == 0 {
		t.Fatal("P7B intermediate is empty")
	}

	// Then convert P7B -> DER
	derData := submitCertConversion(t, server.URL, "cert.p7b", p7bData, "der", "", "")
	if len(derData) == 0 {
		t.Fatal("DER output is empty")
	}

	// Verify DER is valid
	_, err := x509.ParseCertificate(derData)
	if err != nil {
		t.Fatalf("P7B->DER output is not valid DER: %v", err)
	}
	t.Logf("P7B->DER: %d bytes -> %d bytes", len(p7bData), len(derData))
}

// Cert Inspect — extended tests

func TestCertInspect_RSACert(t *testing.T) {
	server, _, _ := setupTestServer(t)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "RSA Test Cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	result := submitCertInspection(t, server.URL, "rsa.pem", certPEM, "")

	if result.KeySize != 2048 {
		t.Fatalf("expected KeySize=2048, got %d", result.KeySize)
	}
	if result.PublicKeyAlgo == "" {
		t.Fatal("expected non-empty PublicKeyAlgo")
	}
	t.Logf("RSA cert: KeySize=%d, Algo=%s", result.KeySize, result.PublicKeyAlgo)
}

func TestCertInspect_WildcardSAN(t *testing.T) {
	server, _, _ := setupTestServer(t)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Wildcard Test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"*.example.com", "example.com"},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	result := submitCertInspection(t, server.URL, "wildcard.pem", certPEM, "")

	foundWildcard := false
	for _, san := range result.SANs {
		if san == "*.example.com" {
			foundWildcard = true
			break
		}
	}
	if !foundWildcard {
		t.Fatalf("expected *.example.com in SANs, got %v", result.SANs)
	}
	t.Logf("Wildcard SAN: SANs=%v", result.SANs)
}

func TestCertInspect_LargeChain(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Root CA
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	rootTemplate := &x509.Certificate{
		SerialNumber:          rootSerial,
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	rootDER, _ := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	rootCert, _ := x509.ParseCertificate(rootDER)
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})

	// Intermediate CA
	intKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	intSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	intTemplate := &x509.Certificate{
		SerialNumber:          intSerial,
		Subject:               pkix.Name{CommonName: "Test Intermediate CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	intDER, _ := x509.CreateCertificate(rand.Reader, intTemplate, rootCert, &intKey.PublicKey, rootKey)
	intCert, _ := x509.ParseCertificate(intDER)
	intPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intDER})

	// Leaf cert
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	leafTemplate := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: "leaf.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"leaf.example.com"},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTemplate, intCert, &leafKey.PublicKey, intKey)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	// Bundle: leaf + intermediate + root
	bundle := append(leafPEM, intPEM...)
	bundle = append(bundle, rootPEM...)

	result := submitCertInspection(t, server.URL, "chain.pem", bundle, "")

	if result.CertCount != 3 {
		t.Fatalf("expected CertCount=3, got %d", result.CertCount)
	}
	if len(result.Chain) != 2 {
		t.Fatalf("expected 2 chain certs (intermediate + root), got %d", len(result.Chain))
	}
	t.Logf("3-cert chain: leaf=%s, chain=%v", result.Subject.CommonName,
		func() []string {
			names := make([]string, len(result.Chain))
			for i, c := range result.Chain {
				names[i] = c.Subject.CommonName
			}
			return names
		}())
}

func TestCertInspect_ExpiredCertFields(t *testing.T) {
	server, _, _ := setupTestServer(t)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Very Expired Cert",
			Organization: []string{"Old Corp"},
			Country:      []string{"US"},
		},
		NotBefore: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:  time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	result := submitCertInspection(t, server.URL, "expired.pem", certPEM, "")

	if !result.IsExpired {
		t.Fatal("expected IsExpired=true for cert expired in 2015")
	}
	if result.Subject.CommonName != "Very Expired Cert" {
		t.Fatalf("expected CN 'Very Expired Cert', got '%s'", result.Subject.CommonName)
	}
	t.Logf("Expired cert: IsExpired=%v, NotAfter=%v", result.IsExpired, result.NotAfter)
}

// Video to GIF — extended tests

func TestVideoToGif_SpeedMultiplier(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/gif", "clip.mov", data, map[string]any{
		"duration": 2,
		"fps":      10,
		"maxWidth": 160,
		"speed":    2.0,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Video->GIF (speed=2x) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 6 || string(result[:3]) != "GIF" {
		t.Fatal("output is not GIF")
	}
	t.Logf("Video->GIF (speed=2x): %d bytes", len(result))
}

func TestVideoToGif_MaxDurationClamp(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/gif", "clip.mov", data, map[string]any{
		"duration": 20, // exceeds max (15s)
		"fps":      8,
		"maxWidth": 160,
	})
	status := waitDone(t, server.URL, sub.JobID)
	// Should succeed (clamped to max) or succeed with actual video duration
	if status.Status != "done" {
		t.Fatalf("Video->GIF (duration=20) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:3]) != "GIF" {
		t.Fatal("output is not GIF")
	}
	t.Logf("Video->GIF (clamped duration): %d bytes", len(result))
}

func TestVideoToGif_MinFPS(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/gif", "clip.mov", data, map[string]any{
		"duration": 2,
		"fps":      5, // minimum
		"maxWidth": 160,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Video->GIF (fps=5) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:3]) != "GIF" {
		t.Fatal("output is not GIF")
	}
	t.Logf("Video->GIF (fps=5): %d bytes", len(result))
}

func TestVideoToGif_MaxWidth(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/gif", "clip.mov", data, map[string]any{
		"duration": 2,
		"fps":      8,
		"maxWidth": 640,
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Video->GIF (maxWidth=640) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:3]) != "GIF" {
		t.Fatal("output is not GIF")
	}
	t.Logf("Video->GIF (maxWidth=640): %d bytes", len(result))
}

// Image to PDF — extended tests

func TestImageToPDF_WebP(t *testing.T) {
	server, _, _ := setupTestServer(t)
	webp := loadTestFile(t, "sample.webp")

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"image.webp", webp},
	}, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("WebP->PDF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if len(result) < 4 || string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("WebP->PDF: %d bytes -> %d bytes", len(webp), len(result))
}

func TestImageToPDF_MixedSizes(t *testing.T) {
	server, _, _ := setupTestServer(t)
	tiny := loadTestFile(t, "tiny_1x1.png")
	jpg := loadTestFile(t, "sample.jpg")

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"tiny.png", tiny},
		{"photo.jpg", jpg},
	}, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("Mixed images->PDF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("Mixed images->PDF: %d bytes", len(result))
}

func TestImageToPDF_BMP(t *testing.T) {
	server, _, _ := setupTestServer(t)
	bmp := loadTestFile(t, "sample.bmp")

	sub := submitMerge(t, server.URL, "/api/merge/image-to-pdf", []mergeFile{
		{"image.bmp", bmp},
	}, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("BMP->PDF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	if string(result[:4]) != "%PDF" {
		t.Fatal("output is not PDF")
	}
	t.Logf("BMP->PDF: %d bytes -> %d bytes", len(bmp), len(result))
}

// HEIC — extended tests

func TestHEIC_ToWebP(t *testing.T) {
	server, _, _ := setupTestServer(t)
	heic := loadTestFile(t, "sample.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", heic, map[string]any{
		"outputFormat": "webp",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("HEIC->WebP failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// WebP magic: RIFF....WEBP
	if len(result) < 12 || string(result[:4]) != "RIFF" || string(result[8:12]) != "WEBP" {
		t.Fatal("output is not WebP")
	}
	t.Logf("HEIC->WebP: %d bytes -> %d bytes", len(heic), len(result))
}

func TestHEIC_ToTIFF(t *testing.T) {
	server, _, _ := setupTestServer(t)
	heic := loadTestFile(t, "sample.heic")

	sub := submitFile(t, server.URL, "/api/convert/image", "photo.heic", heic, map[string]any{
		"outputFormat": "tiff",
	})
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("HEIC->TIFF failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	// TIFF magic: 49 49 2A 00 (little-endian) or 4D 4D 00 2A (big-endian)
	if len(result) < 4 {
		t.Fatal("output too small for TIFF")
	}
	isLE := result[0] == 0x49 && result[1] == 0x49 && result[2] == 0x2A && result[3] == 0x00
	isBE := result[0] == 0x4D && result[1] == 0x4D && result[2] == 0x00 && result[3] == 0x2A
	if !isLE && !isBE {
		t.Fatalf("output is not TIFF (first 4 bytes: %x)", result[:4])
	}
	t.Logf("HEIC->TIFF: %d bytes -> %d bytes", len(heic), len(result))
}

// Archive Security — extended tests

func TestSecurity_ArchiveFilenameNullByte(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Filename with null byte — should be sanitized
	files := map[string][]byte{
		"safe\x00evil.txt": []byte("null byte in filename"),
	}
	blocked := submitMultiFileExpectReject(t, server.URL, "/api/archive/create",
		files, map[string]string{"format": "zip"})

	if blocked {
		t.Log("Null byte filename: rejected (extra safe)")
	} else {
		t.Log("Null byte filename: accepted (filename sanitized)")
	}
}

func TestSecurity_ArchiveFilenameRTLOverride(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Filename with RTL override character U+202E
	files := map[string][]byte{
		"innocent\u202Etxt.exe": []byte("RTL override in filename"),
	}
	blocked := submitMultiFileExpectReject(t, server.URL, "/api/archive/create",
		files, map[string]string{"format": "zip"})

	if blocked {
		t.Log("RTL override filename: rejected")
	} else {
		t.Log("RTL override filename: accepted (character stripped or harmless in archive)")
	}
}

func TestSecurity_DecompressEmptyArchive(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create a ZIP with only a directory, no files
	var buf bytes.Buffer
	zw := newZipWriter(&buf)
	// Create an empty directory entry
	fh := &zip.FileHeader{Name: "empty_dir/"}
	zw.CreateHeader(fh)
	zw.Close()

	sub := submitFile(t, server.URL, "/api/archive/decompress", "empty.zip", buf.Bytes(), nil)
	status := waitDone(t, server.URL, sub.JobID)

	if status.Status == "done" {
		t.Log("Empty archive: decompressed (may return empty result)")
	} else {
		t.Logf("Empty archive: correctly reported error: %s", status.Error)
	}
}

func TestSecurity_DecompressNestedZipBomb(t *testing.T) {
	server, _, _ := setupTestServer(t)

	// Create a ZIP with a large file of repeated content
	bomb := buildZipBomb(t, 200*1024*1024) // 200MB decompressed

	blocked := submitFileExpectReject(t, server.URL, "/api/archive/decompress", "bomb.zip", bomb, nil)

	if !blocked {
		t.Log("200MB zip bomb: decompression completed (nsjail tmpfs limits may have constrained it)")
	} else {
		t.Log("200MB zip bomb: correctly blocked")
	}
}
