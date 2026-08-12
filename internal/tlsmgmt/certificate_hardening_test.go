package tlsmgmt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- key/cert generation helpers, one per algorithm ---

func genRSAPair(t *testing.T, cn string, daysValid int) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	certDER := selfSignedDER(t, cn, daysValid, key.Public(), key)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func genECDSAPair(t *testing.T, cn string, daysValid int) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	certDER := selfSignedDER(t, cn, daysValid, key.Public(), key)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal ecdsa key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func genEd25519Pair(t *testing.T, cn string, daysValid int) (certPEM, keyPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	certDER := selfSignedDER(t, cn, daysValid, pub, priv)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal ed25519 key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// selfSignedDER builds a minimal self-signed leaf certificate.
// *rsa.PrivateKey, *ecdsa.PrivateKey, and ed25519.PrivateKey all
// implement crypto.Signer directly.
func selfSignedDER(t *testing.T, cn string, daysValid int, pub crypto.PublicKey, signer crypto.Signer) []byte {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(time.Duration(daysValid) * 24 * time.Hour),
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, signer)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return der
}

func newSSLService(t *testing.T) (*Service, string) {
	t.Helper()
	svc, dir := testService(t)
	if err := svc.EnsureUploadedCertSchema(context.Background()); err != nil {
		t.Fatalf("ensure uploaded cert schema: %v", err)
	}
	certDir := filepath.Join(dir, "certs")
	return svc, certDir
}

func mustImport(t *testing.T, svc *Service, name string, certPEM, keyPEM []byte, dir string) *TLSCertificate {
	t.Helper()
	cert, _, err := svc.ImportCertificate(context.Background(), name, certPEM, keyPEM, dir, 1, 1)
	if err != nil {
		t.Fatalf("import %q: %v", name, err)
	}
	return cert
}

// --- algorithm cross-validation: never panic ---

func TestImportCertificate_RSACertECDSAKeyRejectedNoPanic(t *testing.T) {
	svc, dir := newSSLService(t)
	rsaCert, _ := genRSAPair(t, "a.example.com", 365)
	_, ecdsaKey := genECDSAPair(t, "a.example.com", 365)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked instead of returning an error: %v", r)
		}
	}()
	_, _, err := svc.ImportCertificate(context.Background(), "mismatch1", rsaCert, ecdsaKey, dir, 1, 1)
	if err == nil {
		t.Fatal("expected an error for RSA cert + ECDSA key")
	}
}

func TestImportCertificate_ECDSACertRSAKeyRejectedNoPanic(t *testing.T) {
	svc, dir := newSSLService(t)
	ecdsaCert, _ := genECDSAPair(t, "b.example.com", 365)
	_, rsaKey := genRSAPair(t, "b.example.com", 365)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked instead of returning an error: %v", r)
		}
	}()
	_, _, err := svc.ImportCertificate(context.Background(), "mismatch2", ecdsaCert, rsaKey, dir, 1, 1)
	if err == nil {
		t.Fatal("expected an error for ECDSA cert + RSA key")
	}
}

// --- valid pairs across all three supported algorithms ---

func TestImportCertificate_ValidRSAAccepted(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "rsa.example.com", 365)
	cert := mustImport(t, svc, "rsa-cert", certPEM, keyPEM, dir)
	if cert.CommonName != "rsa.example.com" {
		t.Fatalf("unexpected common name %q", cert.CommonName)
	}
}

func TestImportCertificate_ValidECDSAAccepted(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genECDSAPair(t, "ecdsa.example.com", 365)
	cert := mustImport(t, svc, "ecdsa-cert", certPEM, keyPEM, dir)
	if cert.CommonName != "ecdsa.example.com" {
		t.Fatalf("unexpected common name %q", cert.CommonName)
	}
}

func TestImportCertificate_ValidEd25519Accepted(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genEd25519Pair(t, "ed25519.example.com", 365)
	cert := mustImport(t, svc, "ed25519-cert", certPEM, keyPEM, dir)
	if cert.CommonName != "ed25519.example.com" {
		t.Fatalf("unexpected common name %q", cert.CommonName)
	}
}

// --- size limits ---

func TestImportCertificate_OversizedCertRejectedBeforeFSMutation(t *testing.T) {
	svc, dir := newSSLService(t)
	_, keyPEM := genRSAPair(t, "big.example.com", 365)
	oversizedCert := make([]byte, MaxPEMSize+1)
	copy(oversizedCert, []byte("-----BEGIN CERTIFICATE-----\n"))

	_, _, err := svc.ImportCertificate(context.Background(), "oversized-cert", oversizedCert, keyPEM, dir, 1, 1)
	if err == nil {
		t.Fatal("expected size-limit rejection")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no filesystem mutation for a rejected oversized cert, found %d entries", len(entries))
	}
}

func TestImportCertificate_OversizedKeyRejectedBeforeFSMutation(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, _ := genRSAPair(t, "big2.example.com", 365)
	oversizedKey := make([]byte, MaxPEMSize+1)
	copy(oversizedKey, []byte("-----BEGIN PRIVATE KEY-----\n"))

	_, _, err := svc.ImportCertificate(context.Background(), "oversized-key", certPEM, oversizedKey, dir, 1, 1)
	if err == nil {
		t.Fatal("expected size-limit rejection")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no filesystem mutation for a rejected oversized key, found %d entries", len(entries))
	}
}

// --- atomicity: a DB failure must not disturb the previous
// certificate/key/row, and must clean up only what it just wrote ---

func TestImportCertificate_DBFailurePreservesOldFilesAndRow(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM1, keyPEM1 := genRSAPair(t, "stable.example.com", 400)
	first := mustImport(t, svc, "replaceme", certPEM1, keyPEM1, dir)

	oldCertBytes, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("read original cert file: %v", err)
	}

	var oldCertPath, oldKeyPath, oldFingerprint string
	if err := svc.db.QueryRow(
		`SELECT cert_path, key_path, fingerprint_sha256 FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`,
		int64(1), "replaceme",
	).Scan(&oldCertPath, &oldKeyPath, &oldFingerprint); err != nil {
		t.Fatalf("read original row: %v", err)
	}

	// Break the schema to force the second INSERT to fail, without
	// destroying the row/files already committed above.
	if _, err := svc.db.Exec(`ALTER TABLE coremail_uploaded_certificates RENAME TO coremail_uploaded_certificates_bak`); err != nil {
		t.Fatalf("rename table: %v", err)
	}

	certPEM2, keyPEM2 := genRSAPair(t, "stable.example.com", 400) // different content -> different fingerprint/filenames
	_, _, err = svc.ImportCertificate(context.Background(), "replaceme", certPEM2, keyPEM2, dir, 1, 1)
	if err == nil {
		t.Fatal("expected the forced DB failure to surface as an error")
	}

	if _, err := svc.db.Exec(`ALTER TABLE coremail_uploaded_certificates_bak RENAME TO coremail_uploaded_certificates`); err != nil {
		t.Fatalf("restore table: %v", err)
	}

	// The previous row must be byte-for-byte exactly what it was.
	var afterCertPath, afterKeyPath, afterFingerprint string
	if err := svc.db.QueryRow(
		`SELECT cert_path, key_path, fingerprint_sha256 FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`,
		int64(1), "replaceme",
	).Scan(&afterCertPath, &afterKeyPath, &afterFingerprint); err != nil {
		t.Fatalf("read row after failed import: %v", err)
	}
	if afterCertPath != oldCertPath || afterKeyPath != oldKeyPath || afterFingerprint != oldFingerprint {
		t.Fatalf("row changed after a failed import: before=(%s,%s,%s) after=(%s,%s,%s)",
			oldCertPath, oldKeyPath, oldFingerprint, afterCertPath, afterKeyPath, afterFingerprint)
	}

	// The previous files must still exist, unmodified.
	newBytes, err := os.ReadFile(oldCertPath)
	if err != nil {
		t.Fatalf("original cert file missing after failed import: %v", err)
	}
	if string(newBytes) != string(oldCertBytes) {
		t.Fatal("original cert file content changed after a failed import")
	}

	// Only the just-attempted new files may be gone; no other stray
	// files should exist in the target directory besides the
	// original pair.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected exactly 2 files (the original pair) to remain, found %d", len(entries))
	}
}

// --- key file permissions land on 0600 (POSIX only — Windows chmod
// semantics don't support fine-grained Unix permission bits) ---

func TestImportCertificate_KeyFilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-permission bits are not meaningfully testable on Windows")
	}
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "perm.example.com", 365)
	cert := mustImport(t, svc, "perm-cert", certPEM, keyPEM, dir)

	info, err := os.Stat(strings.Replace(cert.Path, ".crt.pem", ".key.pem", 1))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected key file mode 0600, got %o", info.Mode().Perm())
	}
}

// --- delete: active-path protection ---

func TestDeleteUploadedCertificate_BlocksActiveCertPath(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "active.example.com", 365)
	cert := mustImport(t, svc, "active-cert", certPEM, keyPEM, dir)

	var id int64
	if err := svc.db.QueryRow(`SELECT id FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`, int64(1), "active-cert").Scan(&id); err != nil {
		t.Fatalf("find row: %v", err)
	}

	_, err := svc.DeleteUploadedCertificate(context.Background(), itoa(id), 1, cert.Path, "/some/other/key.pem")
	if err != ErrCertificateActive {
		t.Fatalf("expected ErrCertificateActive, got %v", err)
	}
	if _, statErr := os.Stat(cert.Path); statErr != nil {
		t.Fatal("cert file was removed despite being the active runtime cert")
	}
}

func TestDeleteUploadedCertificate_BlocksActiveKeyPath(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "active2.example.com", 365)
	cert := mustImport(t, svc, "active-cert2", certPEM, keyPEM, dir)
	keyPath := strings.Replace(cert.Path, ".crt.pem", ".key.pem", 1)

	var id int64
	if err := svc.db.QueryRow(`SELECT id FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`, int64(1), "active-cert2").Scan(&id); err != nil {
		t.Fatalf("find row: %v", err)
	}

	_, err := svc.DeleteUploadedCertificate(context.Background(), itoa(id), 1, "/some/other/cert.pem", keyPath)
	if err != ErrCertificateActive {
		t.Fatalf("expected ErrCertificateActive, got %v", err)
	}
	if _, statErr := os.Stat(keyPath); statErr != nil {
		t.Fatal("key file was removed despite being the active runtime key")
	}
}

func TestDeleteUploadedCertificate_RemovesFilesAndRow(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "todelete.example.com", 365)
	cert := mustImport(t, svc, "todelete", certPEM, keyPEM, dir)
	keyPath := strings.Replace(cert.Path, ".crt.pem", ".key.pem", 1)

	var id int64
	if err := svc.db.QueryRow(`SELECT id FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`, int64(1), "todelete").Scan(&id); err != nil {
		t.Fatalf("find row: %v", err)
	}

	result, err := svc.DeleteUploadedCertificate(context.Background(), itoa(id), 1, "", "")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !result.FilesRemoved {
		t.Fatalf("expected files removed, cleanup error: %s", result.CleanupError)
	}
	if _, statErr := os.Stat(cert.Path); !os.IsNotExist(statErr) {
		t.Fatal("cert file still exists after delete")
	}
	if _, statErr := os.Stat(keyPath); !os.IsNotExist(statErr) {
		t.Fatal("key file still exists after delete")
	}

	var deletedAt *time.Time
	if err := svc.db.QueryRow(`SELECT deleted_at FROM coremail_uploaded_certificates WHERE id = ?`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("expected deleted_at to be set")
	}

	uploaded, err := svc.ListUploadedCertificates(context.Background(), 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, u := range uploaded {
		if u.Name == "todelete" {
			t.Fatal("deleted certificate still appears in ListUploadedCertificates")
		}
	}
}

// --- delete: honest cleanup-failure reporting, not a false success ---

func TestDeleteUploadedCertificate_ReportsCleanupFailureHonestly(t *testing.T) {
	svc, dir := newSSLService(t)
	certPEM, keyPEM := genRSAPair(t, "cleanupfail.example.com", 365)
	cert := mustImport(t, svc, "cleanupfail", certPEM, keyPEM, dir)
	keyPath := strings.Replace(cert.Path, ".crt.pem", ".key.pem", 1)

	// Replace the key file with a non-empty directory of the same
	// name's future quarantine path so the final os.Remove of the
	// quarantined key fails with ENOTEMPTY — a real, unforced
	// filesystem failure, not a mock.
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove original key file to replace with a directory: %v", err)
	}
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("mkdir in place of key file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(keyPath, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file inside directory: %v", err)
	}

	var id int64
	if err := svc.db.QueryRow(`SELECT id FROM coremail_uploaded_certificates WHERE tenant_id = ? AND name = ?`, int64(1), "cleanupfail").Scan(&id); err != nil {
		t.Fatalf("find row: %v", err)
	}

	result, err := svc.DeleteUploadedCertificate(context.Background(), itoa(id), 1, "", "")
	if err != nil {
		t.Fatalf("delete should still succeed at the DB layer even if file cleanup fails: %v", err)
	}
	if result.FilesRemoved {
		t.Fatal("expected FilesRemoved=false — the key \"file\" (a non-empty directory) cannot be removed by os.Remove")
	}
	if result.CleanupError == "" {
		t.Fatal("expected a non-empty CleanupError explaining the honest failure")
	}

	// The DB row is still authoritatively deleted regardless of the
	// filesystem cleanup outcome.
	var deletedAt *time.Time
	if err := svc.db.QueryRow(`SELECT deleted_at FROM coremail_uploaded_certificates WHERE id = ?`, id).Scan(&deletedAt); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("expected deleted_at to be set even though file cleanup failed")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
