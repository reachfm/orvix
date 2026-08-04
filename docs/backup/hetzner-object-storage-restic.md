# External Backup — Hetzner Object Storage + Restic

This is the operator runbook for the ORVIX external backup: hourly Restic
snapshots of the ORVIX database, mail directory, config, and (optionally) Caddy
certificates, pushed to a **private** Hetzner Object Storage bucket. It is
complementary to the in-app backup service in `internal/backup/` — both should
be running in production.

Every placeholder below (`<...>`) is intentional. No real bucket, region,
credential, or host is committed to this repository.

## What ships with the release

- `release/scripts/external-backup-stage.sh` — builds a consistent staging tree
  under `/var/cache/orvix-external-backup/staging.XXXXXXXX` (0700 root:root).
- `release/scripts/external-backup-run.sh` — stages, then runs
  `restic backup` + `restic check` + `restic forget --prune`. Every gate is
  fail-closed (see script header).
- `release/scripts/external-backup-check.sh` — `weekly` (5 % subset) and
  `monthly` (full data) integrity checks.
- `release/scripts/external-backup-restore-drill.sh` — restores the latest
  snapshot into an ISOLATED directory and runs `PRAGMA integrity_check`.
- `release/systemd/orvix-external-backup{,-check-weekly,-check-monthly}.{service,timer}`
  — hardened units, matched to `release/systemd/orvix-update.service`.

The installer copies all of the above into place but **never enables any
external-backup timer**. Enabling is an explicit operator step.

## Snapshot consistency

The staging script snapshots the **database first** (via SQLite `VACUUM INTO`,
which is the SQLite-native atomic snapshot), then the mail directory. This
ordering is deliberate:

> `internal/coremail/storage/mailstore.go:StoreMessage` writes the RFC822 file
> to disk **before** the DB row is inserted, and there is no application-level
> quiesce endpoint we can call from a shell script. Snapshotting the DB first
> and the mail dir second means the worst case is a mail file present on disk
> that the DB snapshot does not know about — a benign "orphan file". The
> reverse order would produce DB rows referencing files that the mail snapshot
> missed, which is a hard restore failure.

Do not reorder those steps.

## One-time setup

### 1. Create the Hetzner bucket

1. In the Hetzner Cloud Console, create a new **private** Object Storage
   bucket. Pick a location (`fsn1`, `nbg1`, `hel1`) aligned with your mail
   server for latency.
2. **Enable Object Lock capability at creation time.** Hetzner only allows
   this at creation; you cannot add it later. Do **not** set a default
   retention period in this initial rollout — retention interactions with
   `restic forget --prune` are subtle and need to be tested in a scratch
   bucket first (see caveats below).
3. Under the bucket's "S3 credentials" tab, generate a **new access
   key/secret pair scoped to this bucket only**. Do not reuse a
   project-wide API token.

### 2. Install Restic on the host

Restic is intentionally **not** installed by `release/install.sh`. Install it
with the operator's preferred method, e.g.:

```
sudo apt-get update && sudo apt-get install -y restic
```

### 3. Create the credential files (out-of-band, root only)

```
# Repository password — generate once, keep offline copy in a password manager.
openssl rand -base64 32 | sudo tee /etc/orvix/restic-password >/dev/null
sudo chmod 400 /etc/orvix/restic-password
sudo chown root:root /etc/orvix/restic-password

# Env file — start from the example, fill in real values.
sudo install -m 0600 -o root -g root \
    docs/backup/external-backup.env.example \
    /etc/orvix/external-backup.env
sudo $EDITOR /etc/orvix/external-backup.env
```

The `run` script refuses to start unless `/etc/orvix/restic-password` is mode
`0400` or `0600` and owned by `root`.

### 4. Initialise the repository

```
sudo bash -c 'set -a; . /etc/orvix/external-backup.env; set +a; restic init'
```

### 5. First backup + timer enable

```
# One-off, foreground:
sudo systemctl start orvix-external-backup.service
sudo journalctl -u orvix-external-backup.service -e --no-pager

# Then enable the hourly timer:
sudo systemctl enable --now orvix-external-backup.timer

# And the integrity-check timers:
sudo systemctl enable --now orvix-external-backup-check-weekly.timer
sudo systemctl enable --now orvix-external-backup-check-monthly.timer
```

## Day-to-day monitoring

```
systemctl list-timers | grep orvix-external
journalctl -u orvix-external-backup.service -e
sudo bash -c 'set -a; . /etc/orvix/external-backup.env; set +a; restic snapshots'
```

A non-zero exit code from the service unit is your primary alert signal —
hook it into whatever monitoring you use for `orvix.service` today.

## Retention

Default policy, applied every successful run:

```
--keep-hourly 24 --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune
```

To change it, edit the `RETENTION_KEEP_*` env vars at the top of
`external-backup-run.sh` or override them via `EnvironmentFile=`.

Retention only runs when both the backup and the fast metadata check succeed.

## Restore drill

Run periodically (at minimum after every retention/config change):

```
sudo /usr/share/orvix/scripts/external-backup-restore-drill.sh \
    --target /var/lib/orvix-restore-drill/$(date +%Y%m%d)
```

The drill script refuses any target under `/var/lib/orvix`, `/etc/orvix`,
`/var/lib/caddy`, `/usr/local/bin`, `/usr/share/orvix`, or `/`. It runs
`restic restore latest --target <target>` and then
`sqlite3 <target>/…/database/orvix.db 'PRAGMA integrity_check;'`, failing
if the result is not `ok`.

## Credential rotation

1. Generate new S3 credentials in the Hetzner console.
2. Edit `/etc/orvix/external-backup.env` (keep it 0600 root:root).
3. Test with a one-off run: `sudo systemctl start orvix-external-backup.service`.
4. Once the run is green, revoke the old key in the console.

The Restic repository password should be rotated with `restic key add` +
`restic key remove` while both the old and new password files are readable
by the operator. Never rotate it in-place without a working backup of the
old password.

## Disable / rollback

```
sudo systemctl disable --now orvix-external-backup.timer
sudo systemctl disable --now orvix-external-backup-check-weekly.timer
sudo systemctl disable --now orvix-external-backup-check-monthly.timer
# Optionally keep the last snapshot as an archive, then remove config:
sudo shred -u /etc/orvix/external-backup.env /etc/orvix/restic-password
```

## Full disaster recovery order

1. Provision a fresh VM matching the original host's OS + disk layout.
2. Install ORVIX from the merged main branch using the standard install
   procedure (see the repository README).
3. Install Restic (`apt-get install restic`).
4. Restore the credential files from your offline password-manager copy into
   `/etc/orvix/external-backup.env` (0600) and `/etc/orvix/restic-password`
   (0400).
5. Restore into a scratch directory first (`restic restore latest --target
   /var/restore-scratch/`), then copy each subtree into place:
   `/var/lib/orvix/orvix.db`, `/var/lib/orvix/coremail/`, the three
   `/var/lib/orvix/*` secrets, the `/etc/orvix/*` config, and (if present)
   the Caddy certificates. Do **not** run `restic restore --target /`
   directly on the running host; the paths under `/var/lib/orvix` are also
   used by the running service.
6. Verify the DB: `sqlite3 /var/lib/orvix/orvix.db 'PRAGMA integrity_check;'`
   must print `ok`.
7. `sudo systemctl start orvix` and confirm the health endpoint.
8. Point DNS / MX / SPF / DKIM records at the new host if the IP changed.

## Hetzner-specific caveats

- **`restic forget --prune` needs delete permission on the bucket.** Test in
  a scratch bucket before combining with Object Lock retention or you will
  corrupt the Restic repository.
- **Do not combine Object Lock with time-based bucket lifecycle rules.**
  Lifecycle rules that delete non-current versions can strand pack files
  that Restic still references — the repository becomes unrestorable.
- **Bucket versioning must remain OFF unless you fully understand the
  interaction with Restic's pack file model.** Restic manages its own
  content-addressed storage; S3 versioning duplicates that.
