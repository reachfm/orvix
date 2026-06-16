package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestAdminBootstrapInsertsUserAndLoginSucceeds(t *testing.T) {
	testAdminBootstrapLogin(t, "admin@example.com", "AdminPassword123!", false)
}

func TestAdminBootstrapEncodedPasswordLoginSucceeds(t *testing.T) {
	testAdminBootstrapLogin(t, "admin@orvix.email", `Admin "quoted" \ slash $ dollar ! bang # hash 123`, true)
}

func TestAdminBootstrapInstallerPasswordCasesLoginSucceeds(t *testing.T) {
	passwords := []string{
		"MaghaghaMos086",
		"Password123!",
		"Password$123",
		"Password With Spaces",
		`Password\Slash123`,
		`Password"Quote123`,
		"Password'SingleQuote123",
	}
	for _, password := range passwords {
		t.Run(password, func(t *testing.T) {
			testAdminBootstrapLogin(t, "admin@orvix.email", password, true)
		})
	}
}

func TestAdminBootstrapPromptedPasswordSupportsFreshInstallLoginFlow(t *testing.T) {
	email := "admin@orvix.email"
	password := "  Admin Password 123!  "
	envFile := promptBootstrapEnv(t, email, password)

	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", envFile["ORVIX_ADMIN_PASSWORD_B64"])

	logger := zap.NewNop()
	cfg := config.Defaults()
	stateDir := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(stateDir, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Auth.JWTKeyPath = filepath.Join(stateDir, "jwt_key.pem")

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()

	router := newAdminTestRouter(t, cfg, db, authenticator, logger)

	// First login.
	accessToken := loginForTest(t, router, "/api/v1/auth/login", email, password)
	meForTest(t, router, accessToken)

	// Logout through the authenticated CSRF-protected API.
	csrfToken := csrfTokenForTest(t, router, accessToken)
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Cookie", "csrf_token="+csrfToken)
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("logout request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected logout 200, got %d: %s", resp.StatusCode, body)
	}

	// Second login after logout.
	secondAccessToken := loginForTest(t, router, "/api/v1/auth/login", email, password)
	meForTest(t, router, secondAccessToken)

	// Browser restart equivalent: new request context with the same persisted DB.
	thirdAccessToken := loginForTest(t, router, "/api/v1/auth/login", email, password)
	meForTest(t, router, thirdAccessToken)

	// Service restart equivalent: rebuild authenticator + router against the same DB file.
	restartedAuthenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("restart authenticator: %v", err)
	}
	restartedRouter := newAdminTestRouter(t, cfg, db, restartedAuthenticator, logger)
	restartedAccessToken := loginForTest(t, restartedRouter, "/api/v1/auth/login", email, password)
	meForTest(t, restartedRouter, restartedAccessToken)
}

func testAdminBootstrapLogin(t *testing.T, email, password string, encoded bool) {
	t.Helper()
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	if encoded {
		t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
		t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong-plain-fallback")
	} else {
		t.Setenv("ORVIX_ADMIN_PASSWORD_B64", "")
		t.Setenv("ORVIX_ADMIN_PASSWORD", password)
	}

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedAdminUser(db, authenticator, logger)

	var count int
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one bootstrapped admin user, got %d", count)
	}

	router := newAdminTestRouter(t, cfg, db, authenticator, logger)
	accessToken := loginForTest(t, router, "/admin/login", email, password)
	meForTest(t, router, accessToken)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func bashCommand(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "bash"
	}
	path := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "bash"
}

func promptBootstrapEnv(t *testing.T, email, password string) map[string]string {
	t.Helper()
	root := repoRoot(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}
	harness := strings.Replace(installer, `main "$@"`, `chown() { :; }; chmod() { :; }; BOOTSTRAP_ENV="$2"; password="$(prompt_password)"; write_bootstrap_env "$1" "$password"; cat "$BOOTSTRAP_ENV"`, 1)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "bootstrap-prompt.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	cmd := exec.Command(bashCommand(t), "bootstrap-prompt.sh", email, "bootstrap.env")
	cmd.Dir = harnessDir
	cmd.Stdin = strings.NewReader(password + "\n" + password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap prompt harness failed: %v: %s", err, string(out))
	}

	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
	}
	if values["ORVIX_ADMIN_EMAIL"] != email {
		t.Fatalf("bootstrap env email mismatch: got %q want %q", values["ORVIX_ADMIN_EMAIL"], email)
	}
	decoded, err := base64.StdEncoding.DecodeString(values["ORVIX_ADMIN_PASSWORD_B64"])
	if err != nil {
		t.Fatalf("decode bootstrap password: %v", err)
	}
	if string(decoded) != password {
		t.Fatalf("prompt/bootstrap changed password bytes: got %q want %q", string(decoded), password)
	}
	return values
}

func newAdminTestRouter(t *testing.T, cfg *config.Config, db *gorm.DB, authenticator *auth.Authenticator, logger *zap.Logger) *api.Router {
	t.Helper()
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	return api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
}

func loginForTest(t *testing.T, router *api.Router, path, email, password string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"email": email, "username": email, "password": password})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request %s: %v", path, err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected login 200 from %s, got %d: %s", path, resp.StatusCode, body)
	}
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.AccessToken == "" {
		t.Fatal("expected access token in login response")
	}
	return loginResp.AccessToken
}

func meForTest(t *testing.T, router *api.Router, accessToken string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("me request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected me 200, got %d: %s", resp.StatusCode, body)
	}
}

func csrfTokenForTest(t *testing.T, router *api.Router, accessToken string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf token request: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected csrf token 200, got %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf token: %v", err)
	}
	if data.CSRFToken == "" {
		t.Fatal("csrf token was empty")
	}
	return data.CSRFToken
}
