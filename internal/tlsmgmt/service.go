package tlsmgmt

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type ConfigProvider interface {
	GetCertPath() string
	GetKeyPath() string
	SMTPTLSEnabled() bool
	IMAPTLSEnabled() bool
	POP3TLSEnabled() bool
	JMAPTLSEnabled() bool
	SMTPAddress() string
	IMAPAddress() string
	POP3Address() string
	JMAPAddress() string
}

type Service struct {
	mu      sync.Mutex
	db      *sql.DB
	dialect *dbdialect.Info
	cfg     ConfigProvider
	certs   []TLSCertificate
}

func NewService(db *sql.DB, cfg ConfigProvider) *Service {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{db: db, dialect: dialect, cfg: cfg}
}

func (s *Service) ensureSchema(ctx context.Context) error {
	// PostgreSQL schema is created by models.MigrateAllPostgres; the
	// package-local schema is SQLite-only.
	if s.dialect.IsPostgres() {
		return nil
	}
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// EnsureUploadedCertSchema installs the coremail_uploaded_certificates
// table used by the admin enterprise v2 / v3 SSL page. The table is
// declared in internal/models/models.go (it's idempotent), so this
// function is a defensive belt-and-suspenders that no-ops when the
// table is already there.
//
// On PostgreSQL the table is created by models.MigrateAllPostgres, so
// this function returns early to avoid SQLite-only DDL.
func (s *Service) EnsureUploadedCertSchema(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	if s.dialect.IsPostgres() {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coremail_uploaded_certificates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL DEFAULT '',
		cert_path TEXT NOT NULL DEFAULT '',
		key_path TEXT NOT NULL DEFAULT '',
		common_name TEXT NOT NULL DEFAULT '',
		sans TEXT NOT NULL DEFAULT '',
		issuer TEXT NOT NULL DEFAULT '',
		serial_number TEXT NOT NULL DEFAULT '',
		not_before DATETIME,
		not_after DATETIME,
		fingerprint_sha256 TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'unknown',
		created_by INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME,
		UNIQUE(tenant_id, name)
	)`)
	return err
}

// ImportCertificate PEM-decodes the supplied cert/key bytes, writes
// them to disk under the supplied directory (the admin handlers pass
// /etc/orvix/tls/admin so the operator owns where the files land),
// persists a row in coremail_uploaded_certificates, and returns the
// parsed TLSCertificate. The private key is written with mode 0600
// and the cert with 0644 to match the conventions in
// release/scripts/setup-smtp-tls.sh.
//
// The function intentionally does NOT return the private key bytes
// in any form — the audit-safe contract: once a key is imported, the
// only on-disk copy lives in key_path. The fingerprint + metadata
// row is what the admin UI ever displays.
func (s *Service) ImportCertificate(ctx context.Context, name string, certPEM, keyPEM []byte, targetDir string, tenantID, createdBy int64) (*TLSCertificate, string, error) {
	if s.db == nil {
		return nil, "", fmt.Errorf("tls service: no db")
	}
	if name = strings.TrimSpace(name); name == "" {
		return nil, "", fmt.Errorf("name is required")
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, "", fmt.Errorf("cert and key are required")
	}
	if targetDir == "" {
		targetDir = "/etc/orvix/tls/admin"
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create target dir: %w", err)
	}

	// Save cert + key to per-row files under the target dir.
	safeName := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "_")
	certPath := targetDir + "/" + safeName + ".crt.pem"
	keyPath := targetDir + "/" + safeName + ".key.pem"

	// Parse cert (and cross-check the key) before persisting so the
	// DB row never carries invalid material.
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, "", fmt.Errorf("failed to decode cert PEM")
	}
	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse cert: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, "", fmt.Errorf("failed to decode key PEM")
	}
	var key interface{}
	if k, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err == nil {
		key = k
	} else if k, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); err == nil {
		key = k
	} else {
		return nil, "", fmt.Errorf("parse private key: unsupported format (need PKCS1 / PKCS8)")
	}
	if !certKeyMatch(x509Cert, key) {
		return nil, "", fmt.Errorf("certificate and private key do not match")
	}

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, "", fmt.Errorf("write cert file: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		_ = os.Remove(certPath)
		return nil, "", fmt.Errorf("write key file: %w", err)
	}

	fingerprint := sha256.Sum256(block.Bytes)
	fingerprintHex := hex.EncodeToString(fingerprint[:])

	notBefore := x509Cert.NotBefore
	notAfter := x509Cert.NotAfter
	status := CertActive
	switch {
	case time.Now().After(notAfter):
		status = CertExpired
	case daysUntil(notAfter) <= 30:
		status = CertWarning
	}

	sans := append([]string{}, x509Cert.DNSNames...)
	sans = append(sans, x509Cert.EmailAddresses...)
	sansCSV := strings.Join(sans, ",")

	now := time.Now().UTC()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO coremail_uploaded_certificates
			(tenant_id, name, cert_path, key_path, common_name, sans, issuer, serial_number,
			 not_before, not_after, fingerprint_sha256, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, name) DO UPDATE SET
			cert_path=excluded.cert_path,
			key_path=excluded.key_path,
			common_name=excluded.common_name,
			sans=excluded.sans,
			issuer=excluded.issuer,
			serial_number=excluded.serial_number,
			not_before=excluded.not_before,
			not_after=excluded.not_after,
			fingerprint_sha256=excluded.fingerprint_sha256,
			status=excluded.status,
			updated_at=excluded.updated_at,
			deleted_at=NULL`,
		tenantID, name, certPath, keyPath, x509Cert.Subject.CommonName, sansCSV,
		x509Cert.Issuer.CommonName, formatSerial(x509Cert.SerialNumber),
		notBefore, notAfter, fingerprintHex, string(status), createdBy, now, now,
	); err != nil {
		_ = os.Remove(certPath)
		_ = os.Remove(keyPath)
		return nil, "", fmt.Errorf("persist certificate metadata: %w", err)
	}

	return &TLSCertificate{
		ID:                "uploaded:" + name,
		Name:              name,
		Path:              certPath,
		CommonName:        x509Cert.Subject.CommonName,
		SANs:              sans,
		Issuer:            x509Cert.Issuer.CommonName,
		SerialNumber:      formatSerial(x509Cert.SerialNumber),
		NotBefore:         notBefore,
		NotAfter:          notAfter,
		DaysRemaining:     daysUntil(notAfter),
		FingerprintSHA256: fingerprintHex,
		Status:            status,
	}, fingerprintHex, nil
}

// ListUploadedCertificates returns all non-deleted rows in
// coremail_uploaded_certificates for the supplied tenant. The
// caller (admin handlers) gets a per-tenant view that respects
// RBAC. The key path is returned so the admin UI can show "cert
// is at /path" but the secret bytes are never returned.
func (s *Service) ListUploadedCertificates(ctx context.Context, tenantID int64) ([]TLSCertificate, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, cert_path, key_path, common_name, sans,
		       issuer, serial_number, not_before, not_after, fingerprint_sha256, status, created_at, updated_at
		FROM coremail_uploaded_certificates
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TLSCertificate
	for rows.Next() {
		var c TLSCertificate
		var sans string
		var notBefore, notAfter *time.Time
		var status string
		var createdAt, updatedAt time.Time
		var keyPath string
		if err := rows.Scan(&c.ID, &c.Name, &c.Path, &keyPath, &c.CommonName, &sans,
			&c.Issuer, &c.SerialNumber, &notBefore, &notAfter, &c.FingerprintSHA256,
			&status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		c.KeyPath = keyPath
		if notBefore != nil {
			c.NotBefore = *notBefore
		}
		if notAfter != nil {
			c.NotAfter = *notAfter
		}
		c.Status = CertStatus(status)
		if sans != "" {
			c.SANs = strings.Split(sans, ",")
		}
		c.DaysRemaining = daysUntil(c.NotAfter)
		c.CreatedAt = &createdAt
		c.UpdatedAt = &updatedAt
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── Certificate Inventory ─────────────────────────────────

func (s *Service) LoadCertificates(ctx context.Context) ([]TLSCertificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCertificatesLocked(ctx)
}

func (s *Service) loadCertificatesLocked(ctx context.Context) ([]TLSCertificate, error) {
	// Load from database.
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, common_name, sans, issuer, serial_number, not_before, not_after, fingerprint_sha256, status FROM tls_certificates ORDER BY name")
	if err != nil {
		return s.certsFromFile(ctx)
	}
	defer rows.Close()

	var certs []TLSCertificate
	for rows.Next() {
		var c TLSCertificate
		var sans string
		var notBefore, notAfter sql.NullTime
		var fingerprint, status string
		if err := rows.Scan(&c.ID, &c.Name, &c.CommonName, &sans, &c.Issuer, &c.SerialNumber, &notBefore, &notAfter, &fingerprint, &status); err != nil {
			return nil, err
		}
		if notBefore.Valid {
			c.NotBefore = notBefore.Time
		}
		if notAfter.Valid {
			c.NotAfter = notAfter.Time
		}
		c.FingerprintSHA256 = fingerprint
		c.Status = CertStatus(status)
		if sans != "" {
			c.SANs = strings.Split(sans, ",")
		}
		c.DaysRemaining = daysUntil(c.NotAfter)
		certs = append(certs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(certs) > 0 {
		s.certs = certs
		return certs, nil
	}
	// Fall back to scanning configured certificate paths.
	fileCerts, err := s.certsFromFile(ctx)
	if err == nil && len(fileCerts) > 0 {
		s.certs = fileCerts
	}
	return fileCerts, err
}

func (s *Service) certsFromFile(ctx context.Context) ([]TLSCertificate, error) {
	// Fall back to scanning configured certificate paths.
	return s.scanConfiguredCerts(ctx)
}

func (s *Service) scanConfiguredCerts(ctx context.Context) ([]TLSCertificate, error) {
	if s.cfg == nil {
		return nil, nil
	}
	certPath := s.cfg.GetCertPath()
	keyPath := s.cfg.GetKeyPath()
	if certPath == "" {
		return nil, nil
	}

	cert, err := parseCertificateFile(certPath, keyPath)
	if err != nil {
		return []TLSCertificate{{
			ID: "default", Name: "Default Certificate",
			Path: certPath, Status: CertInvalid,
		}}, nil
	}
	return []TLSCertificate{*cert}, nil
}

// ── Certificate Validation ────────────────────────────────

func (s *Service) ValidateCertificate(ctx context.Context, id string) (*CertValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cert, err := s.getCertByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cert == nil {
		return nil, fmt.Errorf("certificate not found")
	}

	result := &CertValidationResult{Valid: true}

	certData, err := os.ReadFile(cert.Path)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("read cert: %v", err))
		return result, nil
	}

	block, _ := pem.Decode(certData)
	if block == nil {
		result.Valid = false
		result.Errors = append(result.Errors, "failed to decode PEM")
		return result, nil
	}

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("parse cert: %v", err))
		return result, nil
	}

	result.CommonName = x509Cert.Subject.CommonName
	result.SANs = x509Cert.DNSNames
	result.Issuer = x509Cert.Issuer.CommonName
	result.NotAfter = x509Cert.NotAfter.Format(time.RFC3339)
	result.DaysLeft = int(time.Until(x509Cert.NotAfter).Hours() / 24)

	// Check expiry.
	if time.Now().After(x509Cert.NotAfter) {
		result.Valid = false
		result.Errors = append(result.Errors, "certificate has expired")
	}

	// Check key match (if key path available).
	var keyPath string
	if id == "default" && s.cfg != nil {
		keyPath = s.cfg.GetKeyPath()
	}
	if keyPath != "" {
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("read key: %v", err))
			result.Valid = false
			return result, nil
		}
		keyBlock, _ := pem.Decode(keyData)
		if keyBlock == nil {
			result.Valid = false
			result.Errors = append(result.Errors, "failed to decode key PEM")
			return result, nil
		}
		key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
			if err != nil {
				result.Valid = false
				result.Errors = append(result.Errors, "failed to parse private key")
				return result, nil
			}
		}
		if !certKeyMatch(x509Cert, key) {
			result.Valid = false
			result.Errors = append(result.Errors, "certificate and private key do not match")
		}
	}

	return result, nil
}

// ── Expiration Monitoring ─────────────────────────────────

func (s *Service) CheckExpiration(ctx context.Context) ([]TLSCertificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	certs, err := s.loadAllLocked(ctx)
	if err != nil {
		return nil, err
	}

	for i, c := range certs {
		if c.NotAfter.IsZero() {
			continue
		}
		remaining := time.Until(c.NotAfter)
		days := int(remaining.Hours() / 24)

		switch {
		case remaining <= 0:
			certs[i].Status = CertExpired
		case days <= 7:
			certs[i].Status = CertExpired
		case days <= 30:
			certs[i].Status = CertWarning
		default:
			certs[i].Status = CertActive
		}
		certs[i].DaysRemaining = days

		s.saveCert(ctx, &certs[i])
	}
	s.certs = certs
	return certs, nil
}

// ── Certificate Reload ────────────────────────────────────

func (s *Service) ReloadCertificates(ctx context.Context) *ReloadResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate configured certificates first.
	if s.cfg != nil {
		certPath := s.cfg.GetCertPath()
		keyPath := s.cfg.GetKeyPath()
		if certPath != "" {
			// Validate cert exists.
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				return &ReloadResult{Success: false, Message: fmt.Sprintf("certificate not found: %s", certPath)}
			}
			// Try to load it.
			_, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				return &ReloadResult{Success: false, Message: fmt.Sprintf("invalid certificate: %v", err)}
			}
		}
	}

	// Re-scan certificates.
	certs, err := s.scanConfiguredCerts(ctx)
	if err != nil {
		return &ReloadResult{Success: false, Message: fmt.Sprintf("scan failed: %v", err)}
	}
	s.certs = certs
	for _, c := range certs {
		s.saveCert(ctx, &c)
	}

	return &ReloadResult{Success: true, Message: "certificates reloaded"}
}

// ── Runtime TLS Status ────────────────────────────────────

func (s *Service) GetRuntimeTLSStatus(ctx context.Context) []RuntimeTLSStatus {
	var statuses []RuntimeTLSStatus

	if s.cfg != nil {
		statuses = append(statuses, RuntimeTLSStatus{
			Protocol: "SMTP", TLSEnabled: s.cfg.SMTPTLSEnabled(),
			TLSMode: tlsMode(s.cfg.SMTPTLSEnabled()), Address: s.cfg.SMTPAddress(),
		})
		statuses = append(statuses, RuntimeTLSStatus{
			Protocol: "IMAP", TLSEnabled: s.cfg.IMAPTLSEnabled(),
			TLSMode: tlsMode(s.cfg.IMAPTLSEnabled()), Address: s.cfg.IMAPAddress(),
		})
		statuses = append(statuses, RuntimeTLSStatus{
			Protocol: "POP3", TLSEnabled: s.cfg.POP3TLSEnabled(),
			TLSMode: tlsMode(s.cfg.POP3TLSEnabled()), Address: s.cfg.POP3Address(),
		})
		statuses = append(statuses, RuntimeTLSStatus{
			Protocol: "JMAP", TLSEnabled: s.cfg.JMAPTLSEnabled(),
			TLSMode: tlsMode(s.cfg.JMAPTLSEnabled()), Address: s.cfg.JMAPAddress(),
		})
	}

	return statuses
}

func tlsMode(enabled bool) string {
	if enabled {
		return "required"
	}
	return "disabled"
}

// ── Internal ──────────────────────────────────────────────

func (s *Service) getCertByID(ctx context.Context, id string) (*TLSCertificate, error) {
	for i := range s.certs {
		if s.certs[i].ID == id {
			return &s.certs[i], nil
		}
	}
	// Try from DB.
	row := s.db.QueryRowContext(ctx,
		"SELECT id, name, common_name, sans, issuer, serial_number, not_before, not_after, fingerprint_sha256, status FROM tls_certificates WHERE id="+s.dialect.Placeholder(1),
		id)
	var c TLSCertificate
	var sans string
	var notBefore, notAfter sql.NullTime
	var fingerprint, status string
	err := row.Scan(&c.ID, &c.Name, &c.CommonName, &sans, &c.Issuer, &c.SerialNumber, &notBefore, &notAfter, &fingerprint, &status)
	if err != nil {
		return nil, nil
	}
	if notBefore.Valid {
		c.NotBefore = notBefore.Time
	}
	if notAfter.Valid {
		c.NotAfter = notAfter.Time
	}
	c.FingerprintSHA256 = fingerprint
	c.Status = CertStatus(status)
	if sans != "" {
		c.SANs = strings.Split(sans, ",")
	}
	return &c, nil
}

func (s *Service) loadAllLocked(ctx context.Context) ([]TLSCertificate, error) {
	if len(s.certs) > 0 {
		return s.certs, nil
	}
	return s.loadCertificatesLocked(ctx)
}

func (s *Service) saveCert(ctx context.Context, c *TLSCertificate) {
	if s.db == nil {
		return
	}
	q := s.dialect.Upsert(
		"tls_certificates",
		[]string{"id", "name", "common_name", "sans", "issuer", "serial_number", "not_before", "not_after", "fingerprint_sha256", "status", "created_at", "updated_at"},
		[]string{"id"},
		[]string{"name", "common_name", "sans", "issuer", "serial_number", "not_before", "not_after", "fingerprint_sha256", "status", "updated_at"},
	)
	now := time.Now().UTC()
	s.db.ExecContext(ctx, q,
		c.ID, c.Name, c.CommonName, strings.Join(c.SANs, ","), c.Issuer, c.SerialNumber,
		c.NotBefore, c.NotAfter, c.FingerprintSHA256, string(c.Status), now, now)
}

func parseCertificateFile(certPath, keyPath string) (*TLSCertificate, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certData)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM")
	}

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	fingerprint := sha256.Sum256(block.Bytes)

	sans := x509Cert.DNSNames
	sans = append(sans, x509Cert.EmailAddresses...)

	cert := &TLSCertificate{
		ID:                "default",
		Name:              "Default Certificate",
		Path:              certPath,
		CommonName:        x509Cert.Subject.CommonName,
		SANs:              sans,
		Issuer:            x509Cert.Issuer.CommonName,
		SerialNumber:      formatSerial(x509Cert.SerialNumber),
		NotBefore:         x509Cert.NotBefore,
		NotAfter:          x509Cert.NotAfter,
		DaysRemaining:     daysUntil(x509Cert.NotAfter),
		FingerprintSHA256: hex.EncodeToString(fingerprint[:]),
		Status:            CertActive,
	}

	if time.Now().After(x509Cert.NotAfter) {
		cert.Status = CertExpired
	} else if daysUntil(x509Cert.NotAfter) <= 7 {
		cert.Status = CertExpired
	} else if daysUntil(x509Cert.NotAfter) <= 30 {
		cert.Status = CertWarning
	}

	return cert, nil
}

func formatSerial(serial *big.Int) string {
	if serial == nil {
		return ""
	}
	return fmt.Sprintf("%x", serial)
}

func certKeyMatch(cert *x509.Certificate, key interface{}) bool {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return cert.PublicKey.(*rsa.PublicKey).N.Cmp(k.N) == 0
	case *ecdsa.PrivateKey:
		return cert.PublicKey.(*ecdsa.PublicKey).X.Cmp(k.X) == 0 && cert.PublicKey.(*ecdsa.PublicKey).Y.Cmp(k.Y) == 0
	}
	return false
}

func daysUntil(t time.Time) int {
	return int(time.Until(t).Hours() / 24)
}
