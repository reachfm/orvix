package handlers_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

func buildSslHarness(t *testing.T) (*api.Router, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	root := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	if _, err := authenticator.HashPassword("TestPassword123!"); err != nil {
		t.Fatalf("hash: %v", err)
	}
	seedPlatformSuperAdminWithPassword(t, sqlDB, "admin@test.local", "TestPassword123!")

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	token := loginSsl(t, router)
	csrf := csrfSsl(t, router, token)
	return router, token, csrf
}

func loginSsl(t *testing.T, router *api.Router) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"username":"admin@test.local","password":"TestPassword123!"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return data.AccessToken
}

func csrfSsl(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	return data.CSRFToken
}

func sslRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func generateSelfSignedPEM(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return certPEM, keyPEM
}

// TestSslV1_UploadResponseNeverExposesKeyPath proves the upload
// response contains no key_path (or any other key-material field) —
// only metadata and the fingerprint.
func TestSslV1_UploadResponseNeverExposesKeyPath(t *testing.T) {
	router, token, csrf := buildSslHarness(t)
	certPEM, keyPEM := generateSelfSignedPEM(t, "upload-test.example.com")

	body, _ := json.Marshal(map[string]string{
		"name":     "upload-test",
		"cert_pem": certPEM,
		"key_pem":  keyPEM,
	})
	resp, respBody := sslRequest(t, router, "POST", "/api/v1/admin/ssl/certificates", string(body), token, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d: %s", resp.StatusCode, respBody)
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := parsed["key_path"]; ok {
		t.Fatal("upload response exposes key_path")
	}
	if _, ok := parsed["key_pem"]; ok {
		t.Fatal("upload response exposes key_pem")
	}
	if strings.Contains(string(respBody), "PRIVATE KEY") {
		t.Fatal("upload response body contains PEM private-key material")
	}
}

// TestSslV1_ListResponseNeverExposesKeyPath proves the list response
// (both runtime and uploaded certs) never surfaces a key_path field.
func TestSslV1_ListResponseNeverExposesKeyPath(t *testing.T) {
	router, token, csrf := buildSslHarness(t)
	certPEM, keyPEM := generateSelfSignedPEM(t, "list-test.example.com")
	uploadBody, _ := json.Marshal(map[string]string{"name": "list-test", "cert_pem": certPEM, "key_pem": keyPEM})
	if resp, body := sslRequest(t, router, "POST", "/api/v1/admin/ssl/certificates", string(uploadBody), token, csrf); resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}

	resp, body := sslRequest(t, router, "GET", "/api/v1/admin/ssl/certificates", "", token, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	// "config_key_path" is a legitimate, distinct field: the
	// operator's own configured runtime key path, not a per-certificate
	// secret. Only the per-cert "key_path" JSON key is prohibited.
	var parsed struct {
		Runtime  []map[string]any `json:"runtime"`
		Uploaded []map[string]any `json:"uploaded"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, cert := range append(parsed.Runtime, parsed.Uploaded...) {
		if _, ok := cert["key_path"]; ok {
			t.Fatalf("a certificate entry exposes key_path: %v", cert)
		}
	}
	if len(parsed.Uploaded) == 0 {
		t.Fatal("expected at least one uploaded certificate to check")
	}
}

// TestSslV1_OversizedUploadRejected proves the HTTP layer enforces
// the 1 MiB PEM size limit before any certificate is persisted.
func TestSslV1_OversizedUploadRejected(t *testing.T) {
	router, token, csrf := buildSslHarness(t)
	certPEM, keyPEM := generateSelfSignedPEM(t, "oversized.example.com")
	oversizedCert := certPEM + strings.Repeat("A", 1<<20+1)

	body, _ := json.Marshal(map[string]string{"name": "oversized", "cert_pem": oversizedCert, "key_pem": keyPEM})
	resp, respBody := sslRequest(t, router, "POST", "/api/v1/admin/ssl/certificates", string(body), token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized cert_pem, got %d: %s", resp.StatusCode, respBody)
	}

	list, listBody := sslRequest(t, router, "GET", "/api/v1/admin/ssl/certificates", "", token, csrf)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", list.StatusCode, listBody)
	}
	if strings.Contains(string(listBody), "\"oversized\"") {
		t.Fatal("the oversized/rejected certificate was persisted despite the 400")
	}
}
