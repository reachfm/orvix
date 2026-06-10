# Phase 13G Bootstrap Fix

Problem: fresh VPS installs could create an admin row whose bcrypt hash did not match the installer-entered password.

Root cause:

- The installer wrote plaintext `ORVIX_ADMIN_PASSWORD="..."` into a systemd EnvironmentFile.
- That handoff is fragile for special characters because systemd EnvironmentFile parsing is not the same as shell argument handling.

Fix:

- Installer writes `ORVIX_ADMIN_PASSWORD_B64`.
- Runtime decodes `ORVIX_ADMIN_PASSWORD_B64` and hashes that exact value.
- Existing `ORVIX_ADMIN_PASSWORD` remains a fallback for local/dev compatibility.
- Installer removes the bootstrap env file after login verification passes.

Regression tests:

- `TestAdminBootstrapEncodedPasswordLoginSucceeds`
- `TestInstallerBootstrapEnvEncodesPassword`
