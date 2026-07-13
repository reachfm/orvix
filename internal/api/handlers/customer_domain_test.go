package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// TestCustomerDomainEndpointsReturn503WhenServiceUnwired verifies that
// when the customer domain service is not wired (e.g. verification-table
// initialization failed at router construction) the endpoints return a
// deterministic 503 rather than dereferencing a nil service and panicking
// into a 500. The routes are registered unconditionally in setupRoutes,
// so the nil-service path is reachable in production.
func TestCustomerDomainEndpointsReturn503WhenServiceUnwired(t *testing.T) {
	h := &Handler{logger: zap.NewNop()} // customerDomainSvc is nil
	app := fiber.New()
	app.Get("/customer/domains", h.ListCustomerDomains)
	app.Get("/customer/domains/:domain_id", h.GetCustomerDomain)
	app.Get("/customer/domains/:domain_id/dns", h.GetCustomerDomainDNS)
	app.Post("/customer/domains/:domain_id/verify", h.VerifyCustomerDomain)

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/customer/domains"},
		{"GET", "/customer/domains/1"},
		{"GET", "/customer/domains/1/dns"},
		{"POST", "/customer/domains/1/verify"},
	}

	for _, tc := range cases {
		resp, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil))
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode != fiber.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
