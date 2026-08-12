package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/webhooks"
	"github.com/orvix/orvix/internal/webhooks/ssrf"
	"go.uber.org/zap"
)

func newWebhookHandlerHarness(t *testing.T) (*fiber.App, *webhooks.Repository, *webhooks.Service, *sql.DB) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "handlers.db") + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	gdb, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err = models.MigrateAllRaw(gdb); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := webhooks.NewRepository(sqlDB)
	if err = repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := webhooks.NewService(repo, &ssrf.Allowlist{DevMode: true, AllowedHosts: map[string]bool{"receiver.test": true}})
	h := NewHandler(gdb, nil, nil, zap.NewNop(), cfg, modules.NewRegistry(zap.NewNop()), license.NewFeatureFlags(zap.NewNop()), nil)
	h.SetWebhookService(service)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenant_id", uint(1))
		c.Locals("user_id", uint(9))
		c.Locals("role", auth.RoleTenantAdmin)
		return c.Next()
	})
	app.Post("/subscriptions", h.CreateWebhookSubscription)
	app.Get("/subscriptions", h.ListWebhookSubscriptions)
	app.Get("/subscriptions/:id", h.GetWebhookSubscription)
	app.Patch("/subscriptions/:id", h.UpdateWebhookSubscription)
	app.Post("/subscriptions/:id/disable", h.DisableWebhookSubscription)
	app.Delete("/subscriptions/:id", h.DeleteWebhookSubscription)
	app.Get("/subscriptions/:id/history", h.GetWebhookDeliveryHistory)
	app.Get("/deliveries/:id", h.GetWebhookDelivery)
	app.Post("/deliveries/:id/replay", h.ReplayWebhookDelivery)
	app.Post("/subscriptions/:id/rotate", h.RotateWebhookSecret)
	app.Post("/subscriptions/:id/reactivate", h.ReactivateWebhookSubscription)
	return app, repo, service, sqlDB
}

func webhookHandlerRequest(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWebhookHandlersSecretRedactionAndTenantIsolation(t *testing.T) {
	app, _, svc, db := newWebhookHandlerHarness(t)
	create := webhookHandlerRequest(t, app, http.MethodPost, "/subscriptions", `{"url":"https://receiver.test/hook","events":["domain.created"]}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", create.StatusCode)
	}
	var created struct {
		Subscription webhooks.Subscription `json:"subscription"`
		Secret       string                `json:"secret"`
	}
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil || created.Secret == "" {
		t.Fatalf("creation did not return one-time secret: %v", err)
	}
	var encrypted string
	if err := db.QueryRow(`SELECT secret_encrypted FROM webhook_subscriptions WHERE id=?`, created.Subscription.ID).Scan(&encrypted); err != nil || encrypted == "" || encrypted == created.Secret {
		t.Fatalf("secret storage invalid: encrypted=%q err=%v", encrypted, err)
	}

	get := webhookHandlerRequest(t, app, http.MethodGet, fmt.Sprintf("/subscriptions/%d", created.Subscription.ID), "")
	assertWebhookSecretAbsent(t, get, created.Secret, encrypted)
	list := webhookHandlerRequest(t, app, http.MethodGet, "/subscriptions", "")
	assertWebhookSecretAbsent(t, list, created.Secret, encrypted)

	other, _, err := svc.CreateSubscriptionWithSecret(context.Background(), 2, webhooks.ScopeTenant, "https://receiver.test/other", []string{"domain.created"}, []byte("other-secret"))
	if err != nil {
		t.Fatal(err)
	}
	cross := webhookHandlerRequest(t, app, http.MethodGet, fmt.Sprintf("/subscriptions/%d", other.ID), "")
	if cross.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant status=%d", cross.StatusCode)
	}
}

func TestWebhookHandlersUpdateDisableRotateDeleteAndAudit(t *testing.T) {
	app, _, _, db := newWebhookHandlerHarness(t)
	create := webhookHandlerRequest(t, app, http.MethodPost, "/subscriptions", `{"url":"https://receiver.test/hook","events":["domain.created"]}`)
	var created struct {
		Subscription webhooks.Subscription `json:"subscription"`
		Secret       string                `json:"secret"`
	}
	if err := json.NewDecoder(create.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	updatePath := fmt.Sprintf("/subscriptions/%d", created.Subscription.ID)
	updateBody := fmt.Sprintf(`{"url":"https://receiver.test/new","events":["domain.updated"],"version":%d}`, created.Subscription.Version)
	update := webhookHandlerRequest(t, app, http.MethodPatch, updatePath, updateBody)
	if update.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.StatusCode, readWebhookBody(t, update))
	}
	stale := webhookHandlerRequest(t, app, http.MethodPatch, updatePath, updateBody)
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale update status=%d body=%s", stale.StatusCode, readWebhookBody(t, stale))
	}
	rotate := webhookHandlerRequest(t, app, http.MethodPost, fmt.Sprintf("/subscriptions/%d/rotate", created.Subscription.ID), "")
	if rotate.StatusCode != http.StatusOK || !strings.Contains(readWebhookBody(t, rotate), `"secret"`) {
		t.Fatalf("rotate status=%d", rotate.StatusCode)
	}
	for _, operation := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, fmt.Sprintf("/subscriptions/%d/disable", created.Subscription.ID), http.StatusOK},
		{http.MethodPost, fmt.Sprintf("/subscriptions/%d/reactivate", created.Subscription.ID), http.StatusOK},
		{http.MethodDelete, fmt.Sprintf("/subscriptions/%d", created.Subscription.ID), http.StatusNoContent},
	} {
		resp := webhookHandlerRequest(t, app, operation.method, operation.path, "")
		if resp.StatusCode != operation.want {
			t.Fatalf("%s %s status=%d", operation.method, operation.path, resp.StatusCode)
		}
	}
	if resp := webhookHandlerRequest(t, app, http.MethodGet, updatePath, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted subscription read status=%d", resp.StatusCode)
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_audit WHERE action LIKE 'webhook.%' AND actor='user:9' AND role=? AND tenant_id=1`, string(auth.RoleTenantAdmin)).Scan(&auditCount); err != nil || auditCount < 5 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
}

func TestWebhookHandlersManualReplayIsIdempotentImmutableAndTenantScoped(t *testing.T) {
	app, repo, svc, _ := newWebhookHandlerHarness(t)
	sub, _, err := svc.CreateSubscriptionWithSecret(context.Background(), 1, webhooks.ScopeTenant, "https://receiver.test/hook", []string{"domain.created"}, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	original := &webhooks.Delivery{EventID: "evt_manual", SubscriptionID: sub.ID, Status: "terminal"}
	if err = repo.InsertDelivery(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	first := webhookHandlerRequest(t, app, http.MethodPost, fmt.Sprintf("/deliveries/%d/replay", original.ID), "")
	second := webhookHandlerRequest(t, app, http.MethodPost, fmt.Sprintf("/deliveries/%d/replay", original.ID), "")
	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusCreated {
		t.Fatalf("replay status=%d/%d", first.StatusCode, second.StatusCode)
	}
	history, err := repo.DeliveryHistory(context.Background(), sub.ID, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[1].ID != original.ID || history[1].Status != "terminal" || history[0].ReplayOf == nil || *history[0].ReplayOf != original.ID {
		t.Fatalf("history mutated or replay link missing: %+v", history)
	}
	details := webhookHandlerRequest(t, app, http.MethodGet, "/deliveries/"+strconv.Itoa(int(history[0].ID)), "")
	if details.StatusCode != http.StatusOK || strings.Contains(readWebhookBody(t, details), "secret") {
		t.Fatal("unsafe delivery details response")
	}

	other, _, err := svc.CreateSubscriptionWithSecret(context.Background(), 2, webhooks.ScopeTenant, "https://receiver.test/other", []string{"domain.created"}, []byte("other"))
	if err != nil {
		t.Fatal(err)
	}
	otherDelivery := &webhooks.Delivery{EventID: "evt_other", SubscriptionID: other.ID, Status: "terminal"}
	if err = repo.InsertDelivery(context.Background(), otherDelivery); err != nil {
		t.Fatal(err)
	}
	if resp := webhookHandlerRequest(t, app, http.MethodPost, fmt.Sprintf("/deliveries/%d/replay", otherDelivery.ID), ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant replay status=%d", resp.StatusCode)
	}
}

func TestWebhookHandlersRejectInvalidConfigurationAndPagination(t *testing.T) {
	app, _, _, _ := newWebhookHandlerHarness(t)
	for _, body := range []string{
		`{"url":"http://receiver.test/hook","events":["domain.created"]}`,
		`{"url":"https://receiver.test/hook","events":["unknown.event"]}`,
	} {
		resp := webhookHandlerRequest(t, app, http.MethodPost, "/subscriptions", body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("invalid configuration status=%d body=%s", resp.StatusCode, readWebhookBody(t, resp))
		}
	}
	resp := webhookHandlerRequest(t, app, http.MethodGet, "/subscriptions/1/history?page_size=101", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid pagination status=%d", resp.StatusCode)
	}
	created := webhookHandlerRequest(t, app, http.MethodPost, "/subscriptions", `{"url":"https://receiver.test/hook","events":["domain.created"]}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("valid create status=%d", created.StatusCode)
	}
	resp = webhookHandlerRequest(t, app, http.MethodGet, "/subscriptions/1/history?status=made-up", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status filter status=%d", resp.StatusCode)
	}
}

func assertWebhookSecretAbsent(t *testing.T, response *http.Response, forbidden ...string) {
	t.Helper()
	body := readWebhookBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read status=%d body=%s", response.StatusCode, body)
	}
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Fatalf("secret leaked in response: %s", body)
		}
	}
	if strings.Contains(body, "secret_encrypted") {
		t.Fatalf("encrypted secret field leaked in response: %s", body)
	}
}

func readWebhookBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var value bytes.Buffer
	_, _ = value.ReadFrom(response.Body)
	return value.String()
}
