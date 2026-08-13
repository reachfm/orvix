package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platform_cluster_nodes (
			id TEXT PRIMARY KEY,
			role TEXT NOT NULL DEFAULT '',
			capabilities TEXT NOT NULL DEFAULT '[]',
			version TEXT NOT NULL DEFAULT '',
			build TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			zone TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'alive',
			maintenance_reason TEXT NOT NULL DEFAULT '',
			maintenance_until ` + ts + `,
			last_heartbeat_at ` + ts + ` NOT NULL,
			lease_expires_at ` + ts + ` NOT NULL,
			row_version INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + ` NOT NULL,
			-- secret_hash stores the SHA-256 of the node's enrollment
			-- secret (never the secret itself) for authenticating
			-- heartbeats — see auth.go.
			secret_hash TEXT NOT NULL DEFAULT '',
			revoked INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS platform_cluster_leases (
			resource_key TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			fence_token INTEGER NOT NULL DEFAULT 0,
			acquired_at ` + ts + ` NOT NULL,
			expires_at ` + ts + ` NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := r.db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) UpsertNode(ctx context.Context, n *Node, secretHash string, now time.Time) error {
	caps, _ := json.Marshal(n.Capabilities)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_cluster_nodes (id, role, capabilities, version, build, region, zone, status, last_heartbeat_at, lease_expires_at, row_version, created_at, secret_hash)
		VALUES (`+r.dialect.Placeholders(13)+`)
		ON CONFLICT (id) DO UPDATE SET
			role=excluded.role, capabilities=excluded.capabilities, version=excluded.version, build=excluded.build,
			region=excluded.region, zone=excluded.zone, last_heartbeat_at=excluded.last_heartbeat_at,
			lease_expires_at=excluded.lease_expires_at, row_version=platform_cluster_nodes.row_version+1`,
		n.ID, n.Role, string(caps), n.Version, n.Build, n.Region, n.Zone, string(NodeAlive), now, now.Add(30*time.Second), 1, now, secretHash)
	return err
}

func (r *Repository) GetNode(ctx context.Context, id string) (*Node, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, role, capabilities, version, build, region, zone, status, maintenance_reason, maintenance_until, last_heartbeat_at, lease_expires_at, row_version, created_at FROM platform_cluster_nodes WHERE id=`+r.dialect.Placeholder(1), id)
	return scanNode(row)
}

// GetNodeSecretHash is used only by the authentication check
// (auth.go) — kept separate from GetNode so the hash never travels
// through the general-purpose read path by accident.
func (r *Repository) GetNodeAuth(ctx context.Context, id string) (secretHash string, revoked bool, err error) {
	row := r.db.QueryRowContext(ctx, `SELECT secret_hash, revoked FROM platform_cluster_nodes WHERE id=`+r.dialect.Placeholder(1), id)
	var rev int
	if err := row.Scan(&secretHash, &rev); err != nil {
		if err == sql.ErrNoRows {
			return "", false, ErrNodeNotFound
		}
		return "", false, err
	}
	return secretHash, rev != 0, nil
}

func (r *Repository) RevokeNode(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE platform_cluster_nodes SET revoked=1 WHERE id=`+r.dialect.Placeholder(1), id)
	return err
}

func (r *Repository) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, role, capabilities, version, build, region, zone, status, maintenance_reason, maintenance_until, last_heartbeat_at, lease_expires_at, row_version, created_at FROM platform_cluster_nodes ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// Heartbeat is the concurrency-safe liveness update: a single atomic
// UPDATE that both refreshes last_heartbeat_at/lease_expires_at AND
// resolves Suspect/Unavailable back to Alive if the node was
// previously marked down but is now checking in again — heartbeat
// recovery is part of the same statement, not a separate step that
// could race with a concurrent expiry sweep.
func (r *Repository) Heartbeat(ctx context.Context, id string, now time.Time, leaseDuration time.Duration) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE platform_cluster_nodes SET last_heartbeat_at=`+r.dialect.Placeholder(1)+`, lease_expires_at=`+r.dialect.Placeholder(2)+`,
			status = CASE WHEN status IN (`+r.dialect.Placeholder(3)+`, `+r.dialect.Placeholder(4)+`) THEN `+r.dialect.Placeholder(5)+` ELSE status END,
			row_version = row_version + 1
		WHERE id=`+r.dialect.Placeholder(6)+` AND revoked=0`,
		now, now.Add(leaseDuration), string(NodeSuspect), string(NodeUnavailable), string(NodeAlive), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// MarkExpiredNodes transitions nodes whose lease has expired: Alive ->
// Suspect on first miss, Suspect -> Unavailable on continued absence.
// Cordoned/Draining nodes are administrative states and are never
// touched by liveness expiry — an operator's cordon must not be
// silently overwritten by a heartbeat timeout transition.
func (r *Repository) MarkExpiredNodes(ctx context.Context, now time.Time) (suspected, unavailable int64, err error) {
	res1, err := r.db.ExecContext(ctx,
		`UPDATE platform_cluster_nodes SET status=`+r.dialect.Placeholder(1)+`, row_version=row_version+1 WHERE status=`+r.dialect.Placeholder(2)+` AND lease_expires_at<`+r.dialect.Placeholder(3),
		string(NodeSuspect), string(NodeAlive), now)
	if err != nil {
		return 0, 0, err
	}
	suspected, _ = res1.RowsAffected()

	res2, err := r.db.ExecContext(ctx,
		`UPDATE platform_cluster_nodes SET status=`+r.dialect.Placeholder(1)+`, row_version=row_version+1 WHERE status=`+r.dialect.Placeholder(2)+` AND lease_expires_at<`+r.dialect.Placeholder(3),
		string(NodeUnavailable), string(NodeSuspect), now.Add(-30*time.Second))
	if err != nil {
		return suspected, 0, err
	}
	unavailable, err = res2.RowsAffected()
	return suspected, unavailable, err
}

// TransitionMaintenance is the atomic guarded state change for
// cordon/uncordon/drain/resume — the expected current state AND the
// row_version are both in the WHERE clause (same pattern as
// domainlifecycle/bulkprovision), so a concurrent double-cordon or a
// cordon racing an uncordon settles deterministically.
func (r *Repository) TransitionMaintenance(ctx context.Context, id string, expected, next NodeStatus, reason string, until *time.Time, expectedVersion int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE platform_cluster_nodes SET status=`+r.dialect.Placeholder(1)+`, maintenance_reason=`+r.dialect.Placeholder(2)+`, maintenance_until=`+r.dialect.Placeholder(3)+`, row_version=row_version+1
		WHERE id=`+r.dialect.Placeholder(4)+` AND status=`+r.dialect.Placeholder(5)+` AND row_version=`+r.dialect.Placeholder(6),
		next, reason, until, id, expected, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ── Fenced leases ────────────────────────────────────────────────

// AcquireLease is the split-brain-prevention primitive: a single
// atomic upsert that only succeeds if no lease exists, the existing
// lease has expired, or the caller already holds it (renewal) — and
// in every success case, fence_token is incremented, never reused.
// The dialect's native ON CONFLICT DO UPDATE with a WHERE guard on the
// conflict action is what makes "steal only if expired" atomic rather
// than a read-then-write race.
func (r *Repository) AcquireLease(ctx context.Context, resourceKey, nodeID string, now time.Time, duration time.Duration) (*LeaseHolder, error) {
	expiresAt := now.Add(duration)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO platform_cluster_leases (resource_key, node_id, fence_token, acquired_at, expires_at)
		VALUES (`+r.dialect.Placeholders(5)+`)
		ON CONFLICT (resource_key) DO UPDATE SET
			node_id=excluded.node_id, fence_token=platform_cluster_leases.fence_token+1,
			acquired_at=excluded.acquired_at, expires_at=excluded.expires_at
		WHERE platform_cluster_leases.expires_at <= `+r.dialect.Placeholder(6)+` OR platform_cluster_leases.node_id = `+r.dialect.Placeholder(7),
		resourceKey, nodeID, 1, now, expiresAt, now, nodeID)
	if err != nil {
		return nil, err
	}
	return r.GetLease(ctx, resourceKey)
}

func (r *Repository) GetLease(ctx context.Context, resourceKey string) (*LeaseHolder, error) {
	row := r.db.QueryRowContext(ctx, `SELECT resource_key, node_id, fence_token, acquired_at, expires_at FROM platform_cluster_leases WHERE resource_key=`+r.dialect.Placeholder(1), resourceKey)
	var l LeaseHolder
	if err := row.Scan(&l.ResourceKey, &l.NodeID, &l.FenceToken, &l.AcquiredAt, &l.ExpiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

func scanNode(row interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var capsJSON string
	err := row.Scan(&n.ID, &n.Role, &capsJSON, &n.Version, &n.Build, &n.Region, &n.Zone, &n.Status,
		&n.MaintenanceReason, &n.MaintenanceUntil, &n.LastHeartbeatAt, &n.LeaseExpiresAt, &n.RowVersion, &n.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNodeNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(capsJSON), &n.Capabilities)
	return &n, nil
}
