package tlsmgmt

import "time"

type CertStatus string

const (
	CertActive   CertStatus = "active"
	CertWarning  CertStatus = "warning"
	CertExpired  CertStatus = "expired"
	CertInvalid  CertStatus = "invalid"
)

type TLSCertificate struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Path              string     `json:"path"`
	KeyPath           string     `json:"keyPath,omitempty"`
	CommonName        string     `json:"commonName"`
	SANs              []string   `json:"sans,omitempty"`
	Issuer            string     `json:"issuer"`
	SerialNumber      string     `json:"serialNumber"`
	NotBefore         time.Time  `json:"notBefore"`
	NotAfter          time.Time  `json:"notAfter"`
	DaysRemaining     int        `json:"daysRemaining"`
	FingerprintSHA256 string     `json:"fingerprintSHA256"`
	Status            CertStatus `json:"status"`
	CreatedAt         *time.Time `json:"createdAt,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
}

type CertValidationResult struct {
	Valid       bool     `json:"valid"`
	Errors      []string `json:"errors,omitempty"`
	CommonName  string   `json:"commonName,omitempty"`
	SANs        []string `json:"sans,omitempty"`
	Issuer      string   `json:"issuer,omitempty"`
	NotAfter    string   `json:"notAfter,omitempty"`
	DaysLeft    int      `json:"daysLeft"`
}

type RuntimeTLSStatus struct {
	Protocol    string `json:"protocol"`
	TLSEnabled  bool   `json:"tlsEnabled"`
	TLSMode     string `json:"tlsMode"` // "disabled", "opportunistic", "required"
	CertName    string `json:"certName,omitempty"`
	Address     string `json:"address"`
}

type ReloadResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS tls_certificates (
		id TEXT PRIMARY KEY,
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
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`,
}
