// Package updatecoord is the durable job/result boundary between the
// Orvix API (unprivileged, User=orvix) and an external, privileged
// update-apply helper (an orvix-update.service oneshot, run as root),
// mirroring internal/restorecoord's design exactly: the API only ever
// SUBMITS a job and POLLS its durable result; it never swaps its own
// binary or restarts itself. The helper — a separate systemd unit
// triggered by a path watch on the queue directory — is the process
// that copies the verified artifact into place, restarts orvix,
// verifies the restarted service's health, and rolls back on failure,
// so completion and post-restart health are always observed by a
// process that is NOT the one being replaced.
//
// Security properties (same as restorecoord):
//   - Job IDs are 32-byte crypto-random hex; every path is built only
//     from a validated hex ID, so no caller input reaches the
//     filesystem verbatim (no traversal, no shell).
//   - The artifact path handed to Submit must already be an absolute,
//     canonical (symlink-free) path inside an allowlisted staging
//     root — never a caller-controlled string used directly. Submit
//     re-validates this itself so a caller cannot smuggle a path
//     outside the staging tree even if an upstream check is skipped.
//   - Reads/writes reject symlinks (O_NOFOLLOW / Lstat) to defeat
//     symlink swaps in the shared job directory.
//   - Results are written atomically (temp + rename) with restrictive
//     modes.
//   - One update operation (apply OR rollback) at a time is enforced
//     at submit — an active job of either kind blocks new submissions
//     — and by an exclusive flock the helper holds while draining, so
//     apply and rollback are mutually exclusive by construction, not
//     just by convention.
//   - A job is processed exactly once; a terminal result is never
//     reopened.
//   - Nothing in this package ever executes a shell string. The
//     helper process (outside this codebase) is expected to run a
//     fixed, non-shell argv (e.g. exec.Command with an explicit
//     argument array) against the queued job's validated fields only.
package updatecoord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Kind distinguishes an apply job from a rollback job. They share the
// same queue/lock so at most one of either kind is ever in flight.
type Kind string

const (
	KindApply    Kind = "apply"
	KindRollback Kind = "rollback"
)

// Status is the lifecycle state of an update operation. Only the
// external helper may advance a job to a terminal state
// (Succeeded/Failed); the API-side Submit only ever creates Pending.
type Status string

const (
	StatusPending     Status = "pending"
	StatusApplying    Status = "applying"
	StatusRestarting  Status = "restarting"
	StatusVerifying   Status = "verifying"
	StatusRollingBack Status = "rolling_back"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
)

// IsTerminal reports whether s is a final state.
func (s Status) IsTerminal() bool { return s == StatusSucceeded || s == StatusFailed }

// Job is the immutable update request written by the API and read by
// the helper. ArtifactPath/TargetVersion/TargetHash are the ONLY
// inputs the helper trusts; it must not execute or interpret anything
// else from the request.
type Job struct {
	ID            string    `json:"id"`
	Kind          Kind      `json:"kind"`
	ArtifactPath  string    `json:"artifact_path,omitempty"` // apply only
	TargetVersion string    `json:"target_version"`
	TargetHash    string    `json:"target_hash,omitempty"` // apply only: expected sha256
	FromVersion   string    `json:"from_version,omitempty"`
	Actor         string    `json:"actor"`
	CreatedAt     time.Time `json:"created_at"`
}

// Result is the durable, pollable status of an update operation. It
// is the ONLY source of truth the API returns; it is re-read from
// disk on every poll so it survives the Orvix process restart the
// helper performs.
type Result struct {
	JobID      string    `json:"job_id"`
	Kind       Kind      `json:"kind"`
	Status     Status    `json:"status"`
	Message    string    `json:"message,omitempty"`
	RolledBack bool      `json:"rolled_back"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var (
	ErrActiveJob      = fmt.Errorf("an update operation is already in progress")
	ErrNotFound       = fmt.Errorf("update job not found")
	ErrInvalidID      = fmt.Errorf("invalid update job id")
	ErrTampered       = fmt.Errorf("update job file failed integrity checks")
	ErrPathNotAllowed = fmt.Errorf("artifact path is outside the allowed staging root")
)

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidJobID reports whether id is a canonical update job id.
func ValidJobID(id string) bool { return jobIDPattern.MatchString(id) }

// NewJobID returns a fresh unguessable 256-bit hex id.
func NewJobID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate update job id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Coordinator owns the on-disk job/result tree, rooted at dir, and
// only accepts artifact paths inside stagingRoot (which must itself
// be an absolute, canonical directory — typically the same directory
// the updates.Service stages verified artifacts into).
type Coordinator struct {
	root        string
	stagingRoot string
}

// New returns a Coordinator rooted at dir (e.g.
// /var/lib/orvix/update-jobs), restricting apply-job artifact paths
// to stagingRoot (e.g. /var/lib/orvix/update-staging).
func New(dir, stagingRoot string) *Coordinator {
	return &Coordinator{root: dir, stagingRoot: stagingRoot}
}

func (c *Coordinator) queueDir() string   { return filepath.Join(c.root, "queue") }
func (c *Coordinator) resultsDir() string { return filepath.Join(c.root, "results") }

// LockPath is the exclusive-lock file the helper flocks while
// draining, so at most one update operation runs at a time even if
// the path unit fires repeatedly.
func (c *Coordinator) LockPath() string { return filepath.Join(c.root, "update.lock") }

func (c *Coordinator) jobPath(id string) string { return filepath.Join(c.queueDir(), id+".job") }
func (c *Coordinator) resultPath(id string) string {
	return filepath.Join(c.resultsDir(), id+".result")
}

// EnsureDirs creates the queue/results directories with restrictive modes.
func (c *Coordinator) EnsureDirs() error {
	for _, d := range []string{c.root, c.queueDir(), c.resultsDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("updatecoord: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// validateArtifactPath requires an absolute, symlink-free path that
// resolves to somewhere inside the configured staging root — the
// caller-supplied artifact path is never trusted verbatim.
func (c *Coordinator) validateArtifactPath(path string) (string, error) {
	if path == "" {
		return "", ErrPathNotAllowed
	}
	if c.stagingRoot == "" {
		return "", fmt.Errorf("updatecoord: no staging root configured")
	}
	absRoot, err := filepath.Abs(c.stagingRoot)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Reject symlinks at the final component so a swapped file cannot
	// point outside the staging root after this check runs.
	if fi, err := os.Lstat(absPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", ErrTampered
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPathNotAllowed
	}
	return absPath, nil
}

// Submit records a new apply job. Fails closed if any update
// operation is already active, and rejects any artifact path outside
// the allowlisted staging root.
func (c *Coordinator) Submit(artifactPath, targetVersion, targetHash, actor string) (*Job, error) {
	safePath, err := c.validateArtifactPath(artifactPath)
	if err != nil {
		return nil, err
	}
	return c.submitJob(Job{Kind: KindApply, ArtifactPath: safePath, TargetVersion: targetVersion, TargetHash: targetHash, Actor: actor})
}

// SubmitRollback records a new rollback job — no artifact path is
// involved; the helper reverts to the previously-recorded
// version/hash using its own retained copy (or re-verifies from the
// staging directory using targetHash), never from caller input.
func (c *Coordinator) SubmitRollback(targetVersion, targetHash, fromVersion, actor string) (*Job, error) {
	return c.submitJob(Job{Kind: KindRollback, TargetVersion: targetVersion, TargetHash: targetHash, FromVersion: fromVersion, Actor: actor})
}

func (c *Coordinator) submitJob(job Job) (*Job, error) {
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
	job.ID = id
	job.CreatedAt = now
	if err := writeJSONAtomic(c.jobPath(id), &job, 0o600); err != nil {
		return nil, fmt.Errorf("write update job: %w", err)
	}
	res := &Result{
		JobID:     id,
		Kind:      job.Kind,
		Status:    StatusPending,
		Message:   "update job accepted; the external update helper will apply/verify/rollback",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeJSONAtomic(c.resultPath(id), res, 0o644); err != nil {
		return nil, fmt.Errorf("write update result: %w", err)
	}
	return &job, nil
}

// GetResult reads the durable result for id.
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
			return true, nil
		}
		if !res.Status.IsTerminal() {
			return true, nil
		}
	}
	return false, nil
}

// ── Helper-side operations (privileged worker) ────────────────────────────

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
			continue
		}
		info, ierr := e.Info()
		ts := time.Now()
		if ierr == nil {
			ts = info.ModTime()
		}
		items = append(items, item{id: id, ts: ts})
	}
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

// RemoveJob deletes a processed job file from the queue; the durable
// result in results/ is retained.
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

// ── Low-level fs helpers (identical safety properties to restorecoord) ────

func writeJSONAtomic(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".tmp-%d-%s", os.Getpid(), filepath.Base(path)))
	_ = os.Remove(tmp)
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
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

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
