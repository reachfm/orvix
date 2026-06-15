package jmap

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/policy"
)

// ── JMAP Protocol Types ────────────────────────────────────

type Request struct {
	Using       []string          `json:"using"`
	MethodCalls []json.RawMessage `json:"methodCalls"`
}

type MethodCall struct {
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params"`
	ID     string          `json:"id"`
}

type Response struct {
	MethodResponses []MethodResponse `json:"methodResponses"`
	SessionState    string           `json:"sessionState"`
}

type MethodResponse struct {
	Name   string      `json:"name"`
	Params interface{} `json:"params"`
	ID     string      `json:"id"`
}

type ErrorResponse struct {
	Type   string `json:"type"`
	Detail string `json:"detail,omitempty"`
}

// ── JMAP Types ──────────────────────────────────────────────

type Session struct {
	Capabilities    map[string]interface{} `json:"capabilities"`
	Accounts        map[string]*Account    `json:"accounts"`
	PrimaryAccounts map[string]string      `json:"primaryAccounts"`
	Username        string                 `json:"username"`
	APITURL         string                 `json:"apiUrl"`
	DownloadURL     string                 `json:"downloadUrl"`
	UploadURL       string                 `json:"uploadUrl"`
	EventSourceURL  string                 `json:"eventSourceUrl"`
	State           string                 `json:"state"`
}

type Account struct {
	Name                string                 `json:"name"`
	IsPersonal          bool                   `json:"isPersonal"`
	IsReadOnly          bool                   `json:"isReadOnly"`
	AccountCapabilities map[string]interface{} `json:"accountCapabilities"`
}

// ── Server ───────────────────────────────────────────────────

type Server struct {
	Engine         *coremail.Engine
	MailStore      *storage.MailStore
	Observability  *observability.Observability
	Hostname       string
	AllowedOrigins []string

	mu   sync.Mutex
	srv  *http.Server
	mux  *http.ServeMux
	done chan struct{}

	queueEngine interface {
		Enqueue(ctx context.Context, entry *queue.QueueEntry) error
	}
	trustEngine  interface{ IsLockedOut(key string) bool }
	policyEngine interface {
		Evaluate(req *policy.EvaluationRequest) *policy.EvaluationResult
	}
	RecordSession       func(ctx context.Context, mailboxID uint, ip, userAgent string) error
	RecordLoginActivity func(ctx context.Context, mailboxID uint, success bool, ip, userAgent string) error
}

// ── Mailbox Types ───────────────────────────────────────────

type MailboxGetRequest struct {
	AccountID string   `json:"accountId"`
	IDs       []string `json:"ids,omitempty"`
}

type MailboxGetResponse struct {
	AccountID string         `json:"accountId"`
	State     string         `json:"state"`
	List      []MailboxEntry `json:"list"`
	NotFound  []string       `json:"notFound"`
}

type MailboxEntry struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	ParentID      *string        `json:"parentId"`
	Role          *string        `json:"role"`
	SortOrder     int            `json:"sortOrder"`
	TotalEmails   int            `json:"totalEmails"`
	UnreadEmails  int            `json:"unreadEmails"`
	TotalThreads  int            `json:"totalThreads"`
	UnreadThreads int            `json:"unreadThreads"`
	MyRights      *MailboxRights `json:"myRights"`
}

type MailboxRights struct {
	MayRead        bool `json:"mayRead"`
	MayReadItems   bool `json:"mayReadItems"`
	MayRemoveItems bool `json:"mayRemoveItems"`
	MaySetSeen     bool `json:"maySetSeen"`
	MaySetKeywords bool `json:"maySetKeywords"`
}

type MailboxQueryRequest struct {
	AccountID      string              `json:"accountId"`
	Filter         *MailboxQueryFilter `json:"filter,omitempty"`
	Sort           []*MailboxQuerySort `json:"sort,omitempty"`
	Position       int                 `json:"position,omitempty"`
	Limit          *int                `json:"limit,omitempty"`
	CalculateTotal bool                `json:"calculateTotal,omitempty"`
}

type MailboxQueryFilter struct {
	Role     *string `json:"role,omitempty"`
	ParentID *string `json:"parentId,omitempty"`
	Name     string  `json:"name,omitempty"`
}

type MailboxQuerySort struct {
	Property    string `json:"property"`
	IsAscending *bool  `json:"isAscending,omitempty"`
}

type MailboxQueryResponse struct {
	AccountID           string   `json:"accountId"`
	QueryState          string   `json:"queryState"`
	CanCalculateChanges bool     `json:"canCalculateChanges"`
	Position            int      `json:"position"`
	IDs                 []string `json:"ids"`
	Total               *int     `json:"total,omitempty"`
}

type MailboxChangesRequest struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges *int   `json:"maxChanges,omitempty"`
}

type MailboxChangesResponse struct {
	AccountID      string   `json:"accountId"`
	OldState       string   `json:"oldState"`
	NewState       string   `json:"newState"`
	HasMoreChanges bool     `json:"hasMoreChanges"`
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Destroyed      []string `json:"destroyed"`
}

// ── Email Types ─────────────────────────────────────────────

type EmailGetRequest struct {
	AccountID  string   `json:"accountId"`
	IDs        []string `json:"ids,omitempty"`
	Properties []string `json:"properties,omitempty"`
}

type EmailGetResponse struct {
	AccountID string       `json:"accountId"`
	State     string       `json:"state"`
	List      []EmailEntry `json:"list"`
	NotFound  []string     `json:"notFound"`
}

type EmailEntry struct {
	ID              string            `json:"id"`
	MailboxIDs      map[string]bool   `json:"mailboxIds"`
	Keywords        map[string]bool   `json:"keywords"`
	Size            int               `json:"size"`
	ReceivedAt      string            `json:"receivedAt"`
	MessageID       string            `json:"messageId"`
	InReplyTo       *string           `json:"inReplyTo"`
	References      *string           `json:"references"`
	Sender          []*EmailAddress   `json:"sender"`
	From            []*EmailAddress   `json:"from"`
	To              []*EmailAddress   `json:"to"`
	Cc              []*EmailAddress   `json:"cc"`
	Bcc             []*EmailAddress   `json:"bcc"`
	ReplyTo         []*EmailAddress   `json:"replyTo"`
	Subject         string            `json:"subject"`
	SentAt          string            `json:"sentAt"`
	Preview         string            `json:"preview"`
	HasAttachment   bool              `json:"hasAttachment"`
	Attachments     []*AttachmentInfo `json:"attachments,omitempty"`
	TextBody        string            `json:"textBody,omitempty"`
	HTMLBody        string            `json:"htmlBody,omitempty"`
	HasHTML         bool              `json:"hasHTML"`
	HasRemoteImages bool              `json:"hasRemoteImages,omitempty"`
}

type AttachmentInfo struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	IsInline    bool   `json:"isInline"`
}

type EmailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type EmailQueryRequest struct {
	AccountID      string            `json:"accountId"`
	Filter         *EmailQueryFilter `json:"filter,omitempty"`
	Sort           []*EmailQuerySort `json:"sort,omitempty"`
	Position       int               `json:"position,omitempty"`
	Anchor         *string           `json:"anchor,omitempty"`
	AnchorOffset   int               `json:"anchorOffset,omitempty"`
	Limit          *int              `json:"limit,omitempty"`
	CalculateTotal bool              `json:"calculateTotal,omitempty"`
}

type EmailQueryFilter struct {
	InMailbox  []string `json:"inMailbox,omitempty"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	Text       string   `json:"text,omitempty"`
	Before     string   `json:"before,omitempty"`
	After      string   `json:"after,omitempty"`
	HasKeyword *string  `json:"hasKeyword,omitempty"`
	NotKeyword *string  `json:"notKeyword,omitempty"`
}

type EmailQuerySort struct {
	Property    string `json:"property"`
	IsAscending *bool  `json:"isAscending,omitempty"`
}

type EmailQueryResponse struct {
	AccountID           string   `json:"accountId"`
	QueryState          string   `json:"queryState"`
	CanCalculateChanges bool     `json:"canCalculateChanges"`
	Position            int      `json:"position"`
	IDs                 []string `json:"ids"`
	Total               *int     `json:"total,omitempty"`
	Limit               *int     `json:"limit,omitempty"`
}

type EmailChangesRequest struct {
	AccountID  string `json:"accountId"`
	SinceState string `json:"sinceState"`
	MaxChanges *int   `json:"maxChanges,omitempty"`
}

type EmailChangesResponse struct {
	AccountID      string   `json:"accountId"`
	OldState       string   `json:"oldState"`
	NewState       string   `json:"newState"`
	HasMoreChanges bool     `json:"hasMoreChanges"`
	Created        []string `json:"created"`
	Updated        []string `json:"updated"`
	Destroyed      []string `json:"destroyed"`
}

type EmailSetRequest struct {
	AccountID string                    `json:"accountId"`
	Create    map[string]*EmailCreate   `json:"create,omitempty"`
	Update    map[string]*EmailSetPatch `json:"update,omitempty"`
	Destroy   []string                  `json:"destroy,omitempty"`
}

type EmailCreateAttachment struct {
	BlobID      string `json:"blobId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

type EmailCreate struct {
	MailboxIDs  map[string]bool          `json:"mailboxIds"`
	Keywords    map[string]bool          `json:"keywords,omitempty"`
	Subject     string                   `json:"subject,omitempty"`
	From        *EmailAddress            `json:"from,omitempty"`
	To          []*EmailAddress          `json:"to,omitempty"`
	Cc          []*EmailAddress          `json:"cc,omitempty"`
	Bcc         []*EmailAddress          `json:"bcc,omitempty"`
	Body        string                   `json:"body,omitempty"`
	Attachments []*EmailCreateAttachment `json:"attachments,omitempty"`
}

type EmailSetPatch struct {
	Keywords   map[string]*bool `json:"keywords,omitempty"`
	MailboxIDs map[string]*bool `json:"mailboxIds,omitempty"`
}

type EmailSetResponse struct {
	AccountID    string                  `json:"accountId"`
	OldState     string                  `json:"oldState"`
	NewState     string                  `json:"newState"`
	Created      map[string]string       `json:"created,omitempty"`
	Updated      map[string]*interface{} `json:"updated,omitempty"`
	Destroyed    []string                `json:"destroyed,omitempty"`
	NotCreated   map[string]string       `json:"notCreated,omitempty"`
	NotUpdated   map[string]string       `json:"notUpdated,omitempty"`
	NotDestroyed map[string]string       `json:"notDestroyed,omitempty"`
}

// ── Submission Types ────────────────────────────────────────

type SubmissionSetRequest struct {
	AccountID string                       `json:"accountId"`
	Create    map[string]*SubmissionCreate `json:"create,omitempty"`
}

type SubmissionCreate struct {
	EmailID string `json:"emailId"`
	Sender  string `json:"sender,omitempty"`
}

type SubmissionSetResponse struct {
	AccountID  string                        `json:"accountId"`
	Created    map[string]*SubmissionCreated `json:"created,omitempty"`
	NotCreated map[string]string             `json:"notCreated,omitempty"`
}

type SubmissionCreated struct {
	ID string `json:"id"`
}
