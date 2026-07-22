package dns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type CloudflareProvider struct {
	apiToken string
	client   *http.Client
}

type cloudflareDNSRecord struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type cloudflareAPIResponse struct {
	Success bool              `json:"success"`
	Errors  []cloudflareError `json:"errors"`
	Result  json.RawMessage   `json:"result"`
}

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewCloudflareProvider(apiToken string) *CloudflareProvider {
	return &CloudflareProvider{
		apiToken: apiToken,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *CloudflareProvider) getZoneID(domain string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?name=%s", domain), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudflare API error: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Result) == 0 {
		return "", fmt.Errorf("zone not found for domain: %s", domain)
	}
	return result.Result[0].ID, nil
}

func (p *CloudflareProvider) createRecord(zoneID string, record cloudflareDNSRecord) error {
	body, _ := json.Marshal(record)
	req, err := http.NewRequest("POST", fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare API error: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result cloudflareAPIResponse
	json.Unmarshal(respBody, &result)

	if !result.Success {
		if len(result.Errors) > 0 {
			return fmt.Errorf("cloudflare API error: %s (code %d)", result.Errors[0].Message, result.Errors[0].Code)
		}
		return fmt.Errorf("cloudflare API error: request failed")
	}

	return nil
}

func (p *CloudflareProvider) CreateMXRecord(domain string, mxHost string, priority int) error {
	zoneID, err := p.getZoneID(domain)
	if err != nil {
		return err
	}
	return p.createRecord(zoneID, cloudflareDNSRecord{
		Type:    "MX",
		Name:    domain,
		Content: fmt.Sprintf("%d %s", priority, mxHost),
		TTL:     120,
	})
}

func (p *CloudflareProvider) CreateSPFRecord(domain, spfValue string) error {
	zoneID, err := p.getZoneID(domain)
	if err != nil {
		return err
	}
	return p.createRecord(zoneID, cloudflareDNSRecord{
		Type:    "TXT",
		Name:    domain,
		Content: spfValue,
		TTL:     120,
	})
}

func (p *CloudflareProvider) CreateDKIMRecord(domain, selector, dkimValue string) error {
	zoneID, err := p.getZoneID(domain)
	if err != nil {
		return err
	}
	return p.createRecord(zoneID, cloudflareDNSRecord{
		Type:    "TXT",
		Name:    fmt.Sprintf("%s._domainkey.%s", selector, domain),
		Content: dkimValue,
		TTL:     120,
	})
}

func (p *CloudflareProvider) CreateDMARCRecord(domain, dmarcValue string) error {
	zoneID, err := p.getZoneID(domain)
	if err != nil {
		return err
	}
	return p.createRecord(zoneID, cloudflareDNSRecord{
		Type:    "TXT",
		Name:    fmt.Sprintf("_dmarc.%s", domain),
		Content: dmarcValue,
		TTL:     120,
	})
}
