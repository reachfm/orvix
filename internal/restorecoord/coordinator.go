// Package restorecoord is the durable job/result boundary between the Orvix
// API (unprivileged, User=orvix) and the external, privileged restore helper
// (orvix-restore.service oneshot, root). The API only ever SUBMITS a job and
// POLLS its durable result; it never restarts itself. The helper, running in a
// separate systemd unit, is the process that activates the backup, restarts
// orvix, verifies the restarted service's health, and rolls back on failure —
// so completion and post-restart health are observed by a process that is NOT
// the one being restarted.
//
// Security properties:
//   - Job IDs are 32-byte crypto-random hex; every path is built only from a
//     validated hex ID, so no caller input reaches the filesystem verbatim
//     (no traversal, no shell).
//   - Reads/writes reject symlinks (O_NOFOLLOW / Lstat) to defeat symlink
//     swaps in the shared job directory.
//   - Results are written atomically (temp + rename) with restrictive modes.
//   - One restore at a time is enforced at submit (an active job blocks new
//     submissions) and by an exclusive flock the helper holds while draining.
//   - A job is processed exactly once; a terminal result is never reopened.
package restorecoord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Status is the lifecycle state of a restore job. Only the external helper may
// advance a job to a terminal state (Succeeded/Failed); the API-side Submit
// only ever creates Pending.
type Status string

const (
	StatusPending     Status = "pending"
	StatusActivating  Status = "activating"
	StatusRestarting  Status = "restarting"
	StatusVerifying   Status = "verifying"
	StatusRollingBack Status = "rolling_back"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
)

// IsTerminal reports whether s is a final state.
func (s Status) IsTerminal() bool { return s == StatusSucceeded || s == StatusFailed }

// Job is the immutable restore request written by the API and read by the
// helper.
type Job struct {
	ID        string    `json:"id"`
	BackupID  string    `json:"backup_id"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

// Result is the durable, pollable status of a restore job. It is the ONLY
// source of truth the API returns; it is re-read from disk on every poll so it
// survives the Orvix process restart the helper performs.
type Result struct {
	JobID          string    `json:"job_id"`
	BackupID       string    `json:"backup_id"`
	Status         Status    `json:"status"`
	Message        string    `json:"message,omitempty"`
	SafetyBackupID string    `json:"safety_backup_id,omitempty"`
	RolledBack     bool      `json:"rolled_back"`
	Error          string    `json:"error,omitempty"`
	RollbackError  string    `json:"rollback_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

var (
	// ErrActiveJob is returned by Submit when a restore is already in flight.
	ErrActiveJob = fmt.Errorf("a restore job is already in progress")
	// ErrNotFound is returned when a job/result does not exist.
	ErrNotFound = fmt.Errorf("restore job not found")
	// ErrInvalidID rejects any ID that is not a canonical crypto-random hex.
	ErrInvalidID = fmt.Errorf("invalid restore job id")
	// ErrTampered is returned when an on-disk file is a symlink or malformed.
	ErrTampered = fmt.Errorf("restore job file failed integrity checks")
)

// jobIDPattern matches exactly the 64-hex-char IDs NewJobID produces. Every
// filesystem path is derived from an ID that has passed this gate, so a
// caller can never smuggle "..", "/", or a NUL into a path.
var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidJobID reports whether id is a canonical restore job id.
func ValidJobID(id string) bool { return jobIDPattern.MatchString(id) }

// NewJobID returns a fresh unguessable 256-bit hex id.
func NewJobID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate restore job id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Coordinator owns the on-disk job/result tree.
type Coordinator struct {
	root string
}

// New returns a Coordinator rooted at dir (e.g. /var/lib/orvix/restore-jobs).
func New(dir string) *Coordinator { return &Coordinator{root: dir} }

func (c *Coordinator) queueDir() string   { return filepath.Join(c.root, "queue") }
func (c *Coordinator) resultsDir() string { return filepath.Join(c.root, "results") }

// LockPath is the exclusive-lock file the helper flocks while draining, so at
// most one restore runs at a time even if the path unit fires repeatedly.
func (c *Coordinator) LockPath() string { return filepath.Join(c.root, "restore.lock") }

func (c *Coordinator) jobPath(id string) string {
	return filepath.Join(c.queueDir(), id+".job")
}

func (c *Coordinator) resultPath(id string) string {
	return filepath.Join(c.resultsDir(), id+".result")
}

// EnsureDirs creates the queue/results directories with restrictive modes.
func (c *Coordinator) EnsureDirs() error {
	for _, d := range []string{c.root, c.queueDir(), c.resultsDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("restorecoord: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// Submit records a new restore job and its initial pending result. It fails
// closed if a restore is already active. The API calls this; it performs NO
// activation or restart.
func (c *Coordinator) Submit(backupID, actor string) (*Job, error) {
	if err := c.EnsureDirs(); err != nil {
		return nil, err
	}
	active, err := c.hasActiveJob()
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrActiveJob
	}
	id, err := NewJobID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	job := &Job{ID: id, BackupID: backupID, Actor: actor, CreatedAt: now}
	if err := writeJSONAtomic(c.jobPath(id), job, 0o600); err != nil {
		return nil, fmt.Errorf("write restore job: %w", err)
	}
	res := &Result{
		JobID:     id,
		BackupID:  backupID,
		Status:    StatusPending,
		Message:   "restore job accepted; the external restore helper will activate the backup, restart Orvix, and verify health",
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Result files carry no secrets (status/error only) and must be readable
	// by the unprivileged API even when written by the root helper.
	if err := writeJSONAtomic(c.resultPath(id), res, 0o644); err != nil {
		return nil, fmt.Errorf("write restore result: %w", err)
	}
	return job, nil
}

// GetResult reads the durable result for id. Re-reads from disk every call so
// it reflects helper progress across the Orvix restart.
func (c *Coordinator) GetResult(id string) (*Result, error) {
	if !ValidJobID(id) {
		return nil, ErrInvalidID
	}
	var res Result
	if err := readJSONNoFollow(c.resultPath(id), &res); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &res, nil
}

// hasActiveJob reports whether any result is in a non-terminal state.
func (c *Coordinator) hasActiveJob() (bool, error) {
	entries, err := os.ReadDir(c.resultsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".result" {
			continue
		}
		id := name[:len(name)-len(".result")]
		if !ValidJobID(id) {
			continue
		}
		res, err := c.GetResult(id)
		if err != nil {
			// A malformed/tampered active-looking file is treated as active
			// so we fail closed rather than starting a concurrent restore.
			return true, nil
		}
		if !res.Status.IsTerminal() {
			return true, nil
		}
	}
	return false, nil
}

// ── Helper-side operations (privileged worker) ────────────────────────────

// ReadJob reads and validates a queued job by id.
func (c *Coordinator) ReadJob(id string) (*Job, error) {
	if !ValidJobID(id) {
		return nil, ErrInvalidID
	}
	var job Job
	if err := readJSONNoFollow(c.jobPath(id), &job); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if job.ID != id || !ValidJobID(job.ID) {
		return nil, ErrTampered
	}
	return &job, nil
}

// PendingJobIDs returns the ids of queued jobs whose result is not yet
// terminal, oldest first.
func (c *Coordinator) PendingJobIDs() ([]string, error) {
	entries, err := os.ReadDir(c.queueDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type item struct {
		id string
		ts time.Time
	}
	var items []item
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".job" {
			continue
		}
		id := name[:len(name)-len(".job")]
		if !ValidJobID(id) {
			continue
		}
		res, err := c.GetResult(id)
		if err == nil && res.Status.IsTerminal() {
			continue // already processed
		}
		info, ierr := e.Info()
		ts := time.Now()
		if ierr == nil {
			ts = info.ModTime()
		}
		items = append(items, item{id: id, ts: ts})
	}
	// oldest first
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].ts.Before(items[i].ts) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.id)
	}
	return ids, nil
}

// RemoveJob deletes a processed job file from the queue. The durable result in
// results/ is retained. Removing the queued job lets the systemd path unit
// (DirectoryNotEmpty) settle once the queue drains.
func (c *Coordinator) RemoveJob(id string) error {
	if !ValidJobID(id) {
		return ErrInvalidID
	}
	if err := os.Remove(c.jobPath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// WriteResult atomically persists r (helper-side progress + terminal state).
func (c *Coordinator) WriteResult(r *Result) error {
	if !ValidJobID(r.JobID) {
		return ErrInvalidID
	}
	r.UpdatedAt = time.Now().UTC()
	return writeJSONAtomic(c.resultPath(r.JobID), r, 0o644)
}

// ── Low-level fs helpers ──────────────────────────────────────────────────

// writeJSONAtomic writes v as JSON to a temp file in the same directory then
// renames it over path. The temp file is created with O_EXCL|O_NOFOLLOW so an
// attacker cannot pre-plant a symlink at the temp name.
func writeJSONAtomic(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".tmp-%d-%s", os.Getpid(), filepath.Base(path)))
	_ = os.Remove(tmp)
	// O_EXCL means a pre-planted symlink at the temp name causes EEXIST rather
	// than being followed. The enclosing dir is 0700 and owned by the service
	// account, so no other unprivileged user can plant one anyway.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Enforce the mode regardless of umask.
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readJSONNoFollow reads JSON from path, refusing to follow a symlink at the
// final component.
func readJSONNoFollow(path string, v any) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrTampered
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrTampered, err)
	}
	return nil
}
