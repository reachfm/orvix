package handlers

import "testing"

// H-8: the previous secret-shape regex missed the AES master key, the SQLite
// database and its sidecars, the initial admin credentials, .env files and
// backup archives — all reachable under the approved /var/lib/orvix and
// /var/backups/orvix roots. These cases pin the replacement policy.

func TestIsSecretName_DeniesAuditNamedFiles(t *testing.T) {
	// Every name the audit called out explicitly.
	mustDeny := []string{
		"encryption_key",
		"orvix.db",
		"orvix.db-wal",
		"orvix.db-shm",
		"admin-login.txt",
		"orvix.yaml",
		".env",
		".env.production",
		"bootstrap.env",
		"external-backup.env",
		"jwt_key.pem",
		"vapid_private.pem",
		"privkey.pem",
		"id_rsa",
		"id_rsa.pub",
		"id_ed25519",
		"backup_target_secret",
		"orvix-backup-2026-01-01.tar.gz",
		"snapshot.enc",
		"dump.sql",
		"cluster.kdbx",
		"server.key",
		"client.p12",
		"credentials",
		"authorized_keys",
		".pgpass",
		".netrc",
		"shadow",
	}
	for _, name := range mustDeny {
		if !isSecretName(name) {
			t.Errorf("%q must be refused by the secret policy", name)
		}
	}
}

func TestIsSecretName_CaseAndWhitespaceInsensitive(t *testing.T) {
	for _, name := range []string{"Orvix.DB", "ENCRYPTION_KEY", "  admin-login.TXT  ", "ID_RSA"} {
		if !isSecretName(name) {
			t.Errorf("%q must be refused regardless of case/whitespace", name)
		}
	}
}

func TestIsSecretName_DeniesSecretMarkerSubstrings(t *testing.T) {
	for _, name := range []string{
		"my_private_notes.txt",
		"relay-secret.conf",
		"user_password_list.txt",
		"aws_credentials.json",
		"session_token.log",
		"api_key.txt",
	} {
		if !isSecretName(name) {
			t.Errorf("%q embeds a secret marker and must be refused", name)
		}
	}
}

func TestIsSecretName_AllowsOrdinaryOperatorFiles(t *testing.T) {
	// The policy must still leave the endpoint useful for its actual job:
	// reading logs.
	for _, name := range []string{
		"orvix.log",
		"smtp.log",
		"access.log.1",
		"delivery-2026-01-01.log",
		"README.md",
		"notes.txt",
	} {
		if isSecretName(name) {
			t.Errorf("%q is an ordinary operator file and must remain readable", name)
		}
	}
}

func TestIsSecretName_EmptyFailsClosed(t *testing.T) {
	if !isSecretName("") || !isSecretName("   ") {
		t.Fatal("an empty basename must fail closed")
	}
}
