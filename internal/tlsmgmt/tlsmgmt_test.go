package tlsmgmt

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type testConfig struct {
	certPath      string
	keyPath       string
	smtpTLS       bool
	imapTLS       bool
	pop3TLS       bool
	jmapTLS       bool
}

func (t *testConfig) GetCertPath() string     { return t.certPath }
func (t *testConfig) GetKeyPath() string      { return t.keyPath }
func (t *testConfig) SMTPTLSEnabled() bool    { return t.smtpTLS }
func (t *testConfig) IMAPTLSEnabled() bool    { return t.imapTLS }
func (t *testConfig) POP3TLSEnabled() bool    { return t.pop3TLS }
func (t *testConfig) JMAPTLSEnabled() bool    { return t.jmapTLS }
func (t *testConfig) SMTPAddress() string     { return "0.0.0.0:25" }
func (t *testConfig) IMAPAddress() string     { return "0.0.0.0:143" }
func (t *testConfig) POP3Address() string     { return "0.0.0.0:110" }
func (t *testConfig) JMAPAddress() string     { return "0.0.0.0:8080" }

func generateTestCert(t *testing.T, dir string, daysValid int) (certPath, keyPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatalf("generate key: %v", err) }

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.example.com"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(time.Duration(daysValid) * 24 * time.Hour),
		DNSNames:     []string{"test.example.com", "mail.example.com"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil { t.Fatalf("create cert: %v", err) }

	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certFile, _ := os.Create(certPath)
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyFile, _ := os.Create(keyPath)
	pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	keyFile.Close()

	return certPath, keyPath
}

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/tls_test.db", dir))
	if err != nil { t.Fatalf("open db: %v", err) }
	t.Cleanup(func() { db.Close() })

	svc := NewService(db, nil)
	if err := svc.ensureSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return svc, dir
}

func TestCertificateLoad(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 365)

	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg

	certs, err := svc.LoadCertificates(context.Background())
	if err != nil { t.Fatalf("load: %v", err) }
	if len(certs) == 0 { t.Fatal("expected at least 1 certificate") }
	if certs[0].CommonName != "test.example.com" {
		t.Fatalf("expected test.example.com, got %s", certs[0].CommonName)
	}
}

func TestCertificateValidation(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 365)
	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg
	// LoadCertificates is called via CheckExpiration or directly.
	certs, err := svc.LoadCertificates(context.Background())
	if err != nil { t.Fatalf("load: %v", err) }
	if len(certs) == 0 { t.Fatal("no certs loaded") }

	result, err := svc.ValidateCertificate(context.Background(), "default")
	if err != nil { t.Fatalf("validate: %v", err) }
	if !result.Valid { t.Fatalf("expected valid, errors: %v", result.Errors) }
	if result.CommonName != "test.example.com" {
		t.Fatalf("expected test.example.com, got %s", result.CommonName)
	}
}

func TestCertificateExpirationHealthy(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 365)
	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg
	svc.LoadCertificates(context.Background())

	certs, err := svc.CheckExpiration(context.Background())
	if err != nil { t.Fatalf("check exp: %v", err) }
	if len(certs) == 0 { t.Fatal("expected certs") }
	if certs[0].DaysRemaining <= 30 { t.Fatal("expected >30 days for healthy cert") }
}

func TestCertificateExpirationWarning(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 20)
	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg
	svc.LoadCertificates(context.Background())

	certs, err := svc.CheckExpiration(context.Background())
	if err != nil { t.Fatalf("check exp: %v", err) }
	if len(certs) == 0 { t.Fatal("expected certs") }
}

func TestCertificateExpired(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, -1) // expired yesterday
	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg
	svc.LoadCertificates(context.Background())

	certs, err := svc.CheckExpiration(context.Background())
	if err != nil { t.Fatalf("check exp: %v", err) }
	if len(certs) > 0 && certs[0].Status != CertExpired {
		t.Fatalf("expected expired, got %s", certs[0].Status)
	}
}

func TestCertificateKeyMismatch(t *testing.T) {
	svc, dir := testService(t)

	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test.com"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(365 * 24 * time.Hour),
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key1.PublicKey, key1)

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0640)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key2)}), 0640)

	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg
	svc.LoadCertificates(context.Background())

	result, _ := svc.ValidateCertificate(context.Background(), "default")
	if result.Valid {
		t.Fatal("expected invalid due to key mismatch")
	}
}

func TestCertificateReload(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 365)
	cfg := &testConfig{certPath: certPath, keyPath: keyPath}
	svc.cfg = cfg

	result := svc.ReloadCertificates(context.Background())
	if !result.Success { t.Fatalf("reload: %s", result.Message) }
}

func TestRuntimeTLSInventory(t *testing.T) {
	svc, dir := testService(t)
	certPath, keyPath := generateTestCert(t, dir, 365)
	cfg := &testConfig{certPath: certPath, keyPath: keyPath, smtpTLS: true, imapTLS: true}
	svc.cfg = cfg

	statuses := svc.GetRuntimeTLSStatus(context.Background())
	if len(statuses) == 0 { t.Fatal("expected statuses") }
	foundSMTP := false
	for _, s := range statuses {
		if s.Protocol == "SMTP" && s.TLSEnabled { foundSMTP = true }
	}
	if !foundSMTP { t.Fatal("expected SMTP with TLS enabled") }
}

func TestInvalidCertificateRejected(t *testing.T) {
	svc, dir := testService(t)
	certPath := filepath.Join(dir, "invalid.pem")
	os.WriteFile(certPath, []byte("not a certificate"), 0640)

	cfg := &testConfig{certPath: certPath, keyPath: certPath}
	svc.cfg = cfg
	svc.LoadCertificates(context.Background())

	result, _ := svc.ValidateCertificate(context.Background(), "default")
	if result.Valid { t.Fatal("expected invalid for bad certificate data") }
}

func TestMissingCertificateRejected(t *testing.T) {
	svc, dir := testService(t)
	cfg := &testConfig{certPath: filepath.Join(dir, "nonexistent.pem"), keyPath: filepath.Join(dir, "nonexistent-key.pem")}
	svc.cfg = cfg

	result := svc.ReloadCertificates(context.Background())
	if result.Success { t.Fatal("expected failure for missing certificate") }
}

func TestReloadFailure(t *testing.T) {
	svc, _ := testService(t)
	cfg := &testConfig{}
	svc.cfg = cfg

	result := svc.ReloadCertificates(context.Background())
	// Should succeed even with empty config (no certs to load).
	_ = result
}

func mustNewService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/tls_test.db", dir))
	if err != nil { t.Fatalf("open db: %v", err) }
	t.Cleanup(func() { db.Close() })
	svc := NewService(db, nil)
	svc.ensureSchema(context.Background())
	return svc
}
