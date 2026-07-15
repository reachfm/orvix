# Setting Up SPF Records

SPF (Sender Policy Framework) helps prevent spammers from sending email that appears to come from your domain. It tells receiving mail servers which mail servers are authorized to send email on behalf of your domain.

## Locating Your SPF Value

1. Go to **Domains** and select your verified domain.
2. Click the **DNS** tab.
3. Find the **SPF Record** section. Orvix displays the recommended value.

## Required SPF Record

Add the following TXT record at your DNS provider:

| Type | Host | Value |
| ---- | ---- | ----- |
| TXT | @ (or your domain) | `v=spf1 include:_spf.orvix.com -all` |

## Understanding the SPF Value

- `v=spf1` — SPF version 1.
- `include:_spf.orvix.com` — Authorizes Orvix mail servers to send email for your domain.
- `-all` — Rejects mail from any other servers (strict policy). Use `~all` if you want a soft fail instead.

## Including Additional Senders

If you use other services to send email from your domain (e.g., marketing platforms or help desk tools), include them as well:

```
v=spf1 include:_spf.orvix.com include:other-service.com -all
```

## Adding the Record

1. Log in to your DNS provider's control panel.
2. Navigate to DNS management for your domain.
3. If an existing SPF record exists, edit it to include Orvix. If not, add a new TXT record.
4. Copy the value exactly as shown.
5. Save the record.

## Important Notes

- A domain may have only **one** SPF record. If multiple sending services are used, they must be combined into a single record.
- SPF has a 10 DNS lookup limit. Avoid including too many external services.

## Verifying

1. In Orvix, go to **Domains** > your domain > **DNS**.
2. Click **Check Records**.
3. A green checkmark confirms your SPF record is valid.
