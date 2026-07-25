# Orvix Emergency Recovery

Manual, no-UI-required recovery procedures for Orvix Enterprise Mail and
its self-update daemon (`orvix-updater`, Phase D-J). Every command below
is safe to run directly on the VPS over SSH as root; none of them
require the Admin Console, the API, or a working `orvix-updater` socket.

These procedures are written to be **consistent with the automated
paths** they mirror:

- The manual rollback steps below match exactly what
  `internal/selfupdate/orchestrator.go`'s `RestoreRollbackSnapshot` /
  `StartRollback` do internally (same systemctl argv, same file
  layout).
- The manual mail-server restart/rollback steps match
  `release/upgrade.sh`'s `full_rollback` function.

---

## 1. Admin Console is unavailable

The Admin Console is served by `orvix.service`. It has **no
dependency on `orvix-updater`** — see
`release/systemd/orvix.service`'s directives:

```
After=network-online.target redis-server.service
Wants=network-online.target
Requires=redis-server.service
```

There is no `After=`/`Wants=`/`Requires=` reference to
`orvix-updater` anywhere in that file, so a broken or missing updater
daemon can never be the cause of the Admin Console being down.
Diagnose `orvix.service` itself:

```bash
# Status + recent logs
systemctl status orvix.service
journalctl -u orvix.service -n 200 --no-pager

# Is Redis (a HARD dependency) actually up?
systemctl status redis-server.service
```

### Manual restart

```bash
systemctl restart orvix.service
sleep 2
systemctl is-active orvix.service
curl -fsS http://127.0.0.1:8080/api/v1/health
```

### Manual rollback to the last-known-good snapshot, without the UI/API

Orvix keeps two independent kinds of "last known good" state you can
restore from by hand:

**A) `release/upgrade.sh`-taken backups** (`/var/backups/orvix-upgrade/<run-id>/`)
— created by every `upgrade.sh` run before it touches anything:

```bash
# Find the most recent backup
ls -1dt /var/backups/orvix-upgrade/*/ | head -n1

# Restore the binary (mirrors upgrade.sh's full_rollback)
BACKUP_DIR=/var/backups/orvix-upgrade/<run-id>
cp -a "$BACKUP_DIR"/usr/local/bin/orvix /usr/local/bin/orvix   # path inside the backup mirrors the live path
systemctl restart orvix.service
sleep 2
systemctl is-active orvix.service

# Or invoke the script's own rollback entry point directly:
sudo bash /opt/orvix/release/upgrade.sh --rollback-from "$BACKUP_DIR"
```

(The exact backup layout — binary, config, admin/webmail/marketing
asset trees, DKIM keys, `manifest.assets` — is whatever
`preflight_backup()` in `release/upgrade.sh` wrote for that run; `ls
-la "$BACKUP_DIR"` to see exactly what's there before restoring.)

**B) `orvix-updater` rollback snapshots** (`/var/backups/orvix-updater/snapshots/<snap-id>/`)
— created by every self-update install before it replaces anything.
See §3 below for how to restore one of these by hand when the API is
unreachable; the procedure is identical whether the Admin Console or
`orvix-updater` is the thing that's down.

---

## 2. `orvix-updater` itself is unavailable or crashed

`orvix-updater` is socket-activated: `orvix-updater.socket` listens on
`/run/orvix/updater.sock` and starts `orvix-updater.service` on the
first connection.

### Check status

```bash
# Socket unit (should be active/listening at all times)
systemctl status orvix-updater.socket

# Service unit (may be "inactive (dead)" between activations — that is
# normal for a socket-activated unit that isn't currently handling a
# request)
systemctl status orvix-updater.service

# Logs
journalctl -u orvix-updater.service -n 200 --no-pager
journalctl -u orvix-updater.socket -n 100 --no-pager

# Does the socket file actually exist with the right ownership?
ls -la /run/orvix/updater.sock
# expect: srwxrwx--- root orvix  (SocketUser=root SocketGroup=orvix, mode 0660)
```

### Manual restart

```bash
# Restart the socket (safe even if the service is currently "dead" —
# socket units are meant to sit idle between activations)
systemctl restart orvix-updater.socket

# If the service itself is in a failed state and needs a direct kick:
systemctl restart orvix-updater.service
systemctl is-active orvix-updater.service
```

If `/run/orvix` itself is missing (should not happen — systemd creates
it automatically from `RuntimeDirectory=orvix` in
`orvix-updater.service` and `DirectoryMode=0750` in
`orvix-updater.socket` on every socket/service start):

```bash
systemctl daemon-reload
systemctl restart orvix-updater.socket
ls -ld /run/orvix   # should now exist, root:root or root:orvix, 0750
```

### This never blocks mail

**Confirmed by directly reading both unit files** (`release/systemd/orvix.service`
and `release/systemd/orvix-updater.service`):

- `orvix.service` has `After=network-online.target redis-server.service`,
  `Wants=network-online.target`, `Requires=redis-server.service` — no
  mention of `orvix-updater` anywhere.
- `orvix-updater.service` has `After=network-online.target orvix.service`
  — i.e. the *updater* depends on (comes after) the mail server, never
  the other way around, and it uses `After=` rather than `BindsTo=`/
  `PartOf=` specifically so stopping/restarting `orvix.service` during
  an upgrade never takes `orvix-updater` down with it.

So: SMTP/IMAP/POP3/JMAP/webmail/admin console keep running with zero
degradation while `orvix-updater` is down. The only thing lost is the
ability to check for / install updates through the Admin Console until
it's restarted.

---

## 3. An update was interrupted mid-flight

### Inspect job state directly via the database (API unreachable)

The self-update job/event/snapshot tables live in the SQLite database
at `ORVIX_UPDATER_DB` (default `/var/lib/orvix/selfupdate.db` — see
`cmd/orvix-updater/main.go`'s `defaultDBPath`). Schema is created
in-process by `internal/selfupdate/store.go`'s `CreateTables` /
`CreatePreflightTable` (no separate `.sql` migration files to look
for).

```bash
sqlite3 /var/lib/orvix/selfupdate.db <<'SQL'
.headers on
.mode column
SELECT id, kind, phase, requested_version, initiated_by, created_at, updated_at
FROM update_jobs
ORDER BY created_at DESC
LIMIT 10;
SQL
```

Phases (`internal/selfupdate` package, see `Phase` type / `store.go`)
progress roughly: `queued -> checking -> downloading -> verifying ->
preflight -> backing_up -> stopping_service -> migrating ->
replacing_runtime -> starting_service -> health_check -> completed`,
with `rolling_back` / `rolled_back` / `failed` / `cancelled` as
terminal-or-recovery states. A job stuck in any non-terminal phase
after the daemon has clearly stopped making progress (compare
`updated_at` against "now") is the "interrupted mid-flight" case.

Look at that job's event trail for the exact last action taken:

```bash
sqlite3 /var/lib/orvix/selfupdate.db <<'SQL'
.headers on
.mode column
SELECT job_id, phase, percent, message, created_at
FROM update_events
WHERE job_id = '<job-id-from-above>'
ORDER BY created_at ASC;
SQL
```

List available rollback snapshots (one is normally created at
`PhaseBackingUp`, before anything irreversible happens):

```bash
sqlite3 /var/lib/orvix/selfupdate.db <<'SQL'
.headers on
.mode column
SELECT id, source_version, source_commit, last_known_good, created_at
FROM rollback_snapshots
ORDER BY created_at DESC;
SQL
```

Snapshot **files** (not just metadata) live under
`SnapshotRoot` (default `/var/backups/orvix-updater/snapshots/<snap-id>/`,
see `defaultSnapshotRoot` in `cmd/orvix-updater/main.go`):

```bash
ls -la /var/backups/orvix-updater/snapshots/<snap-id>/
cat  /var/backups/orvix-updater/snapshots/<snap-id>/manifest.json
```

### Manually invoke the same rollback the orchestrator would have run

This mirrors `RestoreRollbackSnapshot` in
`internal/selfupdate/orchestrator.go` line-for-line — same files, same
hash verification, same restart command:

```bash
SNAP=/var/backups/orvix-updater/snapshots/<snap-id>

# 1. The manifest lists every file the snapshot captured, its
#    destination path, and its expected sha256. Restore each one and
#    verify the hash — do NOT skip the verification step, a byte
#    mismatch means the snapshot itself is suspect.
python3 - "$SNAP/manifest.json" <<'PY'
import json, sys, shutil, hashlib, os
manifest = json.load(open(sys.argv[1]))
snap_dir = os.path.dirname(sys.argv[1])
for e in manifest["entries"]:
    if e["component"] == "db_backup":
        print("SKIP db_backup entry — restore the DB backup manually, see below")
        continue
    src = os.path.join(snap_dir, e["rel_path"])
    dst = e["orig_path"]
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copyfile(src, dst)
    got = hashlib.sha256(open(dst, "rb").read()).hexdigest()
    status = "OK" if got == e["sha256"] else "MISMATCH!!"
    print(f"{status}  {dst}  (want {e['sha256'][:12]} got {got[:12]})")
PY

# 2. If any entry above was "MISMATCH", STOP — do not restart the
#    service with a partially/incorrectly restored file set. Re-copy
#    from the snapshot directory by hand and re-verify before
#    proceeding.

# 3. Only if manifest.json contains a "db_backup" component AND you
#    know a forward migration actually ran (check update_events above
#    for a "migrating" phase entry): restore the DB backup under
#    $SNAP/db/ using the same mechanism internal/pgbackup or the
#    sqlite backup path uses. If in doubt, leave the database alone —
#    that is the orchestrator's own safe default (see
#    RestoreRollbackSnapshot's doc comment in orchestrator.go).

# 4. Restart the service — exact command the orchestrator itself runs.
systemctl restart orvix

# 5. Confirm.
sleep 2
systemctl is-active orvix
curl -fsS http://127.0.0.1:8080/api/v1/health
```

### Mark the snapshot's job as recovered (optional, for audit trail continuity)

If you want the job history to reflect the manual intervention rather
than sit forever in a stuck intermediate phase:

```bash
sqlite3 /var/lib/orvix/selfupdate.db \
  "UPDATE update_jobs SET phase = 'rolled_back', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = '<job-id>';"
```

This is optional and purely cosmetic for the Admin Console's history
view — it does not affect running state.

---

## 4. Manual rollback procedure end-to-end (no working UI at all)

Use this when both the Admin Console AND `orvix-updater` are
unreachable and you need to get the mail server back to a known-good
state purely from the shell.

```bash
# Step 0 — pick your source of truth.
#   Prefer an orvix-updater snapshot if one exists and is newer than
#   the last upgrade.sh backup; otherwise use the upgrade.sh backup.
ls -1dt /var/backups/orvix-updater/snapshots/*/ 2>/dev/null | head -n3
ls -1dt /var/backups/orvix-upgrade/*/          2>/dev/null | head -n3

# Step 1 — stop the service before touching any files (exact argv the
# orchestrator itself uses: `systemctl stop orvix`).
systemctl stop orvix

# Step 2a — RESTORE FROM AN orvix-updater SNAPSHOT
#   (repeat the manifest-driven restore from §3 above), OR

# Step 2b — RESTORE FROM AN upgrade.sh BACKUP
BACKUP_DIR=/var/backups/orvix-upgrade/<run-id>
[ -f "$BACKUP_DIR/orvix" ] && cp -a "$BACKUP_DIR/orvix" /usr/local/bin/orvix
[ -f "$BACKUP_DIR/orvix.yaml" ] && cp -a "$BACKUP_DIR/orvix.yaml" /etc/orvix/orvix.yaml
# Admin/webmail/marketing asset trees: see $BACKUP_DIR/manifest.assets
# for the exact per-run backup directories, then restore each with:
#   rsync -a --delete <backup-tree>/ /usr/share/orvix/<admin|webmail|marketing>/

# Step 3 — restart (exact argv both the orchestrator and upgrade.sh use).
systemctl restart orvix

# Step 4 — verify.
sleep 2
systemctl is-active orvix
curl -fsS http://127.0.0.1:8080/api/v1/health
journalctl -u orvix.service -n 100 --no-pager

# Step 5 — if orvix-updater is also down and you just changed the
# binary/config it might reference, restart it too so it doesn't act
# on stale state next time it's used.
systemctl restart orvix-updater.socket 2>/dev/null || true
```

If step 4's health check fails, do not repeat step 3 in a loop —
capture `journalctl -u orvix.service -n 200 --no-pager` and
`cat /etc/orvix/orvix.yaml` (redact secrets before sharing) and
escalate; a failed restart after a clean rollback usually indicates a
Redis outage, a corrupted `orvix.yaml`, or a permissions problem on
`/var/lib/orvix`, not a bad binary.
