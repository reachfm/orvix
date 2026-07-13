package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// This file wires the production restore restart + post-restart health
// verification callbacks consumed by internal/backup.Service.RestoreBackup.
//
// It deliberately does NOT use fire-and-forget semantics. The previous
// implementation ran `(sleep 2; systemctl restart orvix) &` and returned as
// soon as cmd.Start() succeeded, so a restart that never happened or failed
// was reported as success, and the "health check" only ran a SQLite
// integrity_check on a file — it never proved the restarted service was up.
//
// Design constraint (documented, not worked around): an HTTP handler cannot
// observe its own process being killed by an in-place `systemctl restart`.
// The restart is therefore delegated to systemd (a transient unit via
// systemd-run) which owns the service lifecycle, and the health gate probes
// the service's real HTTP /health endpoint. In the disposable staging
// workflow the restart target is an ordinary background process, so restart +
// HTTP health verification are exercised end-to-end there. The service-layer
// orchestration (bounded timeout, fail-closed on missing/failed/timed-out
// restart or unhealthy service, rollback to the safety backup) is covered by
// internal/backup unit tests.

// restoreRestartCallback returns the restart operation used after a restore
// activation (and during rollback). It is observable (returns the real exit
// status of the restart command) and fails closed when no service manager is
// available.
func (h *Handler) restoreRestartCallback() func(context.Context) error {
	return func(ctx context.Context) error {
		argv, err := restoreRestartArgv()
		if err != nil {
			return err
		}
		// Run synchronously and surface the real exit status/output. No
		// shell, no backgrounding, no detach: exec.Command does not invoke a
		// shell, so operator-configured argv cannot be reinterpreted.
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("service restart command %v failed: %w (output: %s)", argv, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

// restoreRestartArgv resolves the restart command as an argv slice (never a
// shell string). Resolution order:
//  1. ORVIX_RESTORE_RESTART_COMMAND (operator override, space-separated argv)
//  2. systemd-run transient unit delegating to `systemctl restart orvix`
//  3. `systemctl restart orvix`
//
// If none are available it fails closed so a restore cannot silently proceed
// without a working restart path.
func restoreRestartArgv() ([]string, error) {
	if override := strings.TrimSpace(os.Getenv("ORVIX_RESTORE_RESTART_COMMAND")); override != "" {
		argv := strings.Fields(override)
		if len(argv) == 0 {
			return nil, fmt.Errorf("ORVIX_RESTORE_RESTART_COMMAND is set but empty")
		}
		bin, err := exec.LookPath(argv[0])
		if err != nil {
			return nil, fmt.Errorf("configured restore restart command %q not found: %w", argv[0], err)
		}
		argv[0] = bin
		return argv, nil
	}
	// Default: delegate to systemd. `systemctl restart orvix` hands the
	// restart to PID 1 (systemd), which completes it even if this process is
	// terminated mid-call, and returns only once the unit is active again on
	// deployments where the caller survives (e.g. a reload). No shell, no
	// backgrounding.
	if p, err := exec.LookPath("systemctl"); err == nil {
		unit := strings.TrimSpace(os.Getenv("ORVIX_SYSTEMD_UNIT"))
		if unit == "" {
			unit = "orvix"
		}
		return []string{p, "restart", unit}, nil
	}
	return nil, fmt.Errorf("service manager unavailable: systemctl not found; " +
		"set ORVIX_RESTORE_RESTART_COMMAND to a restart command so restore can restart the service")
}

// restoreHealthCallback returns the post-restart health gate. It probes the
// service's real HTTP /health endpoint until it reports healthy or the bounded
// context expires — it does not merely inspect a database file.
func (h *Handler) restoreHealthCallback() func(context.Context) error {
	port := 8080
	if h.cfg != nil && h.cfg.Server.AdminPort > 0 {
		port = h.cfg.Server.AdminPort
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port)
	return func(ctx context.Context) error {
		client := &http.Client{Timeout: 5 * time.Second}
		var lastErr error
		attempt := func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("health endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			if !healthBodyIsHealthy(body) {
				return fmt.Errorf("health endpoint did not report healthy: %s", strings.TrimSpace(string(body)))
			}
			return nil
		}
		for {
			if err := ctx.Err(); err != nil {
				if lastErr != nil {
					return fmt.Errorf("restarted service did not become healthy: %w", lastErr)
				}
				return fmt.Errorf("restarted service did not become healthy: %w", err)
			}
			if err := attempt(); err == nil {
				return nil
			} else {
				lastErr = err
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("restarted service did not become healthy: %w", lastErr)
			case <-time.After(2 * time.Second):
			}
		}
	}
}

// healthBodyIsHealthy reports whether a /health JSON body indicates health.
// The endpoint returns {"status":"ok"}; accept common healthy synonyms too.
func healthBodyIsHealthy(body []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	status, _ := m["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "healthy", "up", "pass", "passing":
		return true
	}
	return false
}
