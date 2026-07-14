package monitoring

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// DeliveryStatus is the outcome of a single alert delivery attempt.
type DeliveryStatus string

const (
	DeliverySuccess DeliveryStatus = "success"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliverySkipped DeliveryStatus = "skipped"
)

// DeliveryProvider delivers an alert through one channel (in-app,
// webhook, email, …).
//
// Security contract: an implementation MUST NOT return an error whose
// text contains a secret (webhook URL, bearer token, Authorization
// header). The Dispatcher records the returned error verbatim into the
// monitoring_alert_deliveries.detail column and passes it to the
// logger, so any secret in the error string would leak. Return a
// sanitized, secret-free error instead.
type DeliveryProvider interface {
	// Name is a stable, safe identifier (e.g. "inapp", "webhook").
	Name() string
	// Enabled reports whether the provider is configured to deliver.
	// A disabled provider is skipped honestly (never delivered to,
	// recorded as "skipped").
	Enabled() bool
	// Deliver sends the alert. It MUST NOT panic; the Dispatcher also
	// guards against panics, but providers should fail via error.
	Deliver(ctx context.Context, a Alert) error
	// Status returns a redacted, secret-free description of the
	// provider's configuration for the admin status endpoint.
	Status() ProviderStatus
}

// ProviderStatus is the redacted, secret-free view of a delivery
// provider suitable for the admin API. It NEVER contains a webhook URL,
// token, or Authorization header — only whether they are configured.
type ProviderStatus struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Target    string `json:"target,omitempty"` // redacted (e.g. "REDACTED") — never the raw URL
	HasSecret bool   `json:"hasSecret"`        // true when a token/secret is configured
	Detail    string `json:"detail,omitempty"` // safe human-readable note
}

// DeliveryLogger receives safe, secret-free delivery diagnostics. A
// *log.Logger satisfies this interface, as does any adapter around a
// structured logger. The Dispatcher only ever passes it strings that
// have already been stripped of secrets.
type DeliveryLogger interface {
	Printf(format string, v ...any)
}

// DeliveryRecord is a single persisted delivery outcome.
type DeliveryRecord struct {
	ID            uint           `json:"id"`
	AlertTitle    string         `json:"alertTitle"`
	AlertSeverity Severity       `json:"alertSeverity"`
	AlertCategory Category       `json:"alertCategory"`
	Provider      string         `json:"provider"`
	Status        DeliveryStatus `json:"status"`
	Detail        string         `json:"detail"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// Dispatcher fans an alert out to every configured delivery provider
// and records the outcome. Delivery failures are persisted and logged
// but NEVER propagate as a crash: a broken webhook must not take the
// monitoring subsystem down.
type Dispatcher struct {
	db        *sql.DB
	providers []DeliveryProvider
	logger    DeliveryLogger
}

// NewDispatcher builds a Dispatcher. db may be nil (delivery records
// are then not persisted, but delivery still runs); logger may be nil.
func NewDispatcher(db *sql.DB, logger DeliveryLogger, providers ...DeliveryProvider) *Dispatcher {
	return &Dispatcher{db: db, providers: providers, logger: logger}
}

// Providers returns the redacted status of every configured provider.
// Honest reporting: a provider that is present but disabled is reported
// with Enabled=false; a provider that is not configured at all is
// simply absent from the list.
func (d *Dispatcher) Providers() []ProviderStatus {
	if d == nil {
		return nil
	}
	out := make([]ProviderStatus, 0, len(d.providers))
	for _, p := range d.providers {
		out = append(out, p.Status())
	}
	return out
}

// Dispatch delivers the alert through every provider, recording each
// outcome. It is best-effort and never returns an error to the caller;
// a failing provider is isolated so the others still run and monitoring
// keeps working.
func (d *Dispatcher) Dispatch(ctx context.Context, a Alert) {
	if d == nil {
		return
	}
	for _, p := range d.providers {
		d.dispatchOne(ctx, p, a)
	}
}

func (d *Dispatcher) dispatchOne(ctx context.Context, p DeliveryProvider, a Alert) {
	// A panic inside a provider must never crash monitoring.
	defer func() {
		if r := recover(); r != nil {
			d.record(ctx, p.Name(), a, DeliveryFailed, "provider panicked during delivery")
			d.logf("monitoring: delivery provider %q panicked (recovered)", p.Name())
		}
	}()

	if !p.Enabled() {
		d.record(ctx, p.Name(), a, DeliverySkipped, "provider disabled")
		return
	}

	if err := p.Deliver(ctx, a); err != nil {
		// The provider contract guarantees this error is secret-free.
		safe := err.Error()
		d.record(ctx, p.Name(), a, DeliveryFailed, safe)
		d.logf("monitoring: alert delivery via %q failed: %s", p.Name(), safe)
		return
	}
	d.record(ctx, p.Name(), a, DeliverySuccess, "delivered")
}

func (d *Dispatcher) logf(format string, v ...any) {
	if d.logger != nil {
		d.logger.Printf(format, v...)
	}
}

func (d *Dispatcher) record(ctx context.Context, provider string, a Alert, status DeliveryStatus, detail string) {
	if d.db == nil {
		return
	}
	_, _ = d.db.ExecContext(ctx,
		`INSERT INTO monitoring_alert_deliveries (alert_title, alert_severity, alert_category, provider, status, detail, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Title, string(a.Severity), string(a.Category), provider, string(status), detail, time.Now().UTC())
}

// ListDeliveries returns the most recent delivery records.
func (d *Dispatcher) ListDeliveries(ctx context.Context, limit int) ([]DeliveryRecord, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, alert_title, alert_severity, alert_category, provider, status, detail, created_at
		 FROM monitoring_alert_deliveries ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryRecord
	for rows.Next() {
		var r DeliveryRecord
		if err := rows.Scan(&r.ID, &r.AlertTitle, &r.AlertSeverity, &r.AlertCategory, &r.Provider, &r.Status, &r.Detail, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── In-app provider ─────────────────────────────────────

// InAppProvider is the always-on delivery channel backed by the
// monitoring_alerts table. The alert row itself is the in-app
// notification; this provider records that the in-app surface received
// the alert. It never talks to the network and cannot leak secrets.
type InAppProvider struct{}

// NewInAppProvider returns the in-app delivery provider.
func NewInAppProvider() *InAppProvider { return &InAppProvider{} }

func (p *InAppProvider) Name() string  { return "inapp" }
func (p *InAppProvider) Enabled() bool { return true }

func (p *InAppProvider) Deliver(ctx context.Context, a Alert) error {
	// The alert has already been persisted to monitoring_alerts by the
	// evaluation path; the in-app feed reads directly from that table.
	// Delivery therefore always succeeds.
	return nil
}

func (p *InAppProvider) Status() ProviderStatus {
	return ProviderStatus{
		Name:    "inapp",
		Enabled: true,
		Detail:  "alerts surfaced in the admin dashboard",
	}
}

// ── Webhook provider ────────────────────────────────────

// WebhookProvider POSTs a JSON alert payload to an operator-configured
// URL, optionally with a bearer token.
//
// Security contract:
//   - The URL and token are NEVER placed in an error string, a log
//     line, a delivery record, or the status endpoint.
//   - Transport errors from net/http typically embed the target URL
//     (e.g. `Post "https://…": dial tcp …`). Deliver deliberately
//     discards that text and returns a sanitized, secret-free error.
const (
	defaultWebhookTimeout      = 10 * time.Second
	defaultMaxBodySize         = 1 << 20 // 1 MB
	defaultMaxWebhookRedirects = 3
)

type WebhookProvider struct {
	url          string
	token        string
	enabled      bool
	timeout      time.Duration
	maxBodySize  int64
	maxRedirects int
	client       *http.Client
}

// WebhookConfig configures a WebhookProvider.
type WebhookConfig struct {
	Enabled bool
	URL     string
	Token   string
	Timeout time.Duration
}

type webhookDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// NewWebhookProvider builds a webhook provider from config.
// The URL is validated against SSRF attacks. If validation fails the
// provider is not created and an error is returned.
func NewWebhookProvider(cfg WebhookConfig) (*WebhookProvider, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: -1}
	return newWebhookProvider(cfg, systemWebhookResolver{resolver: net.DefaultResolver}, dialer, nil)
}

func newWebhookProvider(cfg WebhookConfig, resolver webhookResolver, dialer webhookDialer, tlsConfig *tls.Config) (*WebhookProvider, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultWebhookTimeout
	}
	if cfg.URL != "" {
		if err := validateWebhookURL(context.Background(), cfg.URL, resolver); err != nil {
			return nil, err
		}
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	transport := &http.Transport{
		// Resolve and validate immediately before every socket connection, then
		// dial the selected IP literal. This closes the validate-then-resolve
		// DNS-rebinding window and deliberately ignores proxy environment vars.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("invalid webhook destination")
			}
			addresses, err := resolveSafeWebhookIPs(ctx, resolver, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, resolved := range addresses {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
			return nil, fmt.Errorf("webhook connection failed: %w", lastErr)
		},
		DisableKeepAlives: true,
		TLSClientConfig:   tlsConfig,
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= defaultMaxWebhookRedirects {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Host == "" {
				return fmt.Errorf("redirect to URL with no host")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to non-HTTPS URL")
			}
			if err := validateWebhookURL(req.Context(), req.URL.String(), resolver); err != nil {
				return fmt.Errorf("unsafe webhook redirect")
			}
			return nil
		},
	}
	return &WebhookProvider{
		url:          cfg.URL,
		token:        cfg.Token,
		enabled:      cfg.Enabled,
		timeout:      timeout,
		maxBodySize:  defaultMaxBodySize,
		maxRedirects: defaultMaxWebhookRedirects,
		client:       client,
	}, nil
}

func (w *WebhookProvider) Name() string { return "webhook" }

// Enabled is true only when the operator both flipped the flag AND
// supplied a URL. A flag with no URL is honestly reported as disabled.
func (w *WebhookProvider) Enabled() bool { return w.enabled && w.url != "" }

// webhookPayload is the JSON body sent to the webhook. It contains only
// safe alert fields — no server-side secrets.
type webhookPayload struct {
	Title     string    `json:"title"`
	Severity  Severity  `json:"severity"`
	Category  Category  `json:"category"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"createdAt"`
}

func (w *WebhookProvider) Deliver(ctx context.Context, a Alert) error {
	body, err := json.Marshal(webhookPayload{
		Title:     a.Title,
		Severity:  a.Severity,
		Category:  a.Category,
		Message:   a.Message,
		Source:    a.Source,
		CreatedAt: a.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("webhook delivery failed: could not encode payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		// err from NewRequestWithContext can embed the URL — do not
		// surface it.
		return fmt.Errorf("webhook delivery failed: invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook delivery failed: transport error")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, w.maxBodySize))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook delivery failed: HTTP status %d", resp.StatusCode)
	}
	return nil
}

func (w *WebhookProvider) Status() ProviderStatus {
	st := ProviderStatus{
		Name:      "webhook",
		Enabled:   w.Enabled(),
		HasSecret: w.token != "",
	}
	if w.url != "" {
		// Never expose the raw URL; the operator knows what they set.
		st.Target = "REDACTED"
	}
	switch {
	case w.enabled && w.url == "":
		st.Detail = "enabled but no URL configured; delivery disabled"
	case !w.enabled && w.url != "":
		st.Detail = "URL configured but provider disabled"
	case w.Enabled():
		st.Detail = "webhook delivery active"
	default:
		st.Detail = "not configured"
	}
	return st
}
