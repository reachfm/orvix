package imap

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// Command represents a parsed IMAP command.
type Command struct {
	Tag       string
	Name      string
	Arguments string
}

// ParseCommand parses a raw IMAP command line.
func ParseCommand(line string) (*Command, error) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, fmt.Errorf("empty command")
	}

	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid command: %s", line)
	}

	tag := parts[0]
	name := strings.ToUpper(parts[1])
	args := ""
	if len(parts) > 2 {
		args = parts[2]
	}

	return &Command{Tag: tag, Name: name, Arguments: args}, nil
}

const capabilities = "IMAP4rev1 LOGIN-REFERRALS AUTH=LOGIN"

// Handle processes a single IMAP command and returns the response string.
func Handle(ctx context.Context, cmd *Command, session *Session, authBackend AuthBackend) string {
	if session.State == StateLogout {
		return BAD(cmd.Tag, "Already logged out")
	}

	// RequireTLSForAuth enforcement: reject LOGIN before TLS when required.
	if cmd.Name == "LOGIN" && session.RequireTLS && !session.TLSActive {
		return BAD(cmd.Tag, "TLS required for LOGIN")
	}

	switch cmd.Name {
	case "CAPABILITY":
		return handleCapability(cmd, session)
	case "NOOP":
		return handleNoop(cmd, session)
	case "LOGOUT":
		return handleLogout(cmd, session)
	case "LOGIN":
		return handleLogin(cmd, session, authBackend)
	case "LIST":
		return handleList(ctx, cmd, session)
	case "SELECT":
		return handleSelect(ctx, cmd, session)
	case "STATUS":
		return handleStatus(ctx, cmd, session)
	case "FETCH":
		return handleFetch(ctx, cmd, session)
	case "STORE":
		return handleStore(ctx, cmd, session)
	case "COPY":
		return handleCopy(ctx, cmd, session)
	case "EXPUNGE":
		return handleExpunge(ctx, cmd, session)
	case "UID":
		return handleUID(ctx, cmd, session)
	default:
		return BAD(cmd.Tag, fmt.Sprintf("Unknown command: %s", cmd.Name))
	}
}

func handleCapability(cmd *Command, session *Session) string {
	resp := Untagged("CAPABILITY", capabilities)
	resp += OK(cmd.Tag, "CAPABILITY completed")
	return resp
}

func handleNoop(cmd *Command, session *Session) string {
	return OK(cmd.Tag, "NOOP completed")
}

func handleLogout(cmd *Command, session *Session) string {
	session.State = StateLogout
	return BYE(cmd.Tag, "Orvix IMAP server logging out")
}

func handleLogin(cmd *Command, session *Session, authBackend AuthBackend) string {
	if session.State != StateNotAuthenticated {
		return BAD(cmd.Tag, "Already authenticated")
	}

	args := strings.TrimSpace(cmd.Arguments)
	if args == "" {
		return BAD(cmd.Tag, "LOGIN requires username and password")
	}

	username, password := parseLoginArgs(args)
	if username == "" || password == "" {
		return BAD(cmd.Tag, "LOGIN requires username and password")
	}

	mailboxID, ok := authBackend.Authenticate(username, password)
	if !ok {
		return NO(cmd.Tag, "LOGIN failed")
	}

	session.State = StateAuthenticated
	session.Username = username
	session.MailboxID = mailboxID
	return OK(cmd.Tag, "LOGIN completed")
}

func handleList(ctx context.Context, cmd *Command, session *Session) string {
	if session.State == StateNotAuthenticated {
		return BAD(cmd.Tag, "Not authenticated")
	}
	if session.MailStore == nil {
		return OK(cmd.Tag, "LIST completed")
	}

	folders, err := session.MailStore.Folders.ListByMailbox(ctx, session.MailboxID, nil)
	if err != nil {
		return OK(cmd.Tag, "LIST completed")
	}

	var resp string
	for _, f := range folders {
		delim := "/"
		flags := `\HasNoChildren`
		resp += Untagged("LIST", fmt.Sprintf(`(%s) "%s" "%s"`, flags, delim, f.Path))
	}
	resp += OK(cmd.Tag, "LIST completed")
	return resp
}

func handleSelect(ctx context.Context, cmd *Command, session *Session) string {
	if session.State == StateNotAuthenticated {
		return BAD(cmd.Tag, "Not authenticated")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	mailboxName := strings.TrimSpace(cmd.Arguments)
	mailboxName = strings.Trim(mailboxName, "\"")

	if mailboxName == "" {
		return BAD(cmd.Tag, "SELECT requires mailbox name")
	}

	folder, err := session.MailStore.Folders.GetByPath(ctx, session.MailboxID, mailboxName, nil)
	if err != nil || folder == nil {
		return NO(cmd.Tag, fmt.Sprintf("Mailbox %s not found", mailboxName))
	}

	session.SelectedMailbox = folder
	session.State = StateSelected

	totalCount, _ := session.MailStore.Messages.CountByFolder(ctx, folder.ID, nil)
	uidValidity := computeUIDValidity(folder.ID)
	uidNext := computeUIDNext(ctx, session.MailStore, session.MailboxID, folder.ID)

	var resp string
	resp += Untagged("FLAGS", `(\Answered \Deleted \Draft \Flagged \Seen)`)
	resp += Untagged("OK", "[PERMANENTFLAGS (\\Answered \\Deleted \\Draft \\Flagged \\Seen)]")
	resp += Untagged(fmt.Sprintf("%d", totalCount), "EXISTS")
	resp += Untagged("0", "RECENT")
	resp += Untagged("OK", fmt.Sprintf("[UIDVALIDITY %d]", uidValidity))
	resp += Untagged("OK", fmt.Sprintf("[UIDNEXT %d]", uidNext))
	resp += OK(cmd.Tag, "[READ-WRITE] SELECT completed")
	return resp
}

func handleStatus(ctx context.Context, cmd *Command, session *Session) string {
	if session.State == StateNotAuthenticated {
		return BAD(cmd.Tag, "Not authenticated")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	args := strings.TrimSpace(cmd.Arguments)
	parenIdx := strings.Index(args, "(")
	if parenIdx < 0 {
		return BAD(cmd.Tag, "STATUS requires mailbox name and data items")
	}

	mailboxName := strings.TrimSpace(args[:parenIdx])
	mailboxName = strings.Trim(mailboxName, "\"")

	folder, err := session.MailStore.Folders.GetByPath(ctx, session.MailboxID, mailboxName, nil)
	if err != nil || folder == nil {
		return NO(cmd.Tag, fmt.Sprintf("Mailbox %s not found", mailboxName))
	}

	total, _ := session.MailStore.Messages.CountByFolder(ctx, folder.ID, nil)

	resp := Untagged("STATUS", fmt.Sprintf(`"%s" (MESSAGES %d RECENT 0 UNSEEN %d)`, mailboxName, total, total))
	resp += OK(cmd.Tag, "STATUS completed")
	return resp
}

// ── UID Helpers ───────────────────────────────────────────────

// computeUIDValidity returns a stable IMAP UIDVALIDITY for a folder.
// Uses a base offset to avoid starting at 0.
func computeUIDValidity(folderID uint) uint {
	return folderID + 1000
}

// computeUIDNext returns the next UID that should be assigned in a mailbox.
// Per RFC 3501: UIDNEXT = MAX(UID in mailbox) + 1.
// Message.ID is the auto-increment primary key, which serves as the UID.
func computeUIDNext(ctx context.Context, ms *storage.MailStore, mailboxID, folderID uint) uint {
	// Use a direct SQL query to get the max message ID for this folder.
	row := ms.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM coremail_messages
		 WHERE mailbox_id = ? AND folder_id = ? AND purged_at IS NULL`,
		mailboxID, folderID)
	var maxID uint
	if err := row.Scan(&maxID); err != nil {
		return 1
	}
	if maxID == 0 {
		return 1
	}
	return maxID + 1
}

// loadMessagesForFolder loads and sorts all messages in a folder by ID.
func loadMessagesForFolder(ctx context.Context, ms *storage.MailStore, mailboxID, folderID uint) ([]storage.Message, error) {
	msgs, _, err := ms.Messages.List(ctx, storage.MessageFilter{
		MailboxID: mailboxID,
		FolderID:  &folderID,
	}, nil)
	if err != nil {
		return nil, err
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].ID < msgs[j].ID
	})
	return msgs, nil
}

// uidToSeq maps a UID (Message.ID) to its sequence number (1-based).
// Returns 0 if not found.
func uidToSeq(msgs []storage.Message, uid uint) int {
	for i, m := range msgs {
		if m.ID == uid {
			return i + 1
		}
	}
	return 0
}

// seqToUID maps a sequence number (1-based) to its UID (Message.ID).
// Returns 0 if not found.
func seqToUID(msgs []storage.Message, seq int) uint {
	if seq < 1 || seq > len(msgs) {
		return 0
	}
	return msgs[seq-1].ID
}

// getUIDRange returns the min and max UID in a message list.
func getUIDRange(msgs []storage.Message) (uint, uint) {
	if len(msgs) == 0 {
		return 0, 0
	}
	return msgs[0].ID, msgs[len(msgs)-1].ID
}

// ParseUIDSequenceSet parses a UID sequence set (same format as regular sequences).
func ParseUIDSequenceSet(s string) (*SequenceSet, error) {
	return ParseSequenceSet(s)
}

// ── FETCH ─────────────────────────────────────────────────────

// parseFetchAttrs extracts attribute names from a FETCH arguments string.
// Handles parenthesized lists like "(FLAGS UID RFC822.SIZE)".
func parseFetchAttrs(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	// Remove outer parentheses if present.
	if strings.HasPrefix(args, "(") && strings.HasSuffix(args, ")") {
		args = args[1 : len(args)-1]
	}
	attrs := strings.Fields(args)
	result := make([]string, 0, len(attrs))
	for _, a := range attrs {
		result = append(result, strings.ToUpper(a))
	}
	return result
}

func handleFetch(ctx context.Context, cmd *Command, session *Session) string {
	if session.State != StateSelected {
		return BAD(cmd.Tag, "Not selected")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	args := strings.TrimSpace(cmd.Arguments)
	// Split into sequence-set and attribute-list.
	// Find the first parenthesized group or the space before the attribute name.
	// Format: FETCH <seq-set> (<attrs>) or FETCH <seq-set> <single-attrs>
	spaceIdx := strings.Index(args, " ")
	if spaceIdx < 0 {
		return BAD(cmd.Tag, "FETCH requires sequence set and attributes")
	}

	seqStr := strings.TrimSpace(args[:spaceIdx])
	attrStr := strings.TrimSpace(args[spaceIdx+1:])

	if seqStr == "" || attrStr == "" {
		return BAD(cmd.Tag, "FETCH requires sequence set and attributes")
	}

	// Parse sequence set.
	seqSet, err := ParseSequenceSet(seqStr)
	if err != nil {
		return BAD(cmd.Tag, fmt.Sprintf("Invalid sequence set: %v", err))
	}

	// Parse attributes.
	attrs := parseFetchAttrs(attrStr)
	if len(attrs) == 0 {
		return BAD(cmd.Tag, "FETCH requires at least one attribute")
	}

	// Get total message count.
	folderID := session.SelectedMailbox.ID
	total, err := session.MailStore.Messages.CountByFolder(ctx, folderID, nil)
	if err != nil {
		return BAD(cmd.Tag, "FETCH failed")
	}

	// Resolve sequences.
	seqs := seqSet.Resolve(int(total))
	if len(seqs) == 0 {
		return OK(cmd.Tag, "FETCH completed")
	}

	// Fetch all messages in the folder.
	msgs, _, err := session.MailStore.Messages.List(ctx, storage.MessageFilter{
		MailboxID: session.MailboxID,
		FolderID:  &folderID,
		Limit:     int(total),
	}, nil)
	if err != nil {
		return BAD(cmd.Tag, "FETCH failed")
	}

	// Sort by ID for sequence number ordering.
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].ID < msgs[j].ID
	})

	// Build a map from sequence number to message.
	// Sequence numbers are 1-based.
	seqMap := make(map[int]*storage.Message)
	for i := range msgs {
		seqNum := i + 1
		seqMap[seqNum] = &msgs[i]
	}

	var resp string
	for _, seq := range seqs {
		msg, ok := seqMap[seq]
		if !ok {
			continue
		}

		dataItems := ""
		for _, attr := range attrs {
			data := fetchAttribute(ctx, msg, attr, session)
			if data != "" {
				if dataItems != "" {
					dataItems += " "
				}
				dataItems += data
			}
		}

		if dataItems != "" {
			resp += Untagged(fmt.Sprintf("%d FETCH", seq), fmt.Sprintf("(%s)", dataItems))
		}
	}

	resp += OK(cmd.Tag, "FETCH completed")
	return resp
}

func fetchAttribute(ctx context.Context, msg *storage.Message, attr string, session *Session) string {
	// Handle BODY[<section>] attributes.
	if strings.HasPrefix(attr, "BODY[") {
		return fetchBodySection(ctx, msg, attr, session)
	}

	switch attr {
	case "FLAGS":
		return fmt.Sprintf("FLAGS %s", formatFlags(msg.Seen, msg.Answered, msg.Flagged, msg.Draft, msg.Deleted))

	case "UID":
		return fmt.Sprintf("UID %d", msg.ID)

	case "RFC822.SIZE":
		return fmt.Sprintf("RFC822.SIZE %d", msg.SizeBytes)

	case "INTERNALDATE":
		return fmt.Sprintf("INTERNALDATE %s", formatIMAPDate(msg.ReceivedDate))

	case "ENVELOPE":
		_, data, err := session.MailStore.LoadMessageByMessageID(ctx, msg.MessageID)
		if err != nil || data == nil {
			return "ENVELOPE NIL"
		}
		env := BuildEnvelope(data)
		return fmt.Sprintf("ENVELOPE %s", FormatEnvelope(env))

	case "BODYSTRUCTURE":
		_, data, err := session.MailStore.LoadMessageByMessageID(ctx, msg.MessageID)
		if err != nil || data == nil {
			return "BODYSTRUCTURE NIL"
		}
		return fmt.Sprintf("BODYSTRUCTURE %s", GetBodyStructure(data))

	default:
		return ""
	}
}

func fetchBodySection(ctx context.Context, msg *storage.Message, attr string, session *Session) string {
	// Size guard: return NIL for messages exceeding the configurable max body size.
	// This prevents unbounded memory growth from large message bodies.
	maxBody := int64(50 * 1024 * 1024) // 50 MB default
	if maxBody > 0 && msg.SizeBytes > maxBody {
		return fmt.Sprintf("%s NIL", attr)
	}

	_, data, err := session.MailStore.LoadMessageByMessageID(ctx, msg.MessageID)
	if err != nil || data == nil {
		return fmt.Sprintf("%s NIL", attr)
	}

	header, body := SplitBody(data)

	section := strings.TrimPrefix(attr, "BODY[")
	section = strings.TrimSuffix(section, "]")
	section = strings.ToUpper(strings.TrimSpace(section))

	switch section {
	case "":
		return FormatBodySection("", data)
	case "HEADER":
		return FormatBodySection("HEADER", append(header, []byte("\r\n\r\n")...))
	case "TEXT":
		return FormatBodySection("TEXT", body)
	default:
		return ""
	}
}

// ── STORE ──────────────────────────────────────────────────────

func parseStoreArgs(args string) (seqStr string, mode string, flagsRaw string) {
	args = strings.TrimSpace(args)
	// Format: <seq-set> <mode> <flags>
	// mode is one of: FLAGS, +FLAGS, -FLAGS
	// flags are parenthesized: (\Seen \Deleted)
	parts := strings.SplitN(args, " ", 3)
	if len(parts) < 3 {
		return "", "", ""
	}
	seqStr = parts[0]
	mode = strings.ToUpper(parts[1])
	flagsRaw = parts[2]
	return
}

func parseFlagList(flagsRaw string) []string {
	f := strings.TrimSpace(flagsRaw)
	f = strings.Trim(f, "()")
	parts := strings.Fields(f)
	var flags []string
	for _, p := range parts {
		flags = append(flags, p)
	}
	return flags
}

func flagNameToField(name string) (string, *bool) {
	switch strings.ToLower(name) {
	case "\\seen":
		return "seen", boolPtr(true)
	case "\\answered":
		return "answered", boolPtr(true)
	case "\\flagged":
		return "flagged", boolPtr(true)
	case "\\deleted":
		return "deleted", boolPtr(true)
	case "\\draft":
		return "draft", boolPtr(true)
	default:
		return "", nil
	}
}

func boolPtr(b bool) *bool { return &b }

func handleStore(ctx context.Context, cmd *Command, session *Session) string {
	if session.State != StateSelected {
		return BAD(cmd.Tag, "Not selected")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	seqStr, mode, flagsRaw := parseStoreArgs(cmd.Arguments)
	if seqStr == "" || mode == "" || flagsRaw == "" {
		return BAD(cmd.Tag, "STORE requires sequence set, mode, and flags")
	}

	seqSet, err := ParseSequenceSet(seqStr)
	if err != nil {
		return BAD(cmd.Tag, fmt.Sprintf("Invalid sequence set: %v", err))
	}

	folderID := session.SelectedMailbox.ID
	total, _ := session.MailStore.Messages.CountByFolder(ctx, folderID, nil)
	seqs := seqSet.Resolve(int(total))
	if len(seqs) == 0 {
		return OK(cmd.Tag, "STORE completed")
	}

	flags := parseFlagList(flagsRaw)

	// Map flags to their field names and collect unknowns.
	var seen, answered, flagged, draft, deleted *bool
	var unknownFlags []string
	for _, f := range flags {
		field, val := flagNameToField(f)
		if field == "" {
			unknownFlags = append(unknownFlags, f)
			continue
		}
		switch field {
		case "seen":
			seen = val
		case "answered":
			answered = val
		case "flagged":
			flagged = val
		case "draft":
			draft = val
		case "deleted":
			deleted = val
		}
	}

	// Apply mode.
	switch mode {
	case "FLAGS":
		// Replace: set listed, clear unlisted.
		applyReplaceMode(&seen, &answered, &flagged, &draft, &deleted, flags)
	case "+FLAGS":
		// Add: set listed (already done above).
	case "-FLAGS":
		// Remove: set listed to false.
		applyRemoveMode(&seen, &answered, &flagged, &draft, &deleted, flags)
	default:
		return BAD(cmd.Tag, fmt.Sprintf("Unknown STORE mode: %s", mode))
	}

	// List messages in folder.
	msgs, _, err := session.MailStore.Messages.List(ctx, storage.MessageFilter{
		MailboxID: session.MailboxID,
		FolderID:  &folderID,
	}, nil)
	if err != nil {
		return BAD(cmd.Tag, "STORE failed")
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })

	var resp string
	for _, seq := range seqs {
		if seq < 1 || seq > len(msgs) {
			continue
		}
		msg := msgs[seq-1]
		if err := session.MailStore.Messages.UpdateFlags(ctx, msg.ID, seen, answered, flagged, draft, deleted, nil, nil); err != nil {
			continue
		}
		// Reload to get updated flags.
		updated, _ := session.MailStore.Messages.GetByID(ctx, msg.ID, nil)
		if updated == nil {
			continue
		}
		resp += Untagged(fmt.Sprintf("%d FETCH", seq), fmt.Sprintf("(FLAGS %s)", formatFlags(updated.Seen, updated.Answered, updated.Flagged, updated.Draft, updated.Deleted)))
	}

	if len(unknownFlags) > 0 {
		resp += OK(cmd.Tag, fmt.Sprintf("STORE completed (unknown flags ignored: %s)", strings.Join(unknownFlags, " ")))
	} else {
		resp += OK(cmd.Tag, "STORE completed")
	}
	return resp
}

func applyReplaceMode(seen, answered, flagged, draft, deleted **bool, flags []string) {
	has := func(name string) bool {
		for _, f := range flags {
			if strings.EqualFold(f, name) {
				return true
			}
		}
		return false
	}
	falseVal := false
	if !has("\\seen") {
		*seen = &falseVal
	}
	if !has("\\answered") {
		*answered = &falseVal
	}
	if !has("\\flagged") {
		*flagged = &falseVal
	}
	if !has("\\draft") {
		*draft = &falseVal
	}
	if !has("\\deleted") {
		*deleted = &falseVal
	}
}

func applyRemoveMode(seen, answered, flagged, draft, deleted **bool, flags []string) {
	falseVal := false
	for _, f := range flags {
		switch strings.ToLower(f) {
		case "\\seen":
			*seen = &falseVal
		case "\\answered":
			*answered = &falseVal
		case "\\flagged":
			*flagged = &falseVal
		case "\\draft":
			*draft = &falseVal
		case "\\deleted":
			*deleted = &falseVal
		}
	}
}

// ── COPY ───────────────────────────────────────────────────────

func handleCopy(ctx context.Context, cmd *Command, session *Session) string {
	if session.State != StateSelected {
		return BAD(cmd.Tag, "Not selected")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	args := strings.TrimSpace(cmd.Arguments)
	// Format: <seq-set> <mailbox>
	// Find last space to separate sequence from mailbox name.
	lastSpace := strings.LastIndex(args, " ")
	if lastSpace < 0 {
		return BAD(cmd.Tag, "COPY requires sequence set and mailbox name")
	}

	seqStr := strings.TrimSpace(args[:lastSpace])
	mailboxName := strings.TrimSpace(args[lastSpace+1:])
	mailboxName = strings.Trim(mailboxName, "\"")

	if seqStr == "" || mailboxName == "" {
		return BAD(cmd.Tag, "COPY requires sequence set and mailbox name")
	}

	seqSet, err := ParseSequenceSet(seqStr)
	if err != nil {
		return BAD(cmd.Tag, fmt.Sprintf("Invalid sequence set: %v", err))
	}

	folderID := session.SelectedMailbox.ID
	total, _ := session.MailStore.Messages.CountByFolder(ctx, folderID, nil)
	seqs := seqSet.Resolve(int(total))
	if len(seqs) == 0 {
		return OK(cmd.Tag, "COPY completed")
	}

	// Find destination folder.
	destFolder, err := session.MailStore.Folders.GetByPath(ctx, session.MailboxID, mailboxName, nil)
	if err != nil || destFolder == nil {
		return NO(cmd.Tag, fmt.Sprintf("Destination mailbox %s not found", mailboxName))
	}

	// List source messages.
	msgs, _, err := session.MailStore.Messages.List(ctx, storage.MessageFilter{
		MailboxID: session.MailboxID,
		FolderID:  &folderID,
	}, nil)
	if err != nil {
		return BAD(cmd.Tag, "COPY failed")
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })

	for _, seq := range seqs {
		if seq < 1 || seq > len(msgs) {
			continue
		}
		msg := msgs[seq-1]
		if _, err := session.MailStore.CopyMessage(ctx, msg.ID, session.MailboxID, destFolder.ID, nil); err != nil {
			continue
		}
	}

	return OK(cmd.Tag, "COPY completed")
}

// ── EXPUNGE ────────────────────────────────────────────────────

func handleExpunge(ctx context.Context, cmd *Command, session *Session) string {
	if session.State != StateSelected {
		return BAD(cmd.Tag, "Not selected")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	folderID := session.SelectedMailbox.ID

	// List messages in folder.
	msgs, _, err := session.MailStore.Messages.List(ctx, storage.MessageFilter{
		MailboxID: session.MailboxID,
		FolderID:  &folderID,
	}, nil)
	if err != nil {
		return BAD(cmd.Tag, "EXPUNGE failed")
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })

	var resp string
	for i := range msgs {
		if !msgs[i].Deleted {
			continue
		}
		// Use Purge to permanently remove.
		if err := session.MailStore.PurgeMessage(ctx, msgs[i].ID, nil); err != nil {
			continue
		}
		// Sequence number is 1-based.
		resp += Untagged(fmt.Sprintf("%d", i+1), "EXPUNGE")
	}

	resp += OK(cmd.Tag, "EXPUNGE completed")
	return resp
}

// ── UID Command Dispatcher ─────────────────────────────────────

func handleUID(ctx context.Context, cmd *Command, session *Session) string {
	if session.State != StateSelected {
		return BAD(cmd.Tag, "Not selected")
	}
	if session.MailStore == nil {
		return BAD(cmd.Tag, "MailStore not available")
	}

	// Parse child command: UID FETCH, UID STORE, UID COPY.
	args := strings.TrimSpace(cmd.Arguments)
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		return BAD(cmd.Tag, "UID requires a sub-command (FETCH, STORE, COPY)")
	}

	subCmd := strings.ToUpper(parts[0])
	subArgs := parts[1]

	// Build a sub-command for the handler.
	sub := &Command{Tag: cmd.Tag, Name: subCmd, Arguments: subArgs}

	switch subCmd {
	case "FETCH":
		return handleUIDFetch(ctx, sub, session)
	case "STORE":
		return handleUIDStore(ctx, sub, session)
	case "COPY":
		return handleUIDCopy(ctx, sub, session)
	default:
		return BAD(cmd.Tag, fmt.Sprintf("Unknown UID sub-command: %s", subCmd))
	}
}

// handleUIDFetch implements UID FETCH: same as FETCH but uses UID for input
// and returns message data as usual (which includes UID in response).
func handleUIDFetch(ctx context.Context, cmd *Command, session *Session) string {
	return handleFetch(ctx, cmd, session)
}

// handleUIDStore implements UID STORE: same as STORE but uses UID.
func handleUIDStore(ctx context.Context, cmd *Command, session *Session) string {
	// Store with UID sequence set. The sequence set is interpreted as UIDs.
	folderID := session.SelectedMailbox.ID
	msgs, err := loadMessagesForFolder(ctx, session.MailStore, session.MailboxID, folderID)
	if err != nil {
		return BAD(cmd.Tag, "UID STORE failed")
	}

	args := strings.TrimSpace(cmd.Arguments)
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		return BAD(cmd.Tag, "UID STORE requires UIDs and flags")
	}

	rawUIDs := parts[0]
	remaining := parts[1]

	// Parse UIDs as sequence set.
	uidSet, err := ParseUIDSequenceSet(rawUIDs)
	if err != nil {
		return BAD(cmd.Tag, fmt.Sprintf("Invalid UID sequence: %v", err))
	}

	// Find the max message ID in the folder to use as total for resolution.
	maxUID := uint(0)
	for _, m := range msgs {
		if m.ID > maxUID {
			maxUID = m.ID
		}
	}
	uidSeqs := uidSet.Resolve(int(maxUID))
	if len(uidSeqs) == 0 {
		return OK(cmd.Tag, "UID STORE completed")
	}

	// Map each resolved sequence number to an actual UID.
	// The sequence set parser resolves numbers against a total count.
	// For UID sets, the "total" is the max UID, and resolved values are UIDs.
	// We need to filter to only valid UIDs.
	var storeResp string
	for _, uidSeq := range uidSeqs {
		uid := uint(uidSeq)
		seq := uidToSeq(msgs, uid)
		if seq == 0 {
			continue
		}
		msg := msgs[seq-1]

		// Parse flags and apply STORE logic (reuse by constructing a sub command).
		storeCmd := &Command{Tag: cmd.Tag, Name: "STORE", Arguments: fmt.Sprintf("%d %s", seq, remaining)}
		_ = storeCmd

		// Apply STORE directly.
		mode := ""
		flagsRaw := ""
		modeParts := strings.SplitN(remaining, " ", 2)
		if len(modeParts) >= 1 {
			mode = strings.ToUpper(modeParts[0])
		}
		if len(modeParts) >= 2 {
			flagsRaw = modeParts[1]
		}

		if mode == "" || flagsRaw == "" {
			return BAD(cmd.Tag, "UID STORE requires mode and flags")
		}

		flags := parseFlagList(flagsRaw)
		var seen, answered, flagged, draft, deleted *bool

		// Apply mode.
		switch mode {
		case "FLAGS":
			applyReplaceModeForMsg(&seen, &answered, &flagged, &draft, &deleted, flags)
		case "+FLAGS":
			applyAddModeForMsg(&seen, &answered, &flagged, &draft, &deleted, &msg, flags)
		case "-FLAGS":
			applyRemoveModeForMsg(&seen, &answered, &flagged, &draft, &deleted, flags)
		default:
			return BAD(cmd.Tag, fmt.Sprintf("Unknown STORE mode: %s", mode))
		}

		if err := session.MailStore.Messages.UpdateFlags(ctx, msg.ID, seen, answered, flagged, draft, deleted, nil, nil); err != nil {
			continue
		}
		storeResp += Untagged(fmt.Sprintf("%d FETCH", seq), fmt.Sprintf("(FLAGS %s UID %d)", formatFlags(
			boolOrDefault(seen, msg.Seen),
			boolOrDefault(answered, msg.Answered),
			boolOrDefault(flagged, msg.Flagged),
			boolOrDefault(draft, msg.Draft),
			boolOrDefault(deleted, msg.Deleted),
		), msg.ID))
	}

	storeResp += OK(cmd.Tag, "UID STORE completed")
	return storeResp
}

// boolOrDefault returns the flag if non-nil, else the default.
func boolOrDefault(f *bool, def bool) bool {
	if f != nil {
		return *f
	}
	return def
}

func applyReplaceModeForMsg(seen, answered, flagged, draft, deleted **bool, flags []string) {
	falseVal := false
	has := func(name string) bool {
		for _, f := range flags {
			if strings.EqualFold(f, name) {
				return true
			}
		}
		return false
	}
	if !has("\\seen") {
		*seen = &falseVal
	}
	if !has("\\answered") {
		*answered = &falseVal
	}
	if !has("\\flagged") {
		*flagged = &falseVal
	}
	if !has("\\draft") {
		*draft = &falseVal
	}
	if !has("\\deleted") {
		*deleted = &falseVal
	}
}

func applyAddModeForMsg(seen, answered, flagged, draft, deleted **bool, msg *storage.Message, flags []string) {
	trueVal := true
	for _, f := range flags {
		switch strings.ToLower(f) {
		case "\\seen":
			*seen = &trueVal
		case "\\answered":
			*answered = &trueVal
		case "\\flagged":
			*flagged = &trueVal
		case "\\draft":
			*draft = &trueVal
		case "\\deleted":
			*deleted = &trueVal
		}
	}
}

func applyRemoveModeForMsg(seen, answered, flagged, draft, deleted **bool, flags []string) {
	falseVal := false
	for _, f := range flags {
		switch strings.ToLower(f) {
		case "\\seen":
			*seen = &falseVal
		case "\\answered":
			*answered = &falseVal
		case "\\flagged":
			*flagged = &falseVal
		case "\\draft":
			*draft = &falseVal
		case "\\deleted":
			*deleted = &falseVal
		}
	}
}

// handleUIDCopy implements UID COPY: copies messages by UID.
func handleUIDCopy(ctx context.Context, cmd *Command, session *Session) string {
	folderID := session.SelectedMailbox.ID
	msgs, err := loadMessagesForFolder(ctx, session.MailStore, session.MailboxID, folderID)
	if err != nil {
		return BAD(cmd.Tag, "UID COPY failed")
	}

	args := strings.TrimSpace(cmd.Arguments)
	lastSpace := strings.LastIndex(args, " ")
	if lastSpace < 0 {
		return BAD(cmd.Tag, "UID COPY requires UIDs and mailbox name")
	}

	rawUIDs := strings.TrimSpace(args[:lastSpace])
	mailboxName := strings.TrimSpace(args[lastSpace+1:])
	mailboxName = strings.Trim(mailboxName, "\"")

	if rawUIDs == "" || mailboxName == "" {
		return BAD(cmd.Tag, "UID COPY requires UIDs and mailbox name")
	}

	uidSet, err := ParseUIDSequenceSet(rawUIDs)
	if err != nil {
		return BAD(cmd.Tag, fmt.Sprintf("Invalid UID sequence: %v", err))
	}

	maxUID := uint(0)
	for _, m := range msgs {
		if m.ID > maxUID {
			maxUID = m.ID
		}
	}
	uidSeqs := uidSet.Resolve(int(maxUID))
	if len(uidSeqs) == 0 {
		return OK(cmd.Tag, "UID COPY completed")
	}

	destFolder, err := session.MailStore.Folders.GetByPath(ctx, session.MailboxID, mailboxName, nil)
	if err != nil || destFolder == nil {
		return NO(cmd.Tag, fmt.Sprintf("Destination mailbox %s not found", mailboxName))
	}

	for _, uidSeq := range uidSeqs {
		uid := uint(uidSeq)
		seq := uidToSeq(msgs, uid)
		if seq == 0 {
			continue
		}
		msg := msgs[seq-1]
		if _, err := session.MailStore.CopyMessage(ctx, msg.ID, session.MailboxID, destFolder.ID, nil); err != nil {
			continue
		}
	}

	return OK(cmd.Tag, "UID COPY completed")
}

// ── Fetch message count ────────────────────────────────────────

func getMessageCount(ctx context.Context, ms *storage.MailStore, mailboxID, folderID uint) int {
	count, err := ms.Messages.CountByFolder(ctx, folderID, nil)
	if err != nil {
		return 0
	}
	return int(count)
}

// ── Helpers ───────────────────────────────────────────────────

// ── Helpers ───────────────────────────────────────────────────

func parseLoginArgs(args string) (string, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}

	var username, password string
	if strings.HasPrefix(args, "\"") {
		end := strings.IndexByte(args[1:], '"')
		if end < 0 {
			return "", ""
		}
		username = args[1 : end+1]
		password = strings.TrimSpace(args[end+3:])
	} else {
		parts := strings.SplitN(args, " ", 2)
		if len(parts) < 2 {
			return "", ""
		}
		username = parts[0]
		password = parts[1]
	}

	if strings.HasPrefix(password, "\"") {
		password = strings.Trim(password, "\"")
	}

	return username, password
}
