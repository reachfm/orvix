package updates

import (
	"context"

	"github.com/orvix/orvix/internal/updatecoord"
)

// CoordinatorAdapter implements both ApplyCoordinator and
// RollbackCoordinator over the real internal/updatecoord job queue —
// the durable hand-off to the external, privileged update helper.
// Neither this adapter nor updatecoord ever executes the artifact or
// any shell string; they only validate and record a job.
type CoordinatorAdapter struct {
	coord *updatecoord.Coordinator
	actor string
}

// NewCoordinatorAdapter wraps coord, tagging every submitted job with
// actor (typically "platform-admin:<id>", supplied by the caller).
func NewCoordinatorAdapter(coord *updatecoord.Coordinator, actor string) *CoordinatorAdapter {
	return &CoordinatorAdapter{coord: coord, actor: actor}
}

// Submit implements ApplyCoordinator. artifactPath must already be
// the staged, verified artifact's path recorded on the Record — it is
// never derived from unauthenticated caller input at this layer.
func (a *CoordinatorAdapter) Submit(ctx context.Context, artifactPath, version string) (string, error) {
	job, err := a.coord.Submit(artifactPath, version, "", a.actor)
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

// SubmitRollback implements RollbackCoordinator.
func (a *CoordinatorAdapter) SubmitRollback(ctx context.Context, targetVersion, targetHash, fromVersion string) (string, error) {
	job, err := a.coord.SubmitRollback(targetVersion, targetHash, fromVersion, a.actor)
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

// GetOperationStatus polls the durable coordinator result for jobID,
// translated into a stable string status/message pair for HTTP
// responses (never exposes filesystem paths or internals).
func (a *CoordinatorAdapter) GetOperationStatus(jobID string) (status, message string, terminal bool, err error) {
	res, err := a.coord.GetResult(jobID)
	if err != nil {
		return "", "", false, err
	}
	return string(res.Status), res.Message, res.Status.IsTerminal(), nil
}

var _ ApplyCoordinator = (*CoordinatorAdapter)(nil)
var _ RollbackCoordinator = (*CoordinatorAdapter)(nil)
