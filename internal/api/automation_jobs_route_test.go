package api

import (
	"net/http"
	"testing"
)

func TestAutomationJobRoutesEnforcePortalSeparationAndCSRF(t *testing.T) {
	h := newSepHarness(t)
	h.insertPSA(t, "jobs-psa@example.test", "PSAPass!2026")
	h.insertTA(t, "jobs-ta@example.test", "TenantPass!2026")
	psa := h.login(t, "jobs-psa@example.test", "PSAPass!2026")
	tenant := h.login(t, "jobs-ta@example.test", "TenantPass!2026")

	status, body := h.hit(t, http.MethodGet, "/api/v1/platform/automation/jobs", "")
	sepMustEq(t, "anonymous platform jobs", http.StatusUnauthorized, status, body)
	status, body = h.hit(t, http.MethodGet, "/api/v1/automation/jobs", "")
	sepMustEq(t, "anonymous tenant jobs", http.StatusUnauthorized, status, body)

	status, body = h.hit(t, http.MethodGet, "/api/v1/automation/jobs", psa)
	sepMustEq(t, "PSA tenant jobs", http.StatusForbidden, status, body)
	status, body = h.hit(t, http.MethodGet, "/api/v1/platform/automation/jobs", tenant)
	sepMustEq(t, "tenant platform jobs", http.StatusForbidden, status, body)

	status, body = h.hit(t, http.MethodGet, "/api/v1/automation/jobs", tenant)
	sepMustEq(t, "tenant job list", http.StatusOK, status, body)
	status, body = h.hit(t, http.MethodGet, "/api/v1/platform/automation/jobs", psa)
	sepMustEq(t, "platform job list", http.StatusOK, status, body)

	status, body = h.hit(t, http.MethodPost, "/api/v1/automation/jobs", tenant)
	sepMustEq(t, "tenant submit without CSRF", http.StatusForbidden, status, body)
	status, body = h.hit(t, http.MethodPost, "/api/v1/platform/automation/jobs", psa)
	sepMustEq(t, "platform submit without CSRF", http.StatusForbidden, status, body)
}
