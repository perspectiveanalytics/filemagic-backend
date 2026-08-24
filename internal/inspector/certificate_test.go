package inspector

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// Malformed BER that makes go.mozilla.org/pkcs7 index out of range. Must be
// turned into an error, not a process-killing panic.
func TestParseCertificateMalformedP7BDoesNotPanic(t *testing.T) {
	for _, in := range [][]byte{{0x1f, 0x80}, {0x1f, 0x80, 0x00}} {
		if _, err := ParseCertificate(in, "evil.p7b", ""); err == nil {
			t.Errorf("expected error for %x, got nil", in)
		}
	}
}

func makeTestP12(t *testing.T, enc *pkcs12.Encoder) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	data, err := enc.Encode(key, cert, nil, "pass")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGuardAcceptsNormalP12(t *testing.T) {
	data := makeTestP12(t, pkcs12.Modern)
	if err := guardP12Iterations(data); err != nil {
		t.Fatalf("normal p12 rejected: %v", err)
	}
	if _, err := ParseCertificate(data, "ok.p12", "pass"); err != nil {
		t.Fatalf("normal p12 failed to parse: %v", err)
	}
}

// A KDF bomb: tiny file, huge iteration count. Must be rejected before the
// decoder runs the (uncancellable) derivation.
func TestGuardRejectsIterationBomb(t *testing.T) {
	base := makeTestP12(t, pkcs12.Modern)

	// Inflate the MAC iteration count via an ASN.1 round-trip.
	type mac struct {
		Mac        asn1.RawValue
		MacSalt    []byte
		Iterations int `asn1:"optional,default:1"`
	}
	type ci struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"tag:0,explicit,optional"`
	}
	type pfx struct {
		Version  int
		AuthSafe ci
		MacData  mac `asn1:"optional"`
	}
	var p pfx
	if _, err := asn1.Unmarshal(base, &p); err != nil {
		t.Fatal(err)
	}
	p.MacData.Iterations = maxKDFIterations + 1
	bomb, err := asn1.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := guardP12Iterations(bomb); err == nil {
		t.Fatal("iteration bomb was not rejected by guard")
	}

	start := time.Now()
	if _, err := ParseCertificate(bomb, "bomb.p12", "pass"); err == nil {
		t.Fatal("iteration bomb parsed without error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("iteration bomb not blocked before KDF: took %s", elapsed)
	}
}

// The encryption-side KDF (PBES2/PBKDF2) is a separate bomb site from the MAC,
// nested inside the encrypted content and the shrouded key bag. This proves the
// guard walks into those, not just the top-level MAC.
func TestGuardRejectsEncryptionIterationBomb(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	bomb, err := pkcs12.Modern.WithIterations(maxKDFIterations + 1).Encode(key, cert, nil, "pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := guardP12Iterations(bomb); err == nil {
		t.Fatal("PBES2 encryption iteration bomb was not rejected (walk failed open)")
	}
}
