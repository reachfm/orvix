package handlers

// Enterprise admin v3 — SSL / TLS certificates.
//
// Endpoints:
//
//   - GET    /api/v1/admin/ssl/certificates          — list uploaded + runtime certs
//   - POST   /api/v1/admin/ssl/certificates          — upload a PEM cert + key
//   - DELETE /api/v1/admin/ssl/certificates/:id      — remove an uploaded certificate
//   - POST   /api/v1/admin/ssl/certificates/reload   — trigger reload
//   - GET    /api/v1/admin/ssl/expiry-warnings       — flat list of warning / expired
//   - GET    /api/v1/admin/ssl/acme/status           — honest ACME status
//
// All writes are CSRF-protected and audited. Private
// keys NEVER leave the backend — uploads persist to a
// per-name file under /etc/orvix/tls/admin/ with mode
// 0600; the listing endpoint returns only metadata +
// fingerprint.

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/tlsmgmt"
	"go.uber.org/zap"
)

// AdminSslListCertificates serves GET
// /api/v1/admin/ssl/certificates.
func (h *Handler) AdminSslListCertificates(c fiber.Ctx) error {
	type certInfo struct {
		ID                string   `json:"id"`
		Name              string   `json:"name"`
		Source            string   `json:"source"`
		Path              string   `json:"path"`
		KeyPath           string   `json:"key_path,omitempty"`
		CommonName        string   `json:"common_name"`
		SANs              []string `json:"sans,omitempty"`
		Issuer            string   `json:"issuer"`
		SerialNumber      string   `json:"serial_number"`
		NotBefore         string   `json:"not_before,omitempty"`
		NotAfter          string   `json:"not_after,omitempty"`
		DaysRemaining     int      `json:"days_remaining"`
		FingerprintSHA256 string   `json:"fingerprint_sha256"`
		Status            string   `json:"status"`
	}
	toInfo := func(ci tlsmgmt.TLSCertificate, source string) certInfo {
		var nb, na string
		if !ci.NotBefore.IsZero() {
			nb = ci.NotBefore.UTC().Format("2006-01-02T15:04:05Z")
		}
		if !ci.NotAfter.IsZero() {
			na = ci.NotAfter.UTC().Format("2006-01-02T15:04:05Z")
		}
		return certInfo{
			ID:                ci.ID,
			Name:              ci.Name,
			Source:            source,
			Path:              ci.Path,
			KeyPath:           ci.KeyPath,
			CommonName:        ci.CommonName,
			SANs:              ci.SANs,
			Issuer:            ci.Issuer,
			SerialNumber:      ci.SerialNumber,
			NotBefore:         nb,
			NotAfter:          na,
			DaysRemaining:     ci.DaysRemaining,
			FingerprintSHA256: ci.FingerprintSHA256,
			Status:            string(ci.Status),
		}
	}

	var runtimeList []certInfo
	if h.tlsService != nil {
		loaded, err := h.tlsService.LoadCertificates(c.Context())
		if err == nil {
			for _, ci := range loaded {
				runtimeList = append(runtimeList, toInfo(ci, "runtime"))
			}
		}
	}
	var uploadedList []certInfo
	if h.tlsService != nil {
		if err := h.tlsService.EnsureUploadedCertSchema(c.Context()); err != nil {
			h.logger.Warn("ensure uploaded cert schema", zap.Error(err))
		}
		uploaded, err := h.tlsService.ListUploadedCertificates(c.Context(), h.tenantID(c))
		if err == nil {
			for _, ci := range uploaded {
				uploadedList = append(uploadedList, toInfo(ci, "uploaded"))
			}
		}
	}

	expiryCutoff := 30
	if h.cfg != nil && h.cfg.Monitoring.CertExpiryWarningDays > 0 {
		expiryCutoff = h.cfg.Monitoring.CertExpiryWarningDays
	}
	warnings := []certInfo{}
	collect := func(ci certInfo) {
		if ci.Status == "expired" || ci.Status == "warning" {
			warnings = append(warnings, ci)
			return
		}
		if ci.NotAfter != "" && ci.DaysRemaining >= 0 && ci.DaysRemaining <= expiryCutoff {
			warnings = append(warnings, ci)
		}
	}
	for _, ci := range runtimeList {
		collect(ci)
	}
	for _, ci := range uploadedList {
		collect(ci)
	}
	if runtimeList == nil {
		runtimeList = []certInfo{}
	}
	if uploadedList == nil {
		uploadedList = []certInfo{}
	}
	if warnings == nil {
		warnings = []certInfo{}
	}
	return c.JSON(fiber.Map{
		"runtime":            runtimeList,
		"uploaded":           uploadedList,
		"expiry_warnings":    warnings,
		"expiry_cutoff_days": expiryCutoff,
		"config_path":        sslConfigStringField(h.cfg, "CoreMail", "TLSCertFile", "/etc/orvix/tls/smtp/fullchain.pem"),
		"config_key_path":    sslConfigStringField(h.cfg, "CoreMail", "TLSKeyFile", "/etc/orvix/tls/smtp/privkey.pem"),
	})
}

// AdminSslUploadCertificate serves POST /api/v1/admin/ssl/certificates.
func (h *Handler) AdminSslUploadCertificate(c fiber.Ctx) error {
	if h.tlsService == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "TLS service not wired")
	}
	var body struct {
		Name    string `json:"name"`
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid JSON body")
	}
	if strings.TrimSpace(body.Name) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if !sslNameValid(body.Name) {
		return fiber.NewError(fiber.StatusBadRequest, "name must be ≤128 chars, printable, no slashes")
	}
	if !strings.Contains(body.CertPEM, "BEGIN CERTIFICATE") {
		return fiber.NewError(fiber.StatusBadRequest, "cert_pem must contain a BEGIN CERTIFICATE block")
	}
	if !strings.Contains(body.KeyPEM, "PRIVATE KEY") {
		return fiber.NewError(fiber.StatusBadRequest, "key_pem must contain a PRIVATE KEY block")
	}
	if err := h.tlsService.EnsureUploadedCertSchema(c.Context()); err != nil {
		h.logger.Warn("ensure uploaded cert schema", zap.Error(err))
	}
	var createdBy int64
	if uid, ok := c.Locals("user_id").(uint); ok {
		createdBy = int64(uid)
	}
	cert, _, err := h.tlsService.ImportCertificate(c.Context(), body.Name, []byte(body.CertPEM), []byte(body.KeyPEM), "/etc/orvix/tls/admin", h.tenantID(c), createdBy)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := h.appendAudit(c, "ssl.certificate.upload", body.Name, "ok"); err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"name":               cert.Name,
		"common_name":        cert.CommonName,
		"issuer":             cert.Issuer,
		"not_after":          cert.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		"days_remaining":     cert.DaysRemaining,
		"status":             string(cert.Status),
		"fingerprint_sha256": cert.FingerprintSHA256,
		"path":               cert.Path,
		"key_path":           cert.KeyPath,
	})
}

// AdminSslDeleteCertificate serves DELETE /api/v1/admin/ssl/certificates/:id.
func (h *Handler) AdminSslDeleteCertificate(c fiber.Ctx) error {
	if h.tlsService == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "TLS service not wired")
	}
	id := strings.TrimSpace(c.Params("id"))
	if id == "" {
		return fiber.NewError(fiber.StatusBadRequest, "id is required")
	}
	tenantID := h.tenantID(c)
	row := h.sqlDB().QueryRowContext(c.Context(),
		h.sqlQ(`SELECT id, name, cert_path, key_path FROM coremail_uploaded_certificates WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`),
		id, tenantID)
	var (
		rowID int64
		name  string
		certP string
		keyP  string
	)
	if err := row.Scan(&rowID, &name, &certP, &keyP); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "certificate not found in this tenant")
	}
	runtimeCert := sslConfigStringField(h.cfg, "CoreMail", "TLSCertFile", "")
	if runtimeCert != "" && (certP == runtimeCert || keyP == runtimeCert) {
		return fiber.NewError(fiber.StatusConflict, "certificate is currently used by the runtime; change coremail.tls_cert_file first")
	}
	if _, err := h.sqlDB().ExecContext(c.Context(),
		`UPDATE coremail_uploaded_certificates SET deleted_at = `+h.dialect.Placeholder(1)+`, updated_at = `+h.dialect.Placeholder(2)+` WHERE id = `+h.dialect.Placeholder(3)+` AND tenant_id = `+h.dialect.Placeholder(4),
		time.Now().UTC(), time.Now().UTC(), rowID, tenantID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("soft delete: %v", err))
	}
	if certP != "" {
		_ = os.Remove(certP)
	}
	if keyP != "" {
		_ = os.Remove(keyP)
	}
	if err := h.appendAudit(c, "ssl.certificate.delete", fmt.Sprintf("id:%d|name:%s", rowID, name), "ok"); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": rowID, "deleted": true})
}

// AdminSslReloadCertificates serves POST /api/v1/admin/ssl/certificates/reload.
func (h *Handler) AdminSslReloadCertificates(c fiber.Ctx) error {
	if h.tlsService == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "TLS service not wired")
	}
	res := h.tlsService.ReloadCertificates(c.Context())
	if res == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "reload returned no result")
	}
	status := 200
	if !res.Success {
		status = fiber.StatusBadRequest
	}
	resultStr := "ok"
	if !res.Success {
		resultStr = "fail"
	}
	if err := h.appendAudit(c, "ssl.certificate.reload", res.Message, resultStr); err != nil {
		return err
	}
	return c.Status(status).JSON(fiber.Map{
		"success": res.Success,
		"message": res.Message,
	})
}

// AdminSslAcmeStatus serves GET /api/v1/admin/ssl/acme/status.
// The honest contract: Orvix Enterprise does NOT perform
// automated ACME / Let's Encrypt issuance in this build.
func (h *Handler) AdminSslAcmeStatus(c fiber.Ctx) error {
	candidates := []string{
		"/var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/",
		"/etc/letsencrypt/live/",
		"/var/lib/dehydrated/certs/",
	}
	detected := []string{}
	for _, base := range candidates {
		dir, err := os.Open(base)
		if err != nil {
			continue
		}
		entries, readErr := dir.Readdirnames(-1)
		dir.Close()
		if readErr != nil {
			continue
		}
		for _, e := range entries {
			if e == "" || strings.HasPrefix(e, ".") {
				continue
			}
			detected = append(detected, base+e)
		}
	}
	return c.JSON(fiber.Map{
		"acme_enabled":         false,
		"issuing_certificates": false,
		"acme_provider":        "none (manual import or external Caddy / dehydrated only)",
		"manual_paths": []string{
			"GET    /api/v1/admin/ssl/certificates",
			"POST   /api/v1/admin/ssl/certificates",
			"DELETE /api/v1/admin/ssl/certificates/:id",
			"POST   /api/v1/admin/ssl/certificates/reload",
		},
		"script_helper":      "release/scripts/setup-smtp-tls.sh",
		"on_disk_candidates": detected,
		"honest_notes": []string{
			"automated ACME / Let's Encrypt issuance is not implemented in this build",
			"to import a cert, POST its PEM + key to /api/v1/admin/ssl/certificates (or call setup-smtp-tls.sh)",
			"the active runtime listener still binds to coremail.tls_cert_file / coremail.tls_key_file",
		},
	})
}

// AdminSslExpiryWarnings serves GET /api/v1/admin/ssl/expiry-warnings.
func (h *Handler) AdminSslExpiryWarnings(c fiber.Ctx) error {
	if h.tlsService == nil {
		return c.JSON(fiber.Map{"warnings": []any{}})
	}
	all := []tlsmgmt.TLSCertificate{}
	loaded, lerr := h.tlsService.LoadCertificates(c.Context())
	if lerr == nil {
		all = append(all, loaded...)
	}
	uploaded, uerr := h.tlsService.ListUploadedCertificates(c.Context(), h.tenantID(c))
	if uerr == nil {
		all = append(all, uploaded...)
	}
	warn := []map[string]any{}
	for _, ci := range all {
		if string(ci.Status) == "warning" || string(ci.Status) == "expired" {
			warn = append(warn, map[string]any{
				"name":           ci.Name,
				"not_after":      ci.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
				"days_remaining": ci.DaysRemaining,
				"status":         string(ci.Status),
			})
		}
	}
	if warn == nil {
		warn = []map[string]any{}
	}
	return c.JSON(fiber.Map{"warnings": warn})
}

// sslConfigStringField is a tiny helper that reads a
// string field from a *config.Config by parent + name.
// Returns fallback when cfg is nil or the field is empty
// or not a string. Used only by the SSL handler so it
// never sees internal reflect.
func sslConfigStringField(cfg interface{}, parent, field, fallback string) string {
	if cfg == nil {
		return fallback
	}
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Struct {
		return fallback
	}
	root := v.FieldByName(parent)
	if !root.IsValid() || root.Kind() != reflect.Struct {
		return fallback
	}
	f := root.FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.String {
		return fallback
	}
	s := f.String()
	if s == "" {
		return fallback
	}
	return s
}

// sslNameValid returns true when s is a safe certificate
// name: ≤128 chars, no slashes / backslashes / NUL /
// control characters.
func sslNameValid(s string) bool {
	if len(s) > 128 || s == "" {
		return false
	}
	for _, r := range s {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
