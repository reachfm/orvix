package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/orvix/orvix/internal/configtruth"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/incident"
	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/supportaccess"
)

var outputJSON bool

type platformCLIDeps struct {
	openDB func() (*sql.DB, *dbdialect.Info, func() error, error)
	now    func() time.Time
	stdout io.Writer
	stderr io.Writer
}

func defaultPlatformCLIDeps() platformCLIDeps {
	return platformCLIDeps{
		openDB: openProductionDB,
		now:    func() time.Time { return time.Now().UTC() },
		stdout: os.Stdout,
		stderr: os.Stderr,
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
apikeys     list

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
		fmt.Fprintln(deps.stderr, "missing action: list")
		return exitBadArgs
	}
	action := args[0]
	rest := args[1:]
	fs.Parse(rest)

	switch action {
	case "list":
		if outputJSON {
			fmt.Fprintln(deps.stdout, `{"note":"API key listing requires a running process"}`)
		} else {
			fmt.Fprintln(deps.stdout, "API key listing requires a running process.")
		}
		return exitSuccess
	default:
		fmt.Fprintln(deps.stderr, "unknown apikeys action:", action)
		return exitBadArgs
	}
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
