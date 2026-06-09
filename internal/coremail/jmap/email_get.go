package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/mime"
	"github.com/orvix/orvix/internal/coremail/storage"
)

func (s *Server) handleEmailGet(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPMethodSuccess()
	}

	var params EmailGetRequest
	json.Unmarshal([]byte(mc.Params), &params)

	folders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var entries []EmailEntry
	var notFound []string

	for _, f := range folders {
		msgs, _, err := s.MailStore.Messages.List(ctx, storage.MessageFilter{
			MailboxID: mailboxID,
			FolderID:  &f.ID,
		}, nil)
		if err != nil {
			continue
		}

		for _, msg := range msgs {
			msgID := fmt.Sprintf("%d", msg.ID)

			if len(params.IDs) > 0 {
				found := false
				for _, id := range params.IDs {
					if id == msgID {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			entry := s.buildEmailEntry(ctx, &msg, &f)
			entries = append(entries, *entry)
		}
	}

	if len(params.IDs) > 0 {
		found := make(map[string]bool)
		for _, e := range entries {
			found[e.ID] = true
		}
		for _, id := range params.IDs {
			if !found[id] {
				notFound = append(notFound, id)
			}
		}
	}

	resp := EmailGetResponse{
		AccountID: accountID,
		State:     fmt.Sprintf("%d", time.Now().Unix()),
		List:      entries,
		NotFound:  notFound,
	}

	return &MethodResponse{Name: "Email/get", ID: mc.ID, Params: resp}
}

func (s *Server) buildEmailEntry(ctx context.Context, msg *storage.Message, folder *storage.Folder) *EmailEntry {
	entry := &EmailEntry{
		ID: fmt.Sprintf("%d", msg.ID),
		MailboxIDs: map[string]bool{
			fmt.Sprintf("%d", folder.ID): true,
		},
		Keywords:   buildKeywords(msg),
		Size:       int(msg.SizeBytes),
		ReceivedAt: msg.ReceivedDate.Format(time.RFC3339),
		Subject:    msg.Subject,
		MessageID:  msg.InternetMessageID,
		From:       parseAddressListToJMAP(msg.FromAddress),
		To:         parseAddressListToJMAP(msg.ToAddresses),
	}

	if msg.MessageDate != nil {
		entry.SentAt = msg.MessageDate.Format(time.RFC3339)
	}

	entry.Preview = msg.Subject
	if len(entry.Preview) > 100 {
		entry.Preview = entry.Preview[:100]
	}

	if s.MailStore != nil {
		atts, err := s.MailStore.Attachments.ListByMessage(ctx, msg.ID, nil)
		if err == nil && len(atts) > 0 {
			entry.HasAttachment = true
			for _, a := range atts {
				entry.Attachments = append(entry.Attachments, &AttachmentInfo{
					ID:          fmt.Sprintf("%d", a.ID),
					Filename:    a.Filename,
					ContentType: a.ContentType,
					Size:        int(a.SizeBytes),
					IsInline:    a.CID != "",
				})
			}
		}
	}

	if s.MailStore != nil {
		rfc822, err := s.MailStore.GetRFC822(ctx, msg.ID, nil)
		if err == nil && len(rfc822) > 0 {
			bc := mime.ExtractBodies(rfc822)
			if bc != nil {
				entry.TextBody = bc.TextBody
				entry.HTMLBody = bc.HTMLBody
				entry.HasHTML = bc.HasHTML
				entry.HasRemoteImages = bc.HasRemoteImages
			}
		}
	}

	return entry
}

func buildKeywords(msg *storage.Message) map[string]bool {
	k := make(map[string]bool)
	if msg.Seen {
		k["$seen"] = true
	}
	if msg.Answered {
		k["$answered"] = true
	}
	if msg.Flagged {
		k["$flagged"] = true
	}
	if msg.Draft {
		k["$draft"] = true
	}
	if msg.Deleted {
		k["$deleted"] = true
	}
	return k
}

func parseAddressListToJMAP(s string) []*EmailAddress {
	if s == "" {
		return nil
	}
	var addrs []*EmailAddress
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		addr := &EmailAddress{Email: p}
		if idx := strings.IndexByte(p, '<'); idx >= 0 {
			name := strings.TrimSpace(p[:idx])
			name = strings.Trim(name, "\"")
			addr.Name = name
			email := p[idx+1:]
			if end := strings.IndexByte(email, '>'); end >= 0 {
				addr.Email = email[:end]
			}
		}
		addrs = append(addrs, addr)
	}
	return addrs
}
