# Setting Up MX Records

MX (Mail Exchange) records tell the internet where to deliver email for your domain. You must configure MX records to receive email through Orvix.

## Locating Your MX Values

1. Go to **Domains** and select your verified domain.
2. Click the **DNS** tab.
3. Find the **MX Records** section. Orvix displays the exact values you need.

## Required MX Records

Add the following MX records at your DNS provider:

| Priority | Host | Value |
| -------- | ---- | ----- |
| 10 | @ (or your domain) | `mx1.orvix.com` |
| 20 | @ (or your domain) | `mx2.orvix.com` |

## Adding MX Records

1. Log in to your DNS provider's control panel.
2. Navigate to DNS management for your domain.
3. Remove any existing MX records pointing to other providers (only if you are switching fully to Orvix).
4. Add the two MX records listed above with the specified priorities.
5. Save your changes.

## Priority Explained

- Lower numbers have higher priority. `mx1.orvix.com` (priority 10) will receive mail first.
- `mx2.orvix.com` (priority 20) is the backup. It receives mail if the primary is unreachable.

## Verifying MX Configuration

1. In Orvix, go to **Domains** > your domain > **DNS**.
2. Click **Check Records** to verify MX records are correctly configured.
3. A green checkmark indicates your MX records are valid.

## Propagation Time

DNS changes can take a few minutes to 48 hours to propagate. Email may not be delivered to Orvix until propagation is complete.

Send a test email to a mailbox on your domain to confirm delivery once records are in place.
