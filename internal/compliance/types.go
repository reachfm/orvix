package compliance

import (
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// PolicyAction defines what happens when a policy matches.
type PolicyAction string

const (
	ActionAllow      PolicyAction = "allow"
	ActionQuarantine PolicyAction = "quarantine"
	ActionReject     PolicyAction = "reject"
)

// PolicyScope defines what the policy filters on.
type PolicyScope string

const (
	ScopeDomain    PolicyScope = "domain"
	ScopeSender    PolicyScope = "sender"
	ScopeRecipient PolicyScope = "recipient"
	ScopeSubject   PolicyScope = "subject"
)

// Policy is a compliance rule.
type Policy struct {
	ID        uint         `json:"id"`
	Name      string       `json:"name"`
	Enabled   bool         `json:"enabled"`
	Action    PolicyAction `json:"action"`
	Scope     PolicyScope  `json:"scope"`
	Value     string       `json:"value"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// QuarantineStatus represents the state of a quarantined message.
type QuarantineStatus string

const (
	QStatusQuarantined QuarantineStatus = "quarantined"
	QStatusReleased    QuarantineStatus = "released"
	QStatusDeleted     QuarantineStatus = "deleted"
)

// QuarantinedMessage is a message held for review.
type QuarantinedMessage struct {
	ID         uint             `json:"id"`
	MessageID  string           `json:"messageId"`
	Sender     string           `json:"sender"`
	Recipient  string           `json:"recipient"`
	Reason     string           `json:"reason"`
	Status     QuarantineStatus `json:"status"`
	CreatedAt  time.Time        `json:"createdAt"`
	ReleasedAt *time.Time       `json:"releasedAt,omitempty"`
	ReleasedBy string           `json:"releasedBy,omitempty"`
}

// AbuseEvent represents a detected abuse incident.
type AbuseEvent struct {
	ID        uint      `json:"id"`
	Source    string    `json:"source"` // "trust", "audit", "policy"
	EventType string    `json:"eventType"`
	Actor     string    `json:"actor"`
	Detail    string    `json:"detail"`
	Timestamp time.Time `json:"timestamp"`
}

func schema(d *dbdialect.Info) []string {
	ts := d.TimestampType()
	autoInc := d.AutoIncrement()
	return []string{
		`CREATE TABLE IF NOT EXISTS compliance_policies (
			id ` + autoInc + `,
			name TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			action TEXT NOT NULL DEFAULT 'allow',
			scope TEXT NOT NULL DEFAULT '',
			value TEXT NOT NULL DEFAULT '',
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_quarantine (
			id ` + autoInc + `,
			message_id TEXT NOT NULL DEFAULT '',
			sender TEXT NOT NULL DEFAULT '',
			recipient TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'quarantined',
			created_at ` + ts + ` NOT NULL,
			released_at ` + ts + `,
			released_by TEXT NOT NULL DEFAULT ''
		)`,
	}
}
