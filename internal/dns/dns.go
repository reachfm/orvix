package dns

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
)

type Provider int

const (
	ProviderUnknown    Provider = 0
	ProviderCloudflare Provider = 1
	ProviderRoute53    Provider = 2
	ProviderManual     Provider = 3
)

type Service struct {
	provider           Provider
	apiKey             string
	cloudflareToken    string
	awsAccessKeyID     string
	awsSecretAccessKey string
	awsRegion          string
}

func NewService() *Service {
	return &Service{provider: ProviderManual}
}

func (s *Service) SetProvider(p Provider, apiKey string) {
	s.provider = p
	s.apiKey = apiKey
}

func (s *Service) SetCloudflare(token string) {
	s.cloudflareToken = token
	s.provider = ProviderCloudflare
}

func (s *Service) SetRoute53(accessKeyID, secretAccessKey, region string) {
	s.awsAccessKeyID = accessKeyID
	s.awsSecretAccessKey = secretAccessKey
	s.awsRegion = region
	s.provider = ProviderRoute53
}

func (s *Service) CreateDNSRecords(domain, selector, dkimPubKey, spfValue, dmarcValue string) error {
	switch s.provider {
	case ProviderCloudflare:
		if s.cloudflareToken == "" {
			return fmt.Errorf("Cloudflare token not configured")
		}
		cf := NewCloudflareProvider(s.cloudflareToken)
		if err := cf.CreateMXRecord(domain, fmt.Sprintf("mail.%s", domain), 10); err != nil {
			return fmt.Errorf("MX record failed: %w", err)
		}
		if err := cf.CreateSPFRecord(domain, spfValue); err != nil {
			return fmt.Errorf("SPF record failed: %w", err)
		}
		if err := cf.CreateDKIMRecord(domain, selector, dkimPubKey); err != nil {
			return fmt.Errorf("DKIM record failed: %w", err)
		}
		if err := cf.CreateDMARCRecord(domain, dmarcValue); err != nil {
			return fmt.Errorf("DMARC record failed: %w", err)
		}
		return nil

	case ProviderRoute53:
		r53 := NewRoute53Provider(s.awsAccessKeyID, s.awsSecretAccessKey, s.awsRegion)
		if err := r53.CreateMXRecord(domain, fmt.Sprintf("mail.%s", domain), 10); err != nil {
			return err
		}
		if err := r53.CreateSPFRecord(domain, spfValue); err != nil {
			return err
		}
		if err := r53.CreateDKIMRecord(domain, selector, dkimPubKey); err != nil {
			return err
		}
		if err := r53.CreateDMARCRecord(domain, dmarcValue); err != nil {
			return err
		}
		return nil

	default:
		// Manual provider - return instructions
		return fmt.Errorf("manual DNS setup required: create MX, SPF, DKIM, and DMARC records for %s", domain)
	}
}

func (s *Service) GenerateDKIMKey(bits int) (string, string, error) {
	if bits <= 0 {
		bits = 2048
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate DKIM key: %w", err)
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	privKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privKeyBytes,
	})

	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	var dkimBuf strings.Builder
	dkimBuf.WriteString("v=DKIM1; k=rsa; p=")
	dkimBuf.WriteString(pubKeyB64)

	return string(privPEM), dkimBuf.String(), nil
}

func (s *Service) GenerateSPFRecord(domain string, includeIPs []string) string {
	var b strings.Builder
	b.WriteString("v=spf1")
	for _, ip := range includeIPs {
		b.WriteString(" ip4:")
		b.WriteString(ip)
	}
	b.WriteString(" include:_spf.orvix.email ~all")
	return b.String()
}

func (s *Service) GenerateDMARCRecord(policy string, reportingEmail string) string {
	if policy == "" {
		policy = "none"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("v=DMARC1; p=%s", policy))
	if reportingEmail != "" {
		b.WriteString(fmt.Sprintf("; rua=mailto:%s", reportingEmail))
		b.WriteString(fmt.Sprintf("; ruf=mailto:%s", reportingEmail))
	}
	b.WriteString("; fo=1; pct=100")
	return b.String()
}

func (s *Service) DKIMSelector(domain string) string {
	h := sha256.Sum256([]byte(domain))
	return fmt.Sprintf("orvix%x", h[:4])
}

func (s *Service) CreateMXRecords(domain string) error {
	return nil
}

func (s *Service) CreateSPFRecord(domain string) error {
	return nil
}

func (s *Service) CreateDKIMRecord(domain, selector, key string) error {
	return nil
}

func (s *Service) CreateDMARCRecord(domain, policy string) error {
	return nil
}

func (s *Service) CheckDNS(domain string) (map[string]bool, error) {
	results := map[string]bool{
		"mx":    false,
		"spf":   false,
		"dkim":  false,
		"dmarc": false,
	}

	// Check MX records
	mxRecords, err := net.LookupMX(domain)
	if err == nil && len(mxRecords) > 0 {
		results["mx"] = true
	}

	// Check TXT records for SPF, DKIM, DMARC
	txtRecords, err := net.LookupTXT(domain)
	if err == nil {
		for _, txt := range txtRecords {
			if strings.HasPrefix(txt, "v=spf1") {
				results["spf"] = true
			}
		}
	}

	// Check DMARC
	dmarcRecords, err := net.LookupTXT("_dmarc." + domain)
	if err == nil {
		for _, txt := range dmarcRecords {
			if strings.HasPrefix(txt, "v=DMARC1") {
				results["dmarc"] = true
			}
		}
	}

	// Check DKIM using common selectors
	for _, sel := range []string{"orvix", "default", "dkim", "mail"} {
		dkimRecords, err := net.LookupTXT(sel + "._domainkey." + domain)
		if err == nil {
			for _, txt := range dkimRecords {
				if strings.HasPrefix(txt, "v=DKIM1") {
					results["dkim"] = true
					break
				}
			}
		}
		if results["dkim"] {
			break
		}
	}

	return results, nil
}
