package queue

import (
	"time"
)

// QueueStatus represents the lifecycle state of a queue entry.
type QueueStatus string

const (
	StatusPending    QueueStatus = "pending"
	StatusLeased     QueueStatus = "leased"
	StatusDelivering QueueStatus = "delivering"
	StatusDeferred   QueueStatus = "deferred"
	StatusDelivered  QueueStatus = "delivered"
	StatusBounced    QueueStatus = "bounced"
	StatusDeadLetter QueueStatus = "dead_letter"
	StatusCancelled  QueueStatus = "cancelled"
)

// Direction indicates whether the message is inbound, outbound, or internal.
type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
	DirectionInternal Direction = "internal"
)

// DeliveryMode classifies how the message should be delivered.
type DeliveryMode string

const (
	DeliveryLocal      DeliveryMode = "local"       // same-server mailbox delivery
	DeliveryRemoteSMTP DeliveryMode = "remote_smtp" // outbound SMTP to external MX
	DeliveryRelay      DeliveryMode = "relay"       // relay to another MTA
	DeliveryWebhook    DeliveryMode = "webhook"     // HTTP webhook delivery
)

// QueueEntry represents a single item in the mail delivery queue.
type QueueEntry struct {
	ID              uint         `json:"id"`
	TenantID        uint         `json:"tenant_id"`
	DomainID        uint         `json:"domain_id"`
	MailboxID       *uint        `json:"mailbox_id,omitempty"`
	MessageID       string       `json:"message_id"`
	FromAddress     string       `json:"from_address"`
	ToAddress       string       `json:"to_address"`
	RecipientDomain string       `json:"recipient_domain"`
	Direction       Direction    `json:"direction"`
	Status          QueueStatus  `json:"status"`
	Priority        int          `json:"priority"` // higher = more urgent
	AttemptCount    int          `json:"attempt_count"`
	MaxAttempts     int          `json:"max_attempts"`
	NextAttemptAt   *time.Time   `json:"next_attempt_at,omitempty"`
	LastAttemptAt   *time.Time   `json:"last_attempt_at,omitempty"`
	LastError       string       `json:"last_error,omitempty"`
	DeliveryMode    DeliveryMode `json:"delivery_mode"`
	RemoteHost      string       `json:"remote_host,omitempty"`
	RemoteIP        string       `json:"remote_ip,omitempty"`
	TLSUsed         bool         `json:"tls_used"`
	// LastStatusCode is the SMTP status code from
	// the most recent attempt (e.g. 550, 450, 421).
	// Populated by DeferWithDiagnostics /
	// BounceWithDiagnostics / DeadLetterWithDiagnostics.
	// The Admin queue UI surfaces this verbatim so
	// the operator can distinguish a permanent
	// 5.1.1 from a transient 4.7.1 without log
	// scraping.
	LastStatusCode int `json:"last_status_code"`
	// LastEnhancedCode is the SMTP enhanced
	// status code (e.g. "5.1.1", "4.7.1",
	// "5.7.0"). The enhanced code is the
	// canonical "why" for bounces and defers —
	// it tells the operator whether the remote
	// rejected the recipient, the message, the
	// transport, the policy, or the security.
	LastEnhancedCode string `json:"last_enhanced_code,omitempty"`
	// Leasing fields for worker safety.
	LeaseOwner     string     `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	// Timestamps.
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DeadLetterAt *time.Time `json:"dead_letter_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// QueueFilter defines search/filter criteria for listing queue entries.
type QueueFilter struct {
	TenantID        *uint
	DomainID        *uint
	MailboxID       *uint
	Direction       *Direction
	Status          *QueueStatus
	Statuses        []QueueStatus
	DeliveryMode    *DeliveryMode
	RecipientDomain string
	Search          string
	Limit           int
	Offset          int
}

// QueueMetrics holds aggregated queue statistics.
type QueueMetrics struct {
	Pending       int64      `json:"pending"`
	Leased        int64      `json:"leased"`
	Delivering    int64      `json:"delivering"`
	Deferred      int64      `json:"deferred"`
	Delivered     int64      `json:"delivered"`
	Bounced       int64      `json:"bounced"`
	DeadLetter    int64      `json:"dead_letter"`
	Cancelled     int64      `json:"cancelled"`
	Total         int64      `json:"total"`
	AvgAttempts   float64    `json:"avg_attempts"`
	OldestPending *time.Time `json:"oldest_pending,omitempty"`
}

// nowFn is overridable for testing.
var nowFn = time.Now

const (
	DefaultMaxAttempts  = 16
	DefaultPageSize     = 100
	MaxPageSize         = 1000
	DefaultLeaseSeconds = 300 // 5 minutes
)
