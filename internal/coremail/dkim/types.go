package dkim

import "time"

// DKIMConfig represents a domain's DKIM signing configuration.
type DKIMConfig struct {
	ID       uint   `json:"id"`
	Domain   string `json:"domain"`   // signing domain (d=)
	Selector string `json:"selector"` // selector (s=)
	// PrivateKeyPEM is the RSA private key in PKCS8 PEM format.
	PrivateKeyPEM string    `json:"-"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// CanonAlgo is the canonicalization algorithm.
type CanonAlgo string

const (
	CanonRelaxed CanonAlgo = "relaxed"
	CanonSimple  CanonAlgo = "simple"
)

// HashAlgo is the hash algorithm.
type HashAlgo string

const (
	HashSHA256 HashAlgo = "sha256"
)

// SignAlgo is the signing algorithm.
type SignAlgo string

const (
	SignRSASHA256 SignAlgo = "rsa-sha256"
)

// DefaultHeaders are the headers signed by default.
var DefaultHeaders = []string{
	"From",
	"To",
	"Subject",
	"Date",
	"Message-ID",
	"MIME-Version",
	"Content-Type",
}

// HeaderSet holds signing parameters for a single message.
type HeaderSet struct {
	Domain        string
	Selector      string
	PrivateKeyPEM string
	SignedHeaders []string
	BodyCanon     CanonAlgo
	HeaderCanon   CanonAlgo
	HashAlgo      HashAlgo
	SignAlgo      SignAlgo
}
