package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/configtruth"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/incident"
	"github.com/orvix/orvix/internal/platform/importer"
	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/supportaccess"
)

var outputJSON bool

type platformCLIDeps struct {
	openDB     func() (*sql.DB, *dbdialect.Info, func() error, error)
	stagingDir func() string
	now        func() time.Time
	stdout     io.Writer
	stderr     io.Writer
}

func defaultPlatformCLIDeps() platformCLIDeps {
	return platformCLIDeps{
		openDB:     openProductionDB,
		stagingDir: defaultStagingDir,
		now:        func() time.Time { return time.Now().UTC() },
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	}
}

const (
	exitSuccess        = 0
	exitInternal       = 1
	exitBadArgs        = 2
	exitNotFound       = 3
	exitForbidden      = 4
	exitConfirmRefused = 5
	exitUnavailable    = 6
)

// defaultStagingDir returns the confined directory used to stage import
// source files. It prefers the configured ORVIX_DATA_DIR and falls back to
// the platform temp directory so the CLI works without a site config.
func defaultStagingDir() string {
	if dir := os.Getenv("ORVIX_IMPORT_STAGING_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("ORVIX_DATA_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "orvix-imports")
}

func platformCommand(args []string) int {
	return runPlatform(args, defaultPlatformCLIDeps())
}

func runPlatform(args []string, deps platformCLIDeps) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, usageText())
		return exitBadArgs
	}
	resource := args[0]
	rest := args[1:]

	fs := flag.NewFlagSet("platform "+resource, flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	fs.BoolVar(&outputJSON, "json", false, "output as JSON")

	switch resource {
	case "orgs":
		return runOrgs(rest, deps, fs)
	case "jobs":
		return runJobs(rest, deps, fs)
	case "incidents":
		return runIncidents(rest, deps, fs)
	case "support":
		return runSupport(rest, deps, fs)
	case "capabilities":
		fs.Parse(rest)
		return runCapabilities(deps)
	case "config":
		return runConfig(rest, deps, fs)
	case "apikeys":
		return runAPIKeys(rest, deps, fs)
	case "imports":
		return runImports(rest, deps, fs)
	case "-h", "--help", "help":
		fmt.Fprintln(deps.stdout, usageText())
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown resource:", resource)
		return exitBadArgs
	}
}

func usageText() string {
	return `orvix platform <resource> <action> [flags]

  orgs        list | get --id <id> | suspend --id <id> --reason <r> --confirm SUSPEND-<id> | reactivate --id <id> --reason <r> --confirm REACTIVATE-<id>
  jobs        list [--status <s>] | get --id <id> | cancel --id <id> | retry --id <id>
  incidents   list [--status <s>] | get --id <id> | create --title <t> [--severity <s>] | update --id <id> --status <s> [--message <m>] | resolve --id <id> [--message <m>]
  support     list [--tenant-id <id>] | get --id <id> | revoke --id <id> --reason <r> --confirm REVOKE-<id>
  capabilities  (read-only JSON summary)
  config      list | get --key <k>
  apikeys     list | create --name <n> --user-id <u> --tenant-id <t> [--role <r>] [--scopes <s>] [--ttl-days <d>] --confirm CREATE-KEY | revoke --id <i> --user-id <u> --reason <r> --confirm REVOKE-<i>
  imports     list | get --id <id> | validate --id <id> | execute --id <id> --confirm EXECUTE-IMPORT-<id> | cancel --id <id> | resume --id <id> | compensate --id <id> --confirm COMPENSATE-IMPORT-<id>

Global: --json (JSON output)`
}

// ── Organizations ────────────────────────────────────────────────

func runOrgs(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get | suspend | reactivate")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	id := fs.Int64("id", 0, "organization ID")
	reason := fs.String("reason", "", "reason")
	confirm := fs.String("confirm", "", "confirmation token")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()

	switch action {
	case "list":
		return renderOrgsList(ctx, db, deps)
	case "get":
		if *id <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		return renderOrg(ctx, db, *id, deps)
	case "suspend":
		if *id <= 0 || *reason == "" {
			fmt.Fprintln(deps.stderr, "--id and --reason are required")
			return exitBadArgs
		}
		want := fmt.Sprintf("SUSPEND-%d", *id)
		if *confirm != want {
			fmt.Fprintln(deps.stderr, "confirmation refused")
			return exitConfirmRefused
		}
		return doSuspend(ctx, db, *id, *reason, deps)
	case "reactivate":
		if *id <= 0 || *reason == "" {
			fmt.Fprintln(deps.stderr, "--id and --reason are required")
			return exitBadArgs
		}
		want := fmt.Sprintf("REACTIVATE-%d", *id)
		if *confirm != want {
			fmt.Fprintln(deps.stderr, "confirmation refused")
			return exitConfirmRefused
		}
		return doReactivate(ctx, db, *id, *reason, deps)
	default:
		fmt.Fprintln(deps.stderr, "unknown orgs action:", action)
		return exitBadArgs
	}
}

func renderOrgsList(ctx context.Context, db *sql.DB, deps platformCLIDeps) int {
	rows, err := db.QueryContext(ctx, "SELECT id, name, domain, plan, active FROM tenants WHERE deleted_at IS NULL ORDER BY id ASC")
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer rows.Close()
	type o struct {
		ID     uint   `json:"id"`
		Name   string `json:"name"`
		Domain string `json:"domain"`
		Plan   string `json:"plan"`
		Active bool   `json:"active"`
	}
	var orgs []o
	for rows.Next() {
		var r o
		var a int
		if rows.Scan(&r.ID, &r.Name, &r.Domain, &r.Plan, &a); err != nil {
			continue
		}
		r.Active = a == 1
		orgs = append(orgs, r)
	}
	if outputJSON {
		json.NewEncoder(deps.stdout).Encode(orgs)
		return exitSuccess
	}
	fmt.Fprintf(deps.stdout, "%-4s %-20s %-30s %-10s %s\n", "ID", "NAME", "DOMAIN", "PLAN", "ACTIVE")
	for _, r := range orgs {
		fmt.Fprintf(deps.stdout, "%-4d %-20s %-30s %-10s %v\n", r.ID, r.Name, r.Domain, r.Plan, r.Active)
	}
	return exitSuccess
}

func renderOrg(ctx context.Context, db *sql.DB, id int64, deps platformCLIDeps) int {
	var name, domain, plan string
	var active int
	err := db.QueryRowContext(ctx, "SELECT name, domain, plan, active FROM tenants WHERE id=? AND deleted_at IS NULL", id).Scan(&name, &domain, &plan, &active)
	if err == sql.ErrNoRows {
		fmt.Fprintln(deps.stderr, "organization not found")
		return exitNotFound
	}
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	if outputJSON {
		json.NewEncoder(deps.stdout).Encode(map[string]any{"id": id, "name": name, "domain": domain, "plan": plan, "active": active == 1})
	} else {
		fmt.Fprintf(deps.stdout, "ID: %d\nName: %s\nDomain: %s\nPlan: %s\nActive: %v\n", id, name, domain, plan, active == 1)
	}
	return exitSuccess
}

func doSuspend(ctx context.Context, db *sql.DB, id int64, reason string, deps platformCLIDeps) int {
	res, err := db.ExecContext(ctx, "UPDATE tenants SET active=0, updated_at=CURRENT_TIMESTAMP WHERE id=? AND active=1 AND deleted_at IS NULL", id)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fmt.Fprintln(deps.stderr, "organization not found or already suspended")
		return exitNotFound
	}
	if outputJSON {
		fmt.Fprintf(deps.stdout, "{\"id\":%d,\"active\":false,\"reason\":%q}\n", id, reason)
	} else {
		fmt.Fprintf(deps.stdout, "Organization %d suspended (reason: %s)\n", id, reason)
	}
	return exitSuccess
}

func doReactivate(ctx context.Context, db *sql.DB, id int64, reason string, deps platformCLIDeps) int {
	res, err := db.ExecContext(ctx, "UPDATE tenants SET active=1, updated_at=CURRENT_TIMESTAMP WHERE id=? AND active=0 AND deleted_at IS NULL", id)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fmt.Fprintln(deps.stderr, "organization not found or already active")
		return exitNotFound
	}
	if outputJSON {
		fmt.Fprintf(deps.stdout, "{\"id\":%d,\"active\":true,\"reason\":%q}\n", id, reason)
	} else {
		fmt.Fprintf(deps.stdout, "Organization %d reactivated (reason: %s)\n", id, reason)
	}
	return exitSuccess
}

// ── Jobs ──────────────────────────────────────────────────────────

func runJobs(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get | cancel | retry")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	jid := fs.Int64("id", 0, "job ID")
	jstatus := fs.String("status", "", "filter by status")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()

	switch action {
	case "list":
		filter := jobs.ListFilter{Page: kernel.PageRequest{PageSize: 50}}
		if *jstatus != "" {
			filter.Status = jobs.Status(*jstatus)
		}
		svc := jobs.NewService(jobs.NewJobRepository(db))
		page, err := svc.List(ctx, filter)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(page.Items)
			return exitSuccess
		}
		fmt.Fprintf(deps.stdout, "%-4s %-12s %-12s %-8s\n", "ID", "TYPE", "STATUS", "PROGRESS")
		for _, j := range page.Items {
			fmt.Fprintf(deps.stdout, "%-4d %-12s %-12s %-8d\n", j.ID, j.Type, j.Status, j.Progress)
		}
		return exitSuccess
	case "get":
		if *jid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		svc := jobs.NewService(jobs.NewJobRepository(db))
		j, err := svc.Get(ctx, uint(*jid), 0, jobs.ScopePlatform)
		if err != nil {
			fmt.Fprintf(deps.stderr, "job not found: %v\n", err)
			return exitNotFound
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(j)
		} else {
			fmt.Fprintf(deps.stdout, "ID: %d\nType: %s\nStatus: %s\nProgress: %d%%\nTenant: %d\nCreated: %s\n",
				j.ID, j.Type, j.Status, j.Progress, j.TenantID, j.CreatedAt.Format(time.RFC3339))
		}
		return exitSuccess
	case "cancel":
		if *jid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		res, err := db.ExecContext(ctx, "UPDATE platform_jobs SET status='cancelled', cancelled_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('queued','running','failed')", *jid)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fmt.Fprintln(deps.stderr, "job not found or not cancellable")
			return exitNotFound
		}
		printOK(deps, fmt.Sprintf("Job %d cancelled", *jid), map[string]any{"id": *jid, "status": "cancelled"})
		return exitSuccess
	case "retry":
		if *jid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		res, err := db.ExecContext(ctx, "UPDATE platform_jobs SET status='queued', updated_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('failed','cancelled')", *jid)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fmt.Fprintln(deps.stderr, "job not found or not retryable")
			return exitNotFound
		}
		printOK(deps, fmt.Sprintf("Job %d re-queued", *jid), map[string]any{"id": *jid, "status": "retried"})
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown jobs action:", action)
		return exitBadArgs
	}
}

// ── Incidents ─────────────────────────────────────────────────────

func runIncidents(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get | create | update | resolve")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	iid := fs.Int64("id", 0, "incident ID")
	ititle := fs.String("title", "", "title (required for create)")
	isev := fs.String("severity", "minor", "severity")
	istatus := fs.String("status", "", "new status")
	imsg := fs.String("message", "", "update/resolution message")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()
	repo := incident.NewRepository(db)
	svc := incident.NewService(repo)
	svc.EnsureSchema(ctx)

	switch action {
	case "list":
		list, err := svc.List(ctx, *istatus, 50)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(list)
		} else {
			fmt.Fprintf(deps.stdout, "%-4s %-12s %-14s %s\n", "ID", "SEVERITY", "STATUS", "TITLE")
			for _, inc := range list {
				fmt.Fprintf(deps.stdout, "%-4d %-12s %-14s %s\n", inc.ID, inc.Severity, inc.Status, inc.Title)
			}
		}
		return exitSuccess
	case "get":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		inc, err := svc.Get(ctx, uint(*iid))
		if err != nil {
			fmt.Fprintln(deps.stderr, "incident not found")
			return exitNotFound
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(inc)
		} else {
			fmt.Fprintf(deps.stdout, "ID: %d\nTitle: %s\nSeverity: %s\nStatus: %s\nCreated: %s\n",
				inc.ID, inc.Title, inc.Severity, inc.Status, inc.CreatedAt.Format(time.RFC3339))
			tl, _ := svc.Timeline(ctx, inc.ID)
			for _, e := range tl {
				fmt.Fprintf(deps.stdout, "  [%s] %s: %s\n", e.CreatedAt.Format(time.RFC3339), e.Operator, e.Message)
			}
		}
		return exitSuccess
	case "create":
		if *ititle == "" {
			fmt.Fprintln(deps.stderr, "--title is required")
			return exitBadArgs
		}
		inc, err := svc.Create(ctx, *ititle, "", incident.Severity(*isev), nil, nil, nil)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		printOK(deps, fmt.Sprintf("Incident %d created", inc.ID), inc)
		return exitSuccess
	case "update":
		if *iid <= 0 || *istatus == "" {
			fmt.Fprintln(deps.stderr, "--id and --status are required")
			return exitBadArgs
		}
		_, err := svc.Update(ctx, uint(*iid), incident.Status(*istatus), *imsg, "cli")
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitForbidden
		}
		printOK(deps, fmt.Sprintf("Incident %d updated to %s", *iid, *istatus), nil)
		return exitSuccess
	case "resolve":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		_, err := svc.Update(ctx, uint(*iid), incident.StatusResolved, *imsg, "cli")
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitForbidden
		}
		printOK(deps, fmt.Sprintf("Incident %d resolved", *iid), nil)
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown incidents action:", action)
		return exitBadArgs
	}
}

// ── Support access ────────────────────────────────────────────────

func runSupport(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get | revoke")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	sid := fs.Int64("id", 0, "grant ID")
	stid := fs.Int64("tenant-id", 0, "filter by tenant ID")
	sreason := fs.String("reason", "", "revocation reason (required)")
	sconfirm := fs.String("confirm", "", "confirmation REVOKE-<id>")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()
	repo := supportaccess.NewRepository(db)
	svc := supportaccess.NewService(repo)
	svc.EnsureSchema(ctx)

	switch action {
	case "list":
		list, err := svc.List(ctx, uint(*stid), 50)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(list)
		} else {
			fmt.Fprintf(deps.stdout, "%-4s %-12s %-12s %-12s %s\n", "ID", "STATUS", "SCOPE", "TENANT", "TICKET")
			for _, g := range list {
				fmt.Fprintf(deps.stdout, "%-4d %-12s %-12s %-12d %s\n", g.ID, g.Status, g.PermissionScope, g.TargetTenantID, g.TicketRef)
			}
		}
		return exitSuccess
	case "get":
		if *sid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		g, err := svc.Get(ctx, uint(*sid))
		if err != nil {
			fmt.Fprintln(deps.stderr, "grant not found")
			return exitNotFound
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(g)
		} else {
			fmt.Fprintf(deps.stdout, "ID: %d\nStatus: %s\nScope: %s\nTenant: %d\nTicket: %s\nReason: %s\nExpires: %s\n",
				g.ID, g.Status, g.PermissionScope, g.TargetTenantID, g.TicketRef, g.Reason, g.ExpiresAt.Format(time.RFC3339))
		}
		return exitSuccess
	case "revoke":
		if *sid <= 0 || *sreason == "" {
			fmt.Fprintln(deps.stderr, "--id and --reason are required")
			return exitBadArgs
		}
		if *sconfirm != fmt.Sprintf("REVOKE-%d", *sid) {
			fmt.Fprintln(deps.stderr, "confirmation refused")
			return exitConfirmRefused
		}
		_, err := svc.RevokeGrant(ctx, uint(*sid), *sreason)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitForbidden
		}
		printOK(deps, fmt.Sprintf("Grant %d revoked", *sid), nil)
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown support action:", action)
		return exitBadArgs
	}
}

// ── Capabilities ──────────────────────────────────────────────────

func runCapabilities(deps platformCLIDeps) int {
	if outputJSON {
		fmt.Fprintln(deps.stdout, `{"note":"capabilities require a running process; use GET /api/v1/platform/capabilities"}`)
	} else {
		fmt.Fprintln(deps.stdout, "Capabilities require a running process.")
	}
	return exitSuccess
}

// ── Configuration ─────────────────────────────────────────────────

func runConfig(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	ckey := fs.String("key", "", "setting key (required for get)")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()
	repo := configtruth.NewRepository(db)
	svc := configtruth.NewService(repo)
	svc.RegisterField(configtruth.Field{Key: "security.password_min_len", Section: "security", Type: "int", RestartRequired: true})
	svc.RegisterField(configtruth.Field{Key: "backup.retention_count", Section: "backup", Type: "int", RestartRequired: false})
	svc.RegisterField(configtruth.Field{Key: "backup.scheduler_enabled", Section: "backup", Type: "bool", RestartRequired: false})
	svc.RegisterField(configtruth.Field{Key: "jwt.secret", Section: "security", Type: "string", Secret: true})

	switch action {
	case "list":
		settings, err := svc.List(ctx)
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(settings)
			return exitSuccess
		}
		fmt.Fprintf(deps.stdout, "%-40s %-8s %-12s %s\n", "KEY", "TYPE", "STATE", "VALUE")
		for _, s := range settings {
			v := fmt.Sprintf("%v", s.Value)
			if s.Secret {
				v = "REDACTED"
			}
			fmt.Fprintf(deps.stdout, "%-40s %-8s %-12s %s\n", s.Key, s.Type, s.State, v)
		}
		return exitSuccess
	case "get":
		if *ckey == "" {
			fmt.Fprintln(deps.stderr, "--key is required")
			return exitBadArgs
		}
		s, err := svc.Get(ctx, *ckey)
		if err != nil {
			fmt.Fprintf(deps.stderr, "setting not found: %v\n", err)
			return exitNotFound
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(s)
		} else {
			fmt.Fprintf(deps.stdout, "Key: %s\nType: %s\nState: %s\nValue: %v\nRestart Required: %v\nSecret: %v\nVersion: %d\n",
				s.Key, s.Type, s.State, s.Value, s.RestartRequired, s.Secret, s.Version)
		}
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown config action:", action)
		return exitBadArgs
	}
}

// ── API Keys ──────────────────────────────────────────────────────

func runAPIKeys(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | create | revoke")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	switch action {
	case "list":
		fs.Parse(rest)
		if outputJSON {
			fmt.Fprintln(deps.stdout, `{"note":"API key listing requires a running process; use GET /api/v1/api-keys"}`)
		} else {
			fmt.Fprintln(deps.stdout, "API key listing requires a running process.")
			fmt.Fprintln(deps.stdout, "Use GET /api/v1/api-keys from a running orvix instance.")
		}
		return exitSuccess
	case "create":
		name := fs.String("name", "", "key name (required)")
		userID := fs.Uint("user-id", 0, "owning user ID (required)")
		tenantID := fs.Uint("tenant-id", 0, "tenant ID (required)")
		role := fs.String("role", "user", "role bound to the key")
		scopes := fs.String("scopes", "", "comma-separated scopes")
		ttlDays := fs.Int("ttl-days", 0, "days until expiry (0=never)")
		confirm := fs.String("confirm", "", "type CREATE-KEY to confirm")
		if err := fs.Parse(rest); err != nil {
			return exitBadArgs
		}
		if *name == "" || *userID == 0 || *tenantID == 0 {
			fmt.Fprintln(deps.stderr, "error: --name, --user-id, and --tenant-id are required")
			return exitBadArgs
		}
		if *confirm != "CREATE-KEY" {
			fmt.Fprintln(deps.stderr, "error: confirmation refused")
			return exitConfirmRefused
		}
		return runAPIKeysCreate(deps, *name, *userID, *tenantID, *role, *scopes, *ttlDays)
	case "revoke":
		id := fs.Uint("id", 0, "key ID (required)")
		userID := fs.Uint("user-id", 0, "owning user ID (required)")
		reason := fs.String("reason", "", "revocation reason (required)")
		confirm := fs.String("confirm", "", "type REVOKE-<id> to confirm")
		if err := fs.Parse(rest); err != nil {
			return exitBadArgs
		}
		if *id == 0 || *userID == 0 || *reason == "" {
			fmt.Fprintln(deps.stderr, "error: --id, --user-id, and --reason are required")
			return exitBadArgs
		}
		if *confirm != fmt.Sprintf("REVOKE-%d", *id) {
			fmt.Fprintln(deps.stderr, "error: confirmation refused")
			return exitConfirmRefused
		}
		return runAPIKeysRevoke(deps, *id, *userID, *reason)
	default:
		fmt.Fprintln(deps.stderr, "unknown apikeys action:", action)
		return exitBadArgs
	}
}

func runAPIKeysCreate(deps platformCLIDeps, name string, userID, tenantID uint, role, scopesStr string, ttlDays int) int {
	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()

	// Generate secure random key (matches auth.NewAPIKeyManager.Generate)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(deps.stderr, "error generating key: %v\n", err)
		return exitInternal
	}
	fullKey := "orv_" + hex.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fullKey)))
	prefix := fullKey[:11]
	scopes := strings.TrimSpace(scopesStr)
	now := time.Now().UTC()
	var expiresAt *time.Time
	if ttlDays > 0 {
		t := now.AddDate(0, 0, ttlDays)
		expiresAt = &t
	}
	// Ensure schema
	db.Exec(`CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		name TEXT NOT NULL, user_id INTEGER NOT NULL, tenant_id INTEGER NOT NULL,
		role TEXT NOT NULL DEFAULT 'user', key_hash TEXT NOT NULL,
		key_prefix TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1, last_used_at DATETIME,
		expires_at DATETIME, deleted_at DATETIME, allowed_ips TEXT NOT NULL DEFAULT ''
	)`)
	res, err := db.Exec(`INSERT INTO api_keys (created_at, updated_at, name, user_id, tenant_id, role, key_hash, key_prefix, scopes, active, expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		now, now, name, userID, tenantID, role, hash, prefix, scopes, 1, expiresAt)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error storing key: %v\n", err)
		return exitInternal
	}
	id, _ := res.LastInsertId()
	// Plaintext key shown exactly once. Never logged, audited, or stored.
	if outputJSON {
		fmt.Fprintf(deps.stdout, "{\"id\":%d,\"name\":%q,\"key\":%q,\"prefix\":%q,\"tenant_id\":%d,\"role\":%q,\"scopes\":%q,\"expires_at\":%s}\n",
			id, name, fullKey, prefix, tenantID, role, scopes, fmt.Sprintf("%q", expiresAt.Format(time.RFC3339)))
	} else {
		fmt.Fprintf(deps.stdout, "API key created (ID %d, name %q):\n", id, name)
		fmt.Fprintf(deps.stdout, "  Key: %s\n", fullKey)
		fmt.Fprintf(deps.stdout, "  Prefix: %s\n", prefix)
		fmt.Fprintf(deps.stdout, "  Tenant: %d\n", tenantID)
		fmt.Fprintf(deps.stdout, "  Role: %s\n", role)
		fmt.Fprintf(deps.stdout, "  Scopes: %s\n", scopes)
		fmt.Fprintf(deps.stdout, "  Store this key securely - it cannot be retrieved again.\n")
	}
	return exitSuccess
}

func runAPIKeysRevoke(deps platformCLIDeps, id, userID uint, reason string) int {
	db, _, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	res, err := db.Exec(`UPDATE api_keys SET active=0, updated_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL`,
		time.Now().UTC(), id, userID)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error revoking key: %v\n", err)
		return exitInternal
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fmt.Fprintln(deps.stderr, "API key not found")
		return exitForbidden
	}
	_ = reason
	printOK(deps, fmt.Sprintf("API key %d revoked", id), nil)
	return exitSuccess
}

// ── Import Commands ────────────────────────────────────────────────

func runImports(args []string, deps platformCLIDeps, fs *flag.FlagSet) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.stderr, "missing action: list | get | validate | execute | cancel | resume | compensate")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	iid := fs.Int64("id", 0, "import ID")
	iconfirm := fs.String("confirm", "", "confirmation token")
	if err := fs.Parse(rest); err != nil {
		return exitBadArgs
	}

	db, dial, closeFn, err := deps.openDB()
	if err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	defer closeFn()
	ctx := context.Background()

	// Real service construction: repository, confined staging service,
	// durable jobs service with the platform.import handler registered, and
	// concrete adapters around the real admin services. There is no inline
	// execution fallback and no nil dependency.
	repo := importer.NewRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		fmt.Fprintf(deps.stderr, "error initializing import schema: %v\n", err)
		return exitInternal
	}

	stagingDir := deps.stagingDir()
	if stagingDir == "" {
		fmt.Fprintln(deps.stderr, "error: no staging directory configured")
		return exitInternal
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		fmt.Fprintf(deps.stderr, "error creating staging directory: %v\n", err)
		return exitInternal
	}
	staging, err := importer.NewStagingService(stagingDir)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error initializing staging: %v\n", err)
		return exitInternal
	}

	jobRepo := jobs.NewJobRepository(db)
	if err := jobRepo.EnsureSchema(ctx); err != nil {
		fmt.Fprintf(deps.stderr, "error initializing jobs schema: %v\n", err)
		return exitInternal
	}
	jobRegistry := jobs.NewRegistry()
	jobSvc := jobs.NewServiceWithRegistry(jobRepo, jobRegistry, kernel.SystemClock{})

	adapters, err := importer.NewProductionAdaptersFromDB(db, dial)
	if err != nil {
		fmt.Fprintf(deps.stderr, "error building import adapters: %v\n", err)
		return exitInternal
	}
	svc := importer.NewService(repo, adapters, staging, jobSvc, nil)
	if err := svc.RequiredDependencies(); err != nil {
		fmt.Fprintf(deps.stderr, "error: %v\n", err)
		return exitInternal
	}
	if err := jobs.RegisterProductionHandlers(jobRegistry, nil, nil, svc); err != nil {
		fmt.Fprintf(deps.stderr, "error registering import job handler: %v\n", err)
		return exitInternal
	}

	switch action {
	case "list":
		list, total, err := repo.List(ctx, importer.ImportFilter{
			Scope: "platform",
			Page:  kernel.PageRequest{PageSize: 50},
		})
		if err != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", err)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(list)
			return exitSuccess
		}
		fmt.Fprintf(deps.stdout, "%-4s %-12s %-14s %-8s %-8s %-8s\n", "ID", "SOURCE", "STATUS", "TOTAL", "SUCC", "FAIL")
		for _, j := range list {
			fmt.Fprintf(deps.stdout, "%-4d %-12s %-14s %-8d %-8d %-8d\n", j.ID, j.SourceType, j.Status, j.TotalRows, j.SucceededRows, j.FailedRows)
		}
		fmt.Fprintf(deps.stdout, "Total: %d\n", total)
		return exitSuccess

	case "get":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		job, err := svc.Get(ctx, uint(*iid), 0, "platform")
		if err != nil {
			fmt.Fprintln(deps.stderr, "import job not found")
			return exitNotFound
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(job)
		} else {
			fmt.Fprintf(deps.stdout, "ID: %d\nSource: %s\nStatus: %s\nPolicy: %s\nHash: %s\nTotal: %d\nSucceeded: %d\nFailed: %d\nCheckpoint: %d/%d\n",
				job.ID, job.SourceType, job.Status, job.ConflictPolicy, job.SourceHash[:16], job.TotalRows, job.SucceededRows, job.FailedRows, job.CurrentCheckpoint, job.TotalRows)
		}
		return exitSuccess

	case "validate":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		report, cerr := svc.Validate(ctx, uint(*iid), 0, "platform")
		if cerr != nil {
			fmt.Fprintf(deps.stderr, "validation error: %v\n", cerr)
			return exitInternal
		}
		if outputJSON {
			json.NewEncoder(deps.stdout).Encode(report)
		} else {
			fmt.Fprintf(deps.stdout, "Validation report (ID %d):\n", report.ImportID)
			fmt.Fprintf(deps.stdout, "  Total: %d\n  Valid: %d\n  Invalid: %d\n  Conflict: %d\n  Deferred: %d\n  Unchanged: %d\n",
				report.Total, report.Valid, report.Invalid, report.Conflict, report.Deferred, report.Unchanged)
		}
		return exitSuccess

	case "execute":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		want := fmt.Sprintf("EXECUTE-IMPORT-%d", *iid)
		if *iconfirm != want {
			fmt.Fprintln(deps.stderr, "confirmation refused")
			return exitConfirmRefused
		}
		job, cerr := svc.Get(ctx, uint(*iid), 0, "platform")
		if cerr != nil {
			fmt.Fprintln(deps.stderr, "import job not found")
			return exitNotFound
		}
		// Stable deterministic idempotency key: retrying the same CLI
		// invocation replays the original result instead of double-submitting.
		idemKey := "cli-execute-import-" + itoaCLI(job.ID)
		result, exerr := svc.Execute(ctx, uint(*iid), 0, "platform", idemKey, want)
		if exerr != nil {
			fmt.Fprintf(deps.stderr, "execute error: %v\n", exerr)
			return exitInternal
		}
		printOK(deps, fmt.Sprintf("Import %d queued for execution (status: %s)", result.ID, result.Status), map[string]any{"id": result.ID, "status": string(result.Status)})
		return exitSuccess

	case "cancel":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		job, cerr := svc.Cancel(ctx, uint(*iid), 0, "platform")
		if cerr != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", cerr)
			return exitInternal
		}
		printOK(deps, fmt.Sprintf("Import %d cancelled", job.ID), nil)
		return exitSuccess

	case "resume":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		idemKey := "cli-resume-import-" + itoaCLI(uint(*iid))
		job, rerr := svc.Resume(ctx, uint(*iid), 0, "platform", idemKey)
		if rerr != nil {
			fmt.Fprintf(deps.stderr, "resume error: %v\n", rerr)
			return exitInternal
		}
		printOK(deps, fmt.Sprintf("Import %d resumed (status: %s)", job.ID, job.Status), map[string]any{"id": job.ID, "status": string(job.Status)})
		return exitSuccess

	case "compensate":
		if *iid <= 0 {
			fmt.Fprintln(deps.stderr, "--id is required")
			return exitBadArgs
		}
		want := fmt.Sprintf("COMPENSATE-IMPORT-%d", *iid)
		if *iconfirm != want {
			fmt.Fprintln(deps.stderr, "confirmation refused")
			return exitConfirmRefused
		}
		idemKey := "cli-compensate-import-" + itoaCLI(uint(*iid))
		job, cerr := svc.Compensate(ctx, uint(*iid), 0, "platform", idemKey, want)
		if cerr != nil {
			fmt.Fprintf(deps.stderr, "error: %v\n", cerr)
			return exitInternal
		}
		printOK(deps, fmt.Sprintf("Import %d compensation: %s", job.ID, job.Status), nil)
		return exitSuccess

	default:
		fmt.Fprintln(deps.stderr, "unknown imports action:", action)
		return exitBadArgs
	}
}

func itoaCLI(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	return string(buf[i:])
}

// ── Helpers ───────────────────────────────────────────────────────

func printOK(deps platformCLIDeps, human string, obj any) {
	if outputJSON && obj != nil {
		json.NewEncoder(deps.stdout).Encode(obj)
	} else if outputJSON {
		fmt.Fprintf(deps.stdout, "{\"status\":\"ok\"}\n")
	} else {
		fmt.Fprintln(deps.stdout, human)
	}
}
