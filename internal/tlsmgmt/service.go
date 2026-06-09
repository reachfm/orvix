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
	cfg     ConfigProvider
	certs   []TLSCertificate
}

func NewService(db *sql.DB, cfg ConfigProvider) *Service {
	return &Service{db: db, cfg: cfg}
}

func (s *Service) ensureSchema(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
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
		if notBefore.Valid { c.NotBefore = notBefore.Time }
		if notAfter.Valid { c.NotAfter = notAfter.Time }
		c.FingerprintSHA256 = fingerprint
		c.Status = CertStatus(status)
		if sans != "" { c.SANs = strings.Split(sans, ",") }
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
	if s.cfg == nil { return nil, nil }
	certPath := s.cfg.GetCertPath()
	keyPath := s.cfg.GetKeyPath()
	if certPath == "" { return nil, nil }

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
	if err != nil { return nil, err }
	if cert == nil { return nil, fmt.Errorf("certificate not found") }

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
	if err != nil { return nil, err }

	for i, c := range certs {
		if c.NotAfter.IsZero() { continue }
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
	if enabled { return "required" }
	return "disabled"
}

// ── Internal ──────────────────────────────────────────────

func (s *Service) getCertByID(ctx context.Context, id string) (*TLSCertificate, error) {
	for i := range s.certs {
		if s.certs[i].ID == id { return &s.certs[i], nil }
	}
	// Try from DB.
	row := s.db.QueryRowContext(ctx, "SELECT id, name, common_name, sans, issuer, serial_number, not_before, not_after, fingerprint_sha256, status FROM tls_certificates WHERE id=?", id)
	var c TLSCertificate
	var sans string
	var notBefore, notAfter sql.NullTime
	var fingerprint, status string
	err := row.Scan(&c.ID, &c.Name, &c.CommonName, &sans, &c.Issuer, &c.SerialNumber, &notBefore, &notAfter, &fingerprint, &status)
	if err != nil { return nil, nil }
	if notBefore.Valid { c.NotBefore = notBefore.Time }
	if notAfter.Valid { c.NotAfter = notAfter.Time }
	c.FingerprintSHA256 = fingerprint
	c.Status = CertStatus(status)
	if sans != "" { c.SANs = strings.Split(sans, ",") }
	return &c, nil
}

func (s *Service) loadAllLocked(ctx context.Context) ([]TLSCertificate, error) {
	if len(s.certs) > 0 { return s.certs, nil }
	return s.loadCertificatesLocked(ctx)
}

func (s *Service) saveCert(ctx context.Context, c *TLSCertificate) {
	if s.db == nil { return }
	s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO tls_certificates (id, name, common_name, sans, issuer, serial_number, not_before, not_after, fingerprint_sha256, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		c.ID, c.Name, c.CommonName, strings.Join(c.SANs, ","), c.Issuer, c.SerialNumber,
		c.NotBefore, c.NotAfter, c.FingerprintSHA256, string(c.Status))
}

func parseCertificateFile(certPath, keyPath string) (*TLSCertificate, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil { return nil, err }
	block, _ := pem.Decode(certData)
	if block == nil { return nil, fmt.Errorf("failed to decode PEM") }

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil { return nil, err }

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
	if serial == nil { return "" }
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
