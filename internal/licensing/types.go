package licensing

import "time"

type Edition string

const (
	EditionCommunity    Edition = "community"
	EditionProfessional Edition = "professional"
	EditionEnterprise   Edition = "enterprise"
	EditionDatacenter   Edition = "datacenter"
	EditionMSP          Edition = "msp"
)

func (e Edition) Valid() bool {
	switch e {
	case EditionCommunity, EditionProfessional, EditionEnterprise, EditionDatacenter, EditionMSP:
		return true
	default:
		return false
	}
}

type License struct {
	LicenseID      string    `json:"licenseId"`
	Edition        Edition   `json:"edition"`
	IssuedAt       time.Time `json:"issuedAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	DomainsLimit   int64     `json:"domainsLimit"`
	MailboxesLimit int64     `json:"mailboxesLimit"`
	StorageLimitGB int64     `json:"storageLimitGB"`
	Features       []string  `json:"features"`
	MachineBinding string    `json:"machineBinding"`
	Signature      string    `json:"signature"`
}

type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type LicenseStatus struct {
	License      *License          `json:"license,omitempty"`
	Edition      Edition           `json:"edition"`
	Valid        bool              `json:"valid"`
	Validation   *ValidationResult `json:"validation,omitempty"`
	MachineID    string            `json:"machineId"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
}
