package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/coremail/mime"
	"github.com/orvix/orvix/internal/coremail/storage"
)

var (
	msgCounter   int64
	msgCounterMu sync.Mutex
)

func msgIDcounter() int64 {
	msgCounterMu.Lock()
	defer msgCounterMu.Unlock()
	msgCounter++
	return msgCounter
}

func parseUint(s string) (uint, error) {
	var n uint
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func fromEmail(addr *EmailAddress) string {
	if addr == nil {
		return ""
	}
	return addr.Email
}

func joinAddresses(addrs []*EmailAddress) string {
	var parts []string
	for _, a := range addrs {
		if a.Email != "" {
			parts = append(parts, a.Email)
		}
	}
	return strings.Join(parts, ",")
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

func (s *Server) handleEmailSet(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string, username string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPEmailSet()
	}

	var params EmailSetRequest
	if err := json.Unmarshal([]byte(mc.Params), &params); err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "invalidArguments"}}
	}

	if params.AccountID != accountID {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "accountNotFound"}}
	}

	oldState, _, _, _, err := s.computeEmailState(ctx, mailboxID)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	resp := EmailSetResponse{
		AccountID:    accountID,
		OldState:     oldState,
		Created:      make(map[string]string),
		Updated:      make(map[string]*interface{}),
		Destroyed:    []string{},
		NotCreated:   make(map[string]string),
		NotUpdated:   make(map[string]string),
		NotDestroyed: make(map[string]string),
	}

	folders, _ := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	folderMap := make(map[string]*storage.Folder)
	for i, f := range folders {
		folderMap[fmt.Sprintf("%d", f.ID)] = &folders[i]
	}

	for idStr, patch := range params.Update {
		if patch == nil {
			continue
		}

		msgID, idErr := parseUint(idStr)
		if idErr != nil {
			resp.NotUpdated[idStr] = "invalid id"
			continue
		}

		msg, err := s.MailStore.Messages.GetByID(ctx, msgID, nil)
		if err != nil || msg == nil || msg.MailboxID != mailboxID {
			resp.NotUpdated[idStr] = "notFound"
			continue
		}

		var seen, answered, flagged, draft, deleted *bool
		if patch.Keywords != nil {
			for kw, val := range patch.Keywords {
				switch kw {
				case "$seen":
					seen = val
				case "$answered":
					answered = val
				case "$flagged":
					flagged = val
				case "$draft":
					draft = val
				case "$deleted":
					deleted = val
				}
			}
		}

		var targetFolderID *uint
		if patch.MailboxIDs != nil {
			for folderIDStr, add := range patch.MailboxIDs {
				if add != nil && *add {
					fid, parseErr := parseUint(folderIDStr)
					if parseErr == nil {
						if _, ok := folderMap[folderIDStr]; ok {
							targetFolderID = &fid
						}
					}
				}
			}
		}

		if seen != nil || answered != nil || flagged != nil || draft != nil || deleted != nil {
			if err := s.MailStore.Messages.UpdateFlags(ctx, msgID, seen, answered, flagged, draft, deleted, nil, nil); err != nil {
				resp.NotUpdated[idStr] = "serverFail"
				continue
			}
			if s.Observability != nil {
				s.Observability.Metrics.IncJMAPEmailUpdated()
			}
		}

		if targetFolderID != nil {
			if err := s.MailStore.MoveMessage(ctx, msgID, *targetFolderID, nil); err != nil {
				resp.NotUpdated[idStr] = "serverFail"
				continue
			}
			if s.Observability != nil {
				s.Observability.Metrics.IncJMAPEmailUpdated()
			}
		}

		if targetFolderID != nil && seen == nil && answered == nil && flagged == nil && draft == nil && deleted == nil {
			if s.Observability != nil {
				s.Observability.Metrics.IncJMAPEmailUpdated()
			}
		}

		resp.Updated[idStr] = nil
	}

	created := make(map[string]string)
	for clientID, create := range params.Create {
		if create == nil {
			resp.NotCreated[clientID] = "invalidArguments"
			continue
		}

		if len(create.Body) > 50*1024*1024 {
			resp.NotCreated[clientID] = "tooLarge"
			continue
		}

		for _, s := range []string{create.Subject, fromEmail(create.From)} {
			if strings.Contains(s, "\n") || strings.Contains(s, "\r") {
				resp.NotCreated[clientID] = "invalidArguments"
				continue
			}
		}
		if resp.NotCreated[clientID] != "" {
			continue
		}

		sender := fromEmail(create.From)
		if sender != "" && sender != username {
			resp.NotCreated[clientID] = "forbidden"
			continue
		}

		// Determine target folder from mailboxIds or default to INBOX.
		var targetFolder *storage.Folder
		if create.MailboxIDs != nil {
			for folderIDStr := range create.MailboxIDs {
				if f, ok := folderMap[folderIDStr]; ok {
					targetFolder = f
					break
				}
			}
		}
		if targetFolder == nil {
			for _, f := range folderMap {
				if f.Name == "INBOX" || f.FolderType == storage.FolderInbox {
					targetFolder = f
					break
				}
			}
		}
		if targetFolder == nil {
			for _, f := range folderMap {
				targetFolder = f
				break
			}
		}
		if targetFolder == nil {
			resp.NotCreated[clientID] = "serverFail"
			continue
		}

		// Check if this is a draft.
		isDraft := false
		if create.Keywords != nil {
			if val, ok := create.Keywords["$draft"]; ok && val {
				isDraft = true
			}
		}

		now := time.Now().UTC()
		messageID := fmt.Sprintf("<orvix-%d-%d@%s>", now.UnixNano(), msgIDcounter(), s.Hostname)
		subject := sanitizeHeader(create.Subject)
		from := fromEmail(create.From)
		to := joinAddresses(create.To)
		cc := joinAddresses(create.Cc)
		bcc := joinAddresses(create.Bcc)

		var rfc822Data []byte
		if len(create.Attachments) > 0 {
			var attachData []mime.AttachmentData
			uploadFailed := false
			for _, att := range create.Attachments {
				if att == nil {
					continue
				}
				data, filename, contentType, err := s.consumeUploadBlob(mailboxID, att.BlobID)
				if err != nil {
					resp.NotCreated[clientID] = "attachmentNotFound"
					uploadFailed = true
					break
				}
				if filename == "" {
					filename = sanitizeUploadFilename(att.Filename)
				}
				if filename == "" {
					filename = "attachment"
				}
				ct := att.ContentType
				if ct == "" {
					ct = contentType
				}
				if ct == "" {
					ct = "application/octet-stream"
				}
				attachData = append(attachData, mime.AttachmentData{
					Filename:    filename,
					ContentType: ct,
					Data:        data,
				})
			}
			if uploadFailed {
				continue
			}
			var err error
			rfc822Data, err = mime.BuildMultipartRFC822(from, to, cc, bcc, subject, create.Body, messageID, attachData)
			if err != nil {
				resp.NotCreated[clientID] = "serverFail"
				continue
			}
		} else {
			var rfc822Builder strings.Builder
			rfc822Builder.WriteString(fmt.Sprintf("From: %s\r\n", from))
			rfc822Builder.WriteString(fmt.Sprintf("To: %s\r\n", to))
			if cc != "" {
				rfc822Builder.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
			}
			if bcc != "" {
				rfc822Builder.WriteString(fmt.Sprintf("Bcc: %s\r\n", bcc))
			}
			rfc822Builder.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
			rfc822Builder.WriteString(fmt.Sprintf("Date: %s\r\n", now.Format(time.RFC1123Z)))
			rfc822Builder.WriteString(fmt.Sprintf("Message-ID: %s\r\n", messageID))
			rfc822Builder.WriteString("MIME-Version: 1.0\r\n")
			rfc822Builder.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
			rfc822Builder.WriteString("\r\n")
			rfc822Builder.WriteString(create.Body)
			if !strings.HasSuffix(create.Body, "\n") {
				rfc822Builder.WriteString("\r\n")
			}
			rfc822Data = []byte(rfc822Builder.String())
		}

		msg := &storage.Message{
			MessageID:        storage.GenerateMessageID(),
			InternetMessageID: messageID,
			TenantID:         0,
			DomainID:         0,
			MailboxID:        mailboxID,
			FolderID:         targetFolder.ID,
			FromAddress:      from,
			ToAddresses:      to,
			CcAddresses:      cc,
			BccAddresses:     bcc,
			Subject:          subject,
			Seen:             !isDraft,
			Draft:            isDraft,
		}

		if err := s.MailStore.StoreMessage(ctx, msg, rfc822Data, nil); err != nil {
			resp.NotCreated[clientID] = "serverFail"
			continue
		}

		created[clientID] = fmt.Sprintf("%d", msg.ID)
	}

	for _, idStr := range params.Destroy {
		msgID, idErr := parseUint(idStr)
		if idErr != nil {
			resp.NotDestroyed[idStr] = "invalid id"
			continue
		}

		msg, err := s.MailStore.Messages.GetByID(ctx, msgID, nil)
		if err != nil || msg == nil || msg.MailboxID != mailboxID {
			resp.NotDestroyed[idStr] = "notFound"
			continue
		}

		if err := s.MailStore.PurgeMessage(ctx, msgID, nil); err != nil {
			resp.NotDestroyed[idStr] = "serverFail"
			continue
		}

		resp.Destroyed = append(resp.Destroyed, idStr)
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPEmailDestroyed()
		}
	}

	newState, _, _, _, _ := s.computeEmailState(ctx, mailboxID)
	resp.OldState = oldState
	resp.NewState = newState
	resp.Created = created

	return &MethodResponse{Name: "Email/set", ID: mc.ID, Params: resp}
}
