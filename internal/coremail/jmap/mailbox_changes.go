package jmap

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Server) handleMailboxChanges(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPMailboxChanges()
	}

	var params MailboxChangesRequest
	json.Unmarshal([]byte(mc.Params), &params)

	currentFolders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	newState := mailboxQueryState(currentFolders)

	if params.SinceState == newState {
		resp := MailboxChangesResponse{
			AccountID:      accountID,
			OldState:       params.SinceState,
			NewState:       newState,
			HasMoreChanges: false,
			Created:        []string{},
			Updated:        []string{},
			Destroyed:      []string{},
		}
		return &MethodResponse{Name: "Mailbox/changes", ID: mc.ID, Params: resp}
	}

	var sinceMaxID uint
	var sinceCount int
	fmt.Sscanf(params.SinceState, "s%d-c%d", &sinceMaxID, &sinceCount)

	var created, updated, destroyed []string

	for _, f := range currentFolders {
		if f.ID > sinceMaxID {
			created = append(created, fmt.Sprintf("%d", f.ID))
		} else {
			updated = append(updated, fmt.Sprintf("%d", f.ID))
		}
	}

	if sinceCount > len(currentFolders) {
		diff := sinceCount - len(currentFolders)
		for i := 0; i < diff; i++ {
			destroyed = append(destroyed, fmt.Sprintf("deleted-%d", i))
		}
	}

	if params.MaxChanges != nil && *params.MaxChanges > 0 && len(created)+len(updated)+len(destroyed) > *params.MaxChanges {
		created = nil
		updated = nil
		destroyed = nil
		for _, f := range currentFolders {
			updated = append(updated, fmt.Sprintf("%d", f.ID))
		}
	}

	resp := MailboxChangesResponse{
		AccountID:      accountID,
		OldState:       params.SinceState,
		NewState:       newState,
		HasMoreChanges: false,
		Created:        created,
		Updated:        updated,
		Destroyed:      destroyed,
	}

	return &MethodResponse{Name: "Mailbox/changes", ID: mc.ID, Params: resp}
}
