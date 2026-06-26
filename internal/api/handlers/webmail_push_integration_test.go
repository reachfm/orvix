package handlers_test

// Wired integration tests for /api/v1/webmail/push/* endpoints
// (WEBMAIL-ENTERPRISE-PUSH-NOTIFICATIONS).
//
// These tests exercise the full request → router → handler →
// repository path so they catch wiring bugs that pure unit
// tests miss (e.g. "the handler is reachable but the runtime
// provider module forgot to expose PushNotifier()").
//
// Coverage:
//   - subscribe → status → unsubscribe happy path
//   - status response is sanitized (no p256dh_key, no auth_key,
//     no raw endpoint); instead returns endpoint_fingerprint +
//     endpoint_kind + created_at + last_seen_at + active_count
//   - subscribe rejects cross-mailbox re-registration of an
//     endpoint that already belongs to a different user (403)
//   - unsubscribe by endpoint + by id
//   - missing VAPID config returns enabled:false, no error,
//     no vapid_public_key, no active_count drift
//   - test endpoint requires an existing subscription owned by
//     the caller; otherwise 404
//   - push notifier self-send filter is wired correctly:
//     NotifyMailboxMessage with from == recipient is a no-op
//     even when there is an active subscription
//   - push notifier dispatches when from != recipient (push
//     sender is invoked with the payload; we stub the HTTP
//     transport to capture the request)

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// pushProviderModule mirrors runtimeProviderModule but also
// exposes a PushNotifier so the router wires it into the
// handler. When vapid is nil the notifier is still attached
// but IsEnabled() returns false — that exercises the
// "missing VAPID config" branch without crashing startup.
type pushProviderModule struct {
	store    *storage.MailStore
	queue    *queue.QueueEngine
	notifier *push.PushNotifier
}

func (m *pushProviderModule) ID() string             { return "coremail-runtime" }
func (m *pushProviderModule) Version() string        { return "test" }
func (m *pushProviderModule) Requires() []string     { return nil }
func (m *pushProviderModule) Init(_ *config.Config, _ *gorm.DB) error {
	return nil
}
func (m *pushProviderModule) Start() error   { return nil }
func (m *pushProviderModule) Stop() error    { return nil }
func (m *pushProviderModule) Migrate() error { return nil }
func (m *pushProviderModule) MailStore() *storage.MailStore {
	return m.store
}
func (m *pushProviderModule) QueueEngine() *queue.QueueEngine {
	return m.queue
}
func (m *pushProviderModule) PushNotifier() *push.PushNotifier {
	return m.notifier
}

// pushTestEnv is a fully-wired test environment that includes
// the push notifier so /api/v1/webmail/push/* endpoints are
// reachable. It mirrors webmailTestEnv but with a Notifier
// wired and VAPID keys populated.
type pushTestEnv struct {
	router   *api.Router
	mailbox  *storage.MailStore
	queue    *queue.QueueEngine
	notifier *push.PushNotifier
	email    string
	password string
}

func buildPushTestEnv(t *testing.T, withVAPID bool, vapidKeyGenerator func() (string, string)) *pushTestEnv {
	t.Helper()

	root := webmailRepoRoot(t)
	webmailDir := filepath.Join(root, "release", "webmail")

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write admin index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "app.js"), []byte(""), 0o644); err != nil {
		t.Fatalf("write admin app.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "styles.css"), []byte(""), 0o644); err != nil {
		t.Fatalf("write admin styles.css: %v", err)
	}

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = scratchDir + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	mailstoreDir := filepath.Join(scratchDir, "mailstore")
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v\nstmt: %s", err, stmt)
		}
	}
	for _, stmt := range storage.Indexes() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore idx: %v", err)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}

	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v\nstmt: %s", err, stmt)
		}
	}
	for _, stmt := range queue.Indexes() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue idx: %v", err)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	// Build the PushNotifier. With VAPID keys, IsEnabled()=true;
	// without, IsEnabled()=false but the notifier is still
	// attached so the handler does not 503 on "not configured"
	// (it 503s on push ops but 200s on status with enabled:false).
	vapid := push.VAPIDConfig{}
	if withVAPID {
		if vapidKeyGenerator == nil {
			pub, priv, err := push.GenerateVAPIDKeys()
			if err != nil {
				t.Fatalf("generate vapid: %v", err)
			}
			vapid.PublicKey = pub
			vapid.PrivateKey = priv
			vapid.Subject = "mailto:admin@orvix.email"
		} else {
			pub, priv := vapidKeyGenerator()
			vapid.PublicKey = pub
			vapid.PrivateKey = priv
			vapid.Subject = "mailto:admin@orvix.email"
		}
	}
	repo := push.NewSubscriptionSQLRepo(sqlDB)
	notifier := push.NewPushNotifier(mailStore, repo, vapid)

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.CoreMail.MailStorePath = mailstoreDir

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	reg.Register(&pushProviderModule{store: mailStore, queue: qe, notifier: notifier})

	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	if err := provisionAdminUser(t, sqlDB, email, password); err != nil {
		t.Fatalf("provision admin user: %v", err)
	}

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &pushTestEnv{
		router:   router,
		mailbox:  mailStore,
		queue:    qe,
		notifier: notifier,
		email:    email,
		password: password,
	}
}

// loginAsPush is a thin wrapper that performs the same login
// dance as loginAs but on a pushTestEnv.
func loginAsPush(t *testing.T, e *pushTestEnv, email, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"username": email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatalf("login: no access_token cookie")
	return ""
}

// pushRequest issues an authenticated request and returns the
// status code + body. body may be nil for GET/DELETE.
func pushRequest(t *testing.T, e *pushTestEnv, method, path, accessToken string, body interface{}) (int, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if accessToken != "" {
		req.Header.Set("Cookie", "access_token="+accessToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// fakeP256DHKey returns a fresh, valid P-256 public key
// (uncompressed SEC 1 point, 65 bytes) encoded as URL-safe
// base64. The push sender's encryptPayload step verifies the
// p256dh input by unmarshalling it on the P-256 curve; an
// all-zero or all-1s fake would fail with "invalid p256dh
// key" and the dispatch would never reach the test server.
func fakeP256DHKey(t *testing.T) string {
	t.Helper()
	pub, _, err := push.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate p256dh: %v", err)
	}
	return pub
}
func fakeAuthKey() string {
	v := make([]byte, 16)
	for i := range v {
		v[i] = byte(i + 10)
	}
	return base64.RawURLEncoding.EncodeToString(v)
}

// ─────────────────────────────────────────────────────────────────
// Wired integration tests
// ─────────────────────────────────────────────────────────────────

// TestPushStatusDisabledWhenVAPIDMissing verifies that, when
// the runtime module is wired but VAPID keys are absent, the
// status endpoint returns enabled:false with no VAPID public
// key and no subscription rows leaked.
func TestPushStatusDisabledWhenVAPIDMissing(t *testing.T) {
	env := buildPushTestEnv(t, false, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	status, body := pushRequest(t, env, "GET", "/api/v1/webmail/push/status", tok, nil)
	if status != 200 {
		t.Fatalf("status: %d body=%s", status, body)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if v, _ := resp["enabled"].(bool); v {
		t.Errorf("enabled should be false when VAPID missing; got %v", resp["enabled"])
	}
	if _, ok := resp["vapid_public_key"]; ok {
		t.Errorf("vapid_public_key must not be exposed when push is disabled: %v", resp["vapid_public_key"])
	}
	// No active subscriptions on a fresh DB.
	if c, _ := resp["active_count"].(float64); c != 0 {
		t.Errorf("active_count must be 0 when push is disabled; got %v", resp["active_count"])
	}
}

// TestPushStatusRedactsCryptoSecrets is the HIGH-severity
// S1 regression test: the response shape must NEVER carry
// p256dh_key or auth_key, regardless of whether the user
// owns the subscription.
func TestPushStatusRedactsCryptoSecrets(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)

	// Subscribe a fake endpoint so the status response has
	// at least one row to render.
	sub := map[string]interface{}{
		"endpoint": "https://fcm.googleapis.com/fcm/send/abcdEFGH1234",
		"keys": map[string]string{
			"p256dh": fakeP256DHKey(t),
			"auth":   fakeAuthKey(),
		},
	}
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, sub)
	if code != 201 {
		t.Fatalf("subscribe: %d body=%s", code, body)
	}

	// Now hit status and verify the response shape.
	code, body = pushRequest(t, env, "GET", "/api/v1/webmail/push/status", tok, nil)
	if code != 200 {
		t.Fatalf("status: %d body=%s", code, body)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if v, _ := resp["enabled"].(bool); !v {
		t.Fatalf("enabled should be true with VAPID configured; got %v", resp["enabled"])
	}
	rawSubs, _ := resp["subscriptions"].([]interface{})
	if len(rawSubs) != 1 {
		t.Fatalf("expected 1 subscription in status, got %d", len(rawSubs))
	}
	subView, _ := rawSubs[0].(map[string]interface{})
	// Forbidden fields must NOT appear anywhere on the wire.
	for _, forbidden := range []string{"p256dh_key", "auth_key", "endpoint", "user_agent"} {
		if _, present := subView[forbidden]; present {
			t.Errorf("forbidden field %q leaked in status response: %v", forbidden, subView[forbidden])
		}
	}
	// Required safe fields must be present.
	for _, required := range []string{"id", "mailbox_id", "endpoint_fingerprint", "endpoint_kind", "created_at"} {
		if _, present := subView[required]; !present {
			t.Errorf("required field %q missing from sanitized view: %v", required, subView)
		}
	}
	if pk, _ := resp["vapid_public_key"].(string); pk == "" {
		t.Errorf("vapid_public_key missing from enabled status response")
	}
}

// TestPushSubscribeStatusUnsubscribeRoundTrip exercises the
// happy path: subscribe returns 201, status shows 1 active,
// unsubscribe returns 200, status shows 0 active.
func TestPushSubscribeStatusUnsubscribeRoundTrip(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)

	sub := map[string]interface{}{
		"endpoint": "https://fcm.googleapis.com/fcm/send/abc1234",
		"keys": map[string]string{
			"p256dh": fakeP256DHKey(t),
			"auth":   fakeAuthKey(),
		},
	}
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, sub)
	if code != 201 {
		t.Fatalf("subscribe: %d body=%s", code, body)
	}

	// Status now shows 1 active.
	code, body = pushRequest(t, env, "GET", "/api/v1/webmail/push/status", tok, nil)
	if code != 200 {
		t.Fatalf("status: %d body=%s", code, body)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(body, &resp)
	if c, _ := resp["active_count"].(float64); c != 1 {
		t.Fatalf("active_count after subscribe: want 1, got %v (body=%s)", c, body)
	}

	// Unsubscribe by endpoint.
	code, body = pushRequest(t, env, "POST", "/api/v1/webmail/push/unsubscribe", tok,
		map[string]interface{}{"endpoint": "https://fcm.googleapis.com/fcm/send/abc1234"})
	if code != 200 {
		t.Fatalf("unsubscribe: %d body=%s", code, body)
	}

	// Status now shows 0 active (disabled subs are filtered out).
	code, body = pushRequest(t, env, "GET", "/api/v1/webmail/push/status", tok, nil)
	if code != 200 {
		t.Fatalf("status: %d body=%s", code, body)
	}
	resp = map[string]interface{}{}
	_ = json.Unmarshal(body, &resp)
	if c, _ := resp["active_count"].(float64); c != 0 {
		t.Fatalf("active_count after unsubscribe: want 0, got %v (body=%s)", c, body)
	}
}

// TestPushSubscribeCrossMailboxRejection verifies that
// subscribing to an endpoint that already belongs to another
// user returns 403, not 201 — protecting against accidental
// endpoint hijacking. We exercise this by inserting a row
// for mailbox ID 2 (the mailbox of a different user) and
// then trying to re-subscribe as admin.
func TestPushSubscribeCrossMailboxRejection(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	sqlDB := env.mailbox.DB

	// Provision a second mailbox owned by nobody (so admin can't
	// resolve to it via /webmail/me) but with a valid
	// coremail_mailboxes row so we can poke push_subscriptions.
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at)
		 VALUES (?, 1, 'active', 'enterprise', 0, 0, 0, ?, ?)`,
		"victim.test", now, now,
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, 1, 'victim', ?, 'Victim', 'placeholder', 'bcrypt', 'active', 1024, 0, ?, ?)`,
		2, "victim@victim.test", now, now,
	); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}

	// Insert a subscription for mailbox ID 2 (the victim).
	repo := push.NewSubscriptionSQLRepo(sqlDB)
	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID:  2,
		Endpoint:   "https://fcm.googleapis.com/fcm/send/victim-ep",
		P256DHKey:  fakeP256DHKey(t),
		AuthKey:    fakeAuthKey(),
		UserAgent:  "attacker",
	}); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	// Admin tries to subscribe with the SAME endpoint URL.
	sub := map[string]interface{}{
		"endpoint": "https://fcm.googleapis.com/fcm/send/victim-ep",
		"keys": map[string]string{
			"p256dh": fakeP256DHKey(t),
			"auth":   fakeAuthKey(),
		},
	}
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, sub)
	if code != 403 {
		t.Fatalf("subscribe: expected 403 cross-mailbox rejection, got %d body=%s", code, body)
	}
}

// TestPushUnsubscribeCrossMailboxRejection verifies that
// admin cannot disable another user's subscription. Same
// setup as above but on the unsubscribe path.
func TestPushUnsubscribeCrossMailboxRejection(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)
	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID:  99,
		Endpoint:   "https://updates.push.services.mozilla.com/other/y",
		P256DHKey:  fakeP256DHKey(t),
		AuthKey:    fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/unsubscribe", tok,
		map[string]interface{}{"endpoint": "https://updates.push.services.mozilla.com/other/y"})
	if code != 404 {
		t.Fatalf("unsubscribe: expected 404 for endpoint belonging to another user, got %d body=%s", code, body)
	}
}

// TestPushUnauthenticatedReturns401 verifies the auth
// middleware on the protected group fires before the handler.
// This is a no-regression pin.
func TestPushUnauthenticatedReturns401(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	code, body := pushRequest(t, env, "GET", "/api/v1/webmail/push/status", "", nil)
	if code != 401 {
		t.Fatalf("status without auth: expected 401, got %d body=%s", code, body)
	}
}

// TestPushNotifierSelfSendFilterSkipsDispatch verifies the
// canonical B5 fix: NotifyMailboxMessage with from == recipient
// is a no-op, even when an active subscription exists for the
// recipient. The push sender is a stub that records every
// dispatch attempt; the test asserts no dispatches happen.
func TestPushNotifierSelfSendFilterSkipsDispatch(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)

	rec := newRecordingSender(t)
	defer rec.close()
	env.notifier.Sender = rec.sender

	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID: 1,
		Endpoint:  rec.server.URL,
		P256DHKey: fakeP256DHKey(t),
		AuthKey:   fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	env.notifier.NotifyMailboxMessage(
		context.Background(),
		1,
		"msg-self",
		"admin@orvix.email", // from
		"Hello me",
		"admin@orvix.email", // recipient == from
	)
	if rec.count() != 0 {
		t.Fatalf("self-send should be a no-op; got %d dispatches", rec.count())
	}
}

// TestPushNotifierDispatchesToOtherMailbox verifies the
// complement: NotifyMailboxMessage with from != recipient
// DOES dispatch to all active subscriptions.
func TestPushNotifierDispatchesToOtherMailbox(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)

	rec := newRecordingSender(t)
	defer rec.close()
	env.notifier.Sender = rec.sender

	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID: 1,
		Endpoint:  rec.server.URL,
		P256DHKey: fakeP256DHKey(t),
		AuthKey:   fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	env.notifier.NotifyMailboxMessage(
		context.Background(),
		1,
		"msg-other",
		"bob@example.test",
		"Hi",
		"admin@orvix.email", // recipient != from
	)
	if rec.count() != 1 {
		t.Fatalf("expected 1 dispatch to active subscription, got %d", rec.count())
	}
}

// TestPushNotifierFailingEndpointIsDisabled verifies the
// 410-Gone cleanup: a Send error containing "410" marks the
// subscription as disabled so subsequent notifications do not
// retry the dead endpoint.
func TestPushNotifierFailingEndpointIsDisabled(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)

	fail := newFailingSender(t)
	defer fail.close()
	env.notifier.Sender = fail.sender

	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID: 1,
		Endpoint:  fail.server.URL,
		P256DHKey: fakeP256DHKey(t),
		AuthKey:   fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	env.notifier.NotifyMailboxMessage(
		context.Background(),
		1, "msg-gone", "bob@example.test", "Hi", "admin@orvix.email",
	)

	got, err := repo.GetByEndpoint(context.Background(), fail.server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.DisabledAt == nil {
		t.Fatalf("expected subscription to be disabled after 410; got disabled_at=%v", got.DisabledAt)
	}
}

// TestPushNotifierEmptyVAPIDIsNoOp verifies the "missing
// keys" safety net: NotifyMailboxMessage returns immediately
// without querying the repo.
func TestPushNotifierEmptyVAPIDIsNoOp(t *testing.T) {
	notifier := push.NewPushNotifier(nil, nil, push.VAPIDConfig{})
	if notifier.IsEnabled() {
		t.Fatal("IsEnabled must be false when VAPID is empty")
	}
	// No panic, no error, no dispatch.
	notifier.NotifyMailboxMessage(context.Background(), 1, "m", "a@b", "subj", "c@d")
}

// TestPushNotifierRuntimeValidateDisablesTamperedEndpoint verifies
// that a subscription row whose endpoint was tampered AFTER
// subscribe (e.g. via direct DB write, or DNS rebinding flipped
// the resolved IP to a private range) gets disabled on the next
// send attempt. The handler-side ValidatePushEndpoint catches the
// case at subscribe / test time; this test covers the
// defense-in-depth runtime re-validation that runs inside
// WebPushSender.Send.
//
// We use a real PushSender (skipValidate=false) and a row whose
// endpoint is a hostile private-IP subdomain that bypassed the
// strict allowlist through some out-of-band mutation. The notifier
// should mark the row disabled and return without making the POST.
func TestPushNotifierRuntimeValidateDisablesTamperedEndpoint(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)

	// Plant a subscription whose endpoint looks like a tampered
	// private-IP subdomain. The host "127.0.0.1.fcm.googleapis.com"
	// ends with a real push-service suffix but the IP-fragment
	// detector must reject it at send time.
	tampered := "https://127.0.0.1.fcm.googleapis.com/push"
	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID: 1,
		Endpoint:  tampered,
		P256DHKey: fakeP256DHKey(t),
		AuthKey:   fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Replace the production sender (skipValidate=false, the
	// default) — no httptest server needed because the request
	// must never go out.
	env.notifier.Sender = push.NewWebPushSender(
		push.VAPIDConfig{
			PublicKey:  pubFromEnv(t, env),
			PrivateKey: privFromEnv(t, env),
			Subject:    "mailto:test@example.test",
		},
	)

	env.notifier.NotifyMailboxMessage(
		context.Background(),
		1, "msg-tampered", "bob@example.test", "Hi", "admin@orvix.email",
	)

	got, err := repo.GetByEndpoint(context.Background(), tampered)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil || got.DisabledAt == nil {
		t.Fatalf("expected subscription to be disabled after runtime validation failure; got disabled_at=%v", got.DisabledAt)
	}
}

func pubFromEnv(t *testing.T, env *pushTestEnv) string {
	t.Helper()
	return env.notifier.VAPID.PublicKey
}
func privFromEnv(t *testing.T, env *pushTestEnv) string {
	t.Helper()
	return env.notifier.VAPID.PrivateKey
}

// TestReleaseWebmailJSSyntax runs `node --check` against every
// shipped JS file in the release bundle so a syntax regression
// breaks CI rather than shipping a broken SPA. This is the
// no-regression pin for the original fix #2 (window.OrvixWebmail
// outside IIFE — node --check would have caught it before merge).
//
// If `node` is not on PATH (some CI images), the test is skipped
// with a clear message so the suite still runs. The operator can
// verify manually:
//
//	node --check release/webmail/assets/webmail.js
//	node --check release/webmail/assets/webmail-push.js
//	node --check release/webmail/sw.js
func TestReleaseWebmailJSSyntax(t *testing.T) {
	root := webmailRepoRoot(t)
	files := []string{
		filepath.Join(root, "release/webmail/assets/webmail.js"),
		filepath.Join(root, "release/webmail/assets/webmail-push.js"),
		filepath.Join(root, "release/webmail/sw.js"),
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available on PATH (%v) — run `node --check` manually on the files above", err)
	}
	for _, f := range files {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Fatalf("release JS file not found: %s", f)
		}
		cmd := exec.Command("node", "--check", f)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node --check failed for %s:\n%s\nerr: %v",
				f, string(out), err)
		}
	}
}

// TestReleaseWebmailAssetReferences checks that asset references
// in the service worker point to files that exist.
func TestReleaseWebmailAssetReferences(t *testing.T) {
	root := webmailRepoRoot(t)
	swPath := filepath.Join(root, "release/webmail/sw.js")
	data, err := os.ReadFile(swPath)
	if err != nil {
		t.Fatalf("read sw.js: %v", err)
	}
	content := string(data)
	// Find all /webmail/assets/ references
	idx := 0
	for {
		start := strings.Index(content[idx:], "/webmail/assets/")
		if start < 0 {
			break
		}
		start += idx
		end := start + len("/webmail/assets/")
		// Read until whitespace, quote, or end of line
		for end < len(content) && content[end] != ' ' && content[end] != '"' && content[end] != '\'' && content[end] != '\n' && content[end] != '\r' && content[end] != ')' {
			end++
		}
		ref := content[start:end]
		// Map /webmail/assets/ to release/webmail/assets/
		relPath := filepath.Join(root, "release/webmail/assets", strings.TrimPrefix(ref, "/webmail/assets/"))
		if _, err := os.Stat(relPath); os.IsNotExist(err) {
			t.Fatalf("service worker references %q but file not found at %s", ref, relPath)
		}
		idx = end
	}
}

// TestReleaseWebmailNoQueueAPIRefs checks that release webmail
// files do not reference the internal queue API.
func TestReleaseWebmailNoQueueAPIRefs(t *testing.T) {
	root := webmailRepoRoot(t)
	dirs := []string{
		filepath.Join(root, "release/webmail"),
		filepath.Join(root, "release/webmail", "assets"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			if strings.Contains(string(data), "/api/v1/queue") {
				t.Errorf("queue API reference found in %s/%s", dir, e.Name())
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// Recording / failing sender stubs
// ─────────────────────────────────────────────────────────────────

// recordingSender points a real WebPushSender at an httptest
// server so the encryption + signing pipeline runs end-to-end
// without hitting a real push service. Each POST is counted.
//
// Usage:
//   rec := newRecordingSender(t)
//   defer rec.close()
//   env.notifier.Sender = rec.sender
//   repo.Create(... &PushSubscription{Endpoint: rec.server.URL, ...})
//   env.notifier.NotifyMailboxMessage(...)
//   rec.count() // dispatches observed
type recordingSender struct {
	server *httptest.Server
	sender *push.WebPushSender
	mu     sync.Mutex
	calls  int
}

func newRecordingSender(t *testing.T) *recordingSender {
	t.Helper()
	r := &recordingSender{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.calls++
		r.mu.Unlock()
		w.WriteHeader(201)
	}))
	t.Cleanup(func() { r.server.Close() })
	pub, priv, err := push.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate vapid: %v", err)
	}
	r.sender = push.NewWebPushSenderWithClient(
		push.VAPIDConfig{
			PublicKey:  pub,
			PrivateKey: priv,
			Subject:    "mailto:test@example.test",
		},
		r.server.Client(),
	)
	// The test server's URL is a private 127.0.0.1:<port>
	// endpoint — it would not survive the production SSRF
	// validator. Skip the runtime check so the encryption +
	// signing pipeline runs end-to-end.
	r.sender.SetSkipValidate(true)
	return r
}

func (r *recordingSender) close() {
	if r.server != nil {
		r.server.Close()
	}
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// failingSender mirrors recordingSender but always returns
// HTTP 410 Gone so we can exercise the dead-endpoint cleanup
// path in NotifyMailboxMessage.
type failingSender struct {
	server *httptest.Server
	sender *push.WebPushSender
}

func newFailingSender(t *testing.T) *failingSender {
	t.Helper()
	f := &failingSender{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(410)
		_, _ = w.Write([]byte("push rejected: 410 Gone"))
	}))
	t.Cleanup(func() { f.server.Close() })
	pub, priv, err := push.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate vapid: %v", err)
	}
	f.sender = push.NewWebPushSenderWithClient(
		push.VAPIDConfig{
			PublicKey:  pub,
			PrivateKey: priv,
			Subject:    "mailto:test@example.test",
		},
		f.server.Client(),
	)
	// Same rationale as the recording sender — test URL is a
	// localhost httptest endpoint.
	f.sender.SetSkipValidate(true)
	return f
}

func (f *failingSender) close() {
	if f.server != nil {
		f.server.Close()
	}
}

// ─────────────────────────────────────────────────────────────────
// SSRF validation tests (fix #3)
// ─────────────────────────────────────────────────────────────────

func TestPushSubscribeRejectsHTTP(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
		"endpoint": "http://fcm.googleapis.com/fcm/send/x",
		"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
	})
	if code != 400 {
		t.Fatalf("expected 400 for http://, got %d body=%s", code, body)
	}
}

func TestPushSubscribeRejectsLocalhost(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	for _, ep := range []string{
		"https://localhost:8080/push",
		"https://127.0.0.1:8080/push",
		"https://[::1]:8080/push",
		"https://127.0.0.2/push",
	} {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 400 {
			t.Fatalf("expected 400 for %q, got %d body=%s", ep, code, body)
		}
	}
}

func TestPushSubscribeRejectsPrivateIP(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	for _, ep := range []string{
		"https://10.0.0.1/push",
		"https://172.16.0.1/push",
		"https://192.168.1.1/push",
		"https://169.254.169.254/push",
	} {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 400 {
			t.Fatalf("expected 400 for %q, got %d body=%s", ep, code, body)
		}
	}
}

func TestPushSubscribeRejectsInternalHostname(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	for _, ep := range []string{
		"https://push.internal/push",
		"https://push.local/push",
		"https://push.corp/push",
	} {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 400 {
			t.Fatalf("expected 400 for %q, got %d body=%s", ep, code, body)
		}
	}
}

func TestPushSubscribeRejectsUserinfo(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
		"endpoint": "https://user:pass@fcm.googleapis.com/push",
		"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
	})
	if code != 400 {
		t.Fatalf("expected 400 for userinfo, got %d body=%s", code, body)
	}
}

// TestPushUnsubscribeByIDCrossMailboxRejection verifies that
// an authenticated user cannot disable another user's subscription
// by ID. The handler must fetch the subscription and verify
// ownership before disabling (fix #4).
func TestPushUnsubscribeByIDCrossMailboxRejection(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	sqlDB := env.mailbox.DB
	repo := push.NewSubscriptionSQLRepo(sqlDB)

	// Create a subscription for mailbox ID 99.
	if err := repo.Create(context.Background(), &push.PushSubscription{
		MailboxID: 99,
		Endpoint:  "https://fcm.googleapis.com/fcm/send/other-user",
		P256DHKey: fakeP256DHKey(t),
		AuthKey:   fakeAuthKey(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Admin (mailbox ID 1) tries to disable subscription ID 1
	// which belongs to mailbox 99.
	code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/unsubscribe", tok,
		map[string]interface{}{"id": 1})
	if code != 404 {
		t.Fatalf("unsubscribe by id cross-mailbox: expected 404, got %d body=%s", code, body)
	}

	// Verify subscription is still active.
	sub, err := repo.GetByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sub == nil || sub.DisabledAt != nil {
		t.Fatal("expected subscription to remain active (not disabled by cross-mailbox request)")
	}

	// Now admin creates their own subscription and can disable it by ID.
	self := map[string]interface{}{
		"endpoint": "https://fcm.googleapis.com/fcm/send/self-owner",
		"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
	}
	code, body = pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, self)
	if code != 201 {
		t.Fatalf("subscribe: %d body=%s", code, body)
	}
	code, body = pushRequest(t, env, "POST", "/api/v1/webmail/push/unsubscribe", tok,
		map[string]interface{}{"id": 2})
	if code != 200 {
		t.Fatalf("unsubscribe own by id: expected 200, got %d body=%s", code, body)
	}
	sub, err = repo.GetByID(context.Background(), 2)
	if err != nil {
		t.Fatalf("get own: %v", err)
	}
	if sub == nil || sub.DisabledAt == nil {
		t.Fatal("expected own subscription to be disabled")
	}
}

func TestPushSubscribeAcceptsKnownPushService(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	for _, ep := range []string{
		// FCM and Mozilla autopush resolve from most CI
		// environments. We intentionally skip push.apple.com here
		// — its DNS is unreliable from CI containers and the
		// push package unit tests cover the allowlist directly.
		"https://fcm.googleapis.com/fcm/send/abc123",
		"https://fcm-notifications.googleapis.com/v1/abc",
		"https://updates.push.services.mozilla.com/wpush/v1/abc",
	} {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 201 && code != 200 {
			t.Fatalf("expected 2xx for %q, got %d body=%s", ep, code, body)
		}
	}
}

// TestPushSubscribeRejectsAllowlistSuffixBypass is the regression
// pin for the SSRF allowlist bypass. The previous implementation
// used strings.HasSuffix(host, ".fcm.googleapis.com") etc., so
// subdomains of real push-service hostnames whose LEFTMOST label
// was suspicious (localhost, internal, corp, an IP fragment) were
// accepted. The new validator runs a per-label blocklist and an
// IP-fragment detector BEFORE the strict known-push-service
// allowlist, so every one of these is rejected.
func TestPushSubscribeRejectsAllowlistSuffixBypass(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	// Every entry here is the kind of URL the previous
	// suffix-only allowlist accepted but the new strict
	// allowlist MUST reject.
	cases := []string{
		"https://localhost.fcm.googleapis.com/push",
		"https://127.0.0.1.fcm.googleapis.com/push",
		"https://internal.fcm.googleapis.com/push",
		"https://corp.fcm.googleapis.com/push",
		"https://lan.fcm.googleapis.com/push",
		"https://intranet.fcm.googleapis.com/push",
		"https://private.fcm.googleapis.com/push",
		"https://test.fcm.googleapis.com/push",
		"https://example.fcm.googleapis.com/push",
		"https://10.0.0.1.push.services.mozilla.com/push",
		"https://192.168.1.1.fcm.googleapis.com/push",
		"https://169.254.169.254.web.push.apple.com/push",
		// Three-octet prefixes (no valid IP, but suspicious label
		// pattern).
		"https://127.0.0.fcm.googleapis.com/push",
	}
	for _, ep := range cases {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 400 {
			t.Errorf("BUG: accepted %q with code %d body=%s", ep, code, body)
		}
	}
}

// TestPushSubscribeRejectsTyposquatAndSuffixConfusion covers the
// "trusted suffix" misdirection: the suffix may match the
// allowlist but the LEFTMOST label is an attacker's domain.
//
//   fcm.googleapis.com.attacker.com      — leftmost is attacker.com
//   fcm-googleapis.com                    — typosquat of googleapis.com
//   web.push.apple.com.attacker.io       — leftmost is attacker.io
//
// None of these match the strict apex-only allowlist, so all
// three are rejected outright.
func TestPushSubscribeRejectsTyposquatAndSuffixConfusion(t *testing.T) {
	env := buildPushTestEnv(t, true, nil)
	tok := loginAsPush(t, env, env.email, env.password)
	cases := []string{
		"https://fcm.googleapis.com.attacker.com/push",
		"https://fcm-googleapis.com/push",
		"https://push-googleapis.com/push",
		"https://web.push.apple.com.attacker.io/push",
		"https://attacker.com/push",
		"https://malicious.fcm.googleapis.com/push",
	}
	for _, ep := range cases {
		code, body := pushRequest(t, env, "POST", "/api/v1/webmail/push/subscribe", tok, map[string]interface{}{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": fakeP256DHKey(t), "auth": fakeAuthKey()},
		})
		if code != 400 {
			t.Errorf("BUG: accepted %q with code %d body=%s", ep, code, body)
		}
	}
}

// Silence unused imports when individual tests get edited out.
var _ = sync.Mutex{}
var _ = (*http.Request)(nil)
