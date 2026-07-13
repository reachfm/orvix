package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
)

func TestHealthBodyIsHealthy(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"status":"ok"}`, true},
		{`{"status":"healthy"}`, true},
		{`{"status":"UP"}`, true},
		{`{"status":"error"}`, false},
		{`{"status":"degraded"}`, false},
		{`{"other":"ok"}`, false},
		{`not json`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := healthBodyIsHealthy([]byte(c.body)); got != c.want {
			t.Errorf("healthBodyIsHealthy(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}

// The restart command must fail closed when the configured override binary
// does not exist, and resolve to an argv (never a shell string) when it does.
func TestRestoreRestartArgv_FailsClosedAndResolves(t *testing.T) {
	t.Setenv("ORVIX_RESTORE_RESTART_COMMAND", "definitely-not-a-real-binary-xyz --now")
	if _, err := restoreRestartArgv(); err == nil {
		t.Fatal("expected fail-closed error for missing restart binary")
	}

	// A resolvable override (the test binary itself) yields a valid argv.
	t.Setenv("ORVIX_RESTORE_RESTART_COMMAND", os.Args[0]+" restart-arg")
	argv, err := restoreRestartArgv()
	if err != nil {
		t.Fatalf("unexpected error for resolvable override: %v", err)
	}
	if len(argv) != 2 || argv[1] != "restart-arg" {
		t.Fatalf("unexpected argv: %v", argv)
	}
}

func newHealthTestHandler(t *testing.T, serverURL string) *Handler {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port from %q: %v", serverURL, err)
	}
	cfg := &config.Config{}
	cfg.Server.AdminPort = port
	return &Handler{cfg: cfg}
}

// The post-restart health gate must accept a real HTTP healthy response and
// reject an unhealthy one — proving it verifies the running service, not a
// database file.
func TestRestoreHealthCallback_HTTPProbe(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer healthy.Close()

	h := newHealthTestHandler(t, healthy.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.restoreHealthCallback()(ctx); err != nil {
		t.Fatalf("healthy service should pass: %v", err)
	}

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"error"}`))
	}))
	defer unhealthy.Close()

	h2 := newHealthTestHandler(t, unhealthy.URL)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel2()
	if err := h2.restoreHealthCallback()(ctx2); err == nil {
		t.Fatal("unhealthy service must fail the health gate")
	}
}
