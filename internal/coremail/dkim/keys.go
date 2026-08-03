package dkim

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// DerivePublicKeyRecordValue parses a PEM-encoded RSA private key (PKCS8 or
// PKCS1) and returns the DKIM DNS TXT record value ("v=DKIM1; k=rsa; p=...")
// for its public component, matching the exact wire format GenerateKeyPair
// produces. Returns ("", false) for any malformed, non-RSA, or otherwise
// unsupported key — callers MUST treat that as "no derivable public key"
// rather than inventing or logging key material. This is the single shared
// implementation of PEM -> public-key-TXT derivation for the whole codebase;
// do not duplicate this parsing logic elsewhere.
func DerivePublicKeyRecordValue(privateKeyPEM string) (string, bool) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", false
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		k1, err1 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err1 != nil {
			return "", false
		}
		keyAny = k1
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return "", false
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("v=DKIM1; k=rsa; p=%s", pemEncodeBase64(pubBytes)), true
}

// GenerateKeyPair creates an RSA 2048-bit private key and returns the PEM-encoded
// private key and the DNS TXT record value for public key publication.
func GenerateKeyPair(selector, domain string) (privateKeyPEM string, dnsRecord string, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate rsa key: %w", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	publicKey := &privateKey.PublicKey
	pubBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal public key: %w", err)
	}

	// Build DNS TXT record value per RFC 6376 §3.6.1.
	dnsValue := fmt.Sprintf("v=DKIM1; k=rsa; p=%s", pemEncodeBase64(pubBytes))

	return string(privPEM), dnsValue, nil
}

// GenerateDNSRecord creates the DNS TXT record name and value for a DKIM key.
func GenerateDNSRecord(selector, domain, publicKeyPEM string) (recordName, recordValue string) {
	recordName = fmt.Sprintf("%s._domainkey.%s", selector, domain)
	recordValue = fmt.Sprintf("v=DKIM1; k=rsa; p=%s", pemEncodeBase64([]byte(publicKeyPEM)))
	return
}

// pemEncodeBase64 encodes raw DER bytes as a single-line base64 string
// suitable for DKIM DNS TXT records (no PEM headers/footers).
func pemEncodeBase64(der []byte) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	result := make([]byte, 0, len(der)*4/3+4)
	val := 0
	bits := 0
	for _, b := range der {
		val = (val << 8) | int(b)
		bits += 8
		for bits >= 6 {
			bits -= 6
			result = append(result, base64Chars[(val>>bits)&0x3f])
		}
	}
	if bits > 0 {
		result = append(result, base64Chars[(val<<(6-bits))&0x3f])
	}
	// Add padding.
	for len(result)%4 != 0 {
		result = append(result, '=')
	}
	return string(result)
}
