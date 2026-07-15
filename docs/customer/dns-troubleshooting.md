# DNS Troubleshooting

Common DNS issues and how to fix them for domains configured with Orvix.

## Record Not Found

**Symptom**: Orvix shows a red X next to a DNS record after clicking **Check Records**.

**Fix**:
1. Verify the record was saved at your DNS provider. Some providers require you to click a separate "Save" or "Apply Changes" button.
2. Confirm the record type (TXT, MX, CNAME) is correct.
3. Check for typos in the host/name field.
4. Wait a few minutes for changes to propagate, then check again.

## Multiple SPF Records

**Symptom**: SPF check fails with an error about multiple records.

**Fix**: A domain can only have one SPF record. Combine all includes into a single record:

```
v=spf1 include:_spf.orvix.com include:other-service.com ~all
```

Do not create separate TXT records for each service. Remove any duplicate SPF records.

## SPF DNS Lookup Limit Exceeded

**Symptom**: SPF validation fails with a "too many DNS lookups" warning.

**Fix**: SPF has a limit of 10 DNS lookups. Each `include`, `a`, `mx`, `ptr`, and `exists` mechanism counts as one lookup. Simplify your SPF record by:
- Removing unused services.
- Replacing `include` mechanisms with `ip4` or `ip6` entries if the provider publishes their IP ranges.
- Removing `ptr` or `mx` mechanisms (these are deprecated and inefficient).

## DKIM Selector Mismatch

**Symptom**: DKIM signatures fail validation.

**Fix**: Ensure the host field in your DNS exactly matches the selector provided by Orvix. The format should be `<selector>._domainkey` (e.g., `orvix._domainkey`). For some providers, you may need to enter just the selector without the domain — `orvix._domainkey` rather than `orvix._domainkey.example.com`.

## Propagation Delays

**Symptom**: Records were correctly added but Orvix still cannot see them.

**Fix**: DNS changes can take up to 48 hours to propagate globally. In practice, most changes propagate within 30 minutes. Use the **Check Records** button periodically instead of making repeated DNS changes.

## CNAME Flattening

**Symptom**: DNS provider does not allow a CNAME at the root/apex domain.

**Fix**: Use A records or ALIAS/ANAME records if your provider supports them, or use a subdomain (e.g., `mail.example.com`) for CNAME-reliant features.

## Conflicting MX Records

**Symptom**: Some email is still delivered to a previous provider after switching to Orvix.

**Fix**:
1. Check that old MX records from your previous provider have been removed.
2. Ensure no backup MX with a higher priority exists pointing elsewhere.
3. Lower-numbered MX records take priority. Verify your Orvix MX records have the lowest priority numbers.

## TXT Record Formatting

**Symptom**: TXT record verification fails.

**Fix**: Some DNS providers automatically add quotation marks around TXT values. Do not add your own quotes. If your provider requires quotes, check their documentation. Also verify there are no leading or trailing spaces in the record value.

## Checking DNS from the Command Line

Use these commands to manually verify your DNS configuration:

```bash
# Check MX records
nslookup -type=mx example.com

# Check TXT records (SPF, DKIM, DMARC, verification)
nslookup -type=txt example.com
nslookup -type=txt _dmarc.example.com
nslookup -type=txt orvix._domainkey.example.com

# Check full zone
dig example.com any
```
