# Mail Client Setup — Orvix Webmail

**Audience:** Operators deploying Orvix, and end users configuring their mail
clients.

This document describes the supported ways for an email client to discover
Orvix's IMAP / SMTP settings automatically, plus the manual setup steps for
clients that do not support auto-discovery.

---

## 1. What Orvix supports today (Webmail Vertical Slice 1)

| Mechanism | Path(s) | Status |
| --- | --- | --- |
| Outlook Autodiscover (XML) | `GET` / `POST` `/autodiscover/autodiscover.xml` and `/Autodiscover/Autodiscover.xml` | **Supported** |
| Thunderbird autoconfig (ISPDB v1.1) | `GET` `/.well-known/autoconfig/mail/config-v1.1.xml` | **Supported** |
| Thunderbird autoconfig (fallback) | `GET` `/mail/config-v1.1.xml` | **Supported** |
| Apple `.mobileconfig` profile | n/a | **NOT supported** — Orvix does not sign MDM profiles |
| Outlook calendar sync (EAS / CalDAV / Exchange) | n/a | **NOT supported** |
| JMAP Calendar | n/a | **NOT supported** |
| Global address list / directory sync | n/a | **NOT supported** |

> **Important:** Orvix's autodiscover / autoconfig endpoints only cover
> email account setup (IMAP + SMTP). They do NOT cover calendar sync, contact
> sync, or any other Exchange / EAS feature. Any client that requires
> Exchange-style calendar sync cannot be served by Orvix today. The full
> webmail calendar / contacts UI is a separate future slice (see
> `docs/WEBMAIL_ENTERPRISE_V1_AUDIT.md`).

---

## 2. Outlook Autodiscover

### 2.1 Discovery flow

When an Outlook client adds a new account, Outlook attempts three paths in
order:

1. **Service Connection Point (SCP)** lookup against Active Directory /
   Exchange. Orvix does not advertise an SCP, so this fails.
2. **DNS-based** lookup: `autodiscover.<email-domain>` `CNAME` / `A` record
   pointing at Orvix. **Recommended.** See §4.1.
3. **HTTP fallback**: Outlook POSTs to
   `https://<email-domain>/autodiscover/autodiscover.xml` and to the
   uppercase variant. Orvix serves both.

Both lowercase and uppercase paths are implemented as a defence against
client builds that hard-code one or the other (older Outlook builds use the
uppercase path, newer builds use lowercase).

### 2.2 Request shape

Outlook sends an XML body:

```xml
<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>user@example.com</EMailAddress>
    <AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema>
  </Request>
</Autodiscover>
```

Orvix parses the `<EMailAddress>` element with a permissive regex; it does
**not** require the exact schema above to be present. Outlook variants in
the wild omit the namespace, swap element order, or include extra fields.

A `GET` form is also supported for the Outlook "external" autodiscover path:

```
GET /autodiscover/autodiscover.xml?email=user@example.com
```

### 2.3 Successful response

Orvix returns a 200 with the response schema envelope. The XML below is
exactly what the handler emits for `user@example.com` against a default
Orvix install:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006">
  <Response xmlns="">
    <User>
      <DisplayName>user@example.com</DisplayName>
      <EMailAddress>user@example.com</EMailAddress>
    </User>
    <Account>
      <AccountType>email</AccountType>
      <Action>settings</Action>
      <Protocol>
        <Type>IMAP</Type>
        <Server>mail.example.com</Server>
        <Port>993</Port>
        <SSL>on</SSL>
        <Encrypted>on</Encrypted>
        <Authentication>password-cleartext</Authentication>
        <LoginName>user@example.com</LoginName>
        <DomainRequired>off</DomainRequired>
        <SPA>off</SPA>
      </Protocol>
      <Protocol>
        <Type>SMTP</Type>
        <Server>mail.example.com</Server>
        <Port>587</Port>
        <SSL>off</SSL>
        <Encrypted>on</Encrypted>
        <Authentication>password-cleartext</Authentication>
        <LoginName>user@example.com</LoginName>
        <DomainRequired>off</DomainRequired>
        <SPA>off</SPA>
      </Protocol>
    </Account>
    <Action>settings</Action>
  </Response>
</Autodiscover>
```

Notes:
- The mail host is `mail.<domain>` by default. Override it server-wide with
  `cfg.CoreMail.Hostname`.
- The IMAP port is `993` by default. Override with
  `cfg.CoreMail.IMAPsPort`.
- The SMTP submission port is `587` by default. Override with
  `cfg.CoreMail.SubmissionPort`.
- `Authentication: password-cleartext` is the documented Outlook value for
  "plain text password over TLS" — this is NOT a leaked password. It tells
  Outlook that the user supplies a password to log in.

### 2.4 Error response (unknown / soft-deleted domain)

When the requested domain is not in `coremail_domains` (deleted_at IS NULL),
Orvix returns a 200 with an Outlook error envelope so Outlook shows the
user a readable message rather than a generic "internal error" dialog:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006">
  <Response xmlns="">
    <Error>
      <ErrorCode>600</ErrorCode>
      <Message>domain not provisioned: example.com</Message>
      <DebugData>domain not provisioned: example.com</DebugData>
    </Error>
  </Response>
</Autodiscover>
```

`ErrorCode 600` is the documented Outlook value for "invalid request".

If no email is supplied at all, the same envelope is returned with the
message `missing or invalid email address`.

---

## 3. Mozilla Thunderbird autoconfig

### 3.1 Discovery flow

When a user adds a new mail account in Thunderbird, K-9 Mail, or Apple Mail
(IMAP account setup), the client probes in this order:

1. `https://<email-domain>/.well-known/autoconfig/mail/config-v1.1.xml` —
   **Orvix serves this path.**
2. `https://autoconfig.<email-domain>/mail/config-v1.1.xml` — DNS alias to
   Orvix. **Recommended.** See §4.2.
3. `https://<email-domain>/mail/config-v1.1.xml` — fallback. **Orvix serves
   this path too.**
4. Mozilla's hosted ISPDB — not used by Orvix (we do not register our
   domains with Mozilla's central ISPDB).

The client's email is read from the `?emailaddress=` query parameter (Mozilla
also accepts `?email=` and `?EmailAddress=` for compatibility).

### 3.2 Successful response

```xml
<?xml version="1.0" encoding="UTF-8"?>
<clientConfig>
  <emailProvider id="orvix-example.com">
    <displayName>Orvix Mail (example.com)</displayName>
    <displayShortName>Orvix</displayShortName>
    <domains>
      <domain>example.com</domain>
    </domains>
    <incomingServer>
      <type>imap</type>
      <hostname>mail.example.com</hostname>
      <port>993</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>user@example.com</username>
    </incomingServer>
    <outgoingServer>
      <type>smtp</type>
      <hostname>mail.example.com</hostname>
      <port>587</port>
      <socketType>STARTTLS</socketType>
      <authentication>password-cleartext</authentication>
      <username>user@example.com</username>
    </outgoingServer>
  </emailProvider>
</clientConfig>
```

### 3.3 Unknown domain

If the domain is not provisioned, Orvix returns **HTTP 404** with an empty
body. Mozilla's documented behaviour is for Thunderbird to fall back to
manual setup.

---

## 4. Recommended DNS records

These records make autodiscover / autoconfig work even when the Outlook /
Thunderbird client does not trust the email-domain's own HTTPS certificate
for the bare apex (e.g., a self-signed cert on the mailbox host).

### 4.1 Outlook autodiscover

For each domain served by Orvix:

```
autodiscover.<domain>. 300 IN CNAME <orvix-public-hostname>.
```

Replace `<orvix-public-hostname>` with whatever DNS name the Caddy
reverse-proxy listens on (typically `mail.<domain>` for the apex or a
shared hostname like `orvix.example.net` for multi-tenant installs).

If you cannot use a CNAME (e.g., the apex already has an A record and your
provider refuses CNAME-at-apex workarounds), use an A record pointing to
the Orvix backend's public IPv4.

### 4.2 Mozilla autoconfig

For each domain:

```
autoconfig.<domain>. 300 IN CNAME <orvix-public-hostname>.
```

Same caveats as 4.1.

### 4.3 Mail host

If you want the autodiscover / autoconfig responses to advertise
`mail.<domain>` instead of a generic backend hostname, also publish:

```
mail.<domain>. 300 IN A <orvix-public-ipv4>
mail.<domain>. 300 IN AAAA <orvix-public-ipv6>
```

Caddy must be configured to terminate TLS for `mail.<domain>` and route the
request to the Orvix backend.

### 4.4 What NOT to assume

- Orvix does **not** modify DNS automatically in this slice. The operator
  publishes the records above by hand (or through their existing DNS
  automation).
- The autodiscover / autoconfig hostnames must terminate TLS on the same
  backend that serves the API. If you front Orvix with Cloudflare, configure
  the records there; if you expose Orvix directly, terminate TLS on Caddy
  on the Orvix backend.

---

## 5. Manual setup (any client)

If a client does not support autodiscover / autoconfig, or if DNS is not
configured, configure the account manually with these values:

| Setting | Value |
| --- | --- |
| Email address | The full address (e.g. `user@example.com`) |
| Account type | IMAP |
| Incoming mail server | `mail.<your-domain>` |
| Incoming IMAP port | `993` |
| Incoming IMAP encryption | SSL / TLS |
| IMAP username | The full email address (e.g. `user@example.com`) |
| IMAP password | The mailbox password (set by the operator) |
| Outgoing mail server | `mail.<your-domain>` |
| Outgoing SMTP port | `587` |
| Outgoing SMTP encryption | STARTTLS |
| SMTP authentication | Required |
| SMTP username | The full email address |
| SMTP password | Same as IMAP password |

Default ports assume a vanilla Orvix install. If the operator has overridden
`cfg.CoreMail.IMAPsPort` / `cfg.CoreMail.SubmissionPort` / `cfg.CoreMail.Hostname`,
those values take precedence and the autodiscover / autoconfig responses
reflect the override.

### 5.1 Apple Mail

Apple Mail's automatic account setup probes the Mozilla autoconfig path
when adding an IMAP account. If your DNS has `autoconfig.<domain>` pointing
at Orvix, Apple Mail will discover the IMAP and SMTP settings automatically.

We do **not** ship a `.mobileconfig` profile because signing one correctly
requires either an MDM certificate or a profile distributed over HTTPS that
iOS will refuse to install. To deploy Apple Mail across many devices, push
the manual settings through your MDM's email payload, or have each user add
the account by hand.

### 5.2 Gmail (fetchmail via IMAP)

Gmail does not consume either autodiscover or autoconfig. To fetch Orvix
mail into Gmail, configure Gmail's "Check mail from other accounts"
feature with the manual settings in §5.

---

## 6. Security model (read this before changing the handlers)

- **No password in any response.** The four endpoints only emit the
  *username* (full email address) and the IMAP / SMTP host + port + TLS
  info. The user's password is supplied by the client after the user types
  it into the client's account setup form. Orvix never sees it on this
  path.
- **Domain validation.** Every request goes through `coremail_domains`
  (`deleted_at IS NULL`, case-insensitive). Unknown domains return a safe
  error: 200 + Outlook error envelope for Autodiscover, 404 for autoconfig.
- **Host header trust.** The mail host advertised to the client is derived
  from `cfg.CoreMail.Hostname` with a `mail.<domain>` fallback. It is never
  taken from the request `Host` header or any user-controlled field.
- **No secrets in logs.** Failed lookups are logged with the domain name
  only. The DB error is logged but never echoed to the client.
- **Rate limiting.** The endpoints are public. They are NOT currently
  wrapped in the API rate limiter (which lives on `/api/v1/...`). If a
  future deployment sees abusive traffic from these endpoints, add a
  dedicated limiter on the `app` group in `internal/api/router.go`.

---

## 7. Verification

The Go tests in `internal/api/handlers/autodiscover_test.go` cover:

- Outlook lowercase GET (200 + IMAP 993 + SMTP 587 + full email username)
- Outlook lowercase POST (parses `<EMailAddress>` from XML body)
- Outlook uppercase GET and POST
- Outlook unknown domain returns 200 with error envelope
- Outlook missing email returns 200 with error envelope
- Outlook response round-trips through `encoding/xml`
- Thunderbird well-known GET (200 + `<incomingServer>` IMAP SSL +
  `<outgoingServer>` SMTP STARTTLS + full email username)
- Thunderbird fallback `/mail/config-v1.1.xml` GET
- Thunderbird unknown domain returns 404 with empty body
- Thunderbird missing email returns 404
- Domain provisioning lookup (case-insensitive, soft-delete aware)
- `cfg.CoreMail` override path (host + IMAPsPort + SubmissionPort)
- Email extraction from POST XML and GET query param
- Domain extraction from email address
- Error envelope shape
- Mozilla response round-trip

Run them with:

```
go test ./internal/api/handlers/ -run 'Autodiscover|Autoconfig|MailClient|DomainIsProvisioned|ExtractEmail|ExtractDomain' -count=1 -v
```