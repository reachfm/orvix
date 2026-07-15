# Setting Up DMARC Records

DMARC (Domain-based Message Authentication, Reporting, and Conformance) tells receiving mail servers how to handle email that fails SPF or DKIM checks. It also provides reporting on authentication results.

## Locating Your DMARC Value

1. Go to **Domains** and select your verified domain.
2. Click the **DNS** tab.
3. Find the **DMARC Record** section.

## Required DMARC Record

Add the following TXT record at your DNS provider:

| Type | Host | Value |
| ---- | ---- | ----- |
| TXT | `_dmarc` | `v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com; ruf=mailto:dmarc@example.com; pct=100` |

Replace `example.com` with your own domain for the `rua` and `ruf` addresses. Create the `dmarc@` address as a mailbox or alias if it does not exist.

## DMARC Tags Explained

| Tag | Meaning | Recommended Value |
| --- | ------- | ----------------- |
| `v` | Version | `DMARC1` |
| `p` | Policy for failed authentication | Start with `none`, then `quarantine`, then `reject` |
| `rua` | Aggregate report recipient (URI) | An email where you receive daily XML reports |
| `ruf` | Forensic report recipient (URI) | An email where you receive individual failure reports |
| `pct` | Percentage of mail the policy applies to | `100` |
| `sp` | Subdomain policy | Same as `p` if omitted |

## Recommended Deployment Strategy

1. **Start with monitoring** (`p=none`). This lets you collect reports without affecting delivery.

   ```
   v=DMARC1; p=none; rua=mailto:dmarc@example.com; pct=100
   ```

2. After reviewing reports and confirming legitimate senders are authenticated, move to **quarantine** (`p=quarantine`). Suspicious mail goes to spam.

   ```
   v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com; pct=100
   ```

3. Once confident all legitimate mail passes SPF and DKIM, move to **reject** (`p=reject`). Suspicious mail is rejected entirely.

   ```
   v=DMARC1; p=reject; rua=mailto:dmarc@example.com; pct=100
   ```

## Adding the Record

1. Log in to your DNS provider's control panel.
2. Navigate to DNS management for your domain.
3. Add a new TXT record with host `_dmarc`.
4. Paste the DMARC value.
5. Save the record.

## Verifying

1. In Orvix, go to **Domains** > your domain > **DNS**.
2. Click **Check Records**.
3. A green checkmark confirms your DMARC record is valid.
