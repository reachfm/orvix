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
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/orvix/orvix/internal/selfupdate"
)

const (
	defaultSocketPath = "/run/orvix/updater.sock"
	// defaultAllowedUser is the unprivileged service account the main
	// orvix process runs as (see release/systemd/orvix.service's
	// User=orvix/Group=orvix). Only connections from this UID are
	// answered.
	defaultAllowedUser = "orvix"
)

func main() {
	socketPath := envOr("ORVIX_UPDATER_SOCKET", defaultSocketPath)
	allowedUserName := envOr("ORVIX_UPDATER_ALLOWED_USER", defaultAllowedUser)

	allowedUID, err := resolveUID(allowedUserName)
	if err != nil {
		log.Fatalf("orvix-updater: cannot resolve allowed user %q: %v", allowedUserName, err)
	}

	srv := &selfupdate.Server{
		SocketPath: socketPath,
		AllowedUID: allowedUID,
		Handlers:   handlers(),
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

// handlers wires each allow-listed Operation to its implementation.
//
// NOT YET IMPLEMENTED (tracked in docs/adr/0001-admin-console-self-update.md
// "Implementation status"): the persistent job store (Phase D), release
// discovery against the real GitHub API (Phase E), preflight checks
// (Phase F), and the actual install/backup/rollback orchestration
// (Phase G). Until those land, StartInstall/StartRollback/Preflight/
// CheckRelease/ListHistory/ListSnapshots correctly report
// "not implemented" rather than fabricating a fake success — the daemon
// must never claim to have done something it did not do.
func handlers() map[selfupdate.Operation]selfupdate.Handler {
	notImplemented := func(op selfupdate.Operation) selfupdate.Handler {
		return func(r selfupdate.Request) selfupdate.Response {
			return selfupdate.Response{OK: false, Error: fmt.Sprintf("selfupdate: %s is not implemented yet", op)}
		}
	}
	return map[selfupdate.Operation]selfupdate.Handler{
		selfupdate.OpStatus: func(r selfupdate.Request) selfupdate.Response {
			return selfupdate.Response{OK: true}
		},
		selfupdate.OpCheckRelease:             notImplemented(selfupdate.OpCheckRelease),
		selfupdate.OpPreflight:                notImplemented(selfupdate.OpPreflight),
		selfupdate.OpStartInstall:             notImplemented(selfupdate.OpStartInstall),
		selfupdate.OpGetJob:                   notImplemented(selfupdate.OpGetJob),
		selfupdate.OpCancelBeforeIrreversible: notImplemented(selfupdate.OpCancelBeforeIrreversible),
		selfupdate.OpStartRollback:            notImplemented(selfupdate.OpStartRollback),
		selfupdate.OpListHistory:              notImplemented(selfupdate.OpListHistory),
		selfupdate.OpListSnapshots:            notImplemented(selfupdate.OpListSnapshots),
	}
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
