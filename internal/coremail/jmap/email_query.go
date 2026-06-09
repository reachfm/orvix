package jmap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/orvix/orvix/internal/coremail/storage"
)

func (s *Server) handleEmailQuery(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPMethodSuccess()
	}

	var params EmailQueryRequest
	json.Unmarshal([]byte(mc.Params), &params)

	folders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var allMsgs []storage.Message
	for _, f := range folders {
		msgs, _, err := s.MailStore.Messages.List(ctx, storage.MessageFilter{
			MailboxID: mailboxID,
			FolderID:  &f.ID,
		}, nil)
		if err != nil {
			continue
		}
		allMsgs = append(allMsgs, msgs...)
	}

	filtered := s.applyQueryFilter(allMsgs, params.Filter)
	sorted := s.applyQuerySort(filtered, params.Sort)

	pos := params.Position
	if pos < 0 {
		pos = 0
	}
	if pos >= len(sorted) {
		pos = len(sorted)
	}

	var limit int
	if params.Limit != nil && *params.Limit > 0 {
		limit = *params.Limit
		if limit > 500 {
			limit = 500
		}
	} else {
		limit = len(sorted)
	}

	end := pos + limit
	if end > len(sorted) {
		end = len(sorted)
	}

	page := sorted[pos:end]

	ids := make([]string, len(page))
	for i, m := range page {
		ids[i] = fmt.Sprintf("%d", m.ID)
	}

	queryState, _, _, _, _ := s.computeEmailState(ctx, mailboxID)

	resp := EmailQueryResponse{
		AccountID:           accountID,
		QueryState:          queryState,
		CanCalculateChanges: false,
		Position:            pos,
		IDs:                 ids,
	}

	if params.CalculateTotal {
		total := len(filtered)
		resp.Total = &total
	}

	if params.Limit != nil && *params.Limit > 0 {
		resp.Limit = params.Limit
	}

	return &MethodResponse{Name: "Email/query", ID: mc.ID, Params: resp}
}
