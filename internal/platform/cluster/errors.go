package cluster

import "errors"

var (
	ErrNodeNotFound              = errors.New("cluster node not found")
	ErrNodeUnauthorized          = errors.New("node identity not recognized or revoked")
	ErrNoPlaceableNode           = errors.New("no placeable node satisfies the requested capabilities")
	ErrLeaseHeldByOther          = errors.New("resource lease is held by another node")
	ErrStaleFenceToken           = errors.New("fence token is stale")
	ErrVersionConflict           = errors.New("node was modified concurrently")
	ErrMaintenanceReasonRequired = errors.New("a maintenance reason is required")
)
