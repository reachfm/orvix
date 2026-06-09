package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Server) handleMailboxGet(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPMethodSuccess()
	}

	var params MailboxGetRequest
	json.Unmarshal([]byte(mc.Params), &params)

	folders, err := s.Engine.Mailboxes.GetByID(ctx, mailboxID, nil)
	if err != nil || folders == nil {
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPMethodFailure()
		}
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "accountNotFound"}}
	}
	_ = folders

	fs, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var entries []MailboxEntry
	var notFound []string

	for _, f := range fs {
		if len(params.IDs) > 0 {
			found := false
			for _, id := range params.IDs {
				if id == fmt.Sprintf("%d", f.ID) {
					found = true
					break
				}
			}
			if !found {
				notFound = append(notFound, fmt.Sprintf("%d", f.ID))
				continue
			}
		}

		total, _ := s.MailStore.Messages.CountByFolder(ctx, f.ID, nil)

		entry := MailboxEntry{
			ID:        fmt.Sprintf("%d", f.ID),
			Name:      f.Name,
			SortOrder: folderSortOrder(f.Name),
			TotalEmails: int(total),
			UnreadEmails: int(total),
			TotalThreads: int(total),
			UnreadThreads: int(total),
			MyRights: &MailboxRights{
				MayRead: true, MayReadItems: true, MayRemoveItems: true,
				MaySetSeen: true, MaySetKeywords: true,
			},
		}

		role := folderRole(f.Name)
		if role != "" {
			entry.Role = &role
		}

		entries = append(entries, entry)
	}

	if len(entries) == 0 && len(params.IDs) > 0 {
		notFound = params.IDs
	}

	resp := MailboxGetResponse{
		AccountID: accountID,
		State:     fmt.Sprintf("%d", time.Now().Unix()),
		List:      entries,
		NotFound:  notFound,
	}

	return &MethodResponse{Name: "Mailbox/get", ID: mc.ID, Params: resp}
}

func folderRole(name string) string {
	switch strings.ToLower(name) {
	case "inbox":
		return "inbox"
	case "sent":
		return "sent"
	case "trash":
		return "trash"
	case "drafts":
		return "drafts"
	case "spam", "junk":
		return "junk"
	case "archive":
		return "archive"
	default:
		return ""
	}
}

func folderSortOrder(name string) int {
	switch strings.ToLower(name) {
	case "inbox":
		return 1
	case "drafts":
		return 2
	case "sent":
		return 3
	case "archive":
		return 4
	case "spam", "junk":
		return 5
	case "trash":
		return 6
	default:
		return 10
	}
}
