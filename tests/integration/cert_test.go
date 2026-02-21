//go:build integration

package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/inspector"
	"github.com/perspectiveanalytics/filemagic-backend/internal/response"
)

// generateSelfSignedCACert creates a self-signed CA certificate + key in PEM format.
func generateSelfSignedCACert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "FileMagic Test CA",
			Organization: []string{"FileMagic Tests"},
			Country:      []string{"FR"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}

// generateServerCert creates a server certificate signed by the given CA.
func generateServerCert(t *testing.T, caCertPEM, caKeyPEM []byte) (certPEM, keyPEM []byte) {
	t.Helper()

	block, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	block, _ = pem.Decode(caKeyPEM)
	caKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "test.filemagic.app",
			Organization: []string{"FileMagic Tests"},
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"test.filemagic.app", "localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}

// submitCertInspection uploads a cert file and returns the inspection result.
func submitCertInspection(t *testing.T, serverURL, filename string, fileData []byte, password string) *inspector.CertificateInfo {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(fileData)

	if password != "" {
		writer.WriteField("password", password)
	}

	writer.Close()

	resp, err := http.Post(serverURL+"/api/inspect/certificate", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result inspector.CertificateInfo
	json.NewDecoder(resp.Body).Decode(&result)
	return &result
}

// submitCertConversion submits a cert for conversion and waits for result.
func submitCertConversion(t *testing.T, serverURL, filename string, fileData []byte, targetFormat, password, outputPassword string) []byte {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(fileData)

	writer.WriteField("targetFormat", targetFormat)
	if password != "" {
		writer.WriteField("password", password)
	}
	if outputPassword != "" {
		writer.WriteField("outputPassword", outputPassword)
	}

	writer.Close()

	resp, err := http.Post(serverURL+"/api/convert/certificate", writer.FormDataContentType(), &body)
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

	status := waitForJob(t, serverURL, submit.JobID, 15*time.Second)
	if status.Status != "done" {
		t.Fatalf("cert conversion failed: %s: %s", status.Status, status.Error)
	}

	dlResp, err := http.Get(serverURL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("download failed: %d", dlResp.StatusCode)
	}

	data, _ := io.ReadAll(dlResp.Body)
	return data
}

func TestCertInspectPEM(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	result := submitCertInspection(t, server.URL, "ca.pem", caCertPEM, "")

	if result.Subject.CommonName != "FileMagic Test CA" {
		t.Fatalf("expected CN 'FileMagic Test CA', got '%s'", result.Subject.CommonName)
	}
	if result.Format != "PEM" {
		t.Fatalf("expected format PEM, got '%s'", result.Format)
	}
	if !result.IsCA {
		t.Fatal("expected IsCA to be true")
	}
	if !result.IsSelfSigned {
		t.Fatal("expected IsSelfSigned to be true")
	}
	if result.KeySize != 256 {
		t.Fatalf("expected key size 256 (ECDSA P-256), got %d", result.KeySize)
	}
	if result.Fingerprints.SHA256 == "" {
		t.Fatal("expected non-empty SHA256 fingerprint")
	}
}

func TestCertInspectServerCert(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, caKeyPEM := generateSelfSignedCACert(t)
	serverCertPEM, _ := generateServerCert(t, caCertPEM, caKeyPEM)

	result := submitCertInspection(t, server.URL, "server.crt", serverCertPEM, "")

	if result.Subject.CommonName != "test.filemagic.app" {
		t.Fatalf("expected CN 'test.filemagic.app', got '%s'", result.Subject.CommonName)
	}
	if result.IsCA {
		t.Fatal("expected IsCA to be false")
	}
	if result.IsSelfSigned {
		t.Fatal("expected IsSelfSigned to be false for CA-signed cert")
	}
	if len(result.SANs) < 2 {
		t.Fatalf("expected at least 2 SANs, got %d", len(result.SANs))
	}
	if len(result.ExtKeyUsage) == 0 {
		t.Fatal("expected ExtKeyUsage to contain Server Authentication")
	}
}

func TestCertInspectPEMChain(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, caKeyPEM := generateSelfSignedCACert(t)
	serverCertPEM, _ := generateServerCert(t, caCertPEM, caKeyPEM)

	// Bundle: server cert + CA cert
	bundle := append(serverCertPEM, caCertPEM...)

	result := submitCertInspection(t, server.URL, "chain.pem", bundle, "")

	if result.CertCount != 2 {
		t.Fatalf("expected 2 certs in chain, got %d", result.CertCount)
	}
	if len(result.Chain) != 1 {
		t.Fatalf("expected 1 chain cert (CA), got %d", len(result.Chain))
	}
	if result.Chain[0].Subject.CommonName != "FileMagic Test CA" {
		t.Fatalf("expected chain cert CN 'FileMagic Test CA', got '%s'", result.Chain[0].Subject.CommonName)
	}
}

func TestCertInspectInvalidFile(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "garbage.pem")
	part.Write([]byte("this is not a certificate"))
	writer.Close()

	resp, err := http.Post(server.URL+"/api/inspect/certificate", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCertConvertPEMtoDER(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	derData := submitCertConversion(t, server.URL, "ca.pem", caCertPEM, "der", "", "")

	// Verify DER is valid by parsing it
	cert, err := x509.ParseCertificate(derData)
	if err != nil {
		t.Fatalf("output is not valid DER: %v", err)
	}
	if cert.Subject.CommonName != "FileMagic Test CA" {
		t.Fatalf("expected CN 'FileMagic Test CA', got '%s'", cert.Subject.CommonName)
	}
}

func TestCertConvertDERtoPEM(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	// First get DER version
	block, _ := pem.Decode(caCertPEM)
	derData := block.Bytes

	pemResult := submitCertConversion(t, server.URL, "ca.der", derData, "pem", "", "")

	// Verify PEM is valid
	block, _ = pem.Decode(pemResult)
	if block == nil {
		t.Fatal("output is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("PEM content is not valid cert: %v", err)
	}
	if cert.Subject.CommonName != "FileMagic Test CA" {
		t.Fatalf("expected CN 'FileMagic Test CA', got '%s'", cert.Subject.CommonName)
	}
}

func TestCertConvertPEMtoP7B(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	p7bData := submitCertConversion(t, server.URL, "ca.pem", caCertPEM, "p7b", "", "")

	if len(p7bData) == 0 {
		t.Fatal("empty P7B output")
	}
}

func TestCertConvertRoundtripPEMDER(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)

	// PEM → DER
	derData := submitCertConversion(t, server.URL, "ca.pem", caCertPEM, "der", "", "")

	// DER → PEM
	pemResult := submitCertConversion(t, server.URL, "ca.der", derData, "pem", "", "")

	// Parse both and compare serial numbers
	origBlock, _ := pem.Decode(caCertPEM)
	origCert, _ := x509.ParseCertificate(origBlock.Bytes)

	resultBlock, _ := pem.Decode(pemResult)
	if resultBlock == nil {
		t.Fatal("roundtrip result is not valid PEM")
	}
	resultCert, _ := x509.ParseCertificate(resultBlock.Bytes)

	if origCert.SerialNumber.Cmp(resultCert.SerialNumber) != 0 {
		t.Fatal("serial numbers don't match after roundtrip")
	}
}

func TestCertConvertInvalidFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.pem")
	part.Write([]byte("some data"))
	writer.WriteField("targetFormat", "invalid")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/certificate", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestCertConvertUnsupportedExtension(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("some data"))
	writer.WriteField("targetFormat", "der")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/certificate", writer.FormDataContentType(), &body)
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

func TestArchiveCreateZip(t *testing.T) {
	server, _, _ := setupTestServer(t)

	pngData := createTestPNG(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, _ := writer.CreateFormFile("files", "image.png")
	part.Write(pngData)

	part2, _ := writer.CreateFormFile("files", "data.txt")
	part2.Write([]byte("hello world"))

	writer.WriteField("format", "zip")
	writer.WriteField("password", "testpassword123")
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

	var submit response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&submit)

	status := waitForJob(t, server.URL, submit.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("archive creation failed: %s: %s", status.Status, status.Error)
	}

	dlResp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()

	data, _ := io.ReadAll(dlResp.Body)
	// ZIP files start with PK (0x50 0x4B)
	if len(data) < 2 || data[0] != 0x50 || data[1] != 0x4B {
		t.Fatal("output is not a valid ZIP file")
	}
}

func TestArchiveCreateTarGz(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, _ := writer.CreateFormFile("files", "hello.txt")
	part.Write([]byte("hello world"))

	writer.WriteField("format", "tar.gz")
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

	var submit response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&submit)

	status := waitForJob(t, server.URL, submit.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("tar.gz creation failed: %s: %s", status.Status, status.Error)
	}

	dlResp, err := http.Get(server.URL + status.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dlResp.Body.Close()
	data, _ := io.ReadAll(dlResp.Body)

	// Gzip files start with 0x1F 0x8B
	if len(data) < 2 || data[0] != 0x1F || data[1] != 0x8B {
		t.Fatal("output is not a valid gzip file")
	}
}

func TestArchiveZipWithoutPassword(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, _ := writer.CreateFormFile("files", "hello.txt")
	part.Write([]byte("hello world"))

	writer.WriteField("format", "zip")
	// No password — should succeed (unencrypted ZIP)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
}

func TestArchiveNoFiles(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("format", "tar.gz")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestArchiveInvalidFormat(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, _ := writer.CreateFormFile("files", "hello.txt")
	part.Write([]byte("hello"))
	writer.WriteField("format", "rar")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid format, got %d", resp.StatusCode)
	}
}

func TestCertInspectExpiredCert(t *testing.T) {
	server, _, _ := setupTestServer(t)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Expired Cert",
		},
		NotBefore: time.Now().Add(-48 * time.Hour),
		NotAfter:  time.Now().Add(-24 * time.Hour), // expired yesterday
	}

	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	result := submitCertInspection(t, server.URL, "expired.pem", certPEM, "")

	if !result.IsExpired {
		t.Fatal("expected IsExpired to be true")
	}
	if result.Subject.CommonName != "Expired Cert" {
		t.Fatalf("expected CN 'Expired Cert', got '%s'", result.Subject.CommonName)
	}
}

func TestCertInspectDER(t *testing.T) {
	server, _, _ := setupTestServer(t)

	caCertPEM, _ := generateSelfSignedCACert(t)
	block, _ := pem.Decode(caCertPEM)

	result := submitCertInspection(t, server.URL, "ca.der", block.Bytes, "")

	if result.Format != "DER" {
		t.Fatalf("expected format DER, got '%s'", result.Format)
	}
	if result.Subject.CommonName != "FileMagic Test CA" {
		t.Fatalf("expected CN 'FileMagic Test CA', got '%s'", result.Subject.CommonName)
	}
}

func TestArchiveDuplicateFilenames(t *testing.T) {
	server, _, _ := setupTestServer(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Two files with same name
	part, _ := writer.CreateFormFile("files", "data.txt")
	part.Write([]byte("file 1"))
	part2, _ := writer.CreateFormFile("files", "data.txt")
	part2.Write([]byte("file 2"))

	writer.WriteField("format", "tar.gz")
	writer.Close()

	resp, err := http.Post(server.URL+"/api/archive/create", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should succeed — handler renames duplicates
	if resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var submit response.SubmitResponse
	json.NewDecoder(resp.Body).Decode(&submit)

	status := waitForJob(t, server.URL, submit.JobID, 30*time.Second)
	if status.Status != "done" {
		t.Fatalf("archive with duplicates failed: %s: %s", status.Status, status.Error)
	}
}

// Suppress unused import warning
var _ = fmt.Sprintf
