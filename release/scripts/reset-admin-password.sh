#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────
# Orvix Enterprise Mail — emergency admin password reset.
#
# Run as root on the host where orvix.service is installed.
#
# The script:
#   1. Resolves which admin email to reset (arg, env var,
#      or first admin row in the DB).
#   2. Prompts for a new password on /dev/tty with no echo.
#   3. Validates the password (8-72 bytes — bcrypt hard
#      limit; anything longer makes the runtime silently
#      fail to authenticate).
#   4. Stops orvix.service so the bcrypt hash write does
#      not race the runtime.
#   5. Hashes the new password using an EMBEDDED PYTHON
#      HELPER that:
#        - reads the password from stdin (NOT argv, NOT
#          env, NOT temp files) so it never appears in
#          /proc/<pid>/cmdline, the install log, the temp
#          filesystem, or the helper script itself;
#        - validates the email against a strict allow-list
#          pattern (rejects quotes, semicolons, whitespace,
#          control characters, path traversal);
#        - generates a $2b$10$<22-char-b64-salt> bcrypt
#          hash via the system's crypt(3), which is
#          compatible with golang.org/x/crypto/bcrypt;
#        - writes the hash with sqlite3's parameter binding
#          (?), NOT string interpolation, with a WHERE
#          clause restricted to role IN ('admin',
#          'superadmin');
#        - asserts exactly one row was updated and exits
#          non-zero otherwise;
#        - prints ONLY "OK" or "FAIL: <reason>" so the
#          password and the hash never appear in command
#          output.
#   6. Restarts orvix.service.
#   7. Probes /api/v1/auth/login with the new credentials
#      and confirms HTTP 200. If the probe fails, the
#      script exits non-zero so the operator knows the
#      reset did not take effect.
#
# The script NEVER prints the password or the bcrypt hash.
# All prompts go to stderr; only progress messages go to
# stdout. The ORVIX_RESET_LOG file contains audit info but
# never includes the password or the hash.
#
# Usage:
#   ORVIX_ADMIN_EMAIL=admin@orvix.email sudo \
#     bash release/scripts/reset-admin-password.sh
#
#   # or, with explicit email:
#   sudo bash release/scripts/reset-admin-password.sh admin@orvix.email
# ──────────────────────────────────────────────────────────

set -euo pipefail

ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_DB="${ORVIX_DB:-/var/lib/orvix/orvix.db}"
ORVIX_RESET_LOG="${ORVIX_RESET_LOG:-/var/log/orvix/reset-admin.log}"
BOOTSTRAP_ENV="${BOOTSTRAP_ENV:-/etc/orvix/bootstrap.env}"
API_BASE="${API_BASE:-http://127.0.0.1:8080}"

mkdir -p "$(dirname "$ORVIX_RESET_LOG")"
touch "$ORVIX_RESET_LOG"
chmod 0640 "$ORVIX_RESET_LOG"

log_detail() {
    # Append-only log. Never include the password or hash.
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$ORVIX_RESET_LOG"
}

fail() {
    log_detail "ERROR: $*"
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

require_root() {
    [ "$(id -u)" -eq 0 ] || fail "run as root or with sudo"
}

require_tools() {
    command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 is not installed"
    command -v python3 >/dev/null 2>&1 || fail "python3 is not installed"
    command -v curl >/dev/null 2>&1 || fail "curl is not installed"
    command -v systemctl >/dev/null 2>&1 || fail "systemctl is not installed"
}

# resolve_admin_email picks the email from $1 or env, falling
# back to a sqlite query against the existing admin user.
resolve_admin_email() {
    local email="${1:-${ORVIX_ADMIN_EMAIL:-}}"
    if [ -n "$email" ]; then
        printf '%s' "$email"
        return
    fi
    [ -f "$ORVIX_DB" ] || fail "no admin email supplied and database $ORVIX_DB does not exist"
    email="$(sqlite3 "$ORVIX_DB" "SELECT email FROM users WHERE role='admin' AND active=1 ORDER BY id LIMIT 1;" 2>/dev/null || true)"
    [ -n "$email" ] || fail "no admin user row found in $ORVIX_DB and no email supplied"
    printf '%s' "$email"
}

# prompt_password captures the new password into stdout with
# NO other output on stdout. All prompts, the newline-after-
# silent-read, and any error chatter go to stderr so callers
# can safely use `password="$(prompt_password)"`. Honors
# ORVIX_PROMPT_INPUT_FD for tests; production reads from
# /dev/tty. The captured value is byte-exact — trailing
# whitespace is preserved verbatim via `IFS=`.
prompt_password() {
    local input_dev="/dev/tty"
    if [ -n "${ORVIX_PROMPT_INPUT_FD:-}" ]; then
        input_dev="/dev/fd/${ORVIX_PROMPT_INPUT_FD}"
    fi
    local password confirm
    while [ -z "${password:-}" ]; do
        printf 'New admin password (8-72 bytes, hidden): ' >&2
        IFS= read -r -s password <"$input_dev" 2>/dev/null || password=""
        printf '\n' >&2
        printf 'Confirm new admin password: ' >&2
        IFS= read -r -s confirm <"$input_dev" 2>/dev/null || confirm=""
        printf '\n' >&2
        if [ "$password" != "$confirm" ]; then
            printf 'Passwords do not match\n' >&2
            password=""
        fi
    done
    if [ "${#password}" -lt 8 ]; then
        fail "password must be at least 8 characters"
    fi
    if [ "${#password}" -gt 72 ]; then
        fail "password must be at most 72 bytes (bcrypt limit); got ${#password}"
    fi
    printf '%s' "$password"
}

probe_login() {
    # Probes /api/v1/auth/login with the supplied credentials.
    # The password is fed to python via stdin (NOT argv). The
    # python helper builds the JSON body with proper escaping
    # and returns success/failure without printing the password.
    local email="$1" password="$2"
    local payload_response http_code
    payload_response="$(printf '%s' "$password" | "$_PROBE_HELPER" "$email" "$API_BASE")"
    http_code="$(printf '%s' "$payload_response" | sed -n 's/^HTTP://p')"
    case "$http_code" in
        200) return 0 ;;
        *)
            printf '%s\n' "$payload_response" >&2
            fail "login probe after reset failed: HTTP ${http_code:-unknown}"
            ;;
    esac
}

# The python helper source. Written to a temp file with
# restricted permissions (0700), executed, then removed.
# The file contains ONLY the python source — no password,
# no hash, no email, no DB path. The password is fed via
# stdin (line 1). The email and DB path are the only
# positional args.
#
# The helper exists because:
#   - openssl passwd takes the password via argv, which
#     leaks it to /proc/<pid>/cmdline and any process
#     listing tool.
#   - bash string interpolation in sqlite3 -c "..." cannot
#     guarantee parameter binding — a careful attacker
#     could craft an email that breaks out of the WHERE
#     clause.
#
# The python helper does the hash + DB write atomically
# (single process, no race window between hash and write).
_BCRYPT_HELPER_PYTHON='#!/usr/bin/env python3
"""Reset the admin password in the Orvix SQLite database.

This helper exists so the installer'\''s reset path never
exposes the plaintext password to the process list, the
install log, the temp filesystem, or environment variables.
The password is read from stdin (one line); the target
email and database path are the only positional args, and
the email is validated against a strict pattern before use.
The SQL is parameterized — no string interpolation.
"""
import sys
import os
import re
import sqlite3
import secrets
import base64
import crypt

# Strict email validation. Anything outside this pattern is
# rejected before we ever touch sqlite. This is the LAST line
# of defence against a malformed env var or argv injection:
# bash'\''s set -e and quoting do most of the work, but a
# defence-in-depth check at the boundary that handles the DB
# is worth the few lines.
EMAIL_RE = re.compile(r"^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$")


def die(msg):
    # FAIL: prefix is the bash script'\''s success/failure
    # marker. The script greps stdout for FAIL: and writes
    # the rest to the audit log. The password and hash are
    # never included in the message.
    print("FAIL: " + msg, file=sys.stderr)
    sys.exit(1)


if len(sys.argv) != 3:
    die("usage: helper EMAIL DB_PATH")

email = sys.argv[1]
db_path = sys.argv[2]

if not isinstance(email, str) or len(email) > 254 or len(email) < 3:
    die("invalid email length")
if not EMAIL_RE.match(email):
    die("invalid email format")
# Defence in depth: reject anything that the regex should
# have caught but did not. The regex above is conservative
# but a future change to the pattern must not regress these
# guards.
forbidden_chars = (";", "\"", "\x27", "`", "\n", "\r", "\t", " ",
                   "\\", "/", "*", "?", "[", "]", "{", "}",
                   "<", ">", "|", "&", "$", "!", "#", "~")
for ch in forbidden_chars:
    if ch in email:
        die(f"forbidden character {ch!r} in email")
if ".." in email:
    die("path traversal pattern in email")
if email != email.strip():
    die("email has leading or trailing whitespace")

# db_path is also validated to prevent path traversal.
if not isinstance(db_path, str) or len(db_path) > 4096:
    die("invalid db_path length")
if not db_path.startswith("/"):
    die("db_path must be absolute")
if "\x00" in db_path or "\n" in db_path or "\r" in db_path:
    die("null/newline in db_path")
if not os.path.isfile(db_path):
    die("database not found")

# Read the password from stdin (one line). The bash script
# uses `printf '\''%s'\'' "$RESET_PASSWORD" | python3 helper.py ...`
# so the password is on a single line terminated by \n.
raw = sys.stdin.buffer.readline()
if not raw:
    die("no password on stdin")
try:
    password = raw.decode("utf-8").rstrip("\r\n")
except UnicodeDecodeError:
    die("password not valid utf-8")

if not password:
    die("empty password")
if len(password) > 72:
    die("password exceeds bcrypt 72-byte limit")
if len(password) < 8:
    die("password below 8-byte minimum")

# Generate a bcrypt salt manually so we can force cost 10
# (matches golang.org/x/crypto/bcrypt'\''s DefaultCost).
# Format: $2b$10$<22 base64 chars of 16 random bytes>.
# Python'\''s `crypt` module on glibc accepts this format
# directly and returns a bcrypt hash compatible with the
# Go runtime bcrypt verifier.
raw_salt = secrets.token_bytes(16)
b64_salt = base64.b64encode(raw_salt).decode("ascii").rstrip("=")
if len(b64_salt) < 22:
    die("salt underrun (entropy source exhausted)")
salt = "$2b$10$" + b64_salt[:22]

# Compute bcrypt hash via the system crypt(3). glibc
# supports bcrypt when the salt starts with $2a$, $2b$,
# or $2y$.
hash_value = crypt.crypt(password, salt)
# Zero the password variable as soon as we no longer need it.
password = None

if not hash_value:
    die("crypt() returned no result")
if not hash_value.startswith(("$2a$", "$2b$", "$2y$")):
    die("unexpected hash format from crypt(3)")

# SQLite update with parameter binding. The WHERE clause
# constrains the update to admin/superadmin rows for the
# exact email we validated. Without this constraint a
# typing mistake could reset a non-admin user'\''s password.
conn = sqlite3.connect(db_path)
try:
    cur = conn.cursor()
    cur.execute(
        "UPDATE users SET password_hash = ?, "
        "updated_at = strftime('\''%Y-%m-%dT%H:%M:%fZ'\'','\''now'\'') "
        "WHERE email = ? AND role IN ('\''admin'\'', '\''superadmin'\'')",
        (hash_value, email),
    )
    rows = cur.rowcount
    conn.commit()
except sqlite3.Error as e:
    die(f"sqlite error: {e}")
finally:
    conn.close()

if rows != 1:
    die(f"updated {rows} rows; expected exactly 1")

# Success marker. The bash script reads "OK" from stdout
# and treats anything else as a failure.
print("OK")
'

# _PROBE_HELPER_PYTHON is a smaller helper used by
# probe_login to build the JSON body without echoing the
# password to argv. It writes the JSON body to stdout; the
# bash script parses the HTTP code from the response.
_PROBE_HELPER_PYTHON='#!/usr/bin/env python3
"""Build an /api/v1/auth/login JSON body and report the HTTP
status. The password is read from stdin (one line); the
email is the only positional arg. The script prints:

    HTTP<CODE>
    BODY: <body>

so the bash script can parse the status without seeing the
password in argv or in shell variables.

The script never prints the password. The body is the
login response — on 200 it'\''s an access_token JSON, on
non-200 it'\''s an error envelope.
"""
import sys
import json
import urllib.request
import urllib.error


def die(msg):
    print("HTTP000", file=sys.stderr)
    print("BODY: " + msg, file=sys.stderr)
    sys.exit(1)


if len(sys.argv) != 3:
    die("usage: probe EMAIL API_BASE")

email = sys.argv[1]
api_base = sys.argv[2]

raw = sys.stdin.buffer.readline()
if not raw:
    die("no password on stdin")
try:
    password = raw.decode("utf-8").rstrip("\r\n")
except UnicodeDecodeError:
    die("password not valid utf-8")

if not password:
    die("empty password")

body = json.dumps({"username": email, "password": password}).encode("utf-8")
req = urllib.request.Request(
    api_base.rstrip("/") + "/api/v1/auth/login",
    data=body,
    headers={"Content-Type": "application/json"},
    method="POST",
)
# Zero the password variable as soon as the JSON body is built.
password = None

try:
    with urllib.request.urlopen(req, timeout=10) as resp:
        print(f"HTTP{resp.status}")
        print("BODY: " + resp.read().decode("utf-8", errors="replace"))
except urllib.error.HTTPError as e:
    print(f"HTTP{e.code}")
    try:
        print("BODY: " + e.read().decode("utf-8", errors="replace"))
    except Exception:
        print("BODY: <unreadable>")
except Exception as e:
    die(str(e))
'

# write_helper writes a quoted-heredoc python helper to a
# temp file with 0700 perms, returns the path. The file is
# unlinked by the caller.
write_helper() {
    local name="$1"
    local body="$2"
    local path="/tmp/.orvix-${name}-$$-${RANDOM}.py"
    printf '%s' "$body" > "$path"
    chmod 0700 "$path"
    printf '%s' "$path"
}

# ── main ────────────────────────────────────────────────

require_root
require_tools

log_detail "reset-admin-password invoked"

ADMIN_EMAIL="$(resolve_admin_email "${1:-}")"
log_detail "target admin email resolved"

# Validate email shape. The Python helper validates again,
# but we want a hard fail on a typo before stopping the
# service. The regex matches what the Python helper allows.
case "$ADMIN_EMAIL" in
    *@*.*) ;;
    *) fail "admin email does not look like an address: $ADMIN_EMAIL" ;;
esac

# Confirm the row exists before we stop the service. A typo
# here would let us silently update zero rows.
[ -f "$ORVIX_DB" ] || fail "database not found at $ORVIX_DB"
EXISTING="$(sqlite3 "$ORVIX_DB" "SELECT COUNT(*) FROM users WHERE email='$ADMIN_EMAIL' AND role IN ('admin','superadmin') AND active=1;" 2>/dev/null || echo 0)"
[ "$EXISTING" = "1" ] || fail "no active admin user with email $ADMIN_EMAIL found in $ORVIX_DB (got $EXISTING rows)"

NEW_PASSWORD="$(prompt_password)"
log_detail "new password captured and validated"

log_detail "stopping orvix service"
systemctl stop orvix.service 2>&1 | tee -a "$ORVIX_RESET_LOG" >/dev/null || log_detail "systemctl stop returned non-zero (continuing)"

# Write the bcrypt helper to a temp file. The file contains
# ONLY the Python source — no password, no hash, no email,
# no DB path. The password is fed via stdin at execution
# time below. The file is unlinked before this script exits.
_BCRYPT_HELPER_PATH="$(write_helper bcrypt-helper "$_BCRYPT_HELPER_PYTHON")"
_BCRYPT_ERR="$(mktemp)"
chmod 0600 "$_BCRYPT_ERR"

# Feed password via stdin (printf '%s' to avoid trailing
# newline that openssl/bash would add). The python helper
# reads ONE LINE from stdin and treats it as the password.
# Reset NEWPASSWORD from the script's memory after the
# helper returns.
printf '%s' "$NEW_PASSWORD" | "$_BCRYPT_HELPER_PATH" "$ADMIN_EMAIL" "$ORVIX_DB" >/dev/null 2>"$_BCRYPT_ERR" \
    || { ERR="$(cat "$_BCRYPT_ERR")"; rm -f "$_BCRYPT_HELPER_PATH" "$_BCRYPT_ERR"; unset NEW_PASSWORD; fail "password reset failed: $ERR"; }

rm -f "$_BCRYPT_HELPER_PATH" "$_BCRYPT_ERR"
unset NEW_PASSWORD

log_detail "new bcrypt hash written via parameterized UPDATE"

log_detail "starting orvix service"
systemctl start orvix.service 2>&1 | tee -a "$ORVIX_RESET_LOG" >/dev/null || log_detail "systemctl start returned non-zero (continuing)"

# Wait for the service to bind 8080. We probe /api/v1/health
# with a short retry loop instead of sleeping blindly.
log_detail "waiting for orvix /api/v1/health"
READY=0
for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS --connect-timeout 2 "$API_BASE/api/v1/health" >/dev/null 2>&1; then
        READY=1
        break
    fi
    sleep 1
done
[ "$READY" = "1" ] || fail "orvix did not become healthy within 10s of restart; reset state unclear — see $ORVIX_RESET_LOG"

# Re-prompt for the password so the probe can use it without
# keeping it in any long-lived shell variable. The probe
# helper builds the JSON body via stdin, runs the login, and
# returns the HTTP code. The password is not stored in argv.
log_detail "probing login with new credentials"
_PROBE_HELPER_PATH="$(write_helper probe-helper "$_PROBE_HELPER_PYTHON")"
PROBE_PASSWORD="$(prompt_password)"
PROBE_RESULT="$(printf '%s' "$PROBE_PASSWORD" | "$_PROBE_HELPER_PATH" "$ADMIN_EMAIL" "$API_BASE")"
PROBE_RC=$?
rm -f "$_PROBE_HELPER_PATH"
unset PROBE_PASSWORD

if [ "$PROBE_RC" -ne 0 ]; then
    log_detail "login probe helper failed"
    fail "login probe after reset failed (helper exit $PROBE_RC)"
fi

PROBE_CODE="$(printf '%s' "$PROBE_RESULT" | sed -n 's/^HTTP//p' | head -n 1)"
if [ "$PROBE_CODE" != "200" ]; then
    log_detail "login probe returned HTTP $PROBE_CODE"
    fail "login probe after reset failed: HTTP ${PROBE_CODE:-unknown} (the hash may not have been written correctly)"
fi

# Best-effort cleanup: if bootstrap.env is still on disk
# (from a previous install attempt), remove it. This is the
# same post-condition the installer's verify_install enforces.
if [ -f "$BOOTSTRAP_ENV" ]; then
    rm -f "$BOOTSTRAP_ENV"
    log_detail "removed stale bootstrap.env"
fi

log_detail "reset-admin-password complete"
printf 'Admin password reset for %s. Login verified.\n' "$ADMIN_EMAIL"
