# Domain Ownership Verification

Before Orvix can send and receive email for your domain, you must verify that you own it. Verification is done by adding a TXT record to your domain's DNS settings.

## How It Works

1. When you add a domain in Orvix, a unique verification token is generated for you.
2. You add a TXT record containing this token to your domain's DNS configuration.
3. Orvix checks for the TXT record. Once found, your domain is marked as verified.
4. You can then configure MX, SPF, DKIM, and DMARC records for the domain.

## Adding Your Domain

1. Go to **Domains** > **Add Domain**.
2. Enter your domain name (e.g., `example.com`).
3. Click **Add Domain**.

## Adding the TXT Record

Orvix will display the required TXT record:

- **Type**: `TXT`
- **Host/Name**: `@` or your domain name
- **Value**: `orvix-verify=<unique_token>`
- **TTL**: `3600` (or default)

1. Log in to your DNS provider's control panel.
2. Navigate to the DNS management section for your domain.
3. Add a new record with the values shown above.
4. Save the record.

## Verifying

1. Return to Orvix and click **Verify Domain**.
2. Most verifications complete within a few minutes. In rare cases, DNS propagation may take up to 48 hours.
3. Once verified, the domain status changes to **Verified** on the Domains page.

## Troubleshooting Verification

- Double-check that the TXT record value was copied exactly, with no extra spaces.
- Confirm you added the record to the correct domain and at the correct host.
- Use a DNS lookup tool to check if the record is publicly visible.
- If verification fails after 1 hour, try removing and re-adding the record at your DNS provider.
