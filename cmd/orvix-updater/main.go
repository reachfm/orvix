// Command orvix-updater is the privileged self-update daemon. It is the
// ONLY process on an Orvix host permitted to run the official
// release/upgrade.sh path; the main orvix web/API process never gains
// that ability. See docs/adr/0001-admin-console-self-update.md.
//
// orvix-updater must run as root (the upgrade path replaces the running
// binary and systemd unit) but only ever accepts requests from the
// unprivileged `orvix` service account, authenticated via the connecting
// process's kernel-verified UID over a Unix domain socket — never a
// password, token, or anything else the peer sends.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/orvix/orvix/internal/selfupdate"
)

const (
	defaultSocketPath = "/run/orvix/updater.sock"
	// defaultAllowedUser is the unprivileged service account the main
	// orvix process runs as (see release/systemd/orvix.service's
	// User=orvix/Group=orvix). Only connections from this UID are
	// answered.
	defaultAllowedUser = "orvix"

	defaultDBPath            = "/var/lib/orvix/selfupdate.db"
	defaultUpgradeScriptPath = "/opt/orvix/release/upgrade.sh"
	defaultBinaryPath        = "/usr/local/bin/orvix"
	defaultAdminAssetsDir    = "/usr/share/orvix/admin"
	defaultWebmailAssetsDir  = "/usr/share/orvix/webmail"
	defaultConfigDir         = "/etc/orvix"
	defaultSystemdUnitsDir   = "/etc/systemd/system"
	defaultBuildInfoPath     = "/usr/share/orvix/BUILDINFO"
	defaultTrustedPubKeyPath = "/opt/orvix/release/trust/orvix-release-signing.pub.pem"
	defaultSnapshotRoot      = "/var/backups/orvix-updater/snapshots"
	defaultDownloadDir       = "/var/backups/orvix-updater/downloads"
	defaultHealthURL         = "http://127.0.0.1:8080/api/v1/health"
	defaultWebmailHealthURL  = "http://127.0.0.1:8080/webmail"
	defaultAdminHealthURL    = "http://127.0.0.1:8080/admin"
)

func main() {
	socketPath := envOr("ORVIX_UPDATER_SOCKET", defaultSocketPath)
	allowedUserName := envOr("ORVIX_UPDATER_ALLOWED_USER", defaultAllowedUser)

	allowedUID, err := resolveUID(allowedUserName)
	if err != nil {
		log.Fatalf("orvix-updater: cannot resolve allowed user %q: %v", allowedUserName, err)
	}

	store, orch, disc := mustWireSelfUpdate()

	srv := &selfupdate.Server{
		SocketPath: socketPath,
		AllowedUID: allowedUID,
		Handlers:   handlers(store, orch, disc),
	}
	if err := srv.Listen(); err != nil {
		log.Fatalf("orvix-updater: listen on %s: %v", socketPath, err)
	}
	log.Printf("orvix-updater: listening on %s (allowed uid=%d/%s)", socketPath, allowedUID, allowedUserName)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("orvix-updater: serve: %v", err)
	case sig := <-sigCh:
		log.Printf("orvix-updater: received %s, shutting down", sig)
		_ = srv.Close()
	}
}

// mustWireSelfUpdate builds the production Store/Orchestrator/Discoverer
// triple from environment-configured paths (never from anything supplied
// over the IPC socket). It is the ONLY place production paths are
// resolved; internal/selfupdate itself never hardcodes a production path
// beyond the fixed argv shapes documented in orchestrator.go.
func mustWireSelfUpdate() (selfupdate.Store, *selfupdate.Orchestrator, *selfupdate.Discoverer) {
	dbPath := envOr("ORVIX_UPDATER_DB", defaultDBPath)
	if err := os.MkdirAll(dirOf(dbPath), 0o700); err != nil {
		log.Fatalf("orvix-updater: create db directory: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("orvix-updater: open selfupdate db: %v", err)
	}
	if err := selfupdate.CreateTables(db); err != nil {
		log.Fatalf("orvix-updater: create selfupdate tables: %v", err)
	}
	if err := selfupdate.CreatePreflightTable(db); err != nil {
		log.Fatalf("orvix-updater: create preflight table: %v", err)
	}
	store, err := selfupdate.NewStore(db)
	if err != nil {
		log.Fatalf("orvix-updater: construct store: %v", err)
	}

	trustedKeyPath := envOr("ORVIX_RELEASE_TRUST_KEY", defaultTrustedPubKeyPath)
	trustedKeyPEM, err := os.ReadFile(trustedKeyPath)
	if err != nil {
		log.Printf("orvix-updater: WARNING: cannot read trusted release signing key at %s: %v (release discovery/verification will fail until this is fixed)", trustedKeyPath, err)
	}
	disc := selfupdate.NewDiscoverer(trustedKeyPEM)

	orch := selfupdate.NewOrchestrator(selfupdate.OrchestratorDeps{
		Store:             store,
		UpgradeScriptPath: envOr("ORVIX_UPGRADE_SCRIPT", defaultUpgradeScriptPath),
		BinaryPath:        envOr("ORVIX_BIN", defaultBinaryPath),
		AdminAssetsDir:    envOr("ORVIX_ADMIN_UI_DIR", defaultAdminAssetsDir),
		WebmailAssetsDir:  envOr("ORVIX_WEBMAIL_UI_DIR", defaultWebmailAssetsDir),
		ConfigDir:         envOr("ORVIX_CONFIG_DIR", defaultConfigDir),
		SystemdUnitsDir:   envOr("ORVIX_SYSTEMD_UNITS_DIR", defaultSystemdUnitsDir),
		BuildInfoPath:     envOr("ORVIX_BUILDINFO_PATH", defaultBuildInfoPath),
		TrustedPubKeyPath: trustedKeyPath,
		SnapshotRoot:      envOr("ORVIX_UPDATER_SNAPSHOT_ROOT", defaultSnapshotRoot),
		DownloadDir:       envOr("ORVIX_UPDATER_DOWNLOAD_DIR", defaultDownloadDir),
		AdminHealthURL:    envOr("ORVIX_ADMIN_HEALTH_URL", defaultAdminHealthURL),
		WebmailHealthURL:  envOr("ORVIX_WEBMAIL_HEALTH_URL", defaultWebmailHealthURL),
		APIHealthURL:      envOr("ORVIX_API_HEALTH_URL", defaultHealthURL),
		InstalledVersionReader: func() (string, error) {
			return readInstalledVersion(envOr("ORVIX_BUILDINFO_PATH", defaultBuildInfoPath))
		},
		HealthPollInterval: 2 * time.Second,
		HealthPollTimeout:  60 * time.Second,
		// DBBackup is intentionally left nil in this default wiring: a
		// real production deployment that runs PostgreSQL should set it
		// to an adapter over internal/pgbackup.CreateBackup/RestoreBackup
		// (Phase G's DBBackupper interface). Deferred here rather than
		// guessed at, since the concrete pgbackup.BackupConfig
		// (host/port/credentials) is sourced from internal/config, which
		// this minimal daemon entrypoint does not currently load. See
		// the Phase G completion report for the tracked follow-up.
	})

	return store, orch, disc
}

// handlers wires each allow-listed Operation to its implementation.
func handlers(store selfupdate.Store, orch *selfupdate.Orchestrator, disc *selfupdate.Discoverer) map[selfupdate.Operation]selfupdate.Handler {
	ctx := context.Background()

	return map[selfupdate.Operation]selfupdate.Handler{
		selfupdate.OpStatus: func(r selfupdate.Request) selfupdate.Response {
			return selfupdate.Response{OK: true}
		},

		selfupdate.OpCheckRelease: func(r selfupdate.Request) selfupdate.Response {
			installed, err := readInstalledVersion(envOr("ORVIX_BUILDINFO_PATH", defaultBuildInfoPath))
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: cannot determine installed version"}
			}
			channel := selfupdate.ChannelStable
			if r.Channel == "prerelease" {
				channel = selfupdate.ChannelPrerelease
			}
			result, err := disc.DiscoverRelease(ctx, channel, installed)
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: release discovery failed"}
			}
			return selfupdate.Response{OK: true, Releases: []selfupdate.ReleaseInfo{result.Info.ReleaseInfo}}
		},

		selfupdate.OpPreflight: func(r selfupdate.Request) selfupdate.Response {
			return selfupdate.Response{OK: false, Error: "selfupdate: preflight must be driven by the install pipeline (not directly wired for standalone IPC calls in this daemon entrypoint yet)"}
		},

		selfupdate.OpStartInstall: func(r selfupdate.Request) selfupdate.Response {
			return selfupdate.Response{OK: false, Error: "selfupdate: start_install requires a prior check_release + preflight result resolved by the API process; direct IPC wiring for the full pipeline is a tracked follow-up (see Phase G completion report)"}
		},

		selfupdate.OpGetJob: func(r selfupdate.Request) selfupdate.Response {
			job, err := store.GetJob(r.JobID)
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: job not found"}
			}
			return selfupdate.Response{OK: true, Job: &job}
		},

		selfupdate.OpCancelBeforeIrreversible: func(r selfupdate.Request) selfupdate.Response {
			job, err := orch.Cancel(r.JobID)
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: " + err.Error()}
			}
			return selfupdate.Response{OK: true, Job: &job}
		},

		selfupdate.OpStartRollback: func(r selfupdate.Request) selfupdate.Response {
			snaps, err := store.ListSnapshots()
			if err != nil || len(snaps) == 0 {
				return selfupdate.Response{OK: false, Error: "selfupdate: no rollback snapshot available"}
			}
			var target selfupdate.RollbackSnapshot
			found := false
			for _, s := range snaps {
				if s.LastKnownGood {
					target = s
					found = true
					break
				}
			}
			if !found {
				return selfupdate.Response{OK: false, Error: "selfupdate: no last-known-good snapshot available"}
			}
			job, err := orch.StartRollback(ctx, r.IdempotencyKey, r.InitiatedBy, target)
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: rollback failed", Job: &job}
			}
			return selfupdate.Response{OK: true, Job: &job}
		},

		selfupdate.OpListHistory: func(r selfupdate.Request) selfupdate.Response {
			jobs, err := store.ListJobs(100)
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: could not list job history"}
			}
			return selfupdate.Response{OK: true, Jobs: jobs}
		},

		selfupdate.OpListSnapshots: func(r selfupdate.Request) selfupdate.Response {
			snaps, err := store.ListSnapshots()
			if err != nil {
				return selfupdate.Response{OK: false, Error: "selfupdate: could not list snapshots"}
			}
			return selfupdate.Response{OK: true, Snapshots: snaps}
		},
	}
}

// readInstalledVersion parses a `version=X.Y.Z` line out of the
// BUILDINFO file, the same convention release bundles ship (see
// release/scripts/build-release-bundle.sh).
func readInstalledVersion(buildInfoPath string) (string, error) {
	data, err := os.ReadFile(buildInfoPath)
	if err != nil {
		return "", err
	}
	for _, line := range splitLines(string(data)) {
		if v, ok := cutPrefix(line, "version="); ok {
			return v, nil
		}
	}
	return "", fmt.Errorf("no version= line found in %s", buildInfoPath)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, trimCR(s[start:]))
	}
	return lines
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// resolveUID looks up the numeric UID for a system account name. This is
// the only place a "user" string from the environment is used, and it is
// never passed to a shell — os/user.Lookup uses the platform's native
// NSS/passwd lookup directly.
func resolveUID(name string) (uint32, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("unexpected non-numeric uid %q for user %q", u.Uid, name)
	}
	return uint32(uid), nil
}
