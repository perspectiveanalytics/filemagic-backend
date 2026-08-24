package inspector

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"

	"go.mozilla.org/pkcs7"
	"software.sslmate.com/src/go-pkcs12"
)

type SubjectInfo struct {
	CommonName         string   `json:"commonName,omitempty"`
	Organization       []string `json:"organization,omitempty"`
	OrganizationalUnit []string `json:"organizationalUnit,omitempty"`
	Country            []string `json:"country,omitempty"`
	Province           []string `json:"province,omitempty"`
	Locality           []string `json:"locality,omitempty"`
}

type Fingerprints struct {
	SHA256 string `json:"sha256"`
	SHA1   string `json:"sha1"`
}

type CertSummary struct {
	Subject      SubjectInfo `json:"subject"`
	Issuer       SubjectInfo `json:"issuer"`
	SerialNumber string      `json:"serialNumber"`
	NotBefore    time.Time   `json:"notBefore"`
	NotAfter     time.Time   `json:"notAfter"`
	IsExpired    bool        `json:"isExpired"`
	IsCA         bool        `json:"isCA"`
}

type CertificateInfo struct {
	Subject       SubjectInfo `json:"subject"`
	Issuer        SubjectInfo `json:"issuer"`
	SerialNumber  string      `json:"serialNumber"`
	NotBefore     time.Time   `json:"notBefore"`
	NotAfter      time.Time   `json:"notAfter"`
	IsExpired     bool        `json:"isExpired"`
	SignatureAlgo string      `json:"signatureAlgorithm"`
	PublicKeyAlgo string      `json:"publicKeyAlgorithm"`
	KeySize       int         `json:"keySize"`
	SANs          []string    `json:"sans,omitempty"`
	IsCA          bool        `json:"isCA"`
	IsSelfSigned  bool        `json:"isSelfSigned"`
	IsTrusted     bool        `json:"isTrusted"`
	KeyUsage      []string    `json:"keyUsage,omitempty"`
	ExtKeyUsage   []string    `json:"extKeyUsage,omitempty"`
	Fingerprints  Fingerprints `json:"fingerprints"`
	Format        string      `json:"format"`
	CertCount     int         `json:"certCount"`
	Chain         []CertSummary `json:"chain,omitempty"`
}

// ParseCertificate parses certificate data and returns structured information.
// filename is used for format detection. password is for P12/PFX files.
//
// The recover guards against panics in the third-party BER/PKCS parsers
// (go.mozilla.org/pkcs7 indexes past malformed input): this data is
// attacker-controlled, so a panic here must not take down the process.
func ParseCertificate(data []byte, filename string, password string) (info *CertificateInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			info = nil
			err = fmt.Errorf("malformed certificate data")
		}
	}()

	format := detectFormat(data, filename)

	var certs []*x509.Certificate

	switch format {
	case "PEM":
		certs, err = parsePEM(data)
	case "DER":
		certs, err = parseDER(data)
	case "P12":
		certs, err = parseP12(data, password)
	case "P7B":
		certs, err = parseP7B(data)
	case "CSR":
		return parseCSR(data)
	default:
		return nil, fmt.Errorf("unsupported certificate format")
	}

	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found in file")
	}

	primary := certs[0]
	info = buildCertInfo(primary, format)
	info.CertCount = len(certs)

	if len(certs) > 1 {
		for _, c := range certs[1:] {
			info.Chain = append(info.Chain, buildCertSummary(c))
		}
	}

	return info, nil
}

func detectFormat(data []byte, filename string) string {
	lower := strings.ToLower(filename)

	// Check PEM first — it's text-based and easy to detect
	if isPEM(data) {
		if containsCSR(data) {
			return "CSR"
		}
		return "PEM"
	}

	// Extension-based for binary formats
	switch {
	case strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx"):
		return "P12"
	case strings.HasSuffix(lower, ".p7b") || strings.HasSuffix(lower, ".p7c"):
		return "P7B"
	case strings.HasSuffix(lower, ".der"):
		return "DER"
	}

	// Try DER parse as fallback
	if _, err := x509.ParseCertificate(data); err == nil {
		return "DER"
	}

	// Try P7B (DER-encoded PKCS7)
	if _, err := pkcs7.Parse(data); err == nil {
		return "P7B"
	}

	return ""
}

func isPEM(data []byte) bool {
	return strings.Contains(string(data), "-----BEGIN ")
}

func containsCSR(data []byte) bool {
	return strings.Contains(string(data), "-----BEGIN CERTIFICATE REQUEST-----") ||
		strings.Contains(string(data), "-----BEGIN NEW CERTIFICATE REQUEST-----")
}

const maxPEMBlocks = 100

func parsePEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	blocks := 0

	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		blocks++
		if blocks > maxPEMBlocks {
			return nil, fmt.Errorf("too many PEM blocks (limit %d)", maxPEMBlocks)
		}
		if block.Type == "CERTIFICATE" {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			certs = append(certs, cert)
		}
	}

	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found in PEM data")
	}
	return certs, nil
}

func parseDER(data []byte) ([]*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DER certificate: %w", err)
	}
	return []*x509.Certificate{cert}, nil
}

const maxChainLength = 20

func parseP12(data []byte, password string) ([]*x509.Certificate, error) {
	if err := guardP12Iterations(data); err != nil {
		return nil, err
	}

	_, cert, caCerts, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, fmt.Errorf("failed to parse P12/PFX: %w", err)
	}

	if len(caCerts) > maxChainLength {
		caCerts = caCerts[:maxChainLength]
	}

	certs := []*x509.Certificate{cert}
	certs = append(certs, caCerts...)
	return certs, nil
}

func parseP7B(data []byte) ([]*x509.Certificate, error) {
	// Try PEM-encoded P7B first
	if isPEM(data) {
		block, _ := pem.Decode(data)
		if block != nil {
			data = block.Bytes
		}
	}

	p7, err := pkcs7.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse P7B/PKCS7: %w", err)
	}
	if len(p7.Certificates) == 0 {
		return nil, fmt.Errorf("no certificates found in P7B")
	}
	return p7.Certificates, nil
}

func parseCSR(data []byte) (*CertificateInfo, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block for CSR")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSR: %w", err)
	}

	info := &CertificateInfo{
		Subject:       buildSubjectInfo(csr.Subject),
		SignatureAlgo: csr.SignatureAlgorithm.String(),
		PublicKeyAlgo: csr.PublicKeyAlgorithm.String(),
		KeySize:       getKeySize(csr.PublicKey),
		Format:        "CSR",
		CertCount:     0,
	}

	for _, name := range csr.DNSNames {
		info.SANs = append(info.SANs, name)
	}
	for _, ip := range csr.IPAddresses {
		info.SANs = append(info.SANs, ip.String())
	}
	for _, email := range csr.EmailAddresses {
		info.SANs = append(info.SANs, email)
	}

	return info, nil
}

func buildCertInfo(cert *x509.Certificate, format string) *CertificateInfo {
	now := time.Now()

	sha256Sum := sha256.Sum256(cert.Raw)
	sha1Sum := sha1.Sum(cert.Raw)

	info := &CertificateInfo{
		Subject:       buildSubjectInfo(cert.Subject),
		Issuer:        buildSubjectInfo(cert.Issuer),
		SerialNumber:  formatSerial(cert.SerialNumber),
		NotBefore:     cert.NotBefore,
		NotAfter:      cert.NotAfter,
		IsExpired:     now.After(cert.NotAfter) || now.Before(cert.NotBefore),
		SignatureAlgo: cert.SignatureAlgorithm.String(),
		PublicKeyAlgo: cert.PublicKeyAlgorithm.String(),
		KeySize:       getKeySize(cert.PublicKey),
		IsCA:          cert.IsCA,
		IsSelfSigned:  isSelfSigned(cert),
		IsTrusted:     isTrustedBySystem(cert),
		KeyUsage:      formatKeyUsage(cert.KeyUsage),
		ExtKeyUsage:   formatExtKeyUsage(cert.ExtKeyUsage),
		Fingerprints: Fingerprints{
			SHA256: formatHex(sha256Sum[:]),
			SHA1:   formatHex(sha1Sum[:]),
		},
		Format: format,
	}

	for _, name := range cert.DNSNames {
		info.SANs = append(info.SANs, name)
	}
	for _, ip := range cert.IPAddresses {
		info.SANs = append(info.SANs, ip.String())
	}
	for _, email := range cert.EmailAddresses {
		info.SANs = append(info.SANs, email)
	}
	for _, uri := range cert.URIs {
		info.SANs = append(info.SANs, uri.String())
	}

	return info
}

func buildCertSummary(cert *x509.Certificate) CertSummary {
	now := time.Now()
	return CertSummary{
		Subject:      buildSubjectInfo(cert.Subject),
		Issuer:       buildSubjectInfo(cert.Issuer),
		SerialNumber: formatSerial(cert.SerialNumber),
		NotBefore:    cert.NotBefore,
		NotAfter:     cert.NotAfter,
		IsExpired:    now.After(cert.NotAfter) || now.Before(cert.NotBefore),
		IsCA:         cert.IsCA,
	}
}

func buildSubjectInfo(name pkix.Name) SubjectInfo {
	return SubjectInfo{
		CommonName:         name.CommonName,
		Organization:       name.Organization,
		OrganizationalUnit: name.OrganizationalUnit,
		Country:            name.Country,
		Province:           name.Province,
		Locality:           name.Locality,
	}
}

func getKeySize(pub any) int {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return k.N.BitLen()
	case *ecdsa.PublicKey:
		return k.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 256
	default:
		return 0
	}
}

func isSelfSigned(cert *x509.Certificate) bool {
	return cert.CheckSignatureFrom(cert) == nil
}

func isTrustedBySystem(cert *x509.Certificate) bool {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return false
	}

	opts := x509.VerifyOptions{
		Roots: pool,
	}
	_, err = cert.Verify(opts)
	return err == nil
}

func formatSerial(n *big.Int) string {
	if n == nil {
		return ""
	}
	b := n.Bytes()
	return formatHex(b)
}

func formatHex(b []byte) string {
	s := hex.EncodeToString(b)
	var parts []string
	for i := 0; i < len(s); i += 2 {
		end := i + 2
		if end > len(s) {
			end = len(s)
		}
		parts = append(parts, s[i:end])
	}
	return strings.ToUpper(strings.Join(parts, ":"))
}

func formatKeyUsage(usage x509.KeyUsage) []string {
	var result []string
	usages := []struct {
		flag x509.KeyUsage
		name string
	}{
		{x509.KeyUsageDigitalSignature, "Digital Signature"},
		{x509.KeyUsageContentCommitment, "Content Commitment"},
		{x509.KeyUsageKeyEncipherment, "Key Encipherment"},
		{x509.KeyUsageDataEncipherment, "Data Encipherment"},
		{x509.KeyUsageKeyAgreement, "Key Agreement"},
		{x509.KeyUsageCertSign, "Certificate Sign"},
		{x509.KeyUsageCRLSign, "CRL Sign"},
		{x509.KeyUsageEncipherOnly, "Encipher Only"},
		{x509.KeyUsageDecipherOnly, "Decipher Only"},
	}
	for _, u := range usages {
		if usage&u.flag != 0 {
			result = append(result, u.name)
		}
	}
	return result
}

func formatExtKeyUsage(usages []x509.ExtKeyUsage) []string {
	var result []string
	names := map[x509.ExtKeyUsage]string{
		x509.ExtKeyUsageServerAuth:      "Server Authentication",
		x509.ExtKeyUsageClientAuth:      "Client Authentication",
		x509.ExtKeyUsageCodeSigning:     "Code Signing",
		x509.ExtKeyUsageEmailProtection: "Email Protection",
		x509.ExtKeyUsageTimeStamping:    "Time Stamping",
		x509.ExtKeyUsageOCSPSigning:     "OCSP Signing",
	}
	for _, u := range usages {
		if name, ok := names[u]; ok {
			result = append(result, name)
		}
	}
	return result
}
