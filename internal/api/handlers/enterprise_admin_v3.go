package handlers

// Enterprise admin v2-v3 — the remaining sections that the
// brief listed as "Not implemented" in the prior report:
//
//   - SSL certificate status + upload/import
//   - Antivirus / anti-spam status (honest, no ClamAV
//     integration claimed if the service is not running)
//   - Acceptance & routing rules (CRUD + audit)
//   - Admin-scoped incoming message rules (CRUD + audit)
//   - FTP / SFTP backup targets (target config + connection
//     test, no password echo)
//   - File System Access (read-only browser, restricted to
//     approved roots, no path traversal, no secrets shown)
//   - Migration sources (CRUD + connection test, secrets
//     stored in a separate table)
//   - Clustering status + IMAP / POP3 / WebMail proxy status
//     (config-only; runtime consensus is intentionally not
//     claimed since it is not implemented)
//   - Settings split: per-protocol settings endpoints
//     (SMTP-RX / SMTP-TX / IMAP / POP3 / WebMail / WebAdmin
//     / DNS / Remote POP / JMAP / Mobility)
//   - Mailbox create accepts class_id and applies the
//     class's quota / send-limit / feature gates
//
// All mutations go through the CSRF-protected `men` group.
// All reads require admin role. All mutations write a row
// to coremail_audit. Passwords / private keys / license
// material are never echoed in any response — see individual
// handlers for the secret-handling contract.

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/fsguard"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

// configEncryptString is a thin alias over config.EncryptString
// so the file does not have to repeatedly type the long form.
func configEncryptString(s string) (string, error) { return config.EncryptString(s) }

// =====================================================================
// Settings split — per-protocol sub-pages.
// =====================================================================
//
// GlobalSettings stays the "everything at once" endpoint the
// /settings page already uses. PerProtocolSettings is a
// additive split that returns ONLY the keys relevant to one
// protocol, with restart-required awareness. The PATCH
// counterpart applies them against the same DB-backed
// admin_settings table.

// protocolDef describes one protocol split page. Each page reads
// and writes a dedicated subset of admin_settings keys; the
// descriptions here are the source of truth for what each
// protocol page exposes in the UI.
type protocolDef struct {
	ID              string
	Title           string
	Description     string
	Keys            []protocolKey
	RequiresRestart bool // the protocol as a whole needs restart on hot change
}

type protocolKey struct {
	Key             string `json:"key"`
	Type            string `json:"type"` // bool/int/string
	Label           string `json:"label"`
	Description     string `json:"description"`
	RestartRequired bool   `json:"restart_required"`
	Default         any    `json:"default,omitempty"`
	// ReadOnly, when non-empty, is the reason this key can never be
	// PATCHed — e.g. no corresponding live-config field exists to
	// bind it to, or no canonical valid range/format could be proven
	// for it. PATCH always rejects a write to a read-only key with
	// this exact reason rather than silently persisting a no-op
	// setting.
	ReadOnly string `json:"read_only,omitempty"`
}

// validatedProtocolField is one field of a PATCH request that passed
// type coercion and semantic validation, paired with its protocolDef
// entry (for RestartRequired at write time) and its normalized value.
type validatedProtocolField struct {
	key    string
	defKey protocolKey
	value  any
}

var protocolDefs = map[string]protocolDef{
	"smtp_recv": {
		ID:          "smtp_recv",
		Title:       "SMTP receiving",
		Description: "Inbound SMTP listener configuration (port 25, opportunistic / required TLS, banners, size limits, recipient limits).",
		Keys: []protocolKey{
			{Key: "coremail.smtp_port", Type: "int", Label: "SMTP port", RestartRequired: true, Default: 25},
			{Key: "coremail.smtp_host", Type: "string", Label: "SMTP bind host", RestartRequired: true, Default: "0.0.0.0"},
			{Key: "coremail.require_tls_for_auth", Type: "bool", Label: "Require TLS for AUTH", RestartRequired: true, Default: true},
			{Key: "coremail.require_auth_for_submission", Type: "bool", Label: "Require AUTH for submission (587)", RestartRequired: true, Default: true},
			{Key: "coremail.max_attachment_size_mb", Type: "int", Label: "Max attachment size (MB)", RestartRequired: true, Default: 25},
			{Key: "coremail.max_attachments_per_message", Type: "int", Label: "Max attachments per message", RestartRequired: true, Default: 20},
			{Key: "coremail.queue_workers", Type: "int", Label: "Queue workers", RestartRequired: true, Default: 1},
			{Key: "coremail.worker_interval", Type: "string", Label: "Worker interval (duration)", RestartRequired: true, Default: "5s"},
		},
	},
	"smtp_tx": {
		ID:          "smtp_tx",
		Title:       "SMTP sending / submission",
		Description: "Outbound SMTP delivery and submission listener (587, SMTPS 465).",
		Keys: []protocolKey{
			{Key: "coremail.submission_enabled", Type: "bool", Label: "Submission enabled (587)", RestartRequired: true, Default: true},
			{Key: "coremail.submission_port", Type: "int", Label: "Submission port", RestartRequired: true, Default: 587},
			{Key: "coremail.submission_host", Type: "string", Label: "Submission bind host", RestartRequired: true, Default: "0.0.0.0"},
			{Key: "coremail.smtps_enabled", Type: "bool", Label: "SMTPS enabled (465)", RestartRequired: true, Default: false},
			{Key: "coremail.smtps_port", Type: "int", Label: "SMTPS port", RestartRequired: true, Default: 465},
			{Key: "outbound.prefer_ipv4", Type: "bool", Label: "Prefer IPv4 for outbound delivery", RestartRequired: false, Default: false},
		},
	},
	"imap": {
		ID:          "imap",
		Title:       "IMAP",
		Description: "IMAP / IMAPS listener configuration (143 / 993).",
		Keys: []protocolKey{
			{Key: "coremail.imap_host", Type: "string", Label: "IMAP bind host", RestartRequired: true, Default: "0.0.0.0"},
			{Key: "coremail.imap_port", Type: "int", Label: "IMAP port", RestartRequired: true, Default: 143},
			{Key: "coremail.imaps_enabled", Type: "bool", Label: "IMAPS enabled (993)", RestartRequired: true, Default: false},
			{Key: "coremail.imaps_port", Type: "int", Label: "IMAPS port", RestartRequired: true, Default: 993},
		},
	},
	"pop3": {
		ID:          "pop3",
		Title:       "POP3",
		Description: "POP3 / POP3S listener configuration (110 / 995).",
		Keys: []protocolKey{
			{Key: "coremail.pop3_host", Type: "string", Label: "POP3 bind host", RestartRequired: true, Default: "0.0.0.0"},
			{Key: "coremail.pop3_port", Type: "int", Label: "POP3 port", RestartRequired: true, Default: 110},
			{Key: "coremail.pop3s_enabled", Type: "bool", Label: "POP3S enabled (995)", RestartRequired: true, Default: false},
			{Key: "coremail.pop3s_port", Type: "int", Label: "POP3S port", RestartRequired: true, Default: 995},
		},
	},
	"webmail": {
		ID:          "webmail",
		Title:       "WebMail",
		Description: "WebMail UI runtime configuration.",
		Keys: []protocolKey{
			{Key: "auth.cookie_domain", Type: "string", Label: "Auth cookie domain (.parent.com)", RestartRequired: true, Default: ""},
			{Key: "auth.jwt_access_ttl", Type: "string", Label: "Access-token TTL (duration)", RestartRequired: true, Default: "15m"},
			{Key: "auth.jwt_refresh_ttl", Type: "string", Label: "Refresh-token TTL (duration)", RestartRequired: true, Default: "720h"},
		},
	},
	"webadmin": {
		ID:          "webadmin",
		Title:       "WebAdmin",
		Description: "Admin console runtime configuration.",
		Keys: []protocolKey{
			{Key: "auth.password_min_len", Type: "int", Label: "Password min length", RestartRequired: true, Default: 8},
			{Key: "monitoring.disk_usage_warning_pct", Type: "int", Label: "Disk usage warning %", RestartRequired: false, Default: 85},
			{Key: "monitoring.disk_usage_critical_pct", Type: "int", Label: "Disk usage critical %", RestartRequired: false, Default: 95},
		},
	},
	"dns": {
		ID:          "dns",
		Title:       "DNS automation",
		Description: "DNS provider integration for automated record management.",
		Keys: []protocolKey{
			{Key: "dns.public_ipv4", Type: "string", Label: "Public IPv4", RestartRequired: true, Default: ""},
			{Key: "dns.public_ipv6", Type: "string", Label: "Public IPv6", RestartRequired: true, Default: ""},
			{Key: "dns.namecheap_enable_apply", Type: "bool", Label: "Allow Namecheap live apply", RestartRequired: false, Default: false},
		},
	},
	"remote_pop": {
		ID:          "remote_pop",
		Title:       "Remote POP",
		Description: "Remote POP fetch (fetchmail) settings used by per-mailbox external pop3 polling.",
		Keys: []protocolKey{
			{Key: "coremail.imap_idle_enabled", Type: "bool", Label: "IMAP IDLE push", RestartRequired: false, Default: false, ReadOnly: "no corresponding field exists on the live CoreMail config — this key was never wired to a real runtime setting"},
		},
	},
	"jmap": {
		ID:          "jmap",
		Title:       "JMAP / CJA / modern sync",
		Description: "JMAP listener configuration (RFC 8620 / 8621).",
		Keys: []protocolKey{
			{Key: "coremail.jmap_host", Type: "string", Label: "JMAP bind host", RestartRequired: true, Default: "127.0.0.1"},
			{Key: "coremail.jmap_port", Type: "int", Label: "JMAP port", RestartRequired: true, Default: 8081},
		},
	},
	"mobility": {
		ID:          "mobility",
		Title:       "Mobility & Sync",
		Description: "Mobile device sync (EAS / Activesync) and push notification settings.",
		Keys: []protocolKey{
			{Key: "coremail.vapid_subject", Type: "string", Label: "VAPID subject (mailto:)", RestartRequired: true, Default: ""},
			{Key: "coremail.max_attachment_size_mb", Type: "int", Label: "Max attachment size (MB)", RestartRequired: true, Default: 25},
		},
	},
}

// ListProtocolSettings serves GET
// /api/v1/admin/settings/protocol/:protocol
//
// Returns the canonical key list for the protocol and the
// current effective values (live config fall-through to
// persisted DB overrides), with a per-field
// requires_restart flag. The response also carries the
// settings-bridge summary so the admin UI can show
// "applied vs needs restart" honestly.
func (h *Handler) ListProtocolSettings(c fiber.Ctx) error {
	if h.cfg == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "config not ready")
	}
	pid := strings.ToLower(strings.TrimSpace(c.Params("protocol")))
	def, ok := protocolDefs[pid]
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "unknown protocol page: "+pid)
	}
	keys := make([]map[string]any, 0, len(def.Keys))
	for _, k := range def.Keys {
		row := map[string]any{
			"key":              k.Key,
			"label":            k.Label,
			"description":      k.Description,
			"type":             k.Type,
			"restart_required": k.RestartRequired,
			"default":          k.Default,
		}
		if k.ReadOnly != "" {
			row["read_only"] = k.ReadOnly
		}
		if h.settingsStore != nil {
			if entry, err := h.settingsStore.Get(c.Context(), k.Key); err == nil && entry != nil && !entry.Redacted {
				row["value"] = entry.Value
				row["persisted"] = true
				row["updated_at"] = entry.UpdatedAt.Format(time.RFC3339)
				keys = append(keys, row)
				continue
			}
		}
		row["value"] = resolveLiveConfigValue(h.cfg, k.Key)
		row["persisted"] = false
		keys = append(keys, row)
	}
	out := fiber.Map{
		"protocol":    def.ID,
		"title":       def.Title,
		"description": def.Description,
		"keys":        keys,
	}
	if h.settingsBridge != nil {
		out["bridge"] = h.settingsBridge.Snapshot()
	}
	return c.JSON(out)
}

// PatchProtocolSettings serves PATCH
// /api/v1/admin/settings/protocol/:protocol. It accepts a
// JSON body whose top-level keys are the dotted setting
// names. Fields not in the protocol's allowlist are
// rejected. Type mismatches are reported per-field.
func (h *Handler) PatchProtocolSettings(c fiber.Ctx) error {
	pid := strings.ToLower(strings.TrimSpace(c.Params("protocol")))
	def, ok := protocolDefs[pid]
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "unknown protocol page: "+pid)
	}
	if h.settingsStore == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "settings persistence not wired; PATCH unavailable",
		})
	}
	// Truthful hot/pending-restart reporting depends on running the
	// real bridge after every write — if it isn't wired, refuse the
	// mutation rather than silently falling back to unverified static
	// flags the way the previous implementation did.
	if h.settingsBridge == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "settings bridge not wired; cannot report real apply state, PATCH unavailable",
		})
	}
	sqlDB, err := h.db.DB()
	if err != nil || sqlDB == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not ready"})
	}

	var raw map[string]json.RawMessage
	if err := c.Bind().JSON(&raw); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}

	allowed := make(map[string]protocolKey, len(def.Keys))
	for _, k := range def.Keys {
		allowed[k.Key] = k
	}

	var (
		good     []validatedProtocolField
		rejected []map[string]any
	)

	for key, val := range raw {
		defKey, ok := allowed[key]
		if !ok {
			rejected = append(rejected, map[string]any{"key": key, "reason": "not editable on this protocol page"})
			continue
		}
		if defKey.ReadOnly != "" {
			rejected = append(rejected, map[string]any{"key": key, "reason": "read-only: " + defKey.ReadOnly})
			continue
		}
		typed, err := coerceForType(val, defKey.Type)
		if err != nil {
			rejected = append(rejected, map[string]any{"key": key, "reason": err.Error()})
			continue
		}
		validator, hasValidator := protocolKeyValidators[key]
		if !hasValidator {
			// No provable canonical constraint exists for this key —
			// per policy it must be ReadOnly, not silently accepted.
			// A key ending up here is a def/validator drift bug.
			rejected = append(rejected, map[string]any{"key": key, "reason": "no validator registered for this key — treated as not editable"})
			continue
		}
		normalized, err := validator(typed)
		if err != nil {
			rejected = append(rejected, map[string]any{"key": key, "reason": err.Error()})
			continue
		}
		good = append(good, validatedProtocolField{key: key, defKey: defKey, value: normalized})
	}

	// Cross-field: disk usage warning % must stay below critical %.
	// Both keys live on the "webadmin" protocol page; only enforced
	// when at least one of them is actually part of this patch.
	if pid == "webadmin" {
		if rej := h.validateDiskUsageOrdering(c, good); rej != nil {
			rejected = append(rejected, rej...)
		}
	}

	// Any rejection — unknown key, read-only, invalid value, or a
	// failed cross-field check — aborts the ENTIRE request. Nothing
	// below this point may run: zero DB mutations, zero live-config
	// changes, zero audit rows.
	if len(rejected) > 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"applied":  []map[string]any{},
			"rejected": rejected,
		})
	}

	// No-op detection: drop any field whose new value already equals
	// the current effective value (persisted override, else live
	// config). A patch that changes nothing must not write a row or
	// an audit entry.
	toApply := make([]validatedProtocolField, 0, len(good))
	for _, g := range good {
		current := h.currentEffectiveProtocolValue(c, g.key)
		if valuesEqual(current, g.value) {
			continue
		}
		toApply = append(toApply, g)
	}
	if len(toApply) == 0 {
		return c.JSON(fiber.Map{
			"applied":          []map[string]any{},
			"rejected":         []map[string]any{},
			"hot_applied":      []string{},
			"pending_restart":  []string{},
			"restart_required": false,
		})
	}

	now := time.Now().UTC()
	d := h.sqlDialect()
	tx, err := sqlDB.BeginTx(c.Context(), nil)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "begin transaction: "+err.Error())
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	applied := make([]map[string]any, 0, len(toApply))
	for _, g := range toApply {
		encoded, err := json.Marshal(g.value)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "encode "+g.key+": "+err.Error())
		}
		if _, execErr := tx.ExecContext(c.Context(),
			`INSERT INTO admin_settings (key, value, section, requires_restart, updated_at, updated_by) VALUES (`+d.Placeholders(6)+`)
			 ON CONFLICT (key) DO UPDATE SET value=`+d.Excluded("value")+`, section=`+d.Excluded("section")+`, requires_restart=`+d.Excluded("requires_restart")+`, updated_at=`+d.Excluded("updated_at"),
			g.key, string(encoded), pid, boolToInt(g.defKey.RestartRequired), now, auditActorFromCtx(c),
		); execErr != nil {
			// A mid-transaction persist failure aborts the WHOLE
			// patch (the deferred Rollback above undoes every row
			// this loop already wrote) — never a partial commit.
			return fiber.NewError(fiber.StatusInternalServerError, "persist "+g.key+": "+execErr.Error())
		}
		applied = append(applied, map[string]any{
			"key":              g.key,
			"value":            g.value,
			"restart_required": g.defKey.RestartRequired,
			"updated_at":       now.Format(time.RFC3339),
		})
	}
	if err := tx.Commit(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "commit: "+err.Error())
	}
	committed = true

	// Only now — after a successful commit — ask the bridge what
	// ACTUALLY took effect in the live process. This replaces the old
	// behavior of trusting the static per-key RestartRequired flag as
	// if it were the outcome; the flag is only ever used above to
	// decide what to WRITE to admin_settings, never what to report as
	// applied.
	summary, bridgeErr := h.settingsBridge.Apply(c.Context())
	appliedSet := make(map[string]bool, len(toApply))
	for _, g := range toApply {
		appliedSet[g.key] = true
	}
	hotApplied := make([]string, 0, len(toApply))
	pendingRestart := make([]string, 0, len(toApply))
	if bridgeErr == nil {
		hotSet := make(map[string]bool, len(summary.AppliedKeys))
		for _, k := range summary.AppliedKeys {
			hotSet[k] = true
		}
		for _, g := range toApply {
			if hotSet[g.key] {
				hotApplied = append(hotApplied, g.key)
			} else {
				pendingRestart = append(pendingRestart, g.key)
			}
		}
	} else {
		// The write already committed; we just can't confirm which
		// keys are hot vs pending right now. Report everything as
		// pending — the honest, conservative default — rather than
		// guessing "applied".
		for _, g := range toApply {
			pendingRestart = append(pendingRestart, g.key)
		}
	}

	h.writeAuditLog(c, "settings.patch.protocol",
		fmt.Sprintf("protocol:%s|applied:%d|hot_applied:%d|pending_restart:%d", pid, len(applied), len(hotApplied), len(pendingRestart)))

	resp := fiber.Map{
		"applied":          applied,
		"rejected":         []map[string]any{},
		"hot_applied":      hotApplied,
		"pending_restart":  pendingRestart,
		"restart_required": len(pendingRestart) > 0,
	}
	if bridgeErr != nil {
		h.logger.Warn("settings bridge apply failed after successful commit", zap.String("protocol", pid), zap.Error(bridgeErr))
		resp["bridge_apply_error"] = "settings were saved but the live-apply check failed; treat all changed fields as pending restart until confirmed"
	}
	return c.JSON(resp)
}

// currentEffectiveProtocolValue mirrors ListProtocolSettings' own
// value resolution (persisted override, else live config) so no-op
// detection compares against exactly what the operator would see on
// the GET response.
func (h *Handler) currentEffectiveProtocolValue(c fiber.Ctx, key string) any {
	if h.settingsStore != nil {
		if entry, err := h.settingsStore.Get(c.Context(), key); err == nil && entry != nil && !entry.Redacted {
			return entry.Value
		}
	}
	return resolveLiveConfigValue(h.cfg, key)
}

// valuesEqual compares a stored/live value (often float64 or int64
// from JSON round-tripping) against a freshly-validated value without
// caring about the exact numeric Go type.
func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	af, aIsNum := toFloat64(a)
	bf, bIsNum := toFloat64(b)
	if aIsNum && bIsNum {
		return af == bf
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

// validateDiskUsageOrdering enforces monitoring.disk_usage_warning_pct
// < monitoring.disk_usage_critical_pct using the EFFECTIVE value of
// each (the patched value if present in this request, else the
// current stored/live value) — so a patch touching only one of the
// pair still can't put the pair out of order.
func (h *Handler) validateDiskUsageOrdering(c fiber.Ctx, good []validatedProtocolField) []map[string]any {
	const warnKey = "monitoring.disk_usage_warning_pct"
	const critKey = "monitoring.disk_usage_critical_pct"

	touches := false
	patched := map[string]any{}
	for _, g := range good {
		if g.key == warnKey || g.key == critKey {
			touches = true
			patched[g.key] = g.value
		}
	}
	if !touches {
		return nil
	}

	effective := func(key string) (int64, bool) {
		if v, ok := patched[key]; ok {
			if n, ok := v.(int64); ok {
				return n, true
			}
		}
		cur := h.currentEffectiveProtocolValue(c, key)
		if n, ok := toFloat64(cur); ok {
			return int64(n), true
		}
		return 0, false
	}

	warn, warnOK := effective(warnKey)
	crit, critOK := effective(critKey)
	if !warnOK || !critOK {
		return nil // one side has no resolvable current value yet; nothing to compare
	}
	if warn >= crit {
		var rej []map[string]any
		if _, ok := patched[warnKey]; ok {
			rej = append(rej, map[string]any{"key": warnKey, "reason": fmt.Sprintf("warning %% (%d) must be less than critical %% (%d)", warn, crit)})
		}
		if _, ok := patched[critKey]; ok {
			rej = append(rej, map[string]any{"key": critKey, "reason": fmt.Sprintf("critical %% (%d) must be greater than warning %% (%d)", crit, warn)})
		}
		return rej
	}
	return nil
}

// auditActorFromCtx is a tiny helper that returns the current
// admin user id, or nil if absent (legacy tests).
func auditActorFromCtx(c fiber.Ctx) any {
	if uid, ok := c.Locals("user_id").(uint); ok && uid > 0 {
		v := int64(uid)
		return &v
	}
	return nil
}

// resolveLiveConfigValue returns the current effective value
// of a setting from the in-memory config struct, falling
// back to the per-key default declared in the protocol def.
func resolveLiveConfigValue(cfg interface{}, key string) any {
	if cfg == nil {
		return nil
	}
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return nil
	}
	// Map our dotted config paths (which follow the config package's
	// mapstructure tags, e.g. "coremail.smtp_port") to the live Config
	// struct by matching each segment's `mapstructure` tag rather than
	// the exported Go field name — struct fields are capitalized
	// (CoreMail, Auth, DNS, ...) and a literal FieldByName("coremail")
	// never matches FieldByName("CoreMail"), which previously made
	// this resolver return nil for every single key.
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Struct {
		return nil
	}
	sub := fieldByMapstructureTag(v, parts[0])
	if !sub.IsValid() || sub.Kind() != reflect.Struct {
		return nil
	}
	field := fieldByMapstructureTag(sub, parts[1])
	if !field.IsValid() {
		return nil
	}
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return nil
		}
		field = field.Elem()
	}
	// time.Duration fields are declared as "string" in protocolDefs
	// (duration literals like "5s") — surface the same formatted
	// string on GET, not the raw underlying int64 nanosecond count.
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		return time.Duration(field.Int()).String()
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(field.Uint())
	case reflect.Bool:
		return field.Bool()
	case reflect.String:
		return field.String()
	}
	return nil
}

// fieldByMapstructureTag finds the struct field of v whose
// `mapstructure:"..."` tag equals name (case-insensitive). Falls back
// to an exact Go field name match so callers passing an
// already-capitalized name still work.
func fieldByMapstructureTag(v reflect.Value, name string) reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if tag, ok := sf.Tag.Lookup("mapstructure"); ok && strings.EqualFold(tag, name) {
			return v.Field(i)
		}
	}
	return v.FieldByName(name)
}

// coerceForType does the same job as settings.coerceType but
// is duplicated here to avoid an import cycle on the
// settings package (handlers -> settings already exists via
// admin_settings.go and adding the inverse would tangle
// build wiring for tests).
func coerceForType(raw json.RawMessage, want string) (any, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("null")
	}
	switch want {
	case "bool":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("want bool, got %s", string(raw))
		}
		return b, nil
	case "int":
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, fmt.Errorf("want int, got %s", string(raw))
		}
		return n, nil
	case "string":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("want string, got %s", string(raw))
		}
		return s, nil
	}
	return nil, fmt.Errorf("unknown allowlist type %q", want)
}

// =====================================================================
// Antispam / Antivirus status — honest service-status endpoint.
// =====================================================================

// AdminAntivirusStatus serves GET
// /api/v1/admin/security/antivirus. It probes the local
// ClamAV daemon on ClamAVConfig.Host:ClamAVConfig.Port and
// returns a strict status. The endpoint is read-only; we
// never claim scanning is enabled unless (a) the local
// daemon responds to PING, AND (b) the runtime has wired
// the engine into the SMTP receive path (the engine
// flips its runtime_enforced flag after the SMTP
// receiver init attaches it; the admin handler reads
// the same flag and reports it back to the operator).
func (h *Handler) AdminAntivirusStatus(c fiber.Ctx) error {
	host := "localhost"
	port := 3310
	clamavConfigured := false
	clamavReachable := false
	clamavPing := ""
	runtimeEnforced := false
	policyOnInfected := "reject"
	policyOnScannerUnavailable := "fail_closed"
	lastErr := ""
	scanned, infected := int64(0), int64(0)
	rejCount, quarCount, tagCount := int64(0), int64(0), int64(0)
	failOpen, failClosed := int64(0), int64(0)

	if h.cfg != nil {
		if h.cfg.ClamAV.Host != "" {
			host = h.cfg.ClamAV.Host
		}
		if h.cfg.ClamAV.Port > 0 {
			port = h.cfg.ClamAV.Port
		}
		clamavConfigured = true
	}
	if clamavConfigured {
		// PING the daemon. Sends the bytes "PING\n" and
		// expects "PONG\n".
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
		if err == nil {
			conn.SetDeadline(time.Now().Add(800 * time.Millisecond))
			_, werr := conn.Write([]byte("PING\n"))
			if werr == nil {
				buf := make([]byte, 64)
				n, rerr := conn.Read(buf)
				if rerr == nil && n >= 4 && strings.EqualFold(string(buf[:4]), "PONG") {
					clamavReachable = true
					clamavPing = "PONG"
				}
			}
			conn.Close()
		}
	}

	// Use the wired engine snapshot when available.
	// Engine snapshot is the source of truth for
	// runtime_enforced, last_error, and per-policy
	// counters; the probe above is the reachability
	// check the dashboard renders as a green / red dot.
	if h.antivirusService != nil {
		snap := h.antivirusService.Snapshot(c.Context())
		runtimeEnforced = snap.RuntimeEnforced
		policyOnInfected = snap.PolicyOnInfected
		policyOnScannerUnavailable = snap.PolicyOnUnavailable
		lastErr = snap.LastError
		scanned, infected = snap.Scanned, snap.Infected
		if h.antivirusService.RuntimeEnforced() && h.observability != nil {
			mSnap := h.observability.Metrics.Snapshot()
			rejCount = mSnap.AntivirusRejected
			quarCount = mSnap.AntivirusQuarantined
			tagCount = mSnap.AntivirusTagged
			failOpen = mSnap.AntivirusFailOpen
			failClosed = mSnap.AntivirusFailClosed
		}
	}

	// Honest policy defaults — any feature the runtime
	// has NOT wired continues to read as stored-only / not
	// enforced. The operator reads runtime_enforced to
	// know what is live today.
	routingActive := h.rulerService != nil && h.rulerEngineActive()
	incomingActive := h.rulerService != nil && h.rulerEngineActive()
	antispamActive := false // policy not advertised in this build

	return c.JSON(fiber.Map{
		"engine":                        "clamav",
		"engine_configured":             clamavConfigured,
		"engine_reachable":              clamavReachable,
		"engine_active":                 clamavConfigured && clamavReachable,
		"runtime_enforced":              runtimeEnforced,
		"clamav_host":                   host,
		"clamav_port":                   port,
		"clamav_response":               clamavPing,
		"policy_on_infected":            policyOnInfected,
		"policy_on_scanner_unavailable": policyOnScannerUnavailable,
		"last_error":                    lastErr,
		"counts": fiber.Map{
			"scanned":     scanned,
			"infected":    infected,
			"rejected":    rejCount,
			"quarantined": quarCount,
			"tagged":      tagCount,
			"fail_open":   failOpen,
			"fail_closed": failClosed,
		},
		"antispam_engine":     "rspamd_not_wired",
		"antispam_active":     antispamActive,
		"routing_engine":      "internal_ruler",
		"routing_active":      routingActive,
		"incoming_msg_engine": "internal_ruler",
		"incoming_msg_active": incomingActive,
		"honest_notes": []string{
			"runtime_enforced is true only when the SMTP receiver is calling the engine on every AcceptMessage call",
			"engine_active (clamav daemon reachable) is independent of runtime_enforced (operator may have disabled the daemon)",
			"routing and incoming rules use the internal/ruler engine; runtime_enforced requires the SMTP receiver to call it",
		},
	})
}

// rulerEngineActive reports whether either the
// acceptance-rule engine or the incoming-rule engine has
// been installed. The runtime installs both simultaneously
// so this is a single boolean; it remains split into two
// per-engine MarkEnforced calls so the admin status
// endpoint can report them separately in the future.
func (h *Handler) rulerEngineActive() bool {
	if h.rulerService == nil {
		return false
	}
	return h.rulerService.AcceptanceEnforced() || h.rulerService.IncomingEnforced()
}

// =====================================================================
// Acceptance & Routing rules (CRUD).
// =====================================================================

// ListAcceptanceRules serves GET /api/v1/admin/acceptance-rules.
func (h *Handler) ListAcceptanceRules(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), h.sqlQ(`
		SELECT id, name, priority, enabled, scope, scope_target, sender_pattern,
		       recipient_pattern, source_ip_cidr, action, redirect_to, note,
		       created_at, updated_at
		FROM coremail_acceptance_rules
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY priority ASC, id ASC`), tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list rules: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                                                                      int64
			name, scope, scopeTarget, senderPat, recipientPat, sourceCIDR, action, redirectTo, note string
			priority                                                                                int
			enabled                                                                                 int
			created, updated                                                                        time.Time
		)
		if err := rows.Scan(&id, &name, &priority, &enabled, &scope, &scopeTarget, &senderPat,
			&recipientPat, &sourceCIDR, &action, &redirectTo, &note, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan rule: %v", err))
		}
		out = append(out, map[string]any{
			"id":                id,
			"name":              name,
			"priority":          priority,
			"enabled":           enabled == 1,
			"scope":             scope,
			"scope_target":      scopeTarget,
			"sender_pattern":    senderPat,
			"recipient_pattern": recipientPat,
			"source_ip_cidr":    sourceCIDR,
			"action":            action,
			"redirect_to":       redirectTo,
			"note":              note,
			"created_at":        created.UTC().Format(time.RFC3339),
			"updated_at":        updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"rules": out})
}

// CreateAcceptanceRule serves POST /api/v1/admin/acceptance-rules.
func (h *Handler) CreateAcceptanceRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body acceptanceRulePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateAcceptanceRule(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		INSERT INTO coremail_acceptance_rules
			(tenant_id, name, priority, enabled, scope, scope_target, sender_pattern,
			 recipient_pattern, source_ip_cidr, action, redirect_to, note,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		tenantID, body.Name, body.Priority, boolToInt(body.Enabled), body.Scope, body.ScopeTarget,
		body.SenderPattern, body.RecipientPattern, body.SourceIPCIDR, body.Action, body.RedirectTo,
		body.Note, now, now,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create rule: %v", err))
	}
	id, _ := res.LastInsertId()
	if err := h.appendAudit(c, "acceptance_rule.create", body.Name, "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateAcceptanceRule serves PATCH
// /api/v1/admin/acceptance-rules/:id.
func (h *Handler) UpdateAcceptanceRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body acceptanceRulePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	// PATCH must run the same runtime-truthful action
	// contract as POST. Without this call, an operator
	// could PATCH an existing rule with action=redirect
	// or action=hold and the row would silently store an
	// inert value the SMTP receiver never executes.
	if err := validateAcceptanceRule(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		UPDATE coremail_acceptance_rules SET
			priority = ?, enabled = ?, scope = ?, scope_target = ?, sender_pattern = ?,
			recipient_pattern = ?, source_ip_cidr = ?, action = ?, redirect_to = ?, note = ?,
			updated_at = ?
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		body.Priority, boolToInt(body.Enabled), body.Scope, body.ScopeTarget, body.SenderPattern,
		body.RecipientPattern, body.SourceIPCIDR, body.Action, body.RedirectTo, body.Note,
		now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update rule: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "rule not found in this tenant")
	}
	if err := h.appendAudit(c, "acceptance_rule.update", fmt.Sprintf("rule:%d", id), "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteAcceptanceRule serves DELETE
// /api/v1/admin/acceptance-rules/:id.
func (h *Handler) DeleteAcceptanceRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_acceptance_rules SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete rule: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "rule not found in this tenant")
	}
	if err := h.appendAudit(c, "acceptance_rule.delete", fmt.Sprintf("rule:%d", id), "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// TestAcceptanceRule serves POST
// /api/v1/admin/acceptance-rules/test. Given a sample
// sender / recipient / source IP, walk the enabled rules
// in priority order and report which rule (if any) would
// match. This is the "dry-run/test rule" affordance from
// the brief. It does NOT mutate any state.
func (h *Handler) TestAcceptanceRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body struct {
		Sender    string `json:"sender"`
		Recipient string `json:"recipient"`
		SourceIP  string `json:"source_ip"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	rows, err := h.sqlDB().QueryContext(c.Context(),
		`SELECT id, name, priority, enabled, scope, scope_target, sender_pattern,`+
			` recipient_pattern, source_ip_cidr, action`+
			` FROM coremail_acceptance_rules`+
			` WHERE tenant_id = `+h.dialect.Placeholder(1)+` AND deleted_at IS NULL AND enabled = `+h.dialect.TrueLiteral()+
			` ORDER BY priority ASC, id ASC`, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list rules: %v", err))
	}
	defer rows.Close()

	type rule struct {
		ID, Priority                                                          int64
		Name, Scope, ScopeTarget, SenderPat, RecipientPat, SourceCIDR, Action string
	}
	var rules []rule
	for rows.Next() {
		var r rule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &enabled, &r.Scope, &r.ScopeTarget,
			&r.SenderPat, &r.RecipientPat, &r.SourceCIDR, &r.Action); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan rule: %v", err))
		}
		rules = append(rules, r)
	}

	for _, r := range rules {
		if r.ScopeTarget != "" && r.Scope == "domain" && !strings.EqualFold(body.Recipient, r.ScopeTarget) &&
			!strings.HasSuffix(strings.ToLower(body.Recipient), "@"+strings.ToLower(r.ScopeTarget)) {
			continue
		}
		if r.SenderPat != "" && !patternMatch(body.Sender, r.SenderPat) {
			continue
		}
		if r.RecipientPat != "" && !patternMatch(body.Recipient, r.RecipientPat) {
			continue
		}
		if r.SourceCIDR != "" {
			ip := net.ParseIP(body.SourceIP)
			if ip == nil {
				continue
			}
			_, cidr, err := net.ParseCIDR(r.SourceCIDR)
			if err != nil || !cidr.Contains(ip) {
				continue
			}
		}
		return c.JSON(fiber.Map{
			"matched_rule_id":   r.ID,
			"matched_rule_name": r.Name,
			"priority":          r.Priority,
			"action":            r.Action,
			"action_label":      r.Action,
			"sender":            body.Sender,
			"recipient":         body.Recipient,
			"source_ip":         body.SourceIP,
			"rules_walked":      len(rules),
		})
	}
	return c.JSON(fiber.Map{
		"matched_rule_id": nil,
		"action":          "accept",
		"action_label":    "accept",
		"rules_walked":    len(rules),
		"sender":          body.Sender,
		"recipient":       body.Recipient,
		"source_ip":       body.SourceIP,
	})
}

// acceptanceActionLabel is preserved as a thin wrapper
// around the runtime-supported action enum so callers
// that still pass a string value can produce the same
// label as the DB-backed response. The label is now
// always the canonical action string itself — runtime
// action contract matches DB row exactly.
func acceptanceActionLabel(action string) string {
	switch action {
	case "accept", "reject", "quarantine":
		return action
	}
	return ""
}

// acceptanceRulePayload is the wire-format payload accepted by
// Create + Update.
type acceptanceRulePayload struct {
	Name             string `json:"name"`
	Priority         int    `json:"priority"`
	Enabled          bool   `json:"enabled"`
	Scope            string `json:"scope"`
	ScopeTarget      string `json:"scope_target"`
	SenderPattern    string `json:"sender_pattern"`
	RecipientPattern string `json:"recipient_pattern"`
	SourceIPCIDR     string `json:"source_ip_cidr"`
	Action           string `json:"action"`
	RedirectTo       string `json:"redirect_to"`
	Note             string `json:"note"`
}

// acceptanceActions is the runtime-truthful enum of
// actions the SMTP acceptance engine actually executes.
// The values are stored verbatim in the
// coremail_acceptance_rules.action TEXT column and
// returned to the runtime through EvaluateAcceptance,
// which switches on the same set in
// internal/coremail/smtp/receive.go.
//
// Adding an action here requires three things to land
// in the same change:
//  1. an extension to the runtime switch in
//     internal/coremail/smtp/receive.go (or
//     internal/coremail/smtp/commands.go for the
//     MAIL FROM / RCPT TO path),
//  2. a documented matching handler in
//     receive.go that explains how the action is
//     applied to the inbound message,
//  3. a unit test in
//     internal/coremail/smtp/runtime_integration_test.go
//     that exercises the action against a fixture
//     envelope.
//
// Until all three exist, the action MUST NOT appear in
// this map; otherwise the API would accept a rule that
// the runtime silently drops to "accept" (no match).
var acceptanceActions = map[string]bool{
	"accept":     true,
	"reject":     true,
	"quarantine": true,
}

// validateAcceptanceRule does input validation for the
// acceptance-rule payload. The action is restricted to
// the runtime-supported set declared in
// acceptanceActions above — anything else is rejected
// with a clear error so the admin UI cannot store an
// inert rule.
func validateAcceptanceRule(p acceptanceRulePayload) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if !acceptanceActions[p.Action] {
		return fmt.Errorf("action %q is not supported; allowed actions: accept, reject, quarantine", p.Action)
	}
	// Redirect/hold payloads are not just renamed to a
	// supported action: their semantic is fundamentally
	// different (forward / queue + admin review). If the
	// payload still carries redirect_to or note saying
	// "hold", reject loudly instead of silently dropping
	// the field.
	if strings.TrimSpace(p.RedirectTo) != "" {
		return errors.New("redirect_to is not supported; the action=redirect contract has been removed from the runtime. Use a per-recipient routing rule outside this page if you need forwarding.")
	}
	if p.SourceIPCIDR != "" {
		if _, _, err := net.ParseCIDR(p.SourceIPCIDR); err != nil {
			return fmt.Errorf("source_ip_cidr invalid: %v", err)
		}
	}
	return nil
}

// patternMatch is a tiny glob matcher: '*' → any (greedy),
// otherwise case-insensitive substring. Wildcards at the
// start or end of the pattern accept the empty string too.
func patternMatch(value, pattern string) bool {
	if pattern == "" {
		return true
	}
	if strings.Contains(pattern, "*") {
		// Convert glob to regex on the fly.
		parts := strings.Split(pattern, "*")
		idx := 0
		lower := strings.ToLower(value)
		for i, p := range parts {
			if p == "" {
				continue
			}
			j := strings.Index(lower[idx:], strings.ToLower(p))
			if j < 0 {
				return false
			}
			idx += j + len(p)
			// '*' between segments: if pattern starts or ends
			// with '*' allow empty match in between.
			if i > 0 && i < len(parts)-1 && idx > len(lower) {
				return false
			}
		}
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
}

// =====================================================================
// Admin-scoped Incoming Message Rules (CRUD).
// =====================================================================

// ListIncomingMsgRules serves GET
// /api/v1/admin/incoming-msg-rules. Distinct from the
// per-mailbox webmail rules — these are admin-scoped rules
// applied before any per-mailbox filter, scoped by tenant.
func (h *Handler) ListIncomingMsgRules(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), h.sqlQ(`
		SELECT id, name, priority, enabled, field, operator, value, action,
		       action_target, apply_to, stop_processing, note,
		       created_at, updated_at
		FROM coremail_incoming_msg_rules
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY priority ASC, id ASC`), tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list rules: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, priority, stop                                        int64
			name, field, op, val, action, actionTarget, applyTo, note string
			enabled                                                   int
			created, updated                                          time.Time
		)
		if err := rows.Scan(&id, &name, &priority, &enabled, &field, &op, &val, &action,
			&actionTarget, &applyTo, &stop, &note, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan: %v", err))
		}
		out = append(out, map[string]any{
			"id":              id,
			"name":            name,
			"priority":        priority,
			"enabled":         enabled == 1,
			"field":           field,
			"operator":        op,
			"value":           val,
			"action":          action,
			"action_target":   actionTarget,
			"apply_to":        applyTo,
			"stop_processing": stop == 1,
			"note":            note,
			"created_at":      created.UTC().Format(time.RFC3339),
			"updated_at":      updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"rules": out})
}

// CreateIncomingMsgRule serves POST
// /api/v1/admin/incoming-msg-rules.
func (h *Handler) CreateIncomingMsgRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body incomingMsgRulePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateIncomingMsgRule(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		INSERT INTO coremail_incoming_msg_rules
			(tenant_id, name, priority, enabled, field, operator, value,
			 action, action_target, apply_to, stop_processing, note,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		tenantID, body.Name, body.Priority, boolToInt(body.Enabled), body.Field, body.Operator, body.Value,
		body.Action, body.ActionTarget, body.ApplyTo, boolToInt(body.StopProcessing), body.Note,
		now, now)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create: %v", err))
	}
	id, _ := res.LastInsertId()
	if err := h.appendAudit(c, "incoming_msg_rule.create", body.Name, "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateIncomingMsgRule serves PATCH
// /api/v1/admin/incoming-msg-rules/:id.
func (h *Handler) UpdateIncomingMsgRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body incomingMsgRulePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateIncomingMsgRule(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		UPDATE coremail_incoming_msg_rules SET
			priority = ?, enabled = ?, field = ?, operator = ?, value = ?,
			action = ?, action_target = ?, apply_to = ?, stop_processing = ?, note = ?,
			updated_at = ?
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		body.Priority, boolToInt(body.Enabled), body.Field, body.Operator, body.Value,
		body.Action, body.ActionTarget, body.ApplyTo, boolToInt(body.StopProcessing), body.Note,
		now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "rule not found in this tenant")
	}
	if err := h.appendAudit(c, "incoming_msg_rule.update", fmt.Sprintf("rule:%d", id), "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteIncomingMsgRule serves DELETE
// /api/v1/admin/incoming-msg-rules/:id.
func (h *Handler) DeleteIncomingMsgRule(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_incoming_msg_rules SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "rule not found in this tenant")
	}
	if err := h.appendAudit(c, "incoming_msg_rule.delete", fmt.Sprintf("rule:%d", id), "ok"); err != nil {
		return err
	}
	if h.rulerService != nil {
		h.rulerService.Invalidate()
	}
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

type incomingMsgRulePayload struct {
	Name           string `json:"name"`
	Priority       int    `json:"priority"`
	Enabled        bool   `json:"enabled"`
	Field          string `json:"field"`
	Operator       string `json:"operator"`
	Value          string `json:"value"`
	Action         string `json:"action"`
	ActionTarget   string `json:"action_target"`
	ApplyTo        string `json:"apply_to"`
	StopProcessing bool   `json:"stop_processing"`
	Note           string `json:"note"`
}

var (
	allowedIncomingFields = map[string]bool{
		"subject": true, "from": true, "to": true, "any_header": true,
		"size": true, "spf": true, "dkim": true, "dmarc": true,
	}
	allowedIncomingOps = map[string]bool{
		"contains": true, "equals": true, "starts_with": true, "ends_with": true, "matches": true, "gt": true, "lt": true,
	}
	// allowedIncomingActions is the runtime-truthful
	// enum of incoming-rule actions. The runtime in
	// internal/coremail/smtp/receive.go switches on
	// exactly these three strings after the antivirus
	// scan; every other legacy action (move / label /
	// forward / discard / hold) is rejected with a clear
	// 400 by the API so the UI cannot persist a rule
	// that the runtime silently drops to "no decision".
	allowedIncomingActions = map[string]bool{
		"reject":     true,
		"quarantine": true,
		"tag":        true,
	}
	allowedApplyTo = map[string]bool{"all": true, "incoming_only": true, "outgoing_only": true}
)

func validateIncomingMsgRule(p incomingMsgRulePayload) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if !allowedIncomingFields[p.Field] {
		return fmt.Errorf("field %q is not supported", p.Field)
	}
	if !allowedIncomingOps[p.Operator] {
		return fmt.Errorf("operator %q is not supported", p.Operator)
	}
	if !allowedIncomingActions[p.Action] {
		return fmt.Errorf("action %q is not supported; allowed actions: reject, quarantine, tag", p.Action)
	}
	if p.ApplyTo == "" {
		p.ApplyTo = "all"
	}
	if !allowedApplyTo[p.ApplyTo] {
		return fmt.Errorf("apply_to %q is not supported", p.ApplyTo)
	}
	// tag and quarantine accept an optional
	// action_target (header label / quarantine reason).
	// We require it to be a non-empty, low-cardinality
	// string when present, but we do not require it
	// unconditionally — the runtime treats a missing
	// action_target as "use the rule name".
	if p.ActionTarget != "" && len(p.ActionTarget) > 200 {
		return errors.New("action_target too long; max 200 characters")
	}
	return nil
}

// =====================================================================
// Migration sources (CRUD + connection test).
// =====================================================================

// ListMigrationSources serves GET
// /api/v1/admin/migration-sources. The endpoint returns
// every source for the current tenant; passwords are NEVER
// echoed. The boolean `has_secret` tells the UI whether a
// password has been stored.
func (h *Handler) ListMigrationSources(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), h.sqlQ(`
		SELECT id, name, kind, host, port, username, use_tls, allow_insecure,
		       default_base_folder, verify_hostname, note, has_secret,
		       last_test_status, last_test_at, last_test_message,
		       created_at, updated_at
		FROM coremail_migration_sources
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`), tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list sources: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id                                                                                        int64
			name, kind, host, username, baseFolder, verifyHostname, note, lastTestStatus, lastTestMsg string
			port                                                                                      int
			useTLS, allowInsecure, hasSecret                                                          int
			lastTestAt                                                                                *time.Time
			created, updated                                                                          time.Time
		)
		if err := rows.Scan(&id, &name, &kind, &host, &port, &username, &useTLS, &allowInsecure,
			&baseFolder, &verifyHostname, &note, &hasSecret,
			&lastTestStatus, &lastTestAt, &lastTestMsg, &created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan: %v", err))
		}
		var lastTestAtStr string
		if lastTestAt != nil {
			lastTestAtStr = lastTestAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"id":                  id,
			"name":                name,
			"kind":                kind,
			"host":                host,
			"port":                port,
			"username":            username,
			"use_tls":             useTLS == 1,
			"allow_insecure":      allowInsecure == 1,
			"default_base_folder": baseFolder,
			"verify_hostname":     verifyHostname,
			"has_secret":          hasSecret == 1,
			"last_test_status":    lastTestStatus,
			"last_test_at":        lastTestAtStr,
			"last_test_message":   lastTestMsg,
			"note":                note,
			"created_at":          created.UTC().Format(time.RFC3339),
			"updated_at":          updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"sources": out})
}

// CreateMigrationSource serves POST
// /api/v1/admin/migration-sources. The password, if supplied,
// is stored in coremail_migration_source_secrets using
// config.EncryptString and the row's `has_secret` flag is
// set; the listing endpoint never returns the password.
func (h *Handler) CreateMigrationSource(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body migrationSourcePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateMigrationSource(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		INSERT INTO coremail_migration_sources
			(tenant_id, name, kind, host, port, username, use_tls, allow_insecure,
			 default_base_folder, verify_hostname, note, has_secret,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		tenantID, body.Name, body.Kind, body.Host, body.Port, body.Username,
		boolToInt(body.UseTLS), boolToInt(body.AllowInsecure),
		body.DefaultBaseFolder, body.VerifyHostname, body.Note,
		boolToInt(body.Password != ""),
		now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "migration source name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create: %v", err))
	}
	id, _ := res.LastInsertId()
	if body.Password != "" {
		if err := storeMigrationSecret(c, h, id, body.Password); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("store secret: %v", err))
		}
	}
	if err := h.appendAudit(c, "migration_source.create", body.Name, "ok"); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateMigrationSource serves PATCH
// /api/v1/admin/migration-sources/:id.
func (h *Handler) UpdateMigrationSource(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body migrationSourcePayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	now := time.Now().UTC()
	// Only refresh the password row if the operator supplied
	// a non-empty `password`; an empty string keeps the existing
	// secret unchanged. The boolean `clear_secret` is the
	// explicit "wipe" affordance.
	newHasSecret := -1
	if body.ClearSecret {
		newHasSecret = 0
	}
	// Detect if a new password was supplied so we update the
	// secret row and the has_secret flag.
	pwSupplied := body.Password != ""
	if newHasSecret == -1 {
		// preserve existing flag if not explicitly cleared and no new pw
		var existing int
		_ = h.sqlDB().QueryRowContext(c.Context(),
			h.sqlQ(`SELECT has_secret FROM coremail_migration_sources WHERE id=? AND tenant_id=? AND deleted_at IS NULL`),
			id, tenantID).Scan(&existing)
		if pwSupplied {
			newHasSecret = 1
		} else {
			newHasSecret = existing
		}
	}
	if !pwSupplied && !body.ClearSecret {
		newHasSecret = -2 // sentinel: don't touch flag
	}
	if newHasSecret == -2 {
		res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
			UPDATE coremail_migration_sources SET
				name = ?, kind = ?, host = ?, port = ?, username = ?, use_tls = ?, allow_insecure = ?,
				default_base_folder = ?, verify_hostname = ?, note = ?, updated_at = ?
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
			body.Name, body.Kind, body.Host, body.Port, body.Username,
			boolToInt(body.UseTLS), boolToInt(body.AllowInsecure),
			body.DefaultBaseFolder, body.VerifyHostname, body.Note, now, id, tenantID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update: %v", err))
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fiber.NewError(fiber.StatusNotFound, "source not found in this tenant")
		}
	} else {
		res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
			UPDATE coremail_migration_sources SET
				name = ?, kind = ?, host = ?, port = ?, username = ?, use_tls = ?, allow_insecure = ?,
				default_base_folder = ?, verify_hostname = ?, note = ?, has_secret = ?, updated_at = ?
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
			body.Name, body.Kind, body.Host, body.Port, body.Username,
			boolToInt(body.UseTLS), boolToInt(body.AllowInsecure),
			body.DefaultBaseFolder, body.VerifyHostname, body.Note, newHasSecret, now, id, tenantID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update: %v", err))
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fiber.NewError(fiber.StatusNotFound, "source not found in this tenant")
		}
	}
	if pwSupplied {
		if err := storeMigrationSecret(c, h, id, body.Password); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("store secret: %v", err))
		}
	}
	if body.ClearSecret {
		if _, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`DELETE FROM coremail_migration_source_secrets WHERE source_id = ?`), id); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("clear secret: %v", err))
		}
	}
	if err := h.appendAudit(c, "migration_source.update", fmt.Sprintf("source:%d", id), "ok"); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteMigrationSource serves DELETE
// /api/v1/admin/migration-sources/:id.
func (h *Handler) DeleteMigrationSource(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_migration_sources SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "source not found in this tenant")
	}
	// Secrets go with the source. Best-effort.
	if _, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`DELETE FROM coremail_migration_source_secrets WHERE source_id = ?`), id); err != nil {
		h.logger.Warn("migration source: secret row cleanup failed", zap.Int64("source_id", id), zap.Error(err))
	}
	if err := h.appendAudit(c, "migration_source.delete", fmt.Sprintf("source:%d", id), "ok"); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// TestMigrationSource serves POST
// /api/v1/admin/migration-sources/:id/test. Performs a real
// TCP probe to host:port. The status of the probe (success,
// timeout, refused, dns-error, ssl-error) is stored in the
// row and returned. The endpoint never echoes the password.
func (h *Handler) TestMigrationSource(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var (
		host     string
		port     int
		useTLS   bool
		kind     string
		username string
	)
	if err := h.sqlDB().QueryRowContext(c.Context(),
		h.sqlQ(`SELECT host, port, use_tls, kind, username FROM coremail_migration_sources WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		id, tenantID).Scan(&host, &port, &useTLS, &kind, &username); err != nil {
		if err == sql.ErrNoRows {
			return fiber.NewError(fiber.StatusNotFound, "source not found in this tenant")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup: %v", err))
	}
	if host == "" || port == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "source has no host/port")
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	result := probeTCP(address, 3*time.Second)

	// Best-effort TLS handshake probe when use_tls.
	if useTLS && result.Connected {
		conn, err := tlsDial("tcp", address, host)
		if err != nil {
			result.Message = fmt.Sprintf("tcp ok, tls failed: %v", err)
			result.Status = "tls_failed"
		} else {
			conn.Close()
			result.Message = "tcp + tls ok (login not attempted; password not echoed)"
			result.Status = "ok"
		}
	}
	now := time.Now().UTC()
	if _, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_migration_sources SET last_test_status = ?, last_test_at = ?, last_test_message = ? WHERE id = ? AND tenant_id = ?`),
		result.Status, now, result.Message, id, tenantID); err != nil {
		h.logger.Warn("test source: persist failed", zap.Error(err))
	}
	if err := h.appendAudit(c, "migration_source.test", fmt.Sprintf("source:%d:%s", id, result.Status), "ok"); err != nil {
		return err
	}
	return c.JSON(result)
}

// migrationSourcePayload is the wire-format payload for
// Create + Update.
type migrationSourcePayload struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	UseTLS            bool   `json:"use_tls"`
	AllowInsecure     bool   `json:"allow_insecure"`
	DefaultBaseFolder string `json:"default_base_folder"`
	VerifyHostname    string `json:"verify_hostname"`
	Note              string `json:"note"`
	ClearSecret       bool   `json:"clear_secret"`
}

var allowedMigrationKinds = map[string]bool{"imap": true, "jmap": true, "pop3": true, "ews": true, "smtp": true}

func validateMigrationSource(p migrationSourcePayload) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if !allowedMigrationKinds[strings.ToLower(p.Kind)] {
		return fmt.Errorf("kind %q is not supported", p.Kind)
	}
	if strings.TrimSpace(p.Host) == "" {
		return errors.New("host is required")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return errors.New("port must be in [1,65535]")
	}
	return nil
}

func storeMigrationSecret(c fiber.Ctx, h *Handler, sourceID int64, password string) error {
	cipher, err := configEncryptString(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	d := h.sqlDialect()
	_, err = h.sqlDB().ExecContext(c.Context(),
		`INSERT INTO coremail_migration_source_secrets (source_id, password_enc, updated_at) VALUES (`+d.Placeholders(3)+`)
		 ON CONFLICT (source_id) DO UPDATE SET password_enc=`+d.Excluded("password_enc")+`, updated_at=`+d.Excluded("updated_at"),
		sourceID, cipher, now)
	return err
}

// =====================================================================
// FTP / SFTP backup targets (CRUD + connection test).
// =====================================================================

// ListBackupTargets serves GET
// /api/v1/admin/backup-targets. Passwords are NEVER
// echoed. The boolean `has_secret` tells the UI whether a
// password has been stored.
func (h *Handler) ListBackupTargets(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	rows, err := h.sqlDB().QueryContext(c.Context(), h.sqlQ(`
		SELECT id, name, kind, host, port, username, path, enabled, verify_hostname,
		       has_secret, last_test_status, last_test_at, last_test_message, note,
		       created_at, updated_at
		FROM coremail_backup_targets
		WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY name ASC`), tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("list: %v", err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, port, enabled, hasSecret                                            int64
			name, kind, host, username, path, verifyHost, lastStatus, lastMsg, note string
			lastTestAt                                                              *time.Time
			created, updated                                                        time.Time
		)
		if err := rows.Scan(&id, &name, &kind, &host, &port, &username, &path, &enabled,
			&verifyHost, &hasSecret, &lastStatus, &lastTestAt, &lastMsg, &note,
			&created, &updated); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("scan: %v", err))
		}
		var lastAtStr string
		if lastTestAt != nil {
			lastAtStr = lastTestAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"id":                id,
			"name":              name,
			"kind":              kind,
			"host":              host,
			"port":              port,
			"username":          username,
			"path":              path,
			"enabled":           enabled == 1,
			"verify_hostname":   verifyHost,
			"has_secret":        hasSecret == 1,
			"last_test_status":  lastStatus,
			"last_test_at":      lastAtStr,
			"last_test_message": lastMsg,
			"note":              note,
			"created_at":        created.UTC().Format(time.RFC3339),
			"updated_at":        updated.UTC().Format(time.RFC3339),
		})
	}
	return c.JSON(fiber.Map{"targets": out, "honest_note": "FTP / SFTP target transfer is not yet wired into the backup post-processor in this build. Storing a target here is safe and persistent; the runtime will pick it up once the upload step lands."})
}

// CreateBackupTarget serves POST
// /api/v1/admin/backup-targets.
func (h *Handler) CreateBackupTarget(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	var body backupTargetPayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateBackupTarget(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
		INSERT INTO coremail_backup_targets
			(tenant_id, name, kind, host, port, username, path, enabled, verify_hostname,
			 has_secret, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		tenantID, body.Name, body.Kind, body.Host, body.Port, body.Username, body.Path,
		boolToInt(body.Enabled), boolToInt(body.VerifyHostname),
		boolToInt(body.Password != ""), body.Note, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fiber.NewError(fiber.StatusConflict, "backup target name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("create: %v", err))
	}
	id, _ := res.LastInsertId()
	if body.Password != "" {
		if err := storeBackupTargetSecret(c, h, id, body.Password, body.PrivateKeyPath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("store secret: %v", err))
		}
	}
	if err := h.appendAudit(c, "backup_target.create", body.Name, "ok"); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id, "name": body.Name})
}

// UpdateBackupTarget serves PATCH
// /api/v1/admin/backup-targets/:id.
func (h *Handler) UpdateBackupTarget(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var body backupTargetPayload
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := validateBackupTarget(body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	now := time.Now().UTC()
	// Determine has_secret outcome from flags + supplied pw.
	newHasSecret := -1
	if body.ClearSecret {
		newHasSecret = 0
	}
	if newHasSecret == -1 {
		var existing int
		_ = h.sqlDB().QueryRowContext(c.Context(),
			h.sqlQ(`SELECT has_secret FROM coremail_backup_targets WHERE id=? AND tenant_id=? AND deleted_at IS NULL`),
			id, tenantID).Scan(&existing)
		if body.Password != "" {
			newHasSecret = 1
		} else {
			newHasSecret = existing
		}
	}
	pwSupplied := body.Password != ""
	if newHasSecret == -1 && !pwSupplied {
		newHasSecret = -2
	}
	if newHasSecret == -2 {
		res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
			UPDATE coremail_backup_targets SET
				name = ?, kind = ?, host = ?, port = ?, username = ?, path = ?, enabled = ?, verify_hostname = ?,
				note = ?, updated_at = ?
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
			body.Name, body.Kind, body.Host, body.Port, body.Username, body.Path,
			boolToInt(body.Enabled), boolToInt(body.VerifyHostname),
			body.Note, now, id, tenantID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update: %v", err))
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fiber.NewError(fiber.StatusNotFound, "target not found in this tenant")
		}
	} else {
		res, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`
			UPDATE coremail_backup_targets SET
				name = ?, kind = ?, host = ?, port = ?, username = ?, path = ?, enabled = ?, verify_hostname = ?,
				has_secret = ?, note = ?, updated_at = ?
			WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
			body.Name, body.Kind, body.Host, body.Port, body.Username, body.Path,
			boolToInt(body.Enabled), boolToInt(body.VerifyHostname),
			newHasSecret, body.Note, now, id, tenantID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("update: %v", err))
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fiber.NewError(fiber.StatusNotFound, "target not found in this tenant")
		}
	}
	if pwSupplied {
		if err := storeBackupTargetSecret(c, h, id, body.Password, body.PrivateKeyPath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("store secret: %v", err))
		}
	}
	if body.ClearSecret {
		if _, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`DELETE FROM coremail_backup_target_secrets WHERE target_id = ?`), id); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("clear secret: %v", err))
		}
	}
	if err := h.appendAudit(c, "backup_target.update", fmt.Sprintf("target:%d", id), "ok"); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": id, "updated": true})
}

// DeleteBackupTarget serves DELETE
// /api/v1/admin/backup-targets/:id.
func (h *Handler) DeleteBackupTarget(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	now := time.Now().UTC()
	res, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_backup_targets SET deleted_at = ?, updated_at = ?
		 WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		now, now, id, tenantID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("delete: %v", err))
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fiber.NewError(fiber.StatusNotFound, "target not found in this tenant")
	}
	if _, err := h.sqlDB().ExecContext(c.Context(), h.sqlQ(`DELETE FROM coremail_backup_target_secrets WHERE target_id = ?`), id); err != nil {
		h.logger.Warn("backup target: secret row cleanup failed", zap.Int64("target_id", id), zap.Error(err))
	}
	if err := h.appendAudit(c, "backup_target.delete", fmt.Sprintf("target:%d", id), "ok"); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// TestBackupTarget serves POST
// /api/v1/admin/backup-targets/:id/test. Performs a real
// TCP probe to host:port. The status (success, timeout,
// refused, dns-error, ssl-error) is stored on the row and
// returned. Passwords are NEVER echoed.
func (h *Handler) TestBackupTarget(c fiber.Ctx) error {
	if err := h.requireDB(c); err != nil {
		return err
	}
	tenantID := h.tenantID(c)
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid id")
	}
	var (
		host, kind string
		port       int
	)
	if err := h.sqlDB().QueryRowContext(c.Context(),
		h.sqlQ(`SELECT host, port, kind FROM coremail_backup_targets WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		id, tenantID).Scan(&host, &port, &kind); err != nil {
		if err == sql.ErrNoRows {
			return fiber.NewError(fiber.StatusNotFound, "target not found in this tenant")
		}
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("lookup: %v", err))
	}
	if host == "" || port == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "target has no host/port")
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	result := probeTCP(address, 3*time.Second)
	// For SFTP we expect SSH banner; for FTP a 220 greeting.
	if result.Connected {
		conn, err := net.DialTimeout("tcp", address, 3*time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 256)
			n, _ := conn.Read(buf)
			banner := strings.TrimSpace(string(buf[:n]))
			conn.Close()
			result.Banner = banner
			if kind == "ftp" && !strings.HasPrefix(banner, "220") {
				result.Message = "tcp ok but banner is not a 220 greeting: " + banner
				result.Status = "unexpected_banner"
			} else if kind == "sftp" && !strings.HasPrefix(banner, "SSH-") {
				result.Message = "tcp ok but banner is not an SSH greeting: " + banner
				result.Status = "unexpected_banner"
			} else {
				result.Message = "tcp + banner ok (login not attempted; password not echoed)"
				result.Status = "ok"
			}
		}
	}
	now := time.Now().UTC()
	if _, err := h.sqlDB().ExecContext(c.Context(),
		h.sqlQ(`UPDATE coremail_backup_targets SET last_test_status = ?, last_test_at = ?, last_test_message = ? WHERE id = ? AND tenant_id = ?`),
		result.Status, now, result.Message, id, tenantID); err != nil {
		h.logger.Warn("test target: persist failed", zap.Error(err))
	}
	if err := h.appendAudit(c, "backup_target.test", fmt.Sprintf("target:%d:%s", id, result.Status), "ok"); err != nil {
		return err
	}
	return c.JSON(result)
}

type backupTargetPayload struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	PrivateKeyPath string `json:"private_key_path"`
	Path           string `json:"path"`
	Enabled        bool   `json:"enabled"`
	VerifyHostname bool   `json:"verify_hostname"`
	Note           string `json:"note"`
	ClearSecret    bool   `json:"clear_secret"`
}

var allowedBackupKinds = map[string]bool{"ftp": true, "sftp": true}

func validateBackupTarget(p backupTargetPayload) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if !allowedBackupKinds[strings.ToLower(p.Kind)] {
		return fmt.Errorf("kind %q is not supported (allowed: ftp, sftp)", p.Kind)
	}
	if strings.TrimSpace(p.Host) == "" {
		return errors.New("host is required")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return errors.New("port must be in [1,65535]")
	}
	if strings.TrimSpace(p.Path) == "" {
		p.Path = "/"
	}
	if strings.Contains(p.Path, "..") {
		return errors.New("path may not contain ..")
	}
	if p.PrivateKeyPath != "" && p.Kind != "sftp" {
		return errors.New("private_key_path is only valid for sftp targets")
	}
	return nil
}

func storeBackupTargetSecret(c fiber.Ctx, h *Handler, targetID int64, password, privKey string) error {
	enc, err := configEncryptString(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	d := h.sqlDialect()
	_, err = h.sqlDB().ExecContext(c.Context(),
		`INSERT INTO coremail_backup_target_secrets (target_id, password_enc, private_key_path, updated_at) VALUES (`+d.Placeholders(4)+`)
		 ON CONFLICT (target_id) DO UPDATE SET password_enc=`+d.Excluded("password_enc")+`, private_key_path=`+d.Excluded("private_key_path")+`, updated_at=`+d.Excluded("updated_at"),
		targetID, enc, privKey, now)
	return err
}

// =====================================================================
// File System Access — safe read-only browser.
// =====================================================================
//
// H-8: the previous implementation confined paths with a bare
// strings.HasPrefix against a cleaned root and no separator boundary, so
// "/var/lib/orvix-secrets/..." satisfied the "/var/lib/orvix" root. It also
// never resolved symlinks (os.Stat/os.Open follow them), so a link planted
// inside an approved root redirected reads to any file the service account
// could open, and its secret-shape regex missed the AES master key
// (encryption_key), the SQLite database (orvix.db and its -wal/-shm
// sidecars), the initial admin credentials (admin-login.txt), .env files and
// backup archives.
//
// The contract is now:
//   - The caller names a root by OPAQUE ID; host paths never appear in a
//     request or a response.
//   - The caller supplies a RELATIVE path. Absolute paths, drive-qualified
//     paths and any ".." are rejected outright (internal/fsguard).
//   - Confinement is re-verified AFTER symlink evaluation against an exact
//     root or root+separator prefix.
//   - Devices, sockets and FIFOs are refused; listings and reads are bounded.
//   - Secret-shaped files are refused by a canonical basename/extension
//     policy, never returned.
//   - Every allowed and denied access is audited with the root ID and the
//     relative path only.

// fsRootSpecs maps the opaque root ID a caller may request to its host
// directory. Callers never send or receive the host path.
var fsRootSpecs = map[string]string{
	"orvix-logs":    "/var/log/orvix",
	"orvix-backups": "/var/backups/orvix",
	"orvix-data":    "/var/lib/orvix",
	"orvix-tls":     "/etc/orvix/tls",
	"system-logs":   "/var/log",
}

var (
	fsGuardOnce sync.Once
	fsGuardInst *fsguard.Guard
	fsGuardErr  error
)

// fsGuard lazily resolves the approved roots once per process.
func fsGuard() (*fsguard.Guard, error) {
	fsGuardOnce.Do(func() {
		fsGuardInst, fsGuardErr = fsguard.New(fsRootSpecs)
	})
	return fsGuardInst, fsGuardErr
}

// Bounds. A listing or read must never be unbounded: an operator endpoint is
// not a bulk-export channel and an enormous directory would pin memory.
const (
	fsMaxListEntries = 1000
	fsMaxReadBytes   = 64 * 1024
)

// fsSecretBasenames are exact filenames whose contents must never be served.
var fsSecretBasenames = map[string]struct{}{
	"encryption_key":       {},
	"encryption.key":       {},
	"admin-login.txt":      {},
	"bootstrap.env":        {},
	"external-backup.env":  {},
	"orvix.yaml":           {},
	"orvix.yml":            {},
	"credentials":          {},
	"authorized_keys":      {},
	"known_hosts":          {},
	"shadow":               {},
	"passwd":               {},
	"jwt.pem":              {},
	"jwt_key.pem":          {},
	"privkey.pem":          {},
	"id_rsa":               {},
	"id_ed25519":           {},
	"id_ecdsa":             {},
	"id_dsa":               {},
	".env":                 {},
	".netrc":               {},
	".pgpass":              {},
	"dkim.key":             {},
	"backup_target_secret": {},
}

// fsSecretExtensions are suffixes that indicate key material, databases, or
// archives. Matching is on the normalised (lowercased) basename so
// "Orvix.DB-WAL" is caught too.
var fsSecretSuffixes = []string{
	// Key / certificate material.
	".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".asc", ".gpg", ".kdbx",
	// Databases and their sidecars/backups.
	".db", ".db-wal", ".db-shm", ".db-journal", ".sqlite", ".sqlite3", ".sql", ".dump",
	// Archives / backups (may contain any of the above).
	".enc", ".bak", ".backup", ".tar", ".tgz", ".tar.gz", ".zip", ".gz", ".age",
	// Environment / secret files.
	".env",
}

// fsSecretPrefixes catch families like id_rsa.pub, id_rsa_old, .env.production.
var fsSecretPrefixes = []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", ".env", "vapid_private"}

// fsSecretSubstrings catch names that embed a secret marker anywhere.
var fsSecretSubstrings = []string{"private", "secret", "password", "passwd", "credential", "token", "apikey", "api_key"}

// isSecretName reports whether a basename must never have its contents
// served. It deliberately errs toward refusal: a false positive costs an
// operator one denied read, a false negative leaks key material.
func isSecretName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	if _, ok := fsSecretBasenames[lower]; ok {
		return true
	}
	for _, suffix := range fsSecretSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, prefix := range fsSecretPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, needle := range fsSecretSubstrings {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// fsAccessError maps a guard error to a stable, non-leaking response. The
// message never contains a host path.
func fsAccessError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, fsguard.ErrNoSuchRoot):
		return fiber.NewError(fiber.StatusForbidden, "root is not in the filesystem allowlist")
	case errors.Is(err, fsguard.ErrPathEscapesRoot):
		return fiber.NewError(fiber.StatusForbidden, "path is outside the approved filesystem root")
	case errors.Is(err, fsguard.ErrUnsupportedFileType):
		return fiber.NewError(fiber.StatusBadRequest, "unsupported file type")
	case errors.Is(err, fsguard.ErrInvalidRoot):
		return fiber.NewError(fiber.StatusServiceUnavailable, "filesystem access is not available on this host")
	case os.IsNotExist(err):
		return fiber.NewError(fiber.StatusNotFound, "not found")
	default:
		return fiber.NewError(fiber.StatusBadRequest, "filesystem access denied")
	}
}

// AdminFsBrowse serves GET /api/v1/admin/fs/browse?root=<id>&path=<relative>
// and lists directory contents in a safe, structured form. Only relative
// paths are returned; host paths are never disclosed.
func (h *Handler) AdminFsBrowse(c fiber.Ctx) error {
	if h.cfg == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "config not ready")
	}
	guard, err := fsGuard()
	if err != nil {
		return fsAccessError(c, err)
	}
	rootID := c.Query("root", "orvix-logs")
	rel := c.Query("path", "")

	res, info, err := guard.StatConfined(rootID, rel)
	if err != nil {
		h.writeAuditLog(c, "admin.fs.browse.denied", fmt.Sprintf("root:%s|path:%s", rootID, rel))
		return fsAccessError(c, err)
	}
	if !info.IsDir() {
		return fiber.NewError(fiber.StatusBadRequest, "path is not a directory")
	}
	entries, err := os.ReadDir(res.Abs)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list directory")
	}

	type entry struct {
		Name       string `json:"name"`
		Path       string `json:"path"` // root-relative, never absolute
		IsDir      bool   `json:"is_dir"`
		Size       int64  `json:"size"`
		ModifiedAt string `json:"modified_at"`
		SecretFlag bool   `json:"secret_flag"`
	}
	out := make([]entry, 0, len(entries))
	truncated := false
	for _, e := range entries {
		if len(out) >= fsMaxListEntries {
			truncated = true
			break
		}
		fi, ierr := e.Info()
		if ierr != nil {
			continue
		}
		childRel := path.Join(res.Rel, e.Name())
		if res.Rel == "." {
			childRel = e.Name()
		}
		out = append(out, entry{
			Name:       e.Name(),
			Path:       childRel,
			IsDir:      e.IsDir(),
			Size:       fi.Size(),
			ModifiedAt: fi.ModTime().UTC().Format(time.RFC3339),
			SecretFlag: isSecretName(e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	h.writeAuditLog(c, "admin.fs.browse", fmt.Sprintf("root:%s|path:%s|entries:%d", rootID, res.Rel, len(out)))
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"root":           rootID,
		"path":           res.Rel,
		"approved_roots": guard.RootIDs(),
		"entries":        out,
		"truncated":      truncated,
		"max_entries":    fsMaxListEntries,
	})
}

// AdminFsRead serves GET /api/v1/admin/fs/read?root=<id>&path=<relative> and
// returns a bounded slice of a regular file. Secret-shaped files are refused.
func (h *Handler) AdminFsRead(c fiber.Ctx) error {
	if h.cfg == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "config not ready")
	}
	guard, err := fsGuard()
	if err != nil {
		return fsAccessError(c, err)
	}
	rootID := c.Query("root", "orvix-logs")
	rel := c.Query("path", "")
	if strings.TrimSpace(rel) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "path is required")
	}

	res, info, err := guard.StatConfined(rootID, rel)
	if err != nil {
		h.writeAuditLog(c, "admin.fs.read.denied", fmt.Sprintf("root:%s|path:%s", rootID, rel))
		return fsAccessError(c, err)
	}
	if info.IsDir() {
		return fiber.NewError(fiber.StatusBadRequest, "path is a directory; use browse instead")
	}
	if !info.Mode().IsRegular() {
		return fiber.NewError(fiber.StatusBadRequest, "unsupported file type")
	}

	// Refuse secret-shaped files by canonical name policy.
	if isSecretName(filepath.Base(res.Abs)) {
		h.writeAuditLog(c, "admin.fs.read.secret_refused", fmt.Sprintf("root:%s|path:%s", rootID, res.Rel))
		c.Set("Cache-Control", "no-store")
		return c.JSON(fiber.Map{
			"root":            rootID,
			"path":            res.Rel,
			"secret_redacted": true,
			"reason":          "file matches a secret-shape policy (key material, database, credential, or archive); contents are never returned",
		})
	}

	// Bound the read. A file larger than the cap is truncated, never streamed
	// in full.
	f, err := os.Open(res.Abs)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to open file")
	}
	defer f.Close()
	buf := make([]byte, fsMaxReadBytes+1)
	n, rerr := io.ReadFull(f, buf)
	if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to read file")
	}
	truncated := n > fsMaxReadBytes
	if truncated {
		n = fsMaxReadBytes
	}

	h.writeAuditLog(c, "admin.fs.read", fmt.Sprintf("root:%s|path:%s|bytes:%d", rootID, res.Rel, n))
	c.Set("Cache-Control", "no-store")
	return c.JSON(fiber.Map{
		"root":      rootID,
		"path":      res.Rel,
		"size":      info.Size(),
		"truncated": truncated,
		"max_bytes": fsMaxReadBytes,
		"content":   string(buf[:n]),
		"is_text":   isLikelyText(buf[:n]),
	})
}

// isLikelyText estimates whether a buffer looks like a
// text file. If it has NUL bytes in the first 4KB we
// assume binary and tell the UI to render as "binary".
func isLikelyText(buf []byte) bool {
	end := len(buf)
	if end > 4096 {
		end = 4096
	}
	for i := 0; i < end; i++ {
		if buf[i] == 0 {
			return false
		}
	}
	return true
}

// =====================================================================
// Clustering + IMAP / POP3 / WebMail proxy status (config-only).
//
// We deliberately do NOT claim consensus is active — this
// build is single-node. The handler returns the live
// config posture for each proxy slot so operators see
// the truthful state: "configured=off, runtime=absent".

// AdminClusteringStatus serves GET
// /api/v1/admin/cluster/status. Returns one entry per
// configured / unconfigured cluster node and per proxy
// slot.
func (h *Handler) AdminClusteringStatus(c fiber.Ctx) error {
	curNodes, maxNodes := 1, 1
	consensus := "absent"
	peerNodes := []map[string]any{}

	type proxySlot struct {
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		Configured   bool   `json:"configured"`
		RuntimeState string `json:"runtime_state"`
		Detail       string `json:"detail"`
	}
	slots := []proxySlot{
		{
			Name: "imap_proxy", Kind: "imap",
			Configured:   false,
			RuntimeState: "absent",
			Detail:       "single-node deployment; clients connect directly to local IMAP listener",
		},
		{
			Name: "pop3_proxy", Kind: "pop3",
			Configured:   false,
			RuntimeState: "absent",
			Detail:       "single-node deployment; clients connect directly to local POP3 listener",
		},
		{
			Name: "webmail_proxy", Kind: "webmail",
			Configured:   false,
			RuntimeState: "absent",
			Detail:       "single-node deployment; each node serves its own webmail host",
		},
		{
			Name: "jmap_proxy", Kind: "jmap",
			Configured:   false,
			RuntimeState: "absent",
			Detail:       "single-node deployment; clients connect directly to local JMAP listener",
		},
	}
	return c.JSON(fiber.Map{
		"deployment_mode": "single_node",
		"current_nodes":   curNodes,
		"max_nodes":       maxNodes,
		"consensus":       consensus,
		"peer_nodes":      peerNodes,
		"proxies":         slots,
		"honest_note":     "Orvix Enterprise currently runs as a single-node deployment. Clustering + proxy replication is not implemented in this build.",
	})
}

// =====================================================================
// Probe + helper utilities shared across multiple sections.
// =====================================================================

type probeResult struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	Address   string `json:"address"`
	Connected bool   `json:"connected"`
	Banner    string `json:"banner,omitempty"`
}

// probeTCP does a TCP connect test against address and
// times out after timeout. Status values:
//   - "ok"             — connected
//   - "timeout"        — Connect() timed out
//   - "refused"        — ECONNREFUSED
//   - "dns_error"      — failed to resolve host
//   - "network_error"  — anything else
func probeTCP(address string, timeout time.Duration) probeResult {
	out := probeResult{
		Status:  "network_error",
		Message: "probe did not run",
		Address: address,
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err == nil {
		conn.Close()
		out.Status = "ok"
		out.Connected = true
		out.Message = "tcp connect succeeded"
		return out
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "i/o timeout"):
		out.Status = "timeout"
		out.Message = "connection timed out"
	case strings.Contains(msg, "connection refused"):
		out.Status = "refused"
		out.Message = "connection refused"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "no address associated"):
		out.Status = "dns_error"
		out.Message = "could not resolve host"
	default:
		out.Status = "network_error"
		out.Message = msg
	}
	return out
}

// tlsDial wraps crypto/tls.Dial so the migration-source test
// path can verify TLS handshakes. Returns a *tls.Conn the
// caller is responsible for closing. The wrapper exists so
// it can be substituted in tests.
func tlsDial(network, address, serverName string) (*tls.Conn, error) {
	return tls.Dial(network, address, &tls.Config{ServerName: serverName})
}

// =====================================================================
// Utility helpers — keep the rest of the file readable.
// =====================================================================

// boolToInt converts a Go bool to a driver-safe boolean value.
// Historically this returned 1/0 for SQLite; it now returns bool so the
// same parameter works for both SQLite INTEGER and PostgreSQL BOOLEAN.
func boolToInt(b bool) bool {
	return b
}

// Mailbox class_id is wired into the existing CreateMailbox
// handler in handlers.go (see the new fields there). The
// lookup helper is here so we don't have to duplicate the
// SQL.
func (h *Handler) lookupAccountClass(ctx context.Context, tenantID, classID int64) (map[string]any, error) {
	if classID <= 0 {
		return nil, nil
	}
	row := h.sqlDB().QueryRowContext(ctx, h.sqlQ(`
		SELECT id, name, default_quota_mb, max_quota_mb, max_send_per_hour, max_recv_per_hour,
		       allow_external_forwarding, allow_imap, allow_pop3, allow_jmap, allow_webmail, is_admin_class
		FROM coremail_account_classes
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`), classID, tenantID)
	var (
		id                             int64
		name                           string
		dq, mq, msh, mrh               int
		aef, aim, apo, ajm, awe, isAdm int
	)
	if err := row.Scan(&id, &name, &dq, &mq, &msh, &mrh, &aef, &aim, &apo, &ajm, &awe, &isAdm); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("account class %d not found in tenant %d", classID, tenantID)
		}
		return nil, err
	}
	return map[string]any{
		"id":                        id,
		"name":                      name,
		"default_quota_mb":          dq,
		"max_quota_mb":              mq,
		"max_send_per_hour":         msh,
		"max_recv_per_hour":         mrh,
		"allow_external_forwarding": aef == 1,
		"allow_imap":                aim == 1,
		"allow_pop3":                apo == 1,
		"allow_jmap":                ajm == 1,
		"allow_webmail":             awe == 1,
		"is_admin_class":            isAdm == 1,
	}, nil
}
