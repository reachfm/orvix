package dns

import "fmt"

type ManualProvider struct{}

func NewManualProvider() *ManualProvider {
	return &ManualProvider{}
}

func (p *ManualProvider) CreateMXRecord(domain, mxHost string, priority int) error {
	return fmt.Errorf("manual DNS: create MX record for %s pointing to %s (priority %d)", domain, mxHost, priority)
}

func (p *ManualProvider) CreateSPFRecord(domain, spfValue string) error {
	return fmt.Errorf("manual DNS: create TXT record for %s with value: %s", domain, spfValue)
}

func (p *ManualProvider) CreateDKIMRecord(domain, selector, dkimValue string) error {
	return fmt.Errorf("manual DNS: create TXT record for %s._domainkey.%s with value: %s", selector, domain, dkimValue)
}

func (p *ManualProvider) CreateDMARCRecord(domain, dmarcValue string) error {
	return fmt.Errorf("manual DNS: create TXT record for _dmarc.%s with value: %s", domain, dmarcValue)
}
