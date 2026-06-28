package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Rule is a per-mailbox filter rule. Conditions and Actions are
// stored as JSON blobs; the rules engine owns the JSON schema and
// validator. The storage layer is intentionally dumb about the
// blob content — it just round-trips opaque strings so the JSON
// shape can evolve without a schema migration.

// SetFlagValue is the per-flag payload returned by the rules
// engine. Only the supplied pointers are changed on the
// stored message — nil means "leave unchanged". Used by the
// engine output so the storage layer can apply only the
// fields the rule actually flipped.
type SetFlagValue struct {
	Seen    *bool `json:"seen,omitempty"`
	Flagged *bool `json:"flagged,omitempty"`
}
type Rule struct {
	ID             uint   `json:"id"`
	MailboxID      uint   `json:"mailbox_id"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	SortOrder      int    `json:"sort_order"`
	StopProcessing bool   `json:"stop_processing"`
	ConditionsJSON string `json:"conditions_json"`
	ActionsJSON    string `json:"actions_json"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// VacationConfig holds the per-mailbox vacation auto-reply
// configuration. UNIQUE(mailbox_id) ensures exactly one row per
// mailbox. The Engine reads StartAt / EndAt and only replies
// when the inbound timestamp falls inside the window (a nil
// bound means "open-ended on that side").
type VacationConfig struct {
	ID                   uint    `json:"id"`
	MailboxID            uint    `json:"mailbox_id"`
	Enabled              bool    `json:"enabled"`
	Subject              string  `json:"subject"`
	Body                 string  `json:"body"`
	StartAt              *string `json:"start_at,omitempty"`
	EndAt                *string `json:"end_at,omitempty"`
	ReplyIntervalSeconds int     `json:"reply_interval_seconds"`
	UpdatedAt            string  `json:"updated_at"`
}

// ForwardingConfig is the per-mailbox forwarding rule. Separate
// from Rule because the UI exposes forwarding on its own panel
// and the engine has a dedicated one-row evaluation path for it.
type ForwardingConfig struct {
	ID        uint   `json:"id"`
	MailboxID uint   `json:"mailbox_id"`
	Enabled   bool   `json:"enabled"`
	ForwardTo string `json:"forward_to"`
	KeepCopy  bool   `json:"keep_copy"`
	UpdatedAt string `json:"updated_at"`
}

// RulesRepository manages filter rules for one mailbox.
type RulesRepository interface {
	ListByMailbox(ctx context.Context, mailboxID uint) ([]*Rule, error)
	GetByID(ctx context.Context, mailboxID uint, id uint) (*Rule, error)
	Create(ctx context.Context, r *Rule) (*Rule, error)
	Update(ctx context.Context, mailboxID uint, id uint, patch *RulePatch) (*Rule, error)
	Delete(ctx context.Context, mailboxID uint, id uint) error
}

// RulePatch is the partial-update payload. nil pointers mean
// "leave unchanged"; non-nil pointers carry the new value.
// ConditionsJSON / ActionsJSON are validated at the handler
// layer before they reach the storage layer; the storage layer
// just stores what the handler hands it.
type RulePatch struct {
	Name           *string `json:"name,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
	SortOrder      *int    `json:"sort_order,omitempty"`
	StopProcessing *bool   `json:"stop_processing,omitempty"`
	ConditionsJSON *string `json:"conditions_json,omitempty"`
	ActionsJSON    *string `json:"actions_json,omitempty"`
}

// VacationRepository manages vacation auto-reply state.
type VacationRepository interface {
	GetOrCreate(ctx context.Context, mailboxID uint) (*VacationConfig, error)
	Update(ctx context.Context, mailboxID uint, patch *VacationPatch) (*VacationConfig, error)
	// LastRepliedAt returns the last_replied_at timestamp for
	// (mailboxID, senderEmail), or "" if no reply has been sent.
	LastRepliedAt(ctx context.Context, mailboxID uint, senderEmail string) (string, error)
	// RecordReply inserts or updates the (mailboxID, senderEmail)
	// timestamp to "now". Idempotent — repeated calls within the
	// rate-limit window do not produce duplicate replies because
	// the engine checks LastRepliedAt before calling RecordReply.
	RecordReply(ctx context.Context, mailboxID uint, senderEmail string) error
}

// VacationPatch is the partial-update payload for vacation
// configuration. The handler layer validates the body and the
// start/end timestamps; the storage layer just clamps and
// stores.
type VacationPatch struct {
	Enabled              *bool   `json:"enabled,omitempty"`
	Subject              *string `json:"subject,omitempty"`
	Body                 *string `json:"body,omitempty"`
	StartAt              *string `json:"start_at,omitempty"`
	EndAt                *string `json:"end_at,omitempty"`
	ReplyIntervalSeconds *int    `json:"reply_interval_seconds,omitempty"`
}

// ForwardingRepository manages the per-mailbox forwarding row.
type ForwardingRepository interface {
	GetOrCreate(ctx context.Context, mailboxID uint) (*ForwardingConfig, error)
	Update(ctx context.Context, mailboxID uint, patch *ForwardingPatch) (*ForwardingConfig, error)
}

// ForwardingPatch is the partial-update payload for forwarding.
type ForwardingPatch struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	ForwardTo *string `json:"forward_to,omitempty"`
	KeepCopy  *bool   `json:"keep_copy,omitempty"`
}

// ─── Rules ──────────────────────────────────────────────────

// NewRulesRepo wires the SQL implementation.
func NewRulesRepo(db *sql.DB) RulesRepository { return &rulesSQLRepo{db: db} }

type rulesSQLRepo struct{ db *sql.DB }

func (r *rulesSQLRepo) ListByMailbox(ctx context.Context, mailboxID uint) ([]*Rule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, mailbox_id, name, enabled, sort_order, stop_processing,
		 conditions_json, actions_json, created_at, updated_at
		 FROM coremail_rules WHERE mailbox_id = ? ORDER BY sort_order ASC, id ASC`, mailboxID)
	if err != nil {
		return nil, fmt.Errorf("rules list: %w", err)
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		rule, err := scanRuleRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (r *rulesSQLRepo) GetByID(ctx context.Context, mailboxID uint, id uint) (*Rule, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, mailbox_id, name, enabled, sort_order, stop_processing,
		 conditions_json, actions_json, created_at, updated_at
		 FROM coremail_rules WHERE id = ? AND mailbox_id = ?`, id, mailboxID)
	rule, err := scanRuleRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return rule, nil
}

func (r *rulesSQLRepo) Create(ctx context.Context, in *Rule) (*Rule, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_rules (mailbox_id, name, enabled, sort_order, stop_processing,
		 conditions_json, actions_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MailboxID, clampString(in.Name, 200), boolToInt(in.Enabled), in.SortOrder,
		boolToInt(in.StopProcessing), clampString(in.ConditionsJSON, 8192),
		clampString(in.ActionsJSON, 8192), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("rule create: %w", err)
	}
	id, _ := res.LastInsertId()
	return r.GetByID(ctx, in.MailboxID, uint(id))
}

func (r *rulesSQLRepo) Update(ctx context.Context, mailboxID uint, id uint, patch *RulePatch) (*Rule, error) {
	if patch == nil {
		return r.GetByID(ctx, mailboxID, id)
	}
	sets := []string{}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if patch.Name != nil {
		add("name", clampString(*patch.Name, 200))
	}
	if patch.Enabled != nil {
		add("enabled", boolToInt(*patch.Enabled))
	}
	if patch.SortOrder != nil {
		v := *patch.SortOrder
		if v < 0 {
			v = 0
		}
		add("sort_order", v)
	}
	if patch.StopProcessing != nil {
		add("stop_processing", boolToInt(*patch.StopProcessing))
	}
	if patch.ConditionsJSON != nil {
		add("conditions_json", clampString(*patch.ConditionsJSON, 8192))
	}
	if patch.ActionsJSON != nil {
		add("actions_json", clampString(*patch.ActionsJSON, 8192))
	}
	if len(sets) == 0 {
		return r.GetByID(ctx, mailboxID, id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, id, mailboxID)
	q := "UPDATE coremail_rules SET " + joinCommas(sets) + " WHERE id = ? AND mailbox_id = ?"
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("rule update: %w", err)
	}
	return r.GetByID(ctx, mailboxID, id)
}

func (r *rulesSQLRepo) Delete(ctx context.Context, mailboxID uint, id uint) error {
	// The ownership check is part of the WHERE clause so a
	// caller cannot delete a rule they do not own by guessing
	// the id.
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM coremail_rules WHERE id = ? AND mailbox_id = ?`, id, mailboxID)
	if err != nil {
		return fmt.Errorf("rule delete: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type scanner interface{ Scan(dest ...interface{}) error }

func scanRuleRow(s scanner) (*Rule, error) {
	var enabled, stop int
	var rule Rule
	if err := s.Scan(
		&rule.ID, &rule.MailboxID, &rule.Name, &enabled, &rule.SortOrder, &stop,
		&rule.ConditionsJSON, &rule.ActionsJSON, &rule.CreatedAt, &rule.UpdatedAt,
	); err != nil {
		return nil, err
	}
	rule.Enabled = enabled != 0
	rule.StopProcessing = stop != 0
	return &rule, nil
}

// ─── Vacation ──────────────────────────────────────────────

// NewVacationRepo wires the SQL implementation.
func NewVacationRepo(db *sql.DB) VacationRepository { return &vacationSQLRepo{db: db} }

type vacationSQLRepo struct{ db *sql.DB }

func (r *vacationSQLRepo) GetOrCreate(ctx context.Context, mailboxID uint) (*VacationConfig, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, mailbox_id, enabled, subject, body, start_at, end_at,
		 reply_interval_seconds, updated_at
		 FROM coremail_vacation WHERE mailbox_id = ?`, mailboxID)
	v, err := scanVacationRow(row)
	if err == nil {
		return v, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_vacation (mailbox_id, enabled, subject, body,
		 reply_interval_seconds, created_at, updated_at)
		 VALUES (?, 0, '', '', 86400, ?, ?)
		 ON CONFLICT(mailbox_id) DO NOTHING`,
		mailboxID, now, now,
	); err != nil {
		// Race-safe fallback: another goroutine inserted first.
		row := r.db.QueryRowContext(ctx,
			`SELECT id, mailbox_id, enabled, subject, body, start_at, end_at,
			 reply_interval_seconds, updated_at
			 FROM coremail_vacation WHERE mailbox_id = ?`, mailboxID)
		v, err := scanVacationRow(row)
		if err != nil {
			return nil, fmt.Errorf("vacation insert/read race: %w", err)
		}
		return v, nil
	}
	row = r.db.QueryRowContext(ctx,
		`SELECT id, mailbox_id, enabled, subject, body, start_at, end_at,
		 reply_interval_seconds, updated_at
		 FROM coremail_vacation WHERE mailbox_id = ?`, mailboxID)
	v, err = scanVacationRow(row)
	if err != nil {
		return nil, fmt.Errorf("vacation read after insert: %w", err)
	}
	return v, nil
}

func (r *vacationSQLRepo) Update(ctx context.Context, mailboxID uint, patch *VacationPatch) (*VacationConfig, error) {
	if patch == nil {
		return r.GetOrCreate(ctx, mailboxID)
	}
	// Materialise the row first. See forwarding.Update
	// for the rationale (UPDATE-then-GetOrCreate
	// silently drops the patch on the first call).
	if _, err := r.GetOrCreate(ctx, mailboxID); err != nil {
		return nil, fmt.Errorf("vacation materialise: %w", err)
	}
	sets := []string{}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if patch.Enabled != nil {
		add("enabled", boolToInt(*patch.Enabled))
	}
	if patch.Subject != nil {
		// Defence against header injection: the subject is
		// written verbatim into "Subject:" by the runner's
		// buildVacationRfc822. A CR, LF, or NUL inside the
		// user-supplied subject would let an attacker forge
		// additional headers in the auto-reply. Reject the
		// patch instead of clamping — the user needs to see
		// the error rather than have us silently truncate
		// the message body of their reply.
		if err := validateVacationSubject(*patch.Subject); err != nil {
			return nil, err
		}
		add("subject", clampString(*patch.Subject, 256))
	}
	if patch.Body != nil {
		// Cap at 4 KB — vacation bodies are user-authored
		// free-form text; 4 KB is generous for an auto-reply.
		v := *patch.Body
		if len(v) > 4096 {
			v = v[:4096]
		}
		add("body", v)
	}
	if patch.StartAt != nil {
		if *patch.StartAt == "" {
			add("start_at", nil)
		} else {
			add("start_at", *patch.StartAt)
		}
	}
	if patch.EndAt != nil {
		if *patch.EndAt == "" {
			add("end_at", nil)
		} else {
			add("end_at", *patch.EndAt)
		}
	}
	if patch.ReplyIntervalSeconds != nil {
		v := *patch.ReplyIntervalSeconds
		if v < 60 {
			v = 60 // never less than 1 minute to avoid floods
		}
		if v > 30*86400 {
			v = 30 * 86400 // never more than 30 days
		}
		add("reply_interval_seconds", v)
	}
	if len(sets) == 0 {
		return r.GetOrCreate(ctx, mailboxID)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, mailboxID)
	q := "UPDATE coremail_vacation SET " + joinCommas(sets) + " WHERE mailbox_id = ?"
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("vacation update: %w", err)
	}
	return r.GetOrCreate(ctx, mailboxID)
}

func (r *vacationSQLRepo) LastRepliedAt(ctx context.Context, mailboxID uint, senderEmail string) (string, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT last_replied_at FROM coremail_vacation_history
		 WHERE mailbox_id = ? AND sender_email = ? LIMIT 1`,
		mailboxID, senderEmail)
	var ts string
	if err := row.Scan(&ts); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("vacation history: %w", err)
	}
	return ts, nil
}

func (r *vacationSQLRepo) RecordReply(ctx context.Context, mailboxID uint, senderEmail string) error {
	return r.recordReplyAt(ctx, mailboxID, senderEmail, time.Now().UTC().Format(time.RFC3339))
}

// recordReplyAt is the same as RecordReply but with a
// caller-supplied timestamp. It exists so tests can pin a
// specific last_replied_at value when checking the UPSERT
// behaviour. Production callers go through RecordReply,
// which always uses time.Now().

func (r *vacationSQLRepo) recordReplyAt(ctx context.Context, mailboxID uint, senderEmail string, lastRepliedAt string) error {
	// Atomic upsert against the UNIQUE(mailbox_id,
	// sender_email) index. This replaces the previous
	// delete-then-insert pair which was racy: two
	// concurrent inbound messages from the same sender
	// could both pass LastRepliedAt, both reach
	// RecordReply, and each would delete-then-insert a
	// row of its own — the second INSERT could land in
	// a window where the first was already committed,
	// but the runner had no way to observe it because
	// LastRepliedAt ran before any of this. The net
	// effect was that the rate limit was bypassed for
	// one out of every concurrent pair, and the recipient
	// could receive duplicate vacation replies.
	//
	// The new contract:
	//   - INSERT a new row if (mailbox, sender) is unseen.
	//   - UPDATE last_replied_at if the row already
	//     exists. The LastRepliedAt check BEFORE this
	//     call is what enforces the rate window; this
	//     call is the side that "wins" the race.
	//   - The whole operation is one statement, so
	//     concurrent calls cannot interleave.
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_vacation_history
		   (mailbox_id, sender_email, last_replied_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(mailbox_id, sender_email) DO UPDATE
		   SET last_replied_at = excluded.last_replied_at`,
		mailboxID, senderEmail, lastRepliedAt,
	); err != nil {
		return fmt.Errorf("vacation record (upsert): %w", err)
	}
	return nil
}

func scanVacationRow(s scanner) (*VacationConfig, error) {
	var enabled int
	var subject, body, updatedAt string
	var startAt, endAt sql.NullString
	var replyInterval int
	var v VacationConfig
	if err := s.Scan(&v.ID, &v.MailboxID, &enabled, &subject, &body,
		&startAt, &endAt, &replyInterval, &updatedAt); err != nil {
		return nil, err
	}
	v.Enabled = enabled != 0
	v.Subject = subject
	v.Body = body
	v.ReplyIntervalSeconds = replyInterval
	v.UpdatedAt = updatedAt
	if startAt.Valid {
		s := startAt.String
		v.StartAt = &s
	}
	if endAt.Valid {
		s := endAt.String
		v.EndAt = &s
	}
	return &v, nil
}

// ─── Forwarding ─────────────────────────────────────────────

// NewForwardingRepo wires the SQL implementation.
func NewForwardingRepo(db *sql.DB) ForwardingRepository { return &forwardingSQLRepo{db: db} }

type forwardingSQLRepo struct{ db *sql.DB }

func (r *forwardingSQLRepo) GetOrCreate(ctx context.Context, mailboxID uint) (*ForwardingConfig, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, mailbox_id, enabled, forward_to, keep_copy, updated_at
		 FROM coremail_forwarding WHERE mailbox_id = ?`, mailboxID)
	f, err := scanForwardingRow(row)
	if err == nil {
		return f, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_forwarding (mailbox_id, enabled, forward_to, keep_copy, created_at, updated_at)
		 VALUES (?, 0, '', 1, ?, ?)
		 ON CONFLICT(mailbox_id) DO NOTHING`,
		mailboxID, now, now,
	); err != nil {
		row := r.db.QueryRowContext(ctx,
			`SELECT id, mailbox_id, enabled, forward_to, keep_copy, updated_at
			 FROM coremail_forwarding WHERE mailbox_id = ?`, mailboxID)
		f, err := scanForwardingRow(row)
		if err != nil {
			return nil, fmt.Errorf("forwarding insert/read race: %w", err)
		}
		return f, nil
	}
	row = r.db.QueryRowContext(ctx,
		`SELECT id, mailbox_id, enabled, forward_to, keep_copy, updated_at
		 FROM coremail_forwarding WHERE mailbox_id = ?`, mailboxID)
	f, err = scanForwardingRow(row)
	if err != nil {
		return nil, fmt.Errorf("forwarding read after insert: %w", err)
	}
	return f, nil
}

func (r *forwardingSQLRepo) Update(ctx context.Context, mailboxID uint, patch *ForwardingPatch) (*ForwardingConfig, error) {
	if patch == nil {
		return r.GetOrCreate(ctx, mailboxID)
	}
	// Materialise the row first. The original code did
	// UPDATE-then-GetOrCreate which on the first call
	// matches 0 rows and silently drops the patch
	// (GetOrCreate then inserts defaults). GetOrCreate
	// first guarantees the UPDATE has a row to land on.
	if _, err := r.GetOrCreate(ctx, mailboxID); err != nil {
		return nil, fmt.Errorf("forwarding materialise: %w", err)
	}
	sets := []string{}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if patch.Enabled != nil {
		add("enabled", boolToInt(*patch.Enabled))
	}
	if patch.ForwardTo != nil {
		add("forward_to", clampString(*patch.ForwardTo, 254))
	}
	if patch.KeepCopy != nil {
		add("keep_copy", boolToInt(*patch.KeepCopy))
	}
	if len(sets) == 0 {
		return r.GetOrCreate(ctx, mailboxID)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339))
	args = append(args, mailboxID)
	q := "UPDATE coremail_forwarding SET " + joinCommas(sets) + " WHERE mailbox_id = ?"
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("forwarding update: %w", err)
	}
	return r.GetOrCreate(ctx, mailboxID)
}

func scanForwardingRow(s scanner) (*ForwardingConfig, error) {
	var enabled, keep int
	var f ForwardingConfig
	var forwardTo, updatedAt string
	if err := s.Scan(&f.ID, &f.MailboxID, &enabled, &forwardTo, &keep, &updatedAt); err != nil {
		return nil, err
	}
	f.Enabled = enabled != 0
	f.ForwardTo = forwardTo
	f.KeepCopy = keep != 0
	f.UpdatedAt = updatedAt
	return &f, nil
}

// joinCommas is a tiny helper used by the partial-update SQL
// builders above. It avoids importing strings just for
// strings.Join.
func joinCommas(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += ", " + parts[i]
	}
	return out
}

// validateVacationSubject rejects subjects that could be
// used to inject extra RFC 5322 headers into a vacation
// auto-reply. The runner writes the subject verbatim into
// "Subject: <subject>\r\n"; a CR, LF, or NUL inside the
// value would let an attacker forge arbitrary headers.
//
// We also reject ASCII control characters in the C0 range
// (other than tab, which is unlikely in a subject) so the
// reply is well-formed even after folding. Unicode is
// permitted (subjects are decoded as UTF-8 bytes).
func validateVacationSubject(s string) error {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\r':
			return fmt.Errorf("vacation subject contains CR (position %d): header injection", i)
		case c == '\n':
			return fmt.Errorf("vacation subject contains LF (position %d): header injection", i)
		case c == 0:
			return fmt.Errorf("vacation subject contains NUL (position %d): header injection", i)
		case c < 0x20 && c != '\t':
			return fmt.Errorf("vacation subject contains control byte 0x%02X (position %d)", c, i)
		case c == 0x7f:
			return fmt.Errorf("vacation subject contains DEL (position %d)", i)
		}
	}
	return nil
}