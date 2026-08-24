package inspector

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
)

// maxKDFIterations bounds the PBKDF iteration count we accept from a PKCS#12
// file. The count is attacker-controlled and drives an unbounded, uncancellable
// CPU loop inside the decoder (MAC check and each PBE decryption), so a tiny
// file can otherwise pin a core for minutes. 5,000,000 is far above what any
// real generator emits (OpenSSL 2048, Java/Windows a few thousand to ~600k)
// while keeping each derivation well under a second.
const maxKDFIterations = 5_000_000

var errKDFIterationsTooHigh = errors.New("certificate KDF iteration count exceeds the allowed limit")

// PKCS#12 / PBE OIDs, from RFC 7292 and RFC 8018. These are frozen by the
// standards, not tied to any library version.
var (
	oidDataContentType          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedDataContentType = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	oidPKCS8ShroudedKeyBag      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidPBES2                    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
)

// Minimal wire structs mirroring the PKCS#12 fields we need to read the
// declared iteration counts without decrypting anything.
type p12PfxPdu struct {
	Version  int
	AuthSafe p12ContentInfo
	MacData  p12MacData `asn1:"optional"`
}

type p12ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"tag:0,explicit,optional"`
}

type p12MacData struct {
	Mac        asn1.RawValue
	MacSalt    []byte
	Iterations int `asn1:"optional,default:1"`
}

type p12EncryptedData struct {
	Version              int
	EncryptedContentInfo p12EncryptedContentInfo
}

type p12EncryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedContent           []byte `asn1:"tag:0,optional"`
}

type p12SafeBag struct {
	Id         asn1.ObjectIdentifier
	Value      asn1.RawValue `asn1:"tag:0,explicit"`
	Attributes asn1.RawValue `asn1:"set,optional"`
}

type p12EncryptedPrivateKeyInfo struct {
	Algorithm     pkix.AlgorithmIdentifier
	EncryptedData []byte
}

type p12PBEParams struct {
	Salt       []byte
	Iterations int
}

type p12PBES2Params struct {
	Kdf              pkix.AlgorithmIdentifier
	EncryptionScheme pkix.AlgorithmIdentifier
}

type p12PBKDF2Params struct {
	Salt       asn1.RawValue
	Iterations int
	KeyLength  int                      `asn1:"optional"`
	Prf        pkix.AlgorithmIdentifier `asn1:"optional"`
}

// guardP12Iterations rejects a PKCS#12 file whose declared PBKDF iteration count
// (MAC or any visible PBE) exceeds maxKDFIterations, before the expensive decode
// runs. It fails open: if the structure does not match what we expect, we return
// nil and let the real decoder handle it — the standards-defined shape below
// covers every mainstream generator, so a bomb has to be well-formed to be
// dangerous, and a well-formed bomb is exactly what this catches.
func guardP12Iterations(data []byte) error {
	var pfx p12PfxPdu
	if _, err := asn1.Unmarshal(data, &pfx); err != nil {
		return nil
	}
	if pfx.MacData.Iterations > maxKDFIterations {
		return errKDFIterationsTooHigh
	}

	// AuthSafe.Content is an OCTET STRING wrapping the DER of the
	// authenticatedSafe (SEQUENCE OF ContentInfo); unwrap it the same way the
	// decoder does before iterating.
	var inner asn1.RawValue
	if _, err := asn1.Unmarshal(pfx.AuthSafe.Content.Bytes, &inner); err != nil {
		return nil
	}
	var authSafe []p12ContentInfo
	if _, err := asn1.Unmarshal(inner.Bytes, &authSafe); err != nil {
		return nil
	}

	for _, ci := range authSafe {
		switch {
		case ci.ContentType.Equal(oidEncryptedDataContentType):
			var enc p12EncryptedData
			if _, err := asn1.Unmarshal(ci.Content.Bytes, &enc); err != nil {
				continue
			}
			if algorithmIterations(enc.EncryptedContentInfo.ContentEncryptionAlgorithm) > maxKDFIterations {
				return errKDFIterationsTooHigh
			}
		case ci.ContentType.Equal(oidDataContentType):
			// Unencrypted safe contents: private keys live here as shrouded key
			// bags whose own PBE iteration count is another KDF site.
			var octets []byte
			if _, err := asn1.Unmarshal(ci.Content.Bytes, &octets); err != nil {
				continue
			}
			var bags []p12SafeBag
			if _, err := asn1.Unmarshal(octets, &bags); err != nil {
				continue
			}
			for _, bag := range bags {
				if !bag.Id.Equal(oidPKCS8ShroudedKeyBag) {
					continue
				}
				var pk p12EncryptedPrivateKeyInfo
				if _, err := asn1.Unmarshal(bag.Value.Bytes, &pk); err != nil {
					continue
				}
				if algorithmIterations(pk.Algorithm) > maxKDFIterations {
					return errKDFIterationsTooHigh
				}
			}
		}
	}
	return nil
}

// algorithmIterations extracts the PBKDF iteration count from a PBE algorithm
// identifier (PBES2/PBKDF2 or a legacy PKCS#12 PBES1 scheme). Returns 0 when the
// count cannot be read, letting the caller treat it as "not over the limit".
func algorithmIterations(algo pkix.AlgorithmIdentifier) int {
	if algo.Algorithm.Equal(oidPBES2) {
		var params p12PBES2Params
		if _, err := asn1.Unmarshal(algo.Parameters.FullBytes, &params); err != nil {
			return 0
		}
		var kdf p12PBKDF2Params
		if _, err := asn1.Unmarshal(params.Kdf.Parameters.FullBytes, &kdf); err != nil {
			return 0
		}
		return kdf.Iterations
	}

	var params p12PBEParams
	if _, err := asn1.Unmarshal(algo.Parameters.FullBytes, &params); err != nil {
		return 0
	}
	return params.Iterations
}
