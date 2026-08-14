package bulkprovision

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/orvix/orvix/internal/platform/jobs"
)

// ImportJobType is the allowlisted durable job type for bulk mailbox
// execution — registered once at process start, never derived from
// client input.
const ImportJobType = "platform.mailbox.bulk_import"

// ImportJobPayloadVersion is bumped whenever ImportJobPayload's shape
// changes in a way an already-queued job could not tolerate.
const ImportJobPayloadVersion = 1

// ImportJobPayload is the durable, tenant/actor-bound job payload.
// TenantID is NOT carried here: the jobs package itself binds and
// enforces the submitting tenant on the Job row, and Execute is always
// called with that authoritative value — never a client-supplied one.
type ImportJobPayload struct {
	ImportJobID uint   `json:"import_job_id"`
	DomainName  string `json:"domain_name"`
	SourceHash  string `json:"source_hash"`
}

var errInvalidImportJobPayload = errors.New("invalid bulk mailbox import job payload")

// RegisterImportJob wires Execute onto the generic durable jobs
// framework: the framework owns leasing, fencing, heartbeat, retries,
// and cooperative-cancellation signalling; Execute owns bounded-batch
// checkpointing and resume so a crash or lost lease never re-creates a
// mailbox that already succeeded.
func RegisterImportJob(registry *jobs.Registry, svc *Service) error {
	return registry.Register(jobs.Definition{
		Type:           ImportJobType,
		Scope:          jobs.ScopePlatform,
		PayloadVersion: ImportJobPayloadVersion,
		// Generous: a single batch of DefaultBatchSize mailbox creations
		// (each doing real password hashing + folder provisioning) can
		// legitimately take minutes; the worker heartbeats between
		// batches well inside this window.
		Timeout: 15 * time.Minute,
		Validate: func(raw json.RawMessage) error {
			var p ImportJobPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if p.ImportJobID == 0 || p.DomainName == "" {
				return errInvalidImportJobPayload
			}
			return nil
		},
		Handle: func(ctx context.Context, exec jobs.Execution, raw json.RawMessage) (json.RawMessage, error) {
			var p ImportJobPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			hooks := &ExecuteHooks{
				BeforeBatch: func(ctx context.Context) error {
					if err := exec.Heartbeat(ctx); err != nil {
						return err
					}
					cancelled, err := exec.CancellationRequested(ctx)
					if err != nil {
						return err
					}
					if cancelled {
						return context.Canceled
					}
					return nil
				},
			}
			job, _, err := svc.Execute(ctx, p.ImportJobID, exec.TenantID(), 0, p.DomainName, p.SourceHash, hooks)
			if err != nil {
				return nil, err
			}
			result, _ := json.Marshal(map[string]any{
				"job_id": job.ID, "status": job.Status,
				"created": job.CreatedCount, "failed": job.FailedCount, "skipped": job.SkippedCount,
			})
			return result, nil
		},
	})
}
