# Common Troubleshooting

Solutions for the most common issues you may encounter while using Orvix.

## Email Not Sending

**Cause**: SMTP configuration is incorrect, your plan's outbound limit has been reached, or SPF/DKIM are not properly configured.

**Steps**:
1. Verify your SMTP settings: server `smtp.orvix.com`, port `587`, STARTTLS encryption.
2. Check that your username is your full email address.
3. Go to **Organization** > **Usage** and confirm you have not hit the outbound message limit.
4. Verify your [SPF](spf.md) and [DKIM](dkim.md) records are valid in the domain DNS tab.
5. Try sending a test message from [webmail](webmail.md). If it works there, the issue is with your email client.

## Email Not Receiving

**Cause**: MX records are incorrect, domain is not verified, mailbox quota is full, or the sender's domain is blocked.

**Steps**:
1. Check that your [MX records](mx.md) are correctly configured and visible with **Check Records**.
2. Verify the domain status is **Verified** on the Domains page.
3. Check the recipient mailbox quota in **Domains** > your domain > **Mailboxes**.
4. Review the **Blocked Senders** list in webmail settings.
5. Ask the sender to check if they received a bounce message — the bounce may indicate the specific problem.

## DNS Records Not Verifying

See the [DNS troubleshooting guide](dns-troubleshooting.md) for detailed steps.

## Cannot Sign In

**Steps**:
1. Confirm you are using the full email address as your username.
2. Verify you are on the correct sign-in page ([https://app.orvix.com/login](https://app.orvix.com/login) for the dashboard, [https://webmail.orvix.com](https://webmail.orvix.com) for webmail).
3. Reset your password if needed — see [password reset](password-reset.md).
4. If MFA is enabled, ensure your authenticator app time is synced correctly.
5. Check that your account has not been disabled by an admin.
6. If using SSO, verify your identity provider is available.

## Email Client Connection Failure

**Steps**:
1. Confirm IMAP server: `imap.orvix.com`, port `993`, SSL/TLS.
2. Confirm SMTP server: `smtp.orvix.com`, port `587`, STARTTLS.
3. Verify the username is the full email address.
4. Try connecting from a different network (some corporate networks block mail ports).
5. Temporarily disable your firewall or antivirus to test.

## Missing Email

**Steps**:
1. Check all folders: Spam, Trash, and any custom folders.
2. Use the search bar in webmail to search by sender or subject.
3. Check if a [mail rule](rules.md) moved it to a folder automatically.
4. Verify [email forwarding](forwarding.md) is not sending copies elsewhere.
5. Ask the sender to confirm delivery — they may have received a bounce.

## Slow Performance

**Steps**:
1. Clear your browser cache and cookies.
2. Try a different browser.
3. Reduce the number of messages in your inbox (archive or delete old mail).
4. If using an email client, check the IMAP folder sync settings.

## Spam Not Being Filtered

**Steps**:
1. Mark unwanted messages as spam in webmail — this trains the spam filter.
2. Add persistent spammers to your blocked senders list.
3. Configure a [mail rule](rules.md) to automatically delete or move messages matching specific patterns.
4. If spam volumes are high, ensure your [SPF](spf.md), [DKIM](dkim.md), and [DMARC](dmarc.md) records are correctly configured to prevent your domain from being spoofed.

## Still Stuck?

If these steps do not resolve your issue, [contact support](support.md) with details about what you were doing, what went wrong, and any error messages you saw.
