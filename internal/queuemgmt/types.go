package queuemgmt

import "time"

// ── Queue Summary ─────────────────────────────────────────

type Summary struct {
	Pending    int64 `json:"pending"`
	Leased     int64 `json:"leased"`
	Delivering int64 `json:"delivering"`
	Deferred   int64 `json:"deferred"`
	Delivered  int64 `json:"delivered"`
	Bounced    int64 `json:"bounced"`
	DeadLetter int64 `json:"dead_letter"`
	Cancelled  int64 `json:"cancelled"`
	Total      int64 `json:"total"`
}

// ── Queue Entry ───────────────────────────────────────────

type Entry struct {
	ID              uint       `json:"id"`
	MessageID       string     `json:"messageId"`
	FromAddress     string     `json:"fromAddress"`
	ToAddress       string     `json:"toAddress"`
	Status          string     `json:"status"`
	Direction       string     `json:"direction"`
	AttemptCount    int        `json:"attemptCount"`
	MaxAttempts     int        `json:"maxAttempts"`
	LastError       string     `json:"lastError,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	LastAttemptAt   *time.Time `json:"lastAttemptAt,omitempty"`
	NextAttemptAt   *time.Time `json:"nextAttemptAt,omitempty"`
}

// ── Delivery Attempt ──────────────────────────────────────

type Attempt struct {
	ID            uint      `json:"id"`
	AttemptNumber int       `json:"attemptNumber"`
	Status        string    `json:"status"`
	RemoteHost    string    `json:"remoteHost"`
	RemoteIP      string    `json:"remoteIp"`
	StatusCode    int       `json:"statusCode"`
	StatusMsg     string    `json:"statusMsg"`
	DurationMs    int64     `json:"durationMs"`
	TLSUsed       bool      `json:"tlsUsed"`
	AttemptedAt   time.Time `json:"attemptedAt"`
}

// ── List Response ─────────────────────────────────────────

type ListResponse struct {
	Entries []Entry `json:"entries"`
	Total   int64   `json:"total"`
}
