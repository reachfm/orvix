package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// UserSettings holds the per-mailbox user preferences. Defaults
// are applied at the storage layer so a freshly created mailbox
// always has a usable row without the caller having to write one.
//
// Every field has a documented default. The "language" default
// is "en" â€” the UI strings are written in English and the
// webmail SPA does not yet ship a translation bundle; a future
// patch can add bundles without changing this storage contract.
type UserSettings struct {
	MailboxID            uint   `json:"mailbox_id"`
	DisplayName          string `json:"display_name"`
	Timezone             string `json:"timezone"`
	Language             string `json:"language"`
	DateFormat           string `json:"date_format"`
	TimeFormat           string `json:"time_format"`
	TextDirection        string `json:"text_direction"`
	Theme                string `json:"theme"`
	Density              string `json:"density"`
	PreviewLines         int    `json:"preview_lines"`
	ReadingPane          string `json:"reading_pane"`
	SignatureEnabled     bool   `json:"signature_enabled"`
	SignatureText        string `json:"signature_text"`
	SignatureInReplies   bool   `json:"signature_in_replies"`
	DefaultReplyMode     string `json:"default_reply_mode"`
	AutosaveSeconds      int    `json:"autosave_seconds"`
	ConfirmBeforeDiscard bool   `json:"confirm_before_discard"`
	WarnOnEmptySubject   bool   `json:"warn_on_empty_subject"`
	DefaultFolder        string `json:"default_folder"`
	MarkReadDelaySeconds int    `json:"mark_read_delay_seconds"`
	SenderDisplay        string `json:"sender_display"`
	NotifyInApp          bool   `json:"notify_inapp"`
	NotifyPush           bool   `json:"notify_push"`
	UpdatedAt            string `json:"updated_at"`
}

// DefaultUserSettings returns the safe defaults for a new mailbox.
func DefaultUserSettings(mailboxID uint) *UserSettings {
	return &UserSettings{
		MailboxID:            mailboxID,
		DisplayName:          "",
		Timezone:             "",
		Language:             "en",
		DateFormat:           "locale",
		TimeFormat:           "locale",
		TextDirection:        "auto",
		Theme:                "dark",
		Density:              "comfortable",
		PreviewLines:         2,
		ReadingPane:          "right",
		SignatureEnabled:     false,
		SignatureText:        "",
		SignatureInReplies:   true,
		DefaultReplyMode:     "reply",
		AutosaveSeconds:      3,
		ConfirmBeforeDiscard: true,
		WarnOnEmptySubject:   false,
		DefaultFolder:        "INBOX",
		MarkReadDelaySeconds: 0,
		SenderDisplay:        "name",
		NotifyInApp:          true,
		NotifyPush:           true,
	}
}

// UserSettingsRepository manages per-mailbox settings rows.
//
// GetOrCreate returns the existing row or inserts a defaults row.
// The contract is: callers always receive a non-nil *UserSettings
// for any mailbox the caller is allowed to read. This matches the
// expectation that Settings UI loads with current values and only
// surfaces changes when the user actually edited them.
type UserSettingsRepository interface {
	GetOrCreate(ctx context.Context, mailboxID uint) (*UserSettings, error)
	Update(ctx context.Context, mailboxID uint, patch *UserSettingsPatch) (*UserSettings, error)
}

// UserSettingsPatch is a partial-update payload. nil pointers mean
// "leave unchanged"; non-nil pointers carry the new value. Validation
// happens in the handler so the storage layer stays a pure CRUD.
type UserSettingsPatch struct {
	DisplayName          *string `json:"display_name,omitempty"`
	Timezone             *string `json:"timezone,omitempty"`
	Language             *string `json:"language,omitempty"`
	DateFormat           *string `json:"date_format,omitempty"`
	TimeFormat           *string `json:"time_format,omitempty"`
	TextDirection        *string `json:"text_direction,omitempty"`
	Theme                *string `json:"theme,omitempty"`
	Density              *string `json:"density,omitempty"`
	PreviewLines         *int    `json:"preview_lines,omitempty"`
	ReadingPane          *string `json:"reading_pane,omitempty"`
	SignatureEnabled     *bool   `json:"signature_enabled,omitempty"`
	SignatureText        *string `json:"signature_text,omitempty"`
	SignatureInReplies   *bool   `json:"signature_in_replies,omitempty"`
	DefaultReplyMode     *string `json:"default_reply_mode,omitempty"`
	AutosaveSeconds      *int    `json:"autosave_seconds,omitempty"`
	ConfirmBeforeDiscard *bool   `json:"confirm_before_discard,omitempty"`
	WarnOnEmptySubject   *bool   `json:"warn_on_empty_subject,omitempty"`
	DefaultFolder        *string `json:"default_folder,omitempty"`
	MarkReadDelaySeconds *int    `json:"mark_read_delay_seconds,omitempty"`
	SenderDisplay        *string `json:"sender_display,omitempty"`
	NotifyInApp          *bool   `json:"notify_inapp,omitempty"`
	NotifyPush           *bool   `json:"notify_push,omitempty"`
}

// userSettingsCols lists the columns in canonical order. Used by
// scanRow + the SELECT/INSERT/UPDATE statements below.
const userSettingsCols = `
	mailbox_id, display_name, timezone, language, date_format, time_format, text_direction,
	theme, density, preview_lines, reading_pane,
	signature_enabled, signature_text, signature_in_replies, default_reply_mode,
	autosave_seconds, confirm_before_discard, warn_on_empty_subject,
	default_folder, mark_read_delay_seconds, sender_display,
	notify_inapp, notify_push, updated_at`

// NewUserSettingsRepo wires the SQL implementation.
func NewUserSettingsRepo(db *sql.DB) UserSettingsRepository {
	return &userSettingsSQLRepo{db: db}
}

type userSettingsSQLRepo struct {
	db *sql.DB
}

func (r *userSettingsSQLRepo) GetOrCreate(ctx context.Context, mailboxID uint) (*UserSettings, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+userSettingsCols+" FROM coremail_user_settings WHERE mailbox_id = ?", mailboxID)
	s, err := scanUserSettingsRow(row)
	if err == nil {
		return s, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, ierr := r.db.ExecContext(ctx,
		`INSERT INTO coremail_user_settings (mailbox_id, updated_at, created_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(mailbox_id) DO NOTHING`,
		mailboxID, now, now,
	)
	if ierr != nil {
		// Race-safe fallback: another goroutine inserted in parallel.
		// Re-read instead of failing.
		row := r.db.QueryRowContext(ctx,
			"SELECT "+userSettingsCols+" FROM coremail_user_settings WHERE mailbox_id = ?", mailboxID)
		s, err := scanUserSettingsRow(row)
		if err != nil {
			return nil, fmt.Errorf("settings insert/read race: %w", err)
		}
		return s, nil
	}
	row = r.db.QueryRowContext(ctx,
		"SELECT "+userSettingsCols+" FROM coremail_user_settings WHERE mailbox_id = ?", mailboxID)
	s, err = scanUserSettingsRow(row)
	if err != nil {
		return nil, fmt.Errorf("settings read after insert: %w", err)
	}
	return s, nil
}

// Update applies a partial patch atomically and returns the post-update row.
// Returns (nil, ErrMailboxNotFound) if no row exists for the mailbox â€”
// the caller can decide to GetOrCreate first or surface 404 to the client.
// In practice the handler always calls GetOrCreate before Update.
func (r *userSettingsSQLRepo) Update(ctx context.Context, mailboxID uint, patch *UserSettingsPatch) (*UserSettings, error) {
	if patch == nil {
		return r.GetOrCreate(ctx, mailboxID)
	}
	sets := []string{}
	args := []interface{}{}
	add := func(col string, v interface{}) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if patch.DisplayName != nil {
		add("display_name", clampString(*patch.DisplayName, 200))
	}
	if patch.Timezone != nil {
		add("timezone", clampString(*patch.Timezone, 64))
	}
	if patch.Language != nil {
		add("language", clampString(*patch.Language, 16))
	}
	if patch.DateFormat != nil {
		add("date_format", clampString(*patch.DateFormat, 32))
	}
	if patch.TimeFormat != nil {
		add("time_format", clampString(*patch.TimeFormat, 32))
	}
	if patch.TextDirection != nil {
		add("text_direction", clampString(*patch.TextDirection, 16))
	}
	if patch.Theme != nil {
		add("theme", clampString(*patch.Theme, 16))
	}
	if patch.Density != nil {
		add("density", clampString(*patch.Density, 16))
	}
	if patch.PreviewLines != nil {
		v := *patch.PreviewLines
		if v < 0 {
			v = 0
		}
		if v > 6 {
			v = 6
		}
		add("preview_lines", v)
	}
	if patch.ReadingPane != nil {
		add("reading_pane", clampString(*patch.ReadingPane, 16))
	}
	if patch.SignatureEnabled != nil {
		add("signature_enabled", settingsBoolToInt(*patch.SignatureEnabled))
	}
	if patch.SignatureText != nil {
		// Signature text can include CRLF; cap at 4 KB so a hostile client
		// cannot push a 1 MB signature into the row.
		s := *patch.SignatureText
		if len(s) > 4096 {
			s = s[:4096]
		}
		add("signature_text", s)
	}
	if patch.SignatureInReplies != nil {
		add("signature_in_replies", settingsBoolToInt(*patch.SignatureInReplies))
	}
	if patch.DefaultReplyMode != nil {
		add("default_reply_mode", clampString(*patch.DefaultReplyMode, 16))
	}
	if patch.AutosaveSeconds != nil {
		v := *patch.AutosaveSeconds
		if v < 0 {
			v = 0
		}
		if v > 60 {
			v = 60
		}
		add("autosave_seconds", v)
	}
	if patch.ConfirmBeforeDiscard != nil {
		add("confirm_before_discard", settingsBoolToInt(*patch.ConfirmBeforeDiscard))
	}
	if patch.WarnOnEmptySubject != nil {
		add("warn_on_empty_subject", settingsBoolToInt(*patch.WarnOnEmptySubject))
	}
	if patch.DefaultFolder != nil {
		add("default_folder", clampString(*patch.DefaultFolder, 64))
	}
	if patch.MarkReadDelaySeconds != nil {
		v := *patch.MarkReadDelaySeconds
		if v < 0 {
			v = 0
		}
		if v > 60 {
			v = 60
		}
		add("mark_read_delay_seconds", v)
	}
	if patch.SenderDisplay != nil {
		add("sender_display", clampString(*patch.SenderDisplay, 16))
	}
	if patch.NotifyInApp != nil {
		add("notify_inapp", settingsBoolToInt(*patch.NotifyInApp))
	}
	if patch.NotifyPush != nil {
		add("notify_push", settingsBoolToInt(*patch.NotifyPush))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets = append(sets, "updated_at = ?")
	args = append(args, now)
	args = append(args, mailboxID)

	q := "UPDATE coremail_user_settings SET " + strings.Join(sets, ", ") + " WHERE mailbox_id = ?"
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("settings update: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		// No row yet for this mailbox â€” insert defaults with the patch applied.
		// This keeps the contract simple: PUT on an empty mailbox row still works.
		def := DefaultUserSettings(mailboxID)
		applyPatchToDefaults(def, patch)
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO coremail_user_settings (`+userSettingsCols+`, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			def.MailboxID, def.DisplayName, def.Timezone, def.Language, def.DateFormat, def.TimeFormat, def.TextDirection,
			def.Theme, def.Density, def.PreviewLines, def.ReadingPane,
			settingsBoolToInt(def.SignatureEnabled), def.SignatureText, settingsBoolToInt(def.SignatureInReplies), def.DefaultReplyMode,
			def.AutosaveSeconds, settingsBoolToInt(def.ConfirmBeforeDiscard), settingsBoolToInt(def.WarnOnEmptySubject),
			def.DefaultFolder, def.MarkReadDelaySeconds, def.SenderDisplay,
			settingsBoolToInt(def.NotifyInApp), settingsBoolToInt(def.NotifyPush), now, now,
		)
		if err != nil {
			return nil, fmt.Errorf("settings insert after empty update: %w", err)
		}
	}
	return r.GetOrCreate(ctx, mailboxID)
}

func scanUserSettingsRow(row *sql.Row) (*UserSettings, error) {
	s := &UserSettings{}
	var sigEnabled, sigInReplies, confirmDiscard, warnEmpty, notifyInApp, notifyPush int
	var updatedAt string
	err := row.Scan(
		&s.MailboxID, &s.DisplayName, &s.Timezone, &s.Language, &s.DateFormat, &s.TimeFormat, &s.TextDirection,
		&s.Theme, &s.Density, &s.PreviewLines, &s.ReadingPane,
		&sigEnabled, &s.SignatureText, &sigInReplies, &s.DefaultReplyMode,
		&s.AutosaveSeconds, &confirmDiscard, &warnEmpty,
		&s.DefaultFolder, &s.MarkReadDelaySeconds, &s.SenderDisplay,
		&notifyInApp, &notifyPush, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.SignatureEnabled = sigEnabled != 0
	s.SignatureInReplies = sigInReplies != 0
	s.ConfirmBeforeDiscard = confirmDiscard != 0
	s.WarnOnEmptySubject = warnEmpty != 0
	s.NotifyInApp = notifyInApp != 0
	s.NotifyPush = notifyPush != 0
	s.UpdatedAt = updatedAt
	return s, nil
}

// applyPatchToDefaults writes the patch values onto a freshly defaulted
// struct so we can INSERT a complete row in one statement. Used when the
// mailbox has no settings row yet.
func applyPatchToDefaults(d *UserSettings, patch *UserSettingsPatch) {
	if patch.DisplayName != nil {
		d.DisplayName = clampString(*patch.DisplayName, 200)
	}
	if patch.Timezone != nil {
		d.Timezone = clampString(*patch.Timezone, 64)
	}
	if patch.Language != nil {
		d.Language = clampString(*patch.Language, 16)
	}
	if patch.DateFormat != nil {
		d.DateFormat = clampString(*patch.DateFormat, 32)
	}
	if patch.TimeFormat != nil {
		d.TimeFormat = clampString(*patch.TimeFormat, 32)
	}
	if patch.TextDirection != nil {
		d.TextDirection = clampString(*patch.TextDirection, 16)
	}
	if patch.Theme != nil {
		d.Theme = clampString(*patch.Theme, 16)
	}
	if patch.Density != nil {
		d.Density = clampString(*patch.Density, 16)
	}
	if patch.PreviewLines != nil {
		v := *patch.PreviewLines
		if v < 0 {
			v = 0
		}
		if v > 6 {
			v = 6
		}
		d.PreviewLines = v
	}
	if patch.ReadingPane != nil {
		d.ReadingPane = clampString(*patch.ReadingPane, 16)
	}
	if patch.SignatureEnabled != nil {
		d.SignatureEnabled = *patch.SignatureEnabled
	}
	if patch.SignatureText != nil {
		s := *patch.SignatureText
		if len(s) > 4096 {
			s = s[:4096]
		}
		d.SignatureText = s
	}
	if patch.SignatureInReplies != nil {
		d.SignatureInReplies = *patch.SignatureInReplies
	}
	if patch.DefaultReplyMode != nil {
		d.DefaultReplyMode = clampString(*patch.DefaultReplyMode, 16)
	}
	if patch.AutosaveSeconds != nil {
		v := *patch.AutosaveSeconds
		if v < 0 {
			v = 0
		}
		if v > 60 {
			v = 60
		}
		d.AutosaveSeconds = v
	}
	if patch.ConfirmBeforeDiscard != nil {
		d.ConfirmBeforeDiscard = *patch.ConfirmBeforeDiscard
	}
	if patch.WarnOnEmptySubject != nil {
		d.WarnOnEmptySubject = *patch.WarnOnEmptySubject
	}
	if patch.DefaultFolder != nil {
		d.DefaultFolder = clampString(*patch.DefaultFolder, 64)
	}
	if patch.MarkReadDelaySeconds != nil {
		v := *patch.MarkReadDelaySeconds
		if v < 0 {
			v = 0
		}
		if v > 60 {
			v = 60
		}
		d.MarkReadDelaySeconds = v
	}
	if patch.SenderDisplay != nil {
		d.SenderDisplay = clampString(*patch.SenderDisplay, 16)
	}
	if patch.NotifyInApp != nil {
		d.NotifyInApp = *patch.NotifyInApp
	}
	if patch.NotifyPush != nil {
		d.NotifyPush = *patch.NotifyPush
	}
}

func settingsBoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func clampString(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}
