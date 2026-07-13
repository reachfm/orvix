package handlers

// Mail client setup endpoints — Outlook Autodiscover and Mozilla
// Thunderbird autoconfig (WEBMAIL-CLIENT-SETUP-1A).
//
// Four routes are served, all public (no auth, no CSRF, no rate
// limit applied at the handler layer — these are read-only and the
// caller is usually an email client that has not authenticated yet):
//
//   GET  /autodiscover/autodiscover.xml            — Outlook (lowercase)
//   POST /autodiscover/autodiscover.xml            — Outlook (lowercase)
//   GET  /Autodiscover/Autodiscover.xml            — Outlook (uppercase)
//   POST /Autodiscover/Autodiscover.xml            — Outlook (uppercase)
//   GET  /.well-known/autoconfig/mail/config-v1.1.xml  — Mozilla (ISPDB)
//   GET  /mail/config-v1.1.xml                     — Mozilla (fallback)
//
// All four routes do the same thing under the hood: parse the
// requested email / domain, validate that the domain is provisioned
// in `coremail_domains`, and return XML describing how to reach
// IMAP / SMTP. They differ only in the XML schema they emit and in
// how the email / domain is extracted from the request.
//
// SECURITY MODEL — keep this in mind before changing anything:
//
//   * No password is ever returned. The username is the full email
//     address the client supplied; we never invent one.
//   * Domain validation goes through `coremail_domains` (deleted_at
//     IS NULL, case-insensitive). Unknown domains are rejected
//     safely — Autodiscover returns a `<Response><Error>...</Error>`
//     envelope, autoconfig returns HTTP 404.
//   * The Outlook POST body is capped at 64 KiB
//     (`autodiscoverPostBodyLimit`) and rejected with HTTP 413 +
//     an XML error envelope before the body is regex-scanned or
//     reflected. The cap is enforced in addition to Fiber's
//     global body limit so a public, unauthenticated endpoint
//     cannot be used as a regex / parser DoS amplifier.
//   * The handler never echoes a stack trace to the client. DB /
//     runtime errors return a 503 with a generic body and a logged
//     detail.
//   * The IMAP/SMTP host and ports are derived from `cfg.CoreMail`
//     with safe fallbacks; we never trust a Host header or query
//     parameter for the mail host.
//   * These endpoints do NOT implement calendar sync, global
//     address list, MDM enrollment, or any Exchange / EAS feature.
//     They are email-account setup only.

import (
	"context"
	"encoding/xml"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// autodiscoverPostBodyLimit caps the size of POST request bodies
// the Outlook Autodiscover endpoint will accept. The endpoint is
// public and unauthenticated — anyone on the internet can POST
// to it — so we MUST enforce a tight per-endpoint limit on top
// of the global Fiber body limit (which is 50 MiB on the default
// config, way too large for a public autodiscover endpoint).
//
// Outlook's Autodiscover XML envelope in the wild is always well
// under 64 KiB; anything larger is either a malformed client or a
// deliberate abuse probe and must be rejected before the body is
// regex-scanned or reflected back. We use 64 KiB as a comfortable
// ceiling that fits a generous envelope with whitespace and a
// huge user display name, but rejects anything that smells like
// an attempt to consume regex / parser CPU at scale.
//
// 64 KiB = 65536 bytes.
const autodiscoverPostBodyLimit = 64 * 1024

// enforceAutodiscoverPostBodyLimit checks the POST body size for
// the Outlook Autodiscover endpoint. Returns (body, true) when
// the body is oversized, in which case the caller should return
// HTTP 413 with the supplied bytes (an XML error envelope that
// never reflects the offending body). Returns (nil, false) when
// the request is within the limit and the caller should proceed
// normally.
//
// The check uses both the Content-Length header (fail fast
// before parsing the body) and the actual body length (catch
// chunked encoding / missing Content-Length). Neither path
// echoes any portion of the offending body to the response —
// only the static message "request body too large" — so an
// attacker cannot make us reflect their payload contents.
func enforceAutodiscoverPostBodyLimit(c fiber.Ctx) ([]byte, bool) {
	if !strings.EqualFold(c.Method(), fiber.MethodPost) {
		return nil, false
	}
	// Header-level check first so we can reject without reading
	// the body. We treat any value > limit as oversized;
	// missing or non-numeric Content-Length falls through to
	// the body length check below.
	if cl := strings.TrimSpace(c.Get(fiber.HeaderContentLength)); cl != "" {
		if n, err := strconv.Atoi(cl); err == nil && n > autodiscoverPostBodyLimit {
			body, _ := buildOutlookAutodiscoverErrorXML(413, "request body too large")
			return body, true
		}
	}
	// Body-level check (also catches chunked encoding where
	// Content-Length is absent or lies). `c.Body()` returns
	// whatever Fiber parsed from the connection — if the global
	// body limit was hit, the slice is truncated, but its length
	// is still > our limit so we reject.
	if len(c.Body()) > autodiscoverPostBodyLimit {
		body, _ := buildOutlookAutodiscoverErrorXML(413, "request body too large")
		return body, true
	}
	return nil, false
}

// mailClientSettings holds the IMAP / SMTP host + port + auth
// settings the public endpoints should advertise for a given
// domain. The values come from cfg.CoreMail so a deployment can
// override them per server; if not set, sane defaults are used.
type mailClientSettings struct {
	MailHost       string // host the client connects to
	IMAPPort       int
	IMAPSSL        bool // SSL/TLS on connect (imap+ssl)
	SMTPPort       int
	SMTPSubmission bool // STARTTLS on submission port 587
	Domain         string
	Email          string // canonicalised email; may be "" if not supplied
}

func (h *Handler) mailClientSettingsFor(domain string, email string) mailClientSettings {
	cfg := h.cfg.CoreMail
	host := strings.TrimSpace(cfg.Hostname)
	if host == "" {
		host = "mail." + domain
	}
	imapPort := cfg.IMAPsPort
	if imapPort <= 0 {
		imapPort = 993
	}
	smtpPort := cfg.SubmissionPort
	if smtpPort <= 0 {
		smtpPort = 587
	}
	return mailClientSettings{
		MailHost:       host,
		IMAPPort:       imapPort,
		IMAPSSL:        true,
		SMTPPort:       smtpPort,
		SMTPSubmission: true,
		Domain:         strings.ToLower(strings.TrimSpace(domain)),
		Email:          strings.ToLower(strings.TrimSpace(email)),
	}
}

// domainIsProvisioned reports whether the supplied domain exists
// in coremail_domains (deleted_at IS NULL). The lookup is
// case-insensitive. An empty / whitespace domain returns false.
//
// This is the SINGLE gate for "is this domain yours?" decisions
// across all four endpoints. Anything we serve to a client must
// pass through here first.
func (h *Handler) domainIsProvisioned(ctx context.Context, domain string) (bool, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false, nil
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return false, err
	}
	var count int
	row := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_domains WHERE LOWER(name) = ? AND deleted_at IS NULL`,
		domain)
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// outlookPostEmailRE matches the first <EMailAddress>...</EMailAddress>
// element in an Outlook-style Autodiscover POST body. The regex
// is permissive — it accepts leading / trailing whitespace and
// either case in the tag. We deliberately do NOT use
// encoding/xml.Unmarshal on the full body because Outlook
// variants have produced unexpected shapes in the wild and we
// want this endpoint to fail closed without 500-ing.
var outlookPostEmailRE = regexp.MustCompile(`(?is)<EMailAddress[^>]*>\s*([^<\s]+)\s*</EMailAddress>`)

// extractEmailFromRequest pulls the email address from a request
// in either form:
//
//   - Outlook POST: XML body containing <EMailAddress>
//   - GET / fallback: ?email= query param (Outlook's "external"
//     autodiscover path uses this when no SCP / OAB / GPO service
//     connection point is reachable)
//   - Mozilla: ?emailaddress= query param
//
// Returns the email in lowercase, trimmed. Returns "" when no
// email is supplied.
func extractEmailFromRequest(c fiber.Ctx) string {
	if strings.EqualFold(c.Method(), fiber.MethodPost) {
		body := string(c.Body())
		if m := outlookPostEmailRE.FindStringSubmatch(body); len(m) >= 2 {
			return strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	for _, key := range []string{"email", "emailaddress", "EmailAddress"} {
		v := strings.TrimSpace(c.Query(key))
		if v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

// extractDomainFromEmail returns the part after the "@" in a
// canonical email address, lowercased. Returns "" if the input
// does not look like an email address.
func extractDomainFromEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return email[at+1:]
}

// ---- Outlook Autodiscover XML -----------------------------------------
//
// See [MS-OXDISCO] §2.2.3.4. We marshal IMAP and SMTP server
// blocks separately and concatenate the XML so the schema
// (Protocol element appears twice — once for IMAP, once for
// SMTP) is preserved. encoding/xml cannot natively emit two
// sibling elements with the same tag from a single struct field,
// so the manual approach keeps the envelope faithful to what
// Outlook expects.

type outlookAutodiscoverResponse struct {
	XMLName  xml.Name `xml:"http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006 Autodiscover"`
	Response outlookAutodiscoverResponseBody
}

type outlookAutodiscoverResponseBody struct {
	XMLName  xml.Name `xml:"Response"`
	Xmlns    string   `xml:"xmlns,attr"`
	User     outlookUser
	Account  outlookAccount
	Action   string
	Redirect string
}

type outlookUser struct {
	XMLName      xml.Name `xml:"User"`
	DisplayName  string
	EMailAddress string
	LegacyDN     string
	DeploymentId string
}

type outlookAccount struct {
	XMLName         xml.Name          `xml:"Account"`
	AccountType     string            `xml:"AccountType"`
	Action          string            `xml:"Action"`
	Redirect        string            `xml:"Redirect,omitempty"`
	RedirectURL     string            `xml:"RedirectURL,omitempty"`
	Image           string            `xml:"Image,omitempty"`
	ServiceHomePage string            `xml:"ServiceHomePage,omitempty"`
	Protocols       []outlookProtocol `xml:"Protocol"`
}

type outlookProtocol struct {
	XMLName          xml.Name `xml:"Protocol"`
	Type             string
	Server           string
	Port             int
	SSL              string
	Encrypted        string
	Authentication   string
	LoginName        string
	DomainRequired   string
	SPA              string
	OAuth            string
	MailStore        string
	UseHTTPReferral  string
	HTTPReferralPort int
}

// outlookAutodiscoverErrorResponse is the schema-compliant error
// envelope. Outlook does NOT treat an HTTP 4xx as a failure —
// it expects a 200 with <Response><Error>...</Error></Response>
// so it can show the user a readable error.
type outlookAutodiscoverErrorResponse struct {
	XMLName  xml.Name `xml:"http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006 Autodiscover"`
	Response outlookAutodiscoverErrorBody
}

type outlookAutodiscoverErrorBody struct {
	XMLName xml.Name `xml:"Response"`
	Xmlns   string   `xml:"xmlns,attr"`
	Error   outlookAutodiscoverErrorDetail
}

type outlookAutodiscoverErrorDetail struct {
	XMLName   xml.Name `xml:"Error"`
	ErrorCode int
	Message   string
	DebugData string
}

// buildOutlookAutodiscoverXML returns the bytes of a successful
// Autodiscover response for the supplied email + settings. The
// IMAP and SMTP <Protocol> blocks are siblings inside the same
// <Account> element, which is what Outlook's parser expects.
func buildOutlookAutodiscoverXML(email string, s mailClientSettings) ([]byte, error) {
	safeEmail := html.EscapeString(email)
	safeHost := html.EscapeString(s.MailHost)

	resp := outlookAutodiscoverResponse{
		Response: outlookAutodiscoverResponseBody{
			User: outlookUser{
				DisplayName:  safeEmail,
				EMailAddress: safeEmail,
			},
			Account: outlookAccount{
				AccountType: "email",
				Action:      "settings",
				Protocols: []outlookProtocol{
					{
						Type:           "IMAP",
						Server:         safeHost,
						Port:           s.IMAPPort,
						SSL:            boolToOnOff(s.IMAPSSL),
						Encrypted:      boolToOnOff(s.IMAPSSL),
						Authentication: "password-cleartext",
						LoginName:      safeEmail,
						DomainRequired: "off",
						SPA:            "off",
					},
					{
						Type:           "SMTP",
						Server:         safeHost,
						Port:           s.SMTPPort,
						SSL:            boolToOnOff(false),
						Encrypted:      boolToOnOff(s.SMTPSubmission),
						Authentication: "password-cleartext",
						LoginName:      safeEmail,
						DomainRequired: "off",
						SPA:            "off",
					},
				},
			},
			Action: "settings",
		},
	}
	body, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(body)), nil
}

// buildOutlookAutodiscoverErrorXML returns the bytes of an
// Autodiscover error response (HTTP 200, error envelope per
// [MS-OXDISCO]).
func buildOutlookAutodiscoverErrorXML(errorCode int, message string) ([]byte, error) {
	resp := outlookAutodiscoverErrorResponse{
		Response: outlookAutodiscoverErrorBody{
			Error: outlookAutodiscoverErrorDetail{
				ErrorCode: errorCode,
				Message:   html.EscapeString(message),
				DebugData: html.EscapeString(message),
			},
		},
	}
	body, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(body)), nil
}

// OutlookAutodiscoverXML serves GET /autodiscover/autodiscover.xml
// (lowercase path). The uppercase variant
// (OutlookAutodiscoverXMLUpper) is a thin wrapper around this one.
func (h *Handler) OutlookAutodiscoverXML(c fiber.Ctx) error {
	return h.serveOutlookAutodiscover(c)
}

// OutlookAutodiscoverXMLUpper serves GET/POST
// /Autodiscover/Autodiscover.xml. Some Outlook builds hard-code
// the uppercase path; both paths must be served.
func (h *Handler) OutlookAutodiscoverXMLUpper(c fiber.Ctx) error {
	return h.serveOutlookAutodiscover(c)
}

func (h *Handler) serveOutlookAutodiscover(c fiber.Ctx) error {
	// POST body size cap. This MUST run before we regex-scan
	// the body for an email — otherwise an attacker can pin
	// the regex on an arbitrarily large payload. The cap is
	// also the only place we look at the request body before
	// extracting the email, so nothing downstream reflects any
	// portion of the body to the response.
	if errBody, oversized := enforceAutodiscoverPostBodyLimit(c); oversized {
		h.logger.Warn("autodiscover: request body exceeds per-endpoint limit",
			zap.Int("limit_bytes", autodiscoverPostBodyLimit),
			zap.Int("content_length", len(c.Body())))
		c.Set("Content-Type", "text/xml; charset=utf-8")
		return c.Status(http.StatusRequestEntityTooLarge).Send(errBody)
	}
	email := extractEmailFromRequest(c)
	domain := extractDomainFromEmail(email)
	if domain == "" {
		body, err := buildOutlookAutodiscoverErrorXML(600, "missing or invalid email address")
		if err != nil {
			return c.Status(http.StatusInternalServerError).SendString("autodiscover temporarily unavailable")
		}
		c.Set("Content-Type", "text/xml; charset=utf-8")
		return c.Status(http.StatusOK).Send(body)
	}

	ok, err := h.domainIsProvisioned(c.Context(), domain)
	if err != nil {
		h.logger.Warn("autodiscover: domain lookup failed", zap.String("domain", domain), zap.Error(err))
		return c.Status(http.StatusServiceUnavailable).SendString("autodiscover temporarily unavailable")
	}
	if !ok {
		body, err := buildOutlookAutodiscoverErrorXML(600, "domain not provisioned: "+domain)
		if err != nil {
			return c.Status(http.StatusInternalServerError).SendString("autodiscover temporarily unavailable")
		}
		c.Set("Content-Type", "text/xml; charset=utf-8")
		return c.Status(http.StatusOK).Send(body)
	}

	settings := h.mailClientSettingsFor(domain, email)
	body, err := buildOutlookAutodiscoverXML(email, settings)
	if err != nil {
		h.logger.Error("autodiscover: marshal failed", zap.Error(err))
		return c.Status(http.StatusInternalServerError).SendString("autodiscover temporarily unavailable")
	}
	c.Set("Content-Type", "text/xml; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=300")
	return c.Status(http.StatusOK).Send(body)
}

// ---- Mozilla Thunderbird autoconfig -----------------------------------
//
// Schema reference: Mozilla's ISPDB v1.1. The root element is
// <clientConfig> containing a single <emailProvider> with one
// <incomingServer> and one <outgoingServer>. Thunderbird, K-9
// Mail, Apple Mail (IMAP account setup), and other clients all
// consume this format.

type mozillaAutoconfigResponse struct {
	XMLName       xml.Name             `xml:"clientConfig"`
	EmailProvider mozillaEmailProvider `xml:"emailProvider"`
}

type mozillaEmailProvider struct {
	XMLName          xml.Name      `xml:"emailProvider"`
	ID               string        `xml:"id,attr"`
	DisplayName      string        `xml:"displayName"`
	DisplayShortName string        `xml:"displayShortName"`
	Domains          string        `xml:"domains>domain"`
	IncomingServer   mozillaServer `xml:"incomingServer"`
	OutgoingServer   mozillaServer `xml:"outgoingServer"`
}

// mozillaServer deliberately has NO XMLName — the parent struct
// tag (xml:"incomingServer" / xml:"outgoingServer") supplies the
// outer element name, and the inner field tags (xml:"type",
// xml:"hostname", ...) supply the inner element names.
type mozillaServer struct {
	Type           string `xml:"type"`
	Hostname       string `xml:"hostname"`
	Port           int    `xml:"port"`
	SocketType     string `xml:"socketType"`
	Authentication string `xml:"authentication"`
	Username       string `xml:"username"`
}

func buildMozillaAutoconfigXML(email string, s mailClientSettings) ([]byte, error) {
	safeEmail := html.EscapeString(email)
	safeHost := html.EscapeString(s.MailHost)
	resp := mozillaAutoconfigResponse{
		EmailProvider: mozillaEmailProvider{
			ID:               "orvix-" + s.Domain,
			DisplayName:      "Orvix Mail (" + s.Domain + ")",
			DisplayShortName: "Orvix",
			Domains:          s.Domain,
			IncomingServer: mozillaServer{
				Type:           "imap",
				Hostname:       safeHost,
				Port:           s.IMAPPort,
				SocketType:     "SSL",
				Authentication: "password-cleartext",
				Username:       safeEmail,
			},
			OutgoingServer: mozillaServer{
				Type:           "smtp",
				Hostname:       safeHost,
				Port:           s.SMTPPort,
				SocketType:     "STARTTLS",
				Authentication: "password-cleartext",
				Username:       safeEmail,
			},
		},
	}
	body, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(xml.Header + string(body)), nil
}

// MozillaAutoconfig serves GET
// /.well-known/autoconfig/mail/config-v1.1.xml — the canonical
// Mozilla path.
func (h *Handler) MozillaAutoconfig(c fiber.Ctx) error {
	return h.serveMozillaAutoconfig(c)
}

// MozillaAutoconfigFallback serves GET /mail/config-v1.1.xml —
// the secondary Mozilla path some clients probe.
func (h *Handler) MozillaAutoconfigFallback(c fiber.Ctx) error {
	return h.serveMozillaAutoconfig(c)
}

func (h *Handler) serveMozillaAutoconfig(c fiber.Ctx) error {
	email := extractEmailFromRequest(c)
	domain := extractDomainFromEmail(email)
	if domain == "" {
		return c.Status(fiber.StatusNotFound).Send(nil)
	}
	ok, err := h.domainIsProvisioned(c.Context(), domain)
	if err != nil {
		h.logger.Warn("autoconfig: domain lookup failed", zap.String("domain", domain), zap.Error(err))
		return c.SendStatus(fiber.StatusServiceUnavailable)
	}
	if !ok {
		return c.Status(fiber.StatusNotFound).Send(nil)
	}
	settings := h.mailClientSettingsFor(domain, email)
	body, err := buildMozillaAutoconfigXML(email, settings)
	if err != nil {
		h.logger.Error("autoconfig: marshal failed", zap.Error(err))
		return c.Status(http.StatusInternalServerError).SendString("autoconfig temporarily unavailable")
	}
	c.Set("Content-Type", "text/xml; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=300")
	return c.Status(http.StatusOK).Send(body)
}

func boolToOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ---- Test-only helpers -----------------------------------------------
//
// These are exported with a `ForTest` suffix so the production
// binary has a clear signal that they are not for callers.
// They are used by autodiscover_test.go.

// DomainIsProvisionedForTest is the test-exported alias for
// domainIsProvisioned.
func (h *Handler) DomainIsProvisionedForTest(ctx context.Context, domain string) (bool, error) {
	return h.domainIsProvisioned(ctx, domain)
}

// MailClientSettingsForTest is the test-exported alias for
// mailClientSettingsFor.
func (h *Handler) MailClientSettingsForTest(domain, email string) mailClientSettings {
	return h.mailClientSettingsFor(domain, email)
}

// MailClientSettingsForTest returns the zero-value-free
// mailClientSettings for callers that don't have a Handler
// (e.g., test marshal round-trips).
func MailClientSettingsForTest(domain, email string) mailClientSettings {
	s := mailClientSettings{}
	s.Domain = domain
	s.Email = email
	s.IMAPPort = 993
	s.IMAPSSL = true
	s.SMTPPort = 587
	s.SMTPSubmission = true
	s.MailHost = "mail." + domain
	return s
}

// ExtractEmailFromRequestForTest is the test-exported alias for
// extractEmailFromRequest.
func ExtractEmailFromRequestForTest(c fiber.Ctx) string {
	return extractEmailFromRequest(c)
}

// ExtractDomainFromEmailForTest is the test-exported alias for
// extractDomainFromEmail.
func ExtractDomainFromEmailForTest(email string) string {
	return extractDomainFromEmail(email)
}

// BuildOutlookAutodiscoverErrorXMLForTest is the test-exported
// alias for buildOutlookAutodiscoverErrorXML.
func BuildOutlookAutodiscoverErrorXMLForTest(errorCode int, message string) ([]byte, error) {
	return buildOutlookAutodiscoverErrorXML(errorCode, message)
}

// BuildMozillaAutoconfigXMLForTest is the test-exported alias
// for buildMozillaAutoconfigXML.
func BuildMozillaAutoconfigXMLForTest(email string, s mailClientSettings) ([]byte, error) {
	return buildMozillaAutoconfigXML(email, s)
}

// AutodiscoverPostBodyLimitForTest exposes the per-endpoint POST
// body size cap so tests can assert on the exact limit without
// hard-coding the number. If the limit is ever changed, callers
// automatically pick up the new value.
func AutodiscoverPostBodyLimitForTest() int {
	return autodiscoverPostBodyLimit
}

// EnforceAutodiscoverPostBodyLimitForTest is the test-exported
// alias for enforceAutodiscoverPostBodyLimit. It returns the
// (body, oversized) pair the helper would surface to the
// production handler.
func EnforceAutodiscoverPostBodyLimitForTest(c fiber.Ctx) ([]byte, bool) {
	return enforceAutodiscoverPostBodyLimit(c)
}
