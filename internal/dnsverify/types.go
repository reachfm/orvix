package dnsverify

import "time"

type OverallStatus string

const (
	StatusHealthy  OverallStatus = "healthy"
	StatusWarning  OverallStatus = "warning"
	StatusFailed   OverallStatus = "failed"
)

type DNSRecord struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Record  string `json:"record,omitempty"`
	Error   string `json:"error,omitempty"`
}

type SPFResult struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Record  string `json:"record,omitempty"`
}

type DKIMResult struct {
	Present  bool   `json:"present"`
	Selector string `json:"selector"`
	Record   string `json:"record,omitempty"`
}

type DMARCResult struct {
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Record  string `json:"record,omitempty"`
}

type MXResult struct {
	Present bool       `json:"present"`
	Records []MXRecord `json:"records,omitempty"`
}

type MXRecord struct {
	Host    string `json:"host"`
	Pref    uint16 `json:"preference"`
}

type AResult struct {
	Present bool     `json:"present"`
	Records []string `json:"records,omitempty"`
}

type AAAAResult struct {
	Present bool     `json:"present"`
	Records []string `json:"records,omitempty"`
}

type PTRResult struct {
	Present bool     `json:"present"`
	Valid   bool     `json:"valid"`
	Records []string `json:"records,omitempty"`
}

type DomainDNSReport struct {
	Domain      string       `json:"domain"`
	GeneratedAt time.Time    `json:"generatedAt"`
	SPF         SPFResult    `json:"spf"`
	DKIM        DKIMResult   `json:"dkim"`
	DMARC       DMARCResult  `json:"dmarc"`
	MX          MXResult     `json:"mx"`
	A           AResult      `json:"a"`
	AAAA        AAAAResult   `json:"aaaa"`
	PTR         PTRResult    `json:"ptr"`
	Overall     OverallStatus `json:"overall"`
}
