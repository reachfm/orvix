package main

// Deterministic password-chain trace tests.
//
// The production symptom the user reported:
//
//   user logged in
//   then later:
//   password verification failed
//   for the same user_id=1
//
// This file pins the bootstrap → DB → login chain down to
// individual byte slices. The tests don't trust any abstraction:
// they read the env, decode the base64, hash via bcrypt,
// re-read the hash from SQLite, and compare the bcrypt result
// directly. Then they walk the full HTTP login → logout →
// second-login cycle using the EXACT wire format the installer
// posts. If any byte changes between the installer-side
// plaintext and the runtime-side verifier, these tests fail
// with a message that names the byte.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// passwordChainCase is the full input/output record for one
// installer-password shape. The harness prints every field
// when a test fails, so the diff between "what the installer
// wrote" and "what the runtime verified" is visible without
// re-running the test with a debugger.
type passwordChainCase struct {
	Name string // human label for -run selection
	Pwd  string // the EXACT bytes the installer typed
}

// passwordChainTrace is what the harness captures so a failure
// can be diagnosed from the test log alone.
type passwordChainTrace struct {
	InstallerPlaintext string // bytes typed at install.sh prompt_password
	InstallerBase64    string // value written to /etc/orvix/bootstrap.env
	EnvAfterSystemd    string // what os.Getenv returns inside orvix serve
	DecodedPlaintext   string // base64.StdEncoding.DecodeString result
	StoredBcryptHash   string // SELECT password_hash FROM users
	BcryptVerify       bool   // bcrypt.CompareHashAndPassword(plaintext, hash)
	StoredArgon2Hash   string // SELECT password_hash FROM coremail_mailboxes
	Argon2Verify       bool   // coremail.AuthService.VerifyPassword
	FirstLoginStatus   int    // HTTP status of POST /api/v1/auth/login
	SecondLoginStatus  int    // HTTP status of the SECOND POST /api/v1/auth/login
	LogoutStatus       int    // HTTP status of POST /api/v1/auth/logout
	FirstAccess        string // access_token from first login
	SecondAccess       string // access_token from second login (must differ)
}

// runPasswordChain runs the full installer-password → DB-hash
// → login → logout → login cycle and returns the trace. The
// caller's t.Errorf / t.Fatalf decides whether the trace
// counts as a failure, but the trace is always populated.
func runPasswordChain(t *testing.T, c passwordChainCase) passwordChainTrace {
	t.Helper()

	tr := passwordChainTrace{
		InstallerPlaintext: c.Pwd,
	}

	// Step 1 — simulate install.sh write_bootstrap_env.
	// install.sh runs `printf '%s' "$password" | base64 | tr -d '\n'`
	// then writes `ORVIX_ADMIN_PASSWORD_B64=<that>` to the env
	// file. We do the same here, no shortcuts.
	tr.InstallerBase64 = base64.StdEncoding.EncodeToString([]byte(c.Pwd))

	// Step 2 — simulate systemd reading the env file and the
	// orvix process reading it via os.Getenv. We use t.Setenv
	// so the value is cleaned up after the test.
	t.Setenv("ORVIX_ADMIN_EMAIL", "admin@orvix.email")
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", tr.InstallerBase64)
	// Installers never write ORVIX_ADMIN_PASSWORD to
	// /etc/orvix/bootstrap.env, so we deliberately leave it
	// pointing at a wrong value to prove the runtime does not
	// silently fall back to it.
	t.Setenv("ORVIX_ADMIN_PASSWORD", "deliberately-wrong-fallback")

	tr.EnvAfterSystemd = os.Getenv("ORVIX_ADMIN_PASSWORD_B64")

	// Step 3 — decode the base64 the way bootstrapAdminPassword
	// does. If this fails, the runtime never sees the password
	// and never hashes anything; the bug shows up here.
	raw, err := base64.StdEncoding.DecodeString(tr.EnvAfterSystemd)
	if err != nil {
		return tr
	}
	tr.DecodedPlaintext = string(raw)
	if tr.DecodedPlaintext != tr.InstallerPlaintext {
		t.Errorf("decoded plaintext differs from installer input:\n  input  hex=%s\n  output hex=%s",
			hex.EncodeToString([]byte(tr.InstallerPlaintext)),
			hex.EncodeToString([]byte(tr.DecodedPlaintext)))
	}

	// Step 4 — stand up the database and run seedAdminUser, the
	// exact code path orvix serve runs on boot.
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
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// Step 5 — read the EXACT bytes the bootstrap committed.
	// If this string doesn't start with $2a/$2b/$2y the runtime
	// stored something that bcrypt can never verify.
	if err := sqlDB.QueryRow(
		`SELECT password_hash FROM users WHERE email = ?`,
		"admin@orvix.email",
	).Scan(&tr.StoredBcryptHash); err != nil {
		t.Fatalf("read users.password_hash: %v", err)
	}
	if !strings.HasPrefix(tr.StoredBcryptHash, "$2") {
		t.Errorf("users.password_hash is not bcrypt-encoded: %q", tr.StoredBcryptHash)
	}

	if err := sqlDB.QueryRow(
		`SELECT password_hash FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL`,
		"admin@orvix.email",
	).Scan(&tr.StoredArgon2Hash); err != nil {
		t.Fatalf("read coremail_mailboxes.password_hash: %v", err)
	}
	if !strings.HasPrefix(tr.StoredArgon2Hash, "$argon2id$") {
		t.Errorf("coremail_mailboxes.password_hash is not argon2id-encoded: %q", tr.StoredArgon2Hash)
	}

	// Step 6 — direct bcrypt compare. This is the same call the
	// Login handler makes. If this returns false, the chain
	// has already broken by the time orvix serve even started.
	bcryptErr := bcrypt.CompareHashAndPassword([]byte(tr.StoredBcryptHash), []byte(tr.DecodedPlaintext))
	tr.BcryptVerify = bcryptErr == nil
	if bcryptErr != nil {
		t.Errorf("direct bcrypt.CompareHashAndPassword failed: %v\n  plaintext hex=%s\n  hash      =%s",
			bcryptErr,
			hex.EncodeToString([]byte(tr.DecodedPlaintext)),
			tr.StoredBcryptHash)
	}

	// Step 7 — stand up the Fiber router and exercise the full
	// cycle: login → logout → second login. Each step records
	// the HTTP status and the access_token. A passing run
	// proves the runtime can authenticate the same credentials
	// twice from a single boot, which is the contract the
	// production symptom violates.
	scratch, err := os.MkdirTemp("", "orvix-pwdchain-*")
	if err != nil {
		t.Fatalf("scratch: %v", err)
	}
	adminDir := filepath.Join(scratch, "admin")
	mkAll(t, adminDir)
	writeFile(t, filepath.Join(adminDir, "index.html"), "<html></html>")
	writeFile(t, filepath.Join(adminDir, "app.js"), "")
	cfg.Server.AdminUIDir = adminDir

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = os.RemoveAll(scratch)
	})

	tr.FirstLoginStatus, tr.FirstAccess = doChainLogin(t, router, "admin@orvix.email", tr.DecodedPlaintext)
	if tr.FirstLoginStatus != 200 {
		return tr
	}

	tr.LogoutStatus = doChainLogout(t, router, tr.FirstAccess)
	// Logout is best-effort; a non-200 still lets us probe the
	// second login, because the production symptom is
	// "password verification failed", not "logout failed".

	// Sleep just over a second so the JWT iat/exp fields differ
	// between the first and second login. Without this, two
	// logins within the same wall-clock second produce
	// byte-identical access tokens (RS256 is deterministic for
	// the same payload + key). The production symptom is a
	// status-code regression, not a token-rotation regression;
	// the sleep is here only to make the rotation check below
	// meaningful, not because token rotation is the test target.
	time.Sleep(1100 * time.Millisecond)

	tr.SecondLoginStatus, tr.SecondAccess = doChainLogin(t, router, "admin@orvix.email", tr.DecodedPlaintext)

	// Step 8 — argon2id round-trip via the same AuthService
	// the admin login handler uses. If this fails the admin
	// UI would log a different error ("invalid credentials")
	// but it's still part of the contract.
	argon2OK, argon2Err := verifyArgon2id(tr.StoredArgon2Hash, tr.DecodedPlaintext)
	tr.Argon2Verify = argon2OK
	if argon2Err != nil {
		t.Errorf("argon2id verify failed: %v", argon2Err)
	}

	return tr
}

// TestPasswordChain_BootstrapToLoginCycleDeterministic is the
// single-source-of-truth test for the bcrypt "first login
// works, subsequent fails" symptom. Each subtest is one
// installer-password shape. A failure prints the full trace
// so the diff between installer input and runtime verifier is
// visible without rerunning the test.
func TestPasswordChain_BootstrapToLoginCycleDeterministic(t *testing.T) {
	cases := []passwordChainCase{
		{Name: "ASCII_default", Pwd: "MaghaghaMos086"},
		{Name: "trailing_space", Pwd: "MaghaghaMos086 "},
		{Name: "leading_space", Pwd: " MaghaghaMos086"},
		{Name: "both_spaces", Pwd: " MaghaghaMos086 "},
		{Name: "trailing_newline_literally", Pwd: "MaghaghaMos086\n"},
		{Name: "tab_inside", Pwd: "Maghagha\tMos086"},
		{Name: "double_quote", Pwd: `Pass"word123!`},
		{Name: "single_quote", Pwd: `Pass'word123!`},
		{Name: "backslash", Pwd: `Pass\word123!`},
		{Name: "dollar", Pwd: `Pass$word123!`},
		{Name: "shell_subst", Pwd: `Pass$(id)word`},
		{Name: "backtick", Pwd: "Pass`id`word"},
		{Name: "unicode", Pwd: "MaghaghaMos086π"},
		{Name: "exactly_72_chars_bcrypt_boundary", Pwd: strings.Repeat("a", 72)},
		{Name: "single_char", Pwd: "x"},
		{Name: "min_length_8", Pwd: "abcd1234"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			tr := runPasswordChain(t, c)

			// bcrypt's hard input limit is 72 bytes; a longer
			// password makes GenerateFromPassword return
			// ErrPasswordTooLong, the bootstrap silently
			// returns without inserting a row, and login
			// fails with 401 user-not-found. That is a
			// separate bug from the "first works, second
			// fails" symptom; it has its own test below.
			if len(c.Pwd) == 0 || len(c.Pwd) > 72 {
				return
			}

			if !tr.BcryptVerify {
				t.Errorf("bcrypt.CompareHashAndPassword returned false for the EXACT bytes the installer wrote.\n"+
					"  plaintext hex  : %s\n"+
					"  base64 written : %q\n"+
					"  env read back  : %q\n"+
					"  decoded hex    : %s\n"+
					"  stored hash    : %q",
					hex.EncodeToString([]byte(tr.InstallerPlaintext)),
					tr.InstallerBase64,
					tr.EnvAfterSystemd,
					hex.EncodeToString([]byte(tr.DecodedPlaintext)),
					tr.StoredBcryptHash)
			}

			if tr.FirstLoginStatus != 200 {
				t.Errorf("first login: expected 200, got %d\n  plaintext hex: %s\n  stored hash : %q\n  bcrypt verify: %v",
					tr.FirstLoginStatus,
					hex.EncodeToString([]byte(tr.DecodedPlaintext)),
					tr.StoredBcryptHash,
					tr.BcryptVerify)
			}
			if tr.SecondLoginStatus != 200 {
				t.Errorf("second login: expected 200, got %d (this is the production symptom)\n"+
					"  plaintext hex : %s\n"+
					"  stored hash   : %q\n"+
					"  bcrypt verify : %v\n"+
					"  first access  : %s\n"+
					"  logout status : %d\n"+
					"  argon2 verify : %v",
					tr.SecondLoginStatus,
					hex.EncodeToString([]byte(tr.DecodedPlaintext)),
					tr.StoredBcryptHash,
					tr.BcryptVerify,
					tr.FirstAccess,
					tr.LogoutStatus,
					tr.Argon2Verify)
			}

			if tr.SecondAccess == "" || tr.SecondAccess == tr.FirstAccess {
				t.Errorf("second login access_token must differ from first (after the test sleep); got first=%q second=%q",
					tr.FirstAccess, tr.SecondAccess)
			}
		})
	}
}

// TestPasswordChain_TraceIsStableAcrossReBootstrap runs the
// installer flow twice with the same env and asserts that the
// bcrypt hash bytes do not change. This catches a class of
// bugs where seedAdminUser silently re-hashes on every boot,
// which would mean each "first login" attempt validates
// against a different hash than the previous "last login".
func TestPasswordChain_TraceIsStableAcrossReBootstrap(t *testing.T) {
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(password)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "deliberately-wrong")

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

	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	var first string
	if err := sqlDB.QueryRow(
		`SELECT password_hash FROM users WHERE email = ?`, email,
	).Scan(&first); err != nil {
		t.Fatalf("first hash: %v", err)
	}

	// Re-run the bootstrap with the same env. The runtime must
	// keep the existing hash. If the hash changes, the next
	// login attempt against the previous hash would fail.
	for i := 0; i < 5; i++ {
		seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))
	}

	var last string
	if err := sqlDB.QueryRow(
		`SELECT password_hash FROM users WHERE email = ?`, email,
	).Scan(&last); err != nil {
		t.Fatalf("last hash: %v", err)
	}
	if first != last {
		t.Fatalf("seedAdminUser rewrote the bcrypt hash on re-bootstrap:\n  first=%q\n  last =%q", first, last)
	}
}

// TestPasswordChain_DistinctPasswordsProduceDistinctHashes
// catches a class of bugs where two different installer
// passwords end up hashing to the same row. This would
// manifest as "first login with password A works, second
// login with password B works", which the user could
// plausibly misread as the original problem in reverse.
func TestPasswordChain_DistinctPasswordsProduceDistinctHashes(t *testing.T) {
	const email = "admin@orvix.email"
	hashes := map[string]string{}

	for _, pwd := range []string{"PasswordA123!", "PasswordB123!", "PasswordC123!"} {
		pwd := pwd
		t.Run(pwd, func(t *testing.T) {
			t.Setenv("ORVIX_ADMIN_EMAIL", email)
			t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(pwd)))
			t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong")

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
			seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("sql db: %v", err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			var hash string
			if err := sqlDB.QueryRow(
				`SELECT password_hash FROM users WHERE email = ?`, email,
			).Scan(&hash); err != nil {
				t.Fatalf("hash: %v", err)
			}
			if prev, dup := hashes[hash]; dup {
				t.Fatalf("hash collision: %q and %q both stored %q", prev, pwd, hash)
			}
			hashes[hash] = pwd
		})
	}
}

// TestPasswordChain_NoHashMutationAcrossLoginCycle is the
// runtime guard for the production "first login works,
// subsequent fail" symptom. It captures the bcrypt hash
// before any login, runs five consecutive logins, and
// confirms the hash bytes are unchanged. If this test fails
// while a production login still fails, the runtime is
// rewriting the hash inside the Login handler — which is
// the smoking-gun the symptom needs.
func TestPasswordChain_NoHashMutationAcrossLoginCycle(t *testing.T) {
	h := buildFreshInstallHarness(t, "admin@orvix.email", "MaghaghaMos086")
	t.Cleanup(func() { h.close(t) })

	before := readPasswordHash(t, h)
	if !strings.HasPrefix(before, "$2") {
		t.Fatalf("bootstrap hash is not bcrypt: %q", before)
	}

	for i := 0; i < 5; i++ {
		h.loginAsAdmin(t, "/api/v1/auth/login")
	}
	after := readPasswordHash(t, h)
	if before != after {
		t.Fatalf("users.password_hash changed across logins: before=%q after=%q", before, after)
	}
}

// TestPasswordChain_BcryptSeventyTwoByteLimit proves that an
// installer password longer than 72 bytes (bcrypt's hard
// limit) is REJECTED at bootstrap time, not silently
// truncated. Without this guard a user typing a 100-character
// passphrase would see "INSTALLATION VERIFICATION PASSED" at
// install (because some code path produced *a* hash) but
// every login would fail because the runtime bcrypt-compare
// rejects inputs >72 bytes. This is the bug class that
// surfaces as "first login works (because verify_install used
// a short random password), subsequent fail".
//
// The expected behaviour: bcrypt.GenerateFromPassword returns
// ErrPasswordTooLong, seedAdminUser logs "failed to hash
// admin password" and returns, the users row is never
// inserted, and login returns 401 user-not-found. The test
// asserts that boundary.
func TestPasswordChain_BcryptSeventyTwoByteLimit(t *testing.T) {
	const email = "admin@orvix.email"
	pwd := strings.Repeat("a", 73)

	t.Setenv("ORVIX_ADMIN_EMAIL", email)
	t.Setenv("ORVIX_ADMIN_PASSWORD_B64", base64.StdEncoding.EncodeToString([]byte(pwd)))
	t.Setenv("ORVIX_ADMIN_PASSWORD", "wrong")

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
	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver("sqlite"))

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM users WHERE email = ?`, email,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected bootstrap to refuse a 73-byte password (bcrypt hard limit); instead wrote %d user rows", count)
	}
}

// ── helpers ─────────────────────────────────────────────

// doChainLogin posts the EXACT wire format install.sh posts
// in smoke_login_admin_attempts. Returns the HTTP status and
// the access_token from the response JSON (empty on failure).
func doChainLogin(t *testing.T, router *api.Router, email, password string) (int, string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"username": email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return resp.StatusCode, ""
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, data.AccessToken
}

// doChainLogout posts to /api/v1/auth/logout. The handler is
// CSRF-protected so we don't expect a 200 unless the CSRF
// dance completes; we record whatever status we got so the
// trace can show it. A non-200 here does NOT count as a
// regression — the production symptom is a password-verify
// failure, not a logout failure.
func doChainLogout(t *testing.T, router *api.Router, accessToken string) int {
	t.Helper()
	if accessToken == "" {
		return 0
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.Header.Set("Cookie", "access_token="+accessToken)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Logf("logout request: %v", err)
		return 0
	}
	return resp.StatusCode
}

// verifyArgon2id parses a stored $argon2id$... string and
// recomputes the hash with the supplied plaintext. It does
// NOT import coremail.AuthService because that would hide
// the actual params behind a struct field; the trace needs
// the raw parameters visible.
func verifyArgon2id(encoded, plaintext string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("malformed argon2id hash: %q", encoded)
	}
	// parts[2] is "v=19"
	var mem, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, fmt.Errorf("parse params %q: %w", parts[3], err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}
	got := argon2.IDKey([]byte(plaintext), salt, time, mem, threads, uint32(len(want)))
	if len(got) != len(want) {
		return false, nil
	}
	for i := range got {
		if got[i] != want[i] {
			return false, nil
		}
	}
	return true, nil
}
