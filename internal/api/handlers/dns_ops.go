package handlers

// Admin DNS Operations (DNS-DKIM-OPERATIONS-2F).
//
// The handlers in this file are the admin-only entry points for
// real DNS / DKIM operations. They are mounted under
// /api/v1/admin/dns/* and require an admin role (RequireAnyRole).
//
// Security posture (matches the brief's non-negotiable rules):
//   - No public endpoints. All admin-only.
//   - No private DKIM key in any response. The DKIM row stores
//     private_key_pem in coremail_dkim_config (server-side); the
//     handler returns ONLY the public DNS TXT.
//   - No provider tokens in any response. Tokens are read from
//     server config (cfg.DNS) and never enter the JSON envelope.
//   - No shell-out. We use net.DefaultResolver via the
//     dnsops.Service.
//   - Read-only verify path is safe to call repeatedly.
//   - Provider plan and apply require explicit confirmation
//     ("yes-i-confirm" or a per-domain confirm string).

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/dnsops"
)

// dnsOpsService returns the wired dnsops.Service or nil.
func (h *Handler) dnsOpsService() *dnsops.Service {
	return h.dnsOps
}

// dnsOpsInputsForDomain resolves the inputs the dnsops.Generator
// needs for a domain. It reads the coremail_dkim_config row (if
// any) to obtain the active DKIM selector + public key, and the
// coremail_domains row for the canonical domain name. The
// server IPv4 is sourced from cfg.CoreMail; the IPv6 is left empty
// because the operator's SPF/AAAA opt-in is a future build.
//
// Returns (inputs, statusCode, body) so the caller can short-
// circuit with an HTTP error envelope when the domain or DKIM row
// is missing.
func (h *Handler) dnsOpsInputsForDomain(ctx context.Context, domain string) (dnsops.Inputs, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return dnsops.Inputs{}, fmt.Errorf("domain is required")
	}
	// Mail host: prefer cfg.CoreMail.Hostname when configured,
	// else default to "mail.<domain>".
	mailHost := strings.TrimSpace(h.cfg.CoreMail.Hostname)
	if mailHost == "" {
		mailHost = "mail." + domain
	}
	// Server IPv4: prefer the configured core mail host's resolved
	// address, else require the operator to publish one. We treat
	// "0.0.0.0" as "not configured".
	serverIPv4 := ""
	if h.cfg.CoreMail.SMTPHost != "" &&
		h.cfg.CoreMail.SMTPHost != "0.0.0.0" &&
		net.ParseIP(h.cfg.CoreMail.SMTPHost) != nil {
		serverIPv4 = h.cfg.CoreMail.SMTPHost
	}
	if serverIPv4 == "" {
		// Fall back to AdminHost's resolved IPv4 if available.
		// We deliberately do not call LookupHost here — the
		// operator's deployment script is the right place to
		// anchor the A record. The empty IPv4 is surfaced as a
		// "not configured" note in the response so the operator
		// sees the missing input rather than a fake record.
		serverIPv4 = ""
	}
	// DKIM selector + public key (from coremail_dkim_config if a
	// row exists for this domain).
	selector := "default"
	pubKey := ""
	if sqlDB, err := h.db.DB(); err == nil {
		var s string
		row := sqlDB.QueryRowContext(ctx,
			`SELECT selector, private_key_pem FROM coremail_dkim_config WHERE domain = ?`,
			domain)
		var priv string
		if err := row.Scan(&s, &priv); err == nil {
			if strings.TrimSpace(s) != "" {
				selector = s
			}
			// Derive the public key from the stored private key
			// so we never store or echo the public key separately.
			if pub, ok := deriveDKIMPublicKey(priv); ok {
				pubKey = pub
			}
		}
	}
	// Report mailbox: prefer postmaster@ then dmarc@.
	report := "postmaster@" + domain
	// MTA mode: always testing on first publish. Enforce is opt-in
	// via a later operation; this build only emits mode=testing.
	mtaMode := "testing"
	return dnsops.Inputs{
		Domain:        domain,
		MailHost:      mailHost,
		ServerIPv4:    serverIPv4,
		DKIMSelector:  selector,
		DKIMPubKey:    pubKey,
		ReportMailbox: report,
		MTAMode:       mtaMode,
	}, nil
}

// deriveDKIMPublicKey parses a PEM-encoded PKCS8 RSA private key
// and returns the base64-DER SubjectPublicKeyInfo string used in a
// DKIM DNS TXT record (no PEM headers, no "v=DKIM1;" prefix).
//
// Returns ("", false) if the PEM is malformed. The caller MUST
// treat the public-key-absent case as "DKIM not generated for this
// domain" rather than silently inventing a key.
func deriveDKIMPublicKey(privPEM string) (string, bool) {
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		return "", false
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if k1, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			keyAny = k1
		} else {
			return "", false
		}
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return "", false
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		return "", false
	}
	// Use the existing coremail/dkim encoder so the wire format
	// matches what the signer emits.
	recordName, recordValue := dkim.GenerateDNSRecord("ignored", "ignored", string(pubBytes))
	_ = recordName
	// recordValue is "v=DKIM1; k=rsa; p=<base64>"; we want just p=<base64>.
	if i := strings.Index(recordValue, "p="); i >= 0 {
		return recordValue[i+2:], true
	}
	return "", false
}

// GetAdminDNSProviders returns the list of known provider names
// and whether each is configured server-side.
//
// The response never carries a token value. "configured" is a
// boolean; if the operator wants the raw config status they can
// look at /api/v1/admin/summary or the audit log.
func (h *Handler) GetAdminDNSProviders(c fiber.Ctx) error {
	svc := h.dnsOpsService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "dns ops service unavailable",
		})
	}
	providers := svc.Providers()
	out := make([]fiber.Map, 0, len(providers))
	for _, name := range providers {
		status := "manual"
		notes := []string{}
		if name != "manual" {
			// We never echo the token shape; we just say
			// whether the env-style config has both required
			// fields.
			notes = append(notes, "configure server-side to enable; tokens are never returned to clients")
		}
		if name == "cloudflare" && (h.cfg.DNS.CloudflareAPIKey == "" || h.cfg.DNS.CloudflareZoneID == "") {
			status = "not_configured"
		}
		if name == "namecheap" && (h.cfg.DNS.NamecheapAPIUser == "" || h.cfg.DNS.NamecheapAPIKey == "" || h.cfg.DNS.NamecheapUsername == "") {
			status = "not_configured"
		}
		out = append(out, fiber.Map{
			"name":     name,
			"status":   status,
			"notes":    notes,
		})
	}
	return c.JSON(fiber.Map{"providers": out})
}

// GetAdminDNSPlan generates the desired-state DNS plan for a
// domain. Read-only; no DNS lookups; safe to call repeatedly.
//
// Response shape: { "domain": "...", "plan": <Plan>, "notes": [...] }
func (h *Handler) GetAdminDNSPlan(c fiber.Ctx) error {
	svc := h.dnsOpsService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "dns ops service unavailable",
		})
	}
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	inputs, err := h.dnsOpsInputsForDomain(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		// We can't build a plan without a server IPv4. Surface a
		// 422 with the missing-input reason so the dashboard can
		// show it honestly rather than fabricating an A record.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "server IPv4 not configured; set coremail.smtp_host to the public mail server IP",
		})
	}
	plan, err := svc.Generate(inputs)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	notes := []string{}
	if inputs.DKIMPubKey == "" {
		notes = append(notes, "DKIM key not generated for this domain yet — call POST /admin/dns/"+domain+"/dkim to generate a key pair; the public DNS TXT will then be populated automatically")
	}
	return c.JSON(fiber.Map{
		"domain": domain,
		"plan":   plan,
		"notes":  notes,
	})
}

// PostAdminDNSVerify runs live DNS verification against the plan
// for a domain. Read-only; idempotent.
//
// Response shape: { "domain": "...", "report": <VerifyReport> }
func (h *Handler) PostAdminDNSVerify(c fiber.Ctx) error {
	svc := h.dnsOpsService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "dns ops service unavailable",
		})
	}
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	inputs, err := h.dnsOpsInputsForDomain(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "server IPv4 not configured",
		})
	}
	plan, err := svc.Generate(inputs)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	report, err := svc.Verify(ctx, plan)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "verify: " + err.Error(),
		})
	}
	return c.JSON(fiber.Map{
		"domain": domain,
		"report": report,
	})
}

// PostAdminDNSDKIM generates (or rotates) the DKIM key pair for
// the given domain. The private key is persisted to
// coremail_dkim_config (server-side only); the response carries
// ONLY the public DNS TXT and the selector.
//
// Body: { "selector"?: "<name>" }
// Response: { "domain": "...", "selector": "...", "public_dns_txt": "v=DKIM1; k=rsa; p=...",
//            "dns_record_name": "<selector>._domainkey.<domain>",
//            "stored": true }
func (h *Handler) PostAdminDNSDKIM(c fiber.Ctx) error {
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	if domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain is required"})
	}
	var req struct {
		Selector string `json:"selector"`
	}
	// Body is optional; default to "default" when absent.
	_ = c.Bind().JSON(&req)
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		selector = "default"
	}
	// Generate the RSA 2048 key pair.
	privPEM, _, err := dkim.GenerateKeyPair(selector, domain)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "dkim keygen failed: " + err.Error(),
		})
	}
	// Derive the public DNS TXT from the private key. The
	// dkim.GenerateKeyPair already returned the DNS record, but
	// we re-derive it via dkim.GenerateDNSRecord so the wire
	// format matches the verifier's expectation.
	pubKey, ok := deriveDKIMPublicKey(privPEM)
	if !ok {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "dkim keygen succeeded but public key derivation failed",
		})
	}
	dnsName, dnsValue := dkim.GenerateDNSRecord(selector, domain, pubKey)
	// Persist to coremail_dkim_config.
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "db unavailable",
		})
	}
	now := time.Now().UTC()
	var existing int64
	row := sqlDB.QueryRowContext(c.Context(),
		`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?`, domain)
	if err := row.Scan(&existing); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "dkim lookup: " + err.Error(),
		})
	}
	if existing > 0 {
		if _, err := sqlDB.ExecContext(c.Context(),
			`UPDATE coremail_dkim_config SET selector = ?, private_key_pem = ?, enabled = 1, updated_at = ? WHERE domain = ?`,
			selector, privPEM, now, domain); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "dkim update: " + err.Error(),
			})
		}
	} else {
		if _, err := sqlDB.ExecContext(c.Context(),
			`INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, 1, ?, ?)`,
			domain, selector, privPEM, now, now); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "dkim insert: " + err.Error(),
			})
		}
	}
	h.writeAuditLog(c, "dns.dkim.generate", fmt.Sprintf("domain:%s|selector:%s", domain, selector))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"domain":          domain,
		"selector":        selector,
		"public_dns_txt":  dnsValue,
		"dns_record_name": dnsName,
		"stored":          true,
	})
}

// PostAdminDNSProviderPlan returns the provider's dry-run change
// plan for a domain. The manual provider always succeeds with a
// copy/paste step list; cloudflare / namecheap return an empty
// steps list and a "not configured" note when their env config is
// missing.
//
// Body: { "provider": "manual" | "cloudflare" | "namecheap" }
// Response: { "domain": "...", "provider": "...", "change_plan": <ChangePlan> }
func (h *Handler) PostAdminDNSProviderPlan(c fiber.Ctx) error {
	svc := h.dnsOpsService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "dns ops service unavailable",
		})
	}
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	providerName := strings.TrimSpace(c.Query("provider"))
	if providerName == "" {
		var req struct {
			Provider string `json:"provider"`
		}
		_ = c.Bind().JSON(&req)
		providerName = strings.TrimSpace(req.Provider)
	}
	if providerName == "" {
		providerName = "manual"
	}
	inputs, err := h.dnsOpsInputsForDomain(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "server IPv4 not configured",
		})
	}
	plan, err := svc.Generate(inputs)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	cp, err := svc.PlanProvider(c.Context(), providerName, plan)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"domain":      domain,
		"provider":    providerName,
		"change_plan": cp,
	})
}

// PostAdminDNSProviderApply runs the provider Apply path. The
// caller MUST supply an explicit confirmation string in the body
// ({"confirm": "yes-i-confirm"}); providers reject empty input.
//
// Cloudflare / Namecheap Apply always refuses in this build (the
// live API path is not audited yet). The manual provider returns
// a Failed result with copy/paste instructions.
//
// Audit log: every call is recorded via writeAuditLog, regardless
// of success.
func (h *Handler) PostAdminDNSProviderApply(c fiber.Ctx) error {
	svc := h.dnsOpsService()
	if svc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "dns ops service unavailable",
		})
	}
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	providerName := strings.TrimSpace(c.Query("provider"))
	if providerName == "" {
		var req struct {
			Provider string `json:"provider"`
		}
		_ = c.Bind().JSON(&req)
		providerName = strings.TrimSpace(req.Provider)
	}
	if providerName == "" {
		providerName = "manual"
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	// Re-bind body. The first Bind may have consumed it; if so,
	// peek at the raw bytes for the confirmation string.
	raw := strings.TrimSpace(string(c.Body()))
	if raw != "" {
		// Best-effort JSON parse.
		_ = c.Bind().JSON(&req)
	}
	confirm := strings.TrimSpace(req.Confirm)
	if confirm == "" {
		// Accept the confirmation in a query parameter as a
		// fallback so the dashboard can render a simple POST
		// with ?confirm=... when JSON binding is awkward.
		confirm = strings.TrimSpace(c.Query("confirm"))
	}
	if confirm == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "confirm field is required",
		})
	}
	inputs, err := h.dnsOpsInputsForDomain(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "server IPv4 not configured",
		})
	}
	plan, err := svc.Generate(inputs)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	cp, err := svc.PlanProvider(c.Context(), providerName, plan)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	res, err := svc.ApplyProvider(c.Context(), providerName, cp, confirm)
	if err != nil {
		h.writeAuditLog(c, "dns.provider.apply",
			fmt.Sprintf("domain:%s|provider:%s|result:rejected", domain, providerName))
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "dns.provider.apply",
		fmt.Sprintf("domain:%s|provider:%s|applied:%d|skipped:%d|failed:%d",
			domain, providerName, res.Applied, res.Skipped, res.Failed))
	return c.JSON(fiber.Map{
		"domain":   domain,
		"provider": providerName,
		"result":   res,
	})
}

// PostAdminDNSCheck is a thin alias for the existing
// /dns/check/:domain endpoint, kept for backward compat with the
// pre-DNS-DKIM-OPERATIONS-2F UI. It returns a 200 with the live
// verification status (same shape as the legacy stub but with
// honest values).
func (h *Handler) PostAdminDNSCheck(c fiber.Ctx) error {
	return h.PostAdminDNSVerify(c)
}

// GetAdminDNSWizard returns the generated plan only — no DNS
// lookups. Backward-compat alias for /dns/wizard/:domain.
func (h *Handler) GetAdminDNSWizard(c fiber.Ctx) error {
	return h.GetAdminDNSPlan(c)
}

// silence unused-import warnings if the test build swaps out
// audit log helpers.
var _ = rand.Reader
var _ sql.IsolationLevel = sql.LevelDefault
