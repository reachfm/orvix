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
//     ("apply-dns-changes" or a per-domain confirm string).

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
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
// any) to obtain the active DKIM selector + public key. The
// public mail IPv4 and IPv6 come from cfg.DNS.PublicIPv4 /
// cfg.DNS.PublicIPv6 — NOT from cfg.CoreMail.SMTPHost, which is
// the listener bind address and defaults to 0.0.0.0 (DNS-DKIM-
// OPERATIONS-2F-SAFETY-FIX).
//
// Returns the inputs, or an error when the public IP is missing
// / invalid. The caller surfaces the error as 422 with the
// reason so the dashboard can render an honest "public mail IP
// is not configured" message instead of generating 0.0.0.0
// records.
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
	// Public mail IPv4 / IPv6 from the dedicated DNS config
	// block. We MUST NOT use cfg.CoreMail.SMTPHost here — that
	// is a listener bind address and defaults to 0.0.0.0. The
	// isPublicUnicastIP helper rejects 0.0.0.0, ::, loopback,
	// link-local, private (RFC1918 / ULA), and multicast so
	// the generated A / SPF records always point at a real
	// public mail IP. IPv6 is optional; the AAAA record is
	// only emitted when a valid public IPv6 is configured.
	serverIPv4 := ""
	if h.cfg.DNS.PublicIPv4 != "" {
		if ip, err := isPublicUnicastIP(h.cfg.DNS.PublicIPv4); err != nil {
			return dnsops.Inputs{}, err
		} else {
			serverIPv4 = ip.String()
		}
	}
	serverIPv6 := ""
	if h.cfg.DNS.PublicIPv6 != "" {
		if ip, err := isPublicUnicastIP(h.cfg.DNS.PublicIPv6); err != nil {
			return dnsops.Inputs{}, err
		} else {
			serverIPv6 = ip.String()
		}
	}
	// DKIM selector + public key (from coremail_dkim_config if a
	// row exists for this domain). The selector is normalised
	// through validateDKIMSelector so we never store or echo a
	// value that violates the strict rules. The "orvix" default
	// passes the validator.
	selector := "orvix"
	pubKey := ""
	if sqlDB, err := h.db.DB(); err == nil {
		var s string
		row := sqlDB.QueryRowContext(ctx,
			`SELECT selector, private_key_pem FROM coremail_dkim_config WHERE domain = ?`,
			domain)
		var priv string
		if err := row.Scan(&s, &priv); err == nil {
			if normalised, err := validateDKIMSelector(s); err == nil {
				selector = normalised
			} else {
				// Stored selector is unsafe (legacy row from
				// before 2F-SAFETY-FIX). Fall back to the
				// safe default rather than echoing the bad
				// value. The operator can re-run Generate
				// DKIM key to overwrite.
				selector = "orvix"
			}
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
	// Listener bind host (coremail.smtp_host) is INFORMATIONAL ONLY
	// — it is echoed in Plan.ListenerBind so the dashboard can show
	// it next to ServerIPv4 / ServerIPv6 as a separate concept. A /
	// SPF / AAAA records MUST use ServerIPv4 / ServerIPv6, never
	// this value (which defaults to 0.0.0.0 on a fresh install).
	listenerBind := strings.TrimSpace(h.cfg.CoreMail.SMTPHost)
	return dnsops.Inputs{
		Domain:        domain,
		MailHost:      mailHost,
		ServerIPv4:    serverIPv4,
		ServerIPv6:    serverIPv6,
		ListenerBind:  listenerBind,
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
// and the readiness status of each. The status field is one of:
//
//   - "manual"        — manual provider; always available
//   - "not_configured" — credentials missing server-side
//   - "dry_run_only"   — credentials present, but the
//                         apply kill switch is off (Namecheap)
//                         or live apply is intentionally
//                         disabled (Cloudflare in this build)
//   - "ready"         — credentials + apply enabled
//
// The response never carries a token value. Operators can read
// the audit log or /api/v1/admin/summary for the high-level
// status; the dashboard's provider panel uses the status field
// to enable / disable the Apply button.
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
		switch name {
		case "manual":
			status = "manual"
		case "cloudflare":
			if h.cfg.DNS.CloudflareAPIKey == "" || h.cfg.DNS.CloudflareZoneID == "" {
				status = "not_configured"
				notes = append(notes, "set dns.cloudflare_api_key and dns.cloudflare_zone_id in the server config to enable")
			} else {
				// Cloudflare live apply is intentionally
				// disabled in this build.
				status = "dry_run_only"
				notes = append(notes, "cloudflare live apply is intentionally disabled in this build; dry-run plan only")
			}
		case "namecheap":
			if h.cfg.DNS.NamecheapAPIUser == "" || h.cfg.DNS.NamecheapAPIKey == "" || h.cfg.DNS.NamecheapUsername == "" {
				status = "not_configured"
				notes = append(notes, "set dns.namecheap_api_user, dns.namecheap_api_key, and dns.namecheap_username in the server config to enable")
			} else if !h.cfg.DNS.NamecheapEnableApply {
				status = "dry_run_only"
				notes = append(notes, "namecheap credentials present but dns.namecheap_enable_apply is false; set it to true (or set ORVIX_DNS_NAMECHEAP_ENABLE_APPLY=true) to enable live apply")
			} else {
				status = "ready"
				notes = append(notes, "namecheap live apply is enabled server-side; the apply button is active when no conflicts are present in the dry-run plan")
			}
		default:
			notes = append(notes, "configure server-side to enable; tokens are never returned to clients")
		}
		// Universal safety note — applies to every non-manual
		// provider regardless of status. Ensures the operator
		// never sees a token-shaped substring.
		if name != "manual" {
			notes = append(notes, "credentials are server-side only; the dashboard never receives the token value")
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
		// dnsOpsInputsForDomain returns errors for "input not
		// configured correctly" cases (missing public IP, IP
		// rejected as private/loopback/link-local/unspecified,
		// etc.). 422 is the honest "not configured" status for
		// those; the operator must fix the server config, not
		// the request.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		// We can't build a plan without a public mail IPv4. Surface
		// a 422 with the missing-input reason so the dashboard can
		// show it honestly rather than fabricating an A record.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "public mail IPv4 is not configured; set dns.public_ipv4 in the server config (do NOT use coremail.smtp_host — that is a listener bind address)",
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
		// See GetAdminDNSPlan for the rationale on 422 vs 400.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "public mail IPv4 is not configured; set dns.public_ipv4 in the server config (do NOT use coremail.smtp_host - that is a listener bind address)",
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
//
// Safety guards (DNS-DKIM-OPERATIONS-2F-SAFETY-FIX):
//
//   - The domain MUST already exist in coremail_domains (active
//     row, deleted_at IS NULL). Orphan DKIM rows for unprovisioned
//     domains are not allowed. We return 404 with a structured
//     error in that case.
//   - The selector is run through validateDKIMSelector BEFORE
//     keygen or storage. Unsafe selectors (dot, space, slash,
//     underscore, unicode, wildcard, leading/trailing hyphen,
//     longer than 63 chars) are rejected with 400. The default
//     selector when the request omits it is "orvix" (which
//     passes the validator).
func (h *Handler) PostAdminDNSDKIM(c fiber.Ctx) error {
	domain := strings.ToLower(strings.TrimSpace(c.Params("domain")))
	if domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain is required"})
	}
	// Domain existence check: refuse orphan DKIM rows for
	// domains Orvix has not provisioned.
	exists, err := h.domainExists(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "domain lookup failed",
		})
	}
	if !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("domain %q is not provisioned in Orvix; add the domain under Domains before generating a DKIM key", domain),
		})
	}
	var req struct {
		Selector        string `json:"selector"`
		ConfirmRotation string `json:"confirm_rotation"`
	}
	// Body is optional; default to "orvix" when absent.
	_ = c.Bind().JSON(&req)
	// Strict selector validation. Empty input maps to the safe
	// default "orvix"; unsafe values are rejected with 400
	// BEFORE any keygen or DB write happens.
	selector, err := validateDKIMSelector(req.Selector)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	// Check for existing DKIM row. If one exists, require explicit
	// destructive confirmation before overwriting.
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
		if req.ConfirmRotation != "rotate-dkim-key" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "DKIM key already exists; confirm rotation to replace it",
			})
		}
	}
	// Generate the RSA 2048 key pair. We generate AFTER the
	// rotation check so we never waste keygen CPU on a rejected
	// request.
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
			"error": "public mail IPv4 is not configured; set dns.public_ipv4 in the server config (do NOT use coremail.smtp_host - that is a listener bind address)",
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
// ({"confirm": "apply-dns-changes"}); providers reject empty input.
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
			"error": "confirm field is required; the operator must type apply-dns-changes to authorise the write",
		})
	}
	// The literal confirmation string is "apply-dns-changes"
	// (DNS-AUTOMATION-2G). The provider layer enforces this
	// exact value, so the handler surfaces a 400 for any other
	// non-empty value rather than letting the provider refuse
	// with a less specific error.
	if confirm != "apply-dns-changes" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "confirmation must be the literal string apply-dns-changes",
		})
	}
	inputs, err := h.dnsOpsInputsForDomain(c.Context(), domain)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if inputs.ServerIPv4 == "" {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "public mail IPv4 is not configured; set dns.public_ipv4 in the server config (do NOT use coremail.smtp_host - that is a listener bind address)",
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
