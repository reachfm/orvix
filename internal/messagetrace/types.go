package messagetrace

import "time"

// TraceResult represents a single matched message in the trace.
type TraceResult struct {
	ID          uint      `json:"id"`
	MessageID   string    `json:"messageId"`
	FromAddress string    `json:"fromAddress"`
	ToAddress   string    `json:"toAddress"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"createdAt"`
}

// SearchResponse contains the trace search results.
type SearchResponse struct {
	Results []TraceResult `json:"results"`
	Total   int64         `json:"total"`
}

// TimelineEvent represents a single event in the delivery timeline.
type TimelineEvent struct {
	Time    time.Time `json:"time"`
	Event   string    `json:"event"`
	Detail  string    `json:"detail,omitempty"`
}

// DeliveryAttemptInfo contains details about a delivery attempt.
type DeliveryAttemptInfo struct {
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

// TraceDetail is the full trace information for a single queue entry.
type TraceDetail struct {
	Entry    TraceResult          `json:"entry"`
	Attempts []DeliveryAttemptInfo `json:"attempts"`
	Timeline []TimelineEvent      `json:"timeline"`
	LastError string              `json:"lastError,omitempty"`
}
