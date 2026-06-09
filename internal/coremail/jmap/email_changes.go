package jmap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/orvix/orvix/internal/coremail/storage"
)

func (s *Server) handleEmailChanges(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPEmailChanges()
	}

	var params EmailChangesRequest
	if err := json.Unmarshal([]byte(mc.Params), &params); err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "invalidArguments"}}
	}

	newState, newMaxID, msgCount, newMaxUpdated, err := s.computeEmailState(ctx, mailboxID)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var sinceMaxID uint
	var sinceCount int
	var sinceMaxUpdated int64
	n, parseErr := fmt.Sscanf(params.SinceState, "m%d-c%d-t%d", &sinceMaxID, &sinceCount, &sinceMaxUpdated)
	if n < 2 || parseErr != nil {
		fmt.Sscanf(params.SinceState, "s%d-c%d", &sinceMaxID, &sinceCount)
	}

	if newMaxID == sinceMaxID && msgCount == sinceCount && newMaxUpdated == sinceMaxUpdated {
		resp := EmailChangesResponse{
			AccountID: accountID, OldState: params.SinceState, NewState: newState,
			Created: []string{}, Updated: []string{}, Destroyed: []string{},
		}
		return &MethodResponse{Name: "Email/changes", ID: mc.ID, Params: resp}
	}

	folders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var created, updated, destroyed []string

	for _, f := range folders {
		msgs, _, err := s.MailStore.Messages.List(ctx, storage.MessageFilter{
			MailboxID: mailboxID,
			FolderID:  &f.ID,
		}, nil)
		if err != nil {
			continue
		}

		for _, m := range msgs {
			idStr := fmt.Sprintf("%d", m.ID)

			if m.ID > sinceMaxID {
				created = append(created, idStr)
			} else if m.UpdatedAt.Unix() > sinceMaxUpdated {
				updated = append(updated, idStr)
			}
		}
	}

	if sinceCount > msgCount {
		diff := sinceCount - msgCount
		destroyed = make([]string, 0, diff)
		for i := 0; i < diff; i++ {
			destroyed = append(destroyed, fmt.Sprintf("purged-%d", i))
		}
	} else {
		destroyed = []string{}
	}

	hasMore := false
	total := len(created) + len(updated) + len(destroyed)
	if params.MaxChanges != nil && *params.MaxChanges > 0 && total > *params.MaxChanges {
		hasMore = true
		created = truncateList(created, *params.MaxChanges)
		remaining := *params.MaxChanges - len(created)
		if remaining > 0 {
			updated = truncateList(updated, remaining)
		} else {
			updated = nil
		}
		remaining = *params.MaxChanges - len(created) - len(updated)
		if remaining > 0 {
			destroyed = truncateList(destroyed, remaining)
		} else {
			destroyed = nil
		}
	}

	resp := EmailChangesResponse{
		AccountID: accountID, OldState: params.SinceState, NewState: newState,
		HasMoreChanges: hasMore,
		Created: created, Updated: updated, Destroyed: destroyed,
	}
	return &MethodResponse{Name: "Email/changes", ID: mc.ID, Params: resp}
}

func (s *Server) computeEmailState(ctx context.Context, mailboxID uint) (state string, maxID uint, count int, maxUpdated int64, err error) {
	folders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return "", 0, 0, 0, err
	}

	for _, f := range folders {
		msgs, _, lerr := s.MailStore.Messages.List(ctx, storage.MessageFilter{
			MailboxID: mailboxID,
			FolderID:  &f.ID,
		}, nil)
		if lerr != nil {
			continue
		}
		for _, m := range msgs {
			count++
			if m.ID > maxID {
				maxID = m.ID
			}
			u := m.UpdatedAt.Unix()
			if u > maxUpdated {
				maxUpdated = u
			}
		}
	}

	state = fmt.Sprintf("m%d-c%d-t%d", maxID, count, maxUpdated)
	return state, maxID, count, maxUpdated, nil
}

func truncateList(list []string, max int) []string {
	if len(list) <= max {
		return list
	}
	return list[:max]
}

func (s *Server) emailQueryStateFromFolders(ctx context.Context, folders []storage.Folder) string {
	var maxID uint
	var count int
	var maxUpdated int64

	for _, f := range folders {
		msgs, _, err := s.MailStore.Messages.List(ctx, storage.MessageFilter{
			MailboxID: f.MailboxID,
			FolderID:  &f.ID,
		}, nil)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			count++
			if m.ID > maxID {
				maxID = m.ID
			}
			u := m.UpdatedAt.Unix()
			if u > maxUpdated {
				maxUpdated = u
			}
		}
	}

	return fmt.Sprintf("m%d-c%d-t%d", maxID, count, maxUpdated)
}
