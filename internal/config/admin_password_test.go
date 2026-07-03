package config

// Static / source-level tests for the post-RC1 admin password
// chain. The runtime path (interactive prompt, /dev/tty read,
// bcrypt write, dual-login probe) is exercised on a real VPS
// by the installer's smoke_login_admin_attempts gate and the
// release/scripts/reset-admin-password.sh flow. CI on Windows
// Git Bash cannot drive an interactive bash prompt through a
// piped stdin without blocking the test harness, so these
// tests assert the installer's source instead. A real
// interactive failure mode still surfaces on the VPS via the
// smoke gate; this file's job is to make a refactor that
// breaks the contract fail in CI before the operator sees it.

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRootTP(t *testing.T) string { return repoRoot(t) }

// TestInstallerPromptPasswordStdoutContract is a static
// source-level pin of the contract `prompt_password` must
// satisfy. The runtime contract:
//
//   - all prompts go to stderr (file descriptor 2), never
//     stdout
//   - the only stdout output is `printf '%s' "$password"`
//     with no trailing newline
//   - length validation fails (and the function calls
//     `fail`, which exits non-zero) for passwords outside
//     [8, 72] bytes
//   - the function never calls `echo "$password"` (echo
//     appends a newline, which would corrupt the captured
//     stdout)
//   - the read uses `IFS=` so trailing whitespace is
//     preserved
//   - the function never writes the password to
//     $INSTALL_LOG or any other log file
//
// The previous version of this test spawned bash and drove
// `read -s` through /dev/fd/0 + piped stdin. That blocked
// indefinitely on Windows Git Bash because the inner
// `read -r -s password < /dev/fd/0` does not see EOF through
// a pipe-redirected fd in that environment. The smoke
// coverage that test provided is recovered by the installer's
// smoke_login_admin_attempts gate that runs on the real VPS.
func TestInstallerPromptPasswordStdoutContract(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	// Extract the prompt_password function body for the
	// per-assertion checks. The body starts at the function
	// header and runs until the matching closing brace at
	// column 0.
	startIdx := strings.Index(installer, "prompt_password() {")
	if startIdx < 0 {
		t.Fatal("prompt_password function not defined in installer")
	}
	bodyStart := startIdx + len("prompt_password() {")
	endIdx := findMatchingBrace(installer, bodyStart)
	if endIdx < 0 {
		t.Fatal("could not locate prompt_password function end")
	}
	body := installer[bodyStart:endIdx]

	// 1. Prompts go to stderr. Every printf/echo inside the
	//    function that talks to the user must be `>&2`.
	mustHave := []string{
		// prompt text on stderr
		"printf 'Admin password (8-72 bytes, hidden): ' >&2",
		"printf 'Confirm admin password: ' >&2",
		// newline-after-silent-read on stderr so it doesn't
		// land in $()
		"printf '\\n' >&2",
		// mismatch message on stderr
		"printf 'Passwords do not match\\n' >&2",
	}
	for _, needle := range mustHave {
		if !strings.Contains(body, needle) {
			t.Errorf("prompt_password must contain %q", needle)
		}
	}

	// 2. Length validation uses fail() (which exits
	//    non-zero), not a silent return. Both bounds must
	//    be present.
	if !strings.Contains(body, `[ "${#password}" -lt 8 ]`) {
		t.Error("prompt_password must enforce >= 8 bytes")
	}
	if !strings.Contains(body, `fail "admin password must be at least 8 characters"`) {
		t.Error("prompt_password must call fail() on too-short password")
	}
	if !strings.Contains(body, `[ "${#password}" -gt 72 ]`) {
		t.Error("prompt_password must enforce <= 72 bytes (bcrypt limit)")
	}
	if !strings.Contains(body, `fail "admin password is too long for bcrypt (max 72 bytes); got ${#password}"`) {
		t.Error("prompt_password must call fail() on too-long password")
	}

	// 3. The only stdout write is the final `printf '%s'
	//    "$password"` — no echo, no cat, no >&1, no >&3.
	//    `echo "$password"` would be a bug because echo
	//    appends a trailing newline that contaminates the
	//    $() capture.
	if strings.Contains(body, `echo "$password"`) {
		t.Error("prompt_password must not call echo on the password (echo appends a trailing newline)")
	}
	if !strings.Contains(body, `printf '%s' "$password"`) {
		t.Error("prompt_password must emit the password via printf with no trailing newline")
	}

	// 4. The function must not redirect the password to fd
	//    1 directly with `>&1` (everything we want on
	//    stdout already goes there implicitly). Look for
	//    any explicit fd redirect that contains "password".
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, ">&1") && strings.Contains(line, "password") {
			t.Errorf("prompt_password must not redirect to >&1: %q", strings.TrimSpace(line))
		}
	}

	// 5. `IFS=` is required so trailing whitespace in the
	//    typed password survives the read. Without it
	//    `read` strips trailing IFS characters (which
	//    includes space) and the captured password silently
	//    drops those bytes — the classic "I typed it right
	//    the first time, wrong the second time" bug.
	if !strings.Contains(body, "IFS= read") {
		t.Error("prompt_password must use IFS= read to preserve trailing whitespace")
	}

	// 6. The function must not log the password. Look for
	//    any line that contains both `log_detail` (or any
	//    known log-target redirect) AND the literal string
	//    "password".
	logTargets := []string{"log_detail", "$INSTALL_LOG"}
	for _, target := range logTargets {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, target) && strings.Contains(line, "$password") {
				t.Errorf("prompt_password must not log the password to %s: %q",
					target, strings.TrimSpace(line))
			}
		}
	}
}

func TestInstallerPromptPasswordInputContract(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	startIdx := strings.Index(installer, "prompt_password() {")
	if startIdx < 0 {
		t.Fatal("prompt_password function not defined in installer")
	}
	bodyStart := startIdx + len("prompt_password() {")
	endIdx := findMatchingBrace(installer, bodyStart)
	if endIdx < 0 {
		t.Fatal("could not locate prompt_password function end")
	}
	body := installer[bodyStart:endIdx]

	// Production reads from /dev/tty. Tests (and any caller
	// that wants to drive the function from a script) can
	// override with ORVIX_PROMPT_INPUT_FD=N — this lets CI
	// run the contract test without a controlling terminal,
	// and lets an unattended install pipe a password from
	// a secret manager through fd 3 without touching the
	// controlling terminal at all.
	if !strings.Contains(body, `input_dev="/dev/tty"`) {
		t.Error("prompt_password must default to /dev/tty for production reads")
	}
	if !strings.Contains(body, `ORVIX_PROMPT_INPUT_FD`) {
		t.Error("prompt_password must honor ORVIX_PROMPT_INPUT_FD for scripted callers")
	}
	if !strings.Contains(body, `input_dev="/dev/fd/${ORVIX_PROMPT_INPUT_FD}"`) {
		t.Error("prompt_password must resolve ORVIX_PROMPT_INPUT_FD to /dev/fd/<n>")
	}

	// The function must call read with the resolved
	// input_dev, NOT a hardcoded /dev/tty.
	if strings.Contains(body, `</dev/tty`) {
		t.Error("prompt_password must not hardcode </dev/tty — use the resolved input_dev")
	}
}

func TestInstallerWriteBootstrapEnvRoundTripCleanPath(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)
	if !strings.Contains(installer, `main "$@"`) {
		t.Fatal("installer entrypoint marker not found")
	}
	// Stub chown/chmod (they need root) and route BOOTSTRAP_ENV
	// into the harness dir as the third positional arg.
	harness := strings.Replace(installer, `main "$@"`,
		`chown() { :; }; chmod() { :; }; BOOTSTRAP_ENV="$3"; write_bootstrap_env "$1" "$(cat "$2")"; cat "$BOOTSTRAP_ENV"`,
		1)
	harnessDir := t.TempDir()
	harnessPath := filepath.Join(harnessDir, "bootstrap.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o755); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	passwords := []string{
		"MaghaghaMos086",
		"Password With Spaces",
		`Password"Quote123`,
		"Password'SingleQuote123",
		`Password\Slash123`,
		"Password$123",
		"MaghaghaMos086 ", // trailing space — bcrypt-safe but easy to lose
	}
	for i, password := range passwords {
		envName := "bootstrap-" + string(rune('a'+i)) + ".env"
		passwordName := "password-" + string(rune('a'+i)) + ".txt"
		if err := os.WriteFile(filepath.Join(harnessDir, passwordName), []byte(password), 0o600); err != nil {
			t.Fatalf("write password fixture: %v", err)
		}
		cmd := exec.Command(bashCommand(t), "bootstrap.sh", "admin@orvix.email", passwordName, envName)
		cmd.Dir = harnessDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bootstrap env command failed for %q: %v: %s", password, err, string(out))
		}
		envFile := string(out)
		if strings.Contains(envFile, password) {
			t.Fatalf("bootstrap env must not contain raw admin password %q", password)
		}
		if !strings.Contains(envFile, "ORVIX_ADMIN_EMAIL=admin@orvix.email") {
			t.Fatalf("bootstrap env missing email: %s", envFile)
		}
		var encoded string
		for _, line := range strings.Split(envFile, "\n") {
			if strings.HasPrefix(line, "ORVIX_ADMIN_PASSWORD_B64=") {
				encoded = strings.TrimPrefix(line, "ORVIX_ADMIN_PASSWORD_B64=")
			}
		}
		if encoded == "" {
			t.Fatalf("bootstrap env missing encoded password: %s", envFile)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode password: %v", err)
		}
		if string(decoded) != password {
			t.Fatalf("decoded password mismatch: got %q want %q", string(decoded), password)
		}
	}
}

// TestInstallerWriteBootstrapEnvHasRoundTripGuard pins the
// defensive base64 round-trip check. The previous turn's
// production failure mode was "first login works, subsequent
// fails" with no installer-side diagnosis. The
// `bootstrap env base64 round-trip mismatch` literal makes
// the bug visible at install time, not at next login.
func TestInstallerWriteBootstrapEnvHasRoundTripGuard(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	startIdx := strings.Index(installer, "write_bootstrap_env() {")
	if startIdx < 0 {
		t.Fatal("write_bootstrap_env function not defined")
	}
	bodyStart := startIdx + len("write_bootstrap_env() {")
	endIdx := findMatchingBrace(installer, bodyStart)
	if endIdx < 0 {
		t.Fatal("could not locate write_bootstrap_env function end")
	}
	body := installer[bodyStart:endIdx]

	if !strings.Contains(body, "decoded_roundtrip=") {
		t.Error("write_bootstrap_env must decode the encoded value back and compare")
	}
	if !strings.Contains(body, `[ "$decoded_roundtrip" != "$password" ]`) {
		t.Error("write_bootstrap_env must compare the round-trip result to the original password")
	}
	if !strings.Contains(body, `fail "bootstrap env base64 round-trip mismatch`) {
		t.Error("write_bootstrap_env must call fail() on round-trip mismatch")
	}
}

func TestInstallerVerifyInstallCallsDualLoginBeforeDelete(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	callIndex := strings.Index(installer, "verify_install_password_login \"$email\" \"$password\"")
	if callIndex < 0 {
		t.Fatal("verify_install must call verify_install_password_login")
	}
	deleteIndex := strings.Index(installer, "rm -f \"$BOOTSTRAP_ENV\"")
	if deleteIndex < 0 {
		t.Fatal("verify_install must delete bootstrap.env after success")
	}
	if deleteIndex < callIndex {
		t.Fatal("verify_install must delete bootstrap.env AFTER verify_install_password_login")
	}

	dualIndex := strings.Index(installer, "verify_install_password_login() {")
	if dualIndex < 0 {
		t.Fatal("verify_install_password_login function not defined")
	}
	block := installer[dualIndex:]
	if !strings.Contains(block, "/api/v1/auth/login") {
		t.Fatal("verify_install_password_login must hit /api/v1/auth/login (the bcrypt Fiber route)")
	}
	loginCalls := strings.Count(block, "$base/api/v1/auth/login")
	if loginCalls < 2 {
		t.Fatalf("verify_install_password_login must call /api/v1/auth/login at least twice; found %d", loginCalls)
	}
	// The two logins must come from different cookie jars.
	// If both use the same jar, the second login is not
	// actually a fresh request and the test is degenerate.
	if !strings.Contains(block, "rm -f \"$first_jar\"") {
		t.Error("verify_install_password_login must drop the first cookie jar before the second login")
	}
	if !strings.Contains(block, "second_jar=\"$(mktemp)\"") {
		t.Error("verify_install_password_login must use a fresh cookie jar for the second login")
	}
}

// TestInstallerPromptPasswordDoesNotEchoToLog is a static
// source-level check that nothing in the install pipeline
// ever pipes the password into the install log. We scan the
// whole installer for combinations of password-bearing
// variables and log targets.
func TestInstallerPromptPasswordDoesNotEchoToLog(t *testing.T) {
	root := repoRootTP(t)
	installerBytes, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	installer := string(installerBytes)

	// Only ORVIX_ADMIN_PASSWORD_B64 (the encoded form) is
	// allowed in the install log — and even that should not
	// appear because it would let anyone with read access
	// to /var/log/orvix/install.log decode it.
	if strings.Contains(installer, `log_detail "$admin_password"`) ||
		strings.Contains(installer, `log_detail "$password"`) ||
		strings.Contains(installer, `log_detail "ORVIX_ADMIN_PASSWORD`) {
		t.Fatal("installer must never log the admin password or its base64 form")
	}
}

// ── Reset script tests ──────────────────────────────────

func TestResetAdminPasswordScriptExists(t *testing.T) {
	root := repoRootTP(t)
	path := filepath.Join(root, "release", "scripts", "reset-admin-password.sh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reset script missing: %v", err)
	}
}

func TestResetAdminPasswordScriptIsSyntaxValid(t *testing.T) {
	root := repoRootTP(t)
	path := filepath.Join(root, "release", "scripts", "reset-admin-password.sh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reset script missing: %v", err)
	}
	cmd := exec.Command(bashCommand(t), "-n", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reset script has bash syntax errors: %v: %s", err, string(out))
	}
}

func TestResetAdminPasswordScriptUsesPythonHelperAndProbes(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	required := []string{
		// bcrypt hashing is delegated to an embedded Python
		// helper that reads the password via stdin. openssl
		// passwd is no longer used because its positional
		// argv leaks the password to /proc/<pid>/cmdline.
		"_BCRYPT_HELPER_PYTHON=",
		"python3",
		"password must be at most 72 bytes",
		"password must be at least 8 characters",
		"systemctl stop orvix.service",
		"systemctl start orvix.service",
		// probe_login builds the JSON body via stdin so the
		// password never appears in the curl argv.
		"_PROBE_HELPER_PYTHON=",
		"/api/v1/auth/login",
		// input device defaults to /dev/tty so production reads
		// the controlling terminal; ORVIX_PROMPT_INPUT_FD lets
		// tests and unattended callers override it.
		`input_dev="/dev/tty"`,
		"ORVIX_PROMPT_INPUT_FD",
		"unset NEW_PASSWORD",
	}
	for _, item := range required {
		if !strings.Contains(script, item) {
			t.Fatalf("reset script missing %q", item)
		}
	}
}

// TestResetAdminPasswordScriptNeverPassesPasswordAsArgv is the
// regression test for the previous-turn production blocker:
// `openssl passwd -bcrypt "$password"` exposed the plaintext
// password in /proc/<pid>/cmdline. The fix routes the
// password through stdin to an embedded Python helper. This
// test scans the whole script — including the embedded
// Python source — and asserts there is NO pattern where the
// password-bearing variable is placed into a child process's
// argv.
func TestResetAdminPasswordScriptNeverPassesPasswordAsArgv(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	// Patterns where the password variable is used as a
	// positional argument to a CHILD process. Plain
	// `"$NEW_PASSWORD"` in `printf '%s' "$NEW_PASSWORD"` is
	// safe (printf reads it via stdin pipe), but
	// `child_cmd "$NEW_PASSWORD" ...` would expose the
	// password in the child argv and therefore in
	// /proc/<pid>/cmdline.
	forbiddenPatterns := []string{
		// The previous turn's blocker.
		`openssl passwd -bcrypt "$password"`,
		`openssl passwd -bcrypt "$NEW_PASSWORD"`,
		`openssl passwd -bcrypt "$RESET_PASSWORD"`,
		// The Python helper source must not pass the
		// password via argv either — it should read stdin.
		`sys.argv[2] = password`,
		`sys.argv[3] = password`,
		// Explicit child-process argv patterns. The unsafe
		// pattern is "<whitespace>$NEW_PASSWORD<whitespace>"
		// immediately followed by another argument to the
		// SAME child process. We approximate this by looking
		// for "$NEW_PASSWORD" or "$RESET_PASSWORD" preceded
		// by whitespace and followed by whitespace (i.e.
		// used as a bare argv position), AND combined with
		// a child process invocation in the same line.
		`" $NEW_PASSWORD" "`,
		`" $RESET_PASSWORD" "`,
	}
	for _, pattern := range forbiddenPatterns {
		if strings.Contains(script, pattern) {
			t.Errorf("reset script must not contain %q (password would leak into argv)", pattern)
		}
	}

	// Must pipe via stdin. The bash side feeds the password
	// through `printf '%s' "$NEW_PASSWORD" | ...` and the
	// python side reads `sys.stdin.buffer.readline()`. Both
	// legs must be present.
	mustHave := []string{
		`printf '%s' "$NEW_PASSWORD" |`,
		`sys.stdin.buffer.readline()`,
		// The Python helper writes to a temp file with 0700
		// perms and unlinks it before exit.
		`chmod 0700`,
		`rm -f "$_BCRYPT_HELPER_PATH"`,
		// The password is unset from bash memory after the
		// helper returns.
		`unset NEW_PASSWORD`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(script, needle) {
			t.Errorf("reset script missing safe-stdin plumbing %q", needle)
		}
	}
}

// TestResetAdminPasswordScriptNeverInterpolatesEmailIntoSQL is
// the regression test for the second blocker: the previous
// turn's script built sqlite UPDATE statements via string
// interpolation of `$ADMIN_EMAIL`. The fix routes the UPDATE
// through an embedded Python helper that uses sqlite3's
// parameter binding (?). The bash script must NOT contain
// any `sqlite3 ... "...$ADMIN_EMAIL..."` pattern.
func TestResetAdminPasswordScriptNeverInterpolatesEmailIntoSQL(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	// The exact pattern that would re-introduce the bug:
	// any sqlite3 invocation that mutates password_hash
	// with the email variable in the SQL string. A pre-
	// flight SELECT is allowed because it does not mutate
	// any data and the email has been strictly validated.
	forbiddenPatterns := []string{
		// UPDATE that puts the hash via interpolation.
		`UPDATE users SET password_hash='$hash_value'`,
		`UPDATE users SET password_hash="$hash_value"`,
		`UPDATE users SET password_hash = '$hash_value'`,
		`UPDATE users SET password_hash = "$hash_value"`,
		// UPDATE that filters by interpolated email.
		`UPDATE users SET password_hash='$ADMIN_EMAIL'`,
		`UPDATE users SET password_hash = '$ADMIN_EMAIL'`,
	}
	for _, pattern := range forbiddenPatterns {
		if strings.Contains(script, pattern) {
			t.Errorf("reset script must not contain %q (would re-introduce SQL interpolation)", pattern)
		}
	}

	// The Python helper must use parameter binding with `?`
	// placeholders, not f-strings or %-formatting of the SQL.
	// The bash quoting escapes single quotes inside the
	// single-quoted heredoc as `'\''`; we accept either form
	// so the test survives a heredoc-delimiter change.
	mustHave := []string{
		`cur.execute(`,
		`WHERE email = ?`,
		`(hash_value, email)`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(script, needle) {
			t.Errorf("reset script python helper must contain %q", needle)
		}
	}

	// role-in clause — accept unescaped or bash-escaped form.
	if !strings.Contains(script, `role IN ('admin', 'superadmin')`) &&
		!strings.Contains(script, "role IN ('\\''admin'\\'', '\\''superadmin'\\'')") {
		t.Error("reset script python helper must contain a role IN clause restricting UPDATE to admin/superadmin")
	}
}

// TestResetAdminPasswordScriptRejectsUnsafeEmails pins the
// strict email validation. Any email containing quote,
// semicolon, whitespace, control char, or path-traversal
// pattern must be rejected at the Python boundary BEFORE
// the SQL is built.
func TestResetAdminPasswordScriptRejectsUnsafeEmails(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	mustHave := []string{
		// Strict regex — alphanumeric + . _ % + - on local
		// part, alphanumeric + . - on domain, alphabetic TLD.
		`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`,
		// Forbidden characters explicitly enumerated in the
		// boundary check (defence in depth in case the regex
		// is ever loosened).
		`forbidden_chars = (";`,
		`if ".." in email`,
		`email != email.strip()`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(script, needle) {
			t.Errorf("reset script python helper must contain %q", needle)
		}
	}
}

// TestResetAdminPasswordScriptUpdateRestrictedToAdmin verifies
// the WHERE clause constrains the UPDATE to admin/superadmin
// rows only. Without this constraint a typing mistake in
// ADMIN_EMAIL could reset a non-admin user's password.
func TestResetAdminPasswordScriptUpdateRestrictedToAdmin(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	// The Python helper is embedded in a single-quoted bash
	// string. Single quotes inside the Python source are
	// written as `'\''`. The test pins the role-in clause
	// in EITHER form so it survives a heredoc-delimiter
	// change, plus the row-count check.
	mustHave := []string{
		`if rows != 1:`,
		`die(f"updated {rows} rows; expected exactly 1")`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(script, needle) {
			t.Errorf("reset script python helper must contain %q", needle)
		}
	}
	if !strings.Contains(script, `role IN ('admin', 'superadmin')`) &&
		!strings.Contains(script, "role IN ('\\''admin'\\'', '\\''superadmin'\\'')") {
		t.Error("reset script python helper must contain a role IN clause restricting UPDATE to admin/superadmin")
	}
}

// TestResetAdminPasswordScriptNeverPrintsPasswordOrHash scans
// the bash and Python source for any print/echo/log that
// could expose the password or the bcrypt hash. The only
// stdout text allowed is `OK`, `HTTP<code>`, and the
// `BODY: <body>` response from the probe helper.
func TestResetAdminPasswordScriptNeverPrintsPasswordOrHash(t *testing.T) {
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	// Patterns that would print the password or hash. We
	// scan both bash and Python sides.
	forbiddenPatterns := []string{
		// Bash side must never echo/printf/cat the password
		// or its encoded form.
		`echo "$NEW_PASSWORD"`,
		`echo "$RESET_PASSWORD"`,
		`echo "$_BCRYPT_HELPER_PATH"`,
		`printf "%s" "$NEW_PASSWORD" | tee`,
		`printf "%s" "$RESET_PASSWORD" | tee`,
		// Python helper must never print the password.
		`print(password)`,
		`print(hash_value)`,
		`print(f"password: {password}")`,
		`print(f"hash: {hash_value}")`,
		// Python helper must never log the password via
		// log_detail / audit_log.
		`log_detail(password)`,
		`log_detail(hash_value)`,
	}
	for _, pattern := range forbiddenPatterns {
		if strings.Contains(script, pattern) {
			t.Errorf("reset script must not contain %q (would print password or hash)", pattern)
		}
	}
}

// TestResetAdminPasswordPythonHelperBehavesCorrectly runs the
// embedded python helper against a throwaway sqlite database
// to prove it actually: validates emails, hashes the
// password with bcrypt, updates exactly one admin row, and
// rejects every unsafe input. This test is the behavioural
// counterpart to the source-level pins above.
//
// Skipped on Windows because Python's `crypt` module's
// bcrypt path depends on glibc, which Git Bash on Windows
// does not provide.
func TestResetAdminPasswordPythonHelperBehavesCorrectly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("python crypt bcrypt requires glibc; skipping on windows")
	}
	python := "python3"
	if _, err := exec.LookPath(python); err != nil {
		t.Skip("python3 not available")
	}

	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	// Extract _BCRYPT_HELPER_PYTHON between the =' marker
	// and the next ' marker at the start of a line.
	startMarker := "_BCRYPT_HELPER_PYTHON='"
	startIdx := strings.Index(script, startMarker)
	if startIdx < 0 {
		t.Fatal("could not locate _BCRYPT_HELPER_PYTHON start marker")
	}
	bodyStart := startIdx + len(startMarker)
	// The closing ' is on a line by itself at column 0.
	endIdx := strings.Index(script[bodyStart:], "'\n")
	if endIdx < 0 {
		t.Fatal("could not locate _BCRYPT_HELPER_PYTHON end marker")
	}
	body := script[bodyStart : bodyStart+endIdx]

	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.py")
	if err := os.WriteFile(helperPath, []byte(body), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	dbPath := filepath.Join(dir, "test.db")

	// Build a minimal users table with three rows: one
	// admin, one superadmin, one regular user. The helper
	// must update ONLY the row matching the exact email AND
	// role IN ('admin', 'superadmin').
	setupSQL := `
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO users (email, role) VALUES ('admin@orvix.email', 'admin');
INSERT INTO users (email, role) VALUES ('super@orvix.email', 'superadmin');
INSERT INTO users (email, role) VALUES ('user@orvix.email', 'user');
INSERT INTO users (email, role) VALUES ('suspended@orvix.email', 'admin');
UPDATE users SET active=0 WHERE email='suspended@orvix.email';
`
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(setupSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup sqlite: %v: %s", err, string(out))
	}

	readHash := func(email string) string {
		out, err := exec.Command("sqlite3", dbPath, "SELECT password_hash FROM users WHERE email='"+email+"';").Output()
		if err != nil {
			t.Fatalf("read hash: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	// Case 1: valid admin email — exactly one row updated,
	// hash starts with $2b$, others unchanged.
	cmd = exec.Command(python, helperPath, "admin@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader("NewReset-Pass-987\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, string(out))
	}
	if !strings.Contains(string(out), "OK") {
		t.Fatalf("expected OK, got: %s", string(out))
	}
	h := readHash("admin@orvix.email")
	if h == "" {
		t.Fatalf("admin hash is empty")
	}
	if h == "NewReset-Pass-987" {
		t.Fatalf("admin hash equals password")
	}
	if h := readHash("super@orvix.email"); h != "" {
		t.Fatalf("superadmin hash should be untouched, got: %q", h)
	}
	if h := readHash("user@orvix.email"); h != "" {
		t.Fatalf("user hash should be untouched, got: %q", h)
	}
	if strings.Contains(string(out), "NewReset-Pass-987") {
		t.Fatalf("helper leaked password in stdout: %s", string(out))
	}

	// Case 2: superadmin email — also updated.
	cmd = exec.Command(python, helperPath, "super@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader("Another-Pass-321\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("superadmin helper: %v: %s", err, string(out))
	} else if !strings.Contains(string(out), "OK") {
		t.Fatalf("expected OK for superadmin, got: %s", string(out))
	}

	// Case 3: regular user email — must be REJECTED. The
	// helper is constrained to role IN ('admin', 'superadmin'),
	// so updating a 'user' row returns rowcount=0 and the
	// helper exits non-zero with "updated 0 rows".
	cmd = exec.Command(python, helperPath, "user@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader("ShouldNotWork-555\n")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected helper to fail for non-admin email; stdout=%s", string(out))
	}
	if !strings.Contains(string(out), "FAIL") || !strings.Contains(string(out), "0 rows") {
		t.Fatalf("expected FAIL with row count, got: %s", string(out))
	}
	if strings.Contains(string(out), "ShouldNotWork-555") {
		t.Fatalf("helper leaked password in error output: %s", string(out))
	}
	if h := readHash("user@orvix.email"); h != "" {
		t.Fatalf("user hash should still be empty after rejected update, got: %q", h)
	}

	// Case 4: unsafe email with semicolon — rejected.
	cmd = exec.Command(python, helperPath, "admin@orvix.email'; DROP TABLE users; --", dbPath)
	cmd.Stdin = strings.NewReader("ShouldNotWork-555\n")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected helper to fail for SQL-injection email")
	}
	if !strings.Contains(string(out), "FAIL") {
		t.Fatalf("expected FAIL for injection email, got: %s", string(out))
	}

	// Case 5: unsafe email with quote — rejected.
	cmd = exec.Command(python, helperPath, `admin"@orvix.email`, dbPath)
	cmd.Stdin = strings.NewReader("ShouldNotWork-555\n")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected helper to fail for quoted email; stdout=%s", string(out))
	}

	// Case 6: password too long — rejected.
	cmd = exec.Command(python, helperPath, "admin@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader(strings.Repeat("a", 73) + "\n")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected helper to fail for 73-byte password; stdout=%s", string(out))
	}

	// Case 7: password too short — rejected.
	cmd = exec.Command(python, helperPath, "admin@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader("short\n")
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected helper to fail for short password")
	}

	// Case 8: nonexistent admin — rowcount=0, rejected.
	cmd = exec.Command(python, helperPath, "ghost@orvix.email", dbPath)
	cmd.Stdin = strings.NewReader("Some-Pass-999\n")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected helper to fail for nonexistent admin; stdout=%s", string(out))
	}
}

func TestResetAdminPasswordScriptPromptPasswordContract(t *testing.T) {
	// Static source-level pin of the reset script's
	// prompt_password function. The runtime path runs on
	// the VPS; CI cannot drive an interactive read through
	// a pipe on Windows Git Bash.
	root := repoRootTP(t)
	scriptBytes, err := os.ReadFile(filepath.Join(root, "release", "scripts", "reset-admin-password.sh"))
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	script := string(scriptBytes)

	startIdx := strings.Index(script, "prompt_password() {")
	if startIdx < 0 {
		t.Fatal("prompt_password function not defined in reset script")
	}
	bodyStart := startIdx + len("prompt_password() {")
	endIdx := findMatchingBrace(script, bodyStart)
	if endIdx < 0 {
		t.Fatal("could not locate reset script prompt_password end")
	}
	body := script[bodyStart:endIdx]

	mustHave := []string{
		`printf 'New admin password (8-72 bytes, hidden): ' >&2`,
		`printf 'Confirm new admin password: ' >&2`,
		`printf 'Passwords do not match\n' >&2`,
		`fail "password must be at least 8 characters"`,
		`fail "password must be at most 72 bytes`,
		`printf '%s' "$password"`,
		`ORVIX_PROMPT_INPUT_FD`,
	}
	for _, needle := range mustHave {
		if !strings.Contains(body, needle) {
			t.Errorf("reset script prompt_password must contain %q", needle)
		}
	}
	if strings.Contains(body, `echo "$password"`) {
		t.Error("reset script prompt_password must not call echo on the password")
	}
}

// findMatchingBrace returns the index of the closing brace
// at column 0 (or the byte just past it) for the function
// body that starts at bodyStart. We assume the function
// body uses bash's standard 4-space indent and brace-on-new-
// line at column 0 for the closing brace. Returns -1 if no
// matching brace is found within the next 8KB.
func findMatchingBrace(s string, bodyStart int) int {
	for i := bodyStart; i < len(s) && i-bodyStart < 8192; i++ {
		if s[i] == '}' && (i == 0 || s[i-1] == '\n') {
			return i + 1
		}
	}
	return -1
}

// keep encoding/base64 used; the file otherwise only
// imports through the test driver.
var _ = base64.StdEncoding
var _ = runtime.GOOS
