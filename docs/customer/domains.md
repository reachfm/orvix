# Domain Setup

## Adding a Domain

1. Go to Domains > Add Domain
2. Enter your domain name (e.g., example.com)
3. Copy the TXT verification record shown
4. Add this TXT record to your DNS provider
5. Click Verify Ownership

## DNS Records

Orvix requires these DNS records for email delivery:

### MX Record
- Host: @ (or yourdomain.com)
- Value: mx.orvix.email
- Priority: 10

### SPF Record
- Host: @
- Value: v=spf1 include:_spf.orvix.email ~all

### DKIM Record
- Host: default._domainkey
- Value: (provided during setup)

### DMARC Record
- Host: _dmarc
- Value: v=DMARC1; p=quarantine; rua=mailto:dmarc@yourdomain.com

## Verification Status

The dashboard shows independent status for:
- Domain Ownership
- MX Records
- SPF
- DKIM
- DMARC
- Inbound Readiness
- Outbound Readiness
