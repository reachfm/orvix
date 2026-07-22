package dns

import (
	"fmt"
)

type Route53Provider struct {
	accessKeyID     string
	secretAccessKey string
	region          string
}

func NewRoute53Provider(accessKeyID, secretAccessKey, region string) *Route53Provider {
	return &Route53Provider{
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		region:          region,
	}
}

func (p *Route53Provider) CreateMXRecord(domain, mxHost string, priority int) error {
	if p.accessKeyID == "" || p.secretAccessKey == "" {
		return fmt.Errorf("Route53 not configured: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	return fmt.Errorf("Route53 MX record creation requires AWS SDK - install github.com/aws/aws-sdk-go")
}

func (p *Route53Provider) CreateSPFRecord(domain, spfValue string) error {
	if p.accessKeyID == "" || p.secretAccessKey == "" {
		return fmt.Errorf("Route53 not configured: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	return fmt.Errorf("Route53 SPF record creation requires AWS SDK")
}

func (p *Route53Provider) CreateDKIMRecord(domain, selector, dkimValue string) error {
	if p.accessKeyID == "" || p.secretAccessKey == "" {
		return fmt.Errorf("Route53 not configured: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	return fmt.Errorf("Route53 DKIM record creation requires AWS SDK")
}

func (p *Route53Provider) CreateDMARCRecord(domain, dmarcValue string) error {
	if p.accessKeyID == "" || p.secretAccessKey == "" {
		return fmt.Errorf("Route53 not configured: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	return fmt.Errorf("Route53 DMARC record creation requires AWS SDK")
}
