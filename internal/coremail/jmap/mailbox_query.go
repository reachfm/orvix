package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/orvix/orvix/internal/coremail/storage"
)

func (s *Server) handleMailboxQuery(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPMailboxQuery()
	}

	var params MailboxQueryRequest
	json.Unmarshal([]byte(mc.Params), &params)

	folders, err := s.MailStore.Folders.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "serverFail"}}
	}

	var filtered []storage.Folder
	for _, f := range folders {
		if !matchMailboxFilter(&f, params.Filter) {
			continue
		}
		filtered = append(filtered, f)
	}

	if params.Sort != nil && len(params.Sort) > 0 {
		for _, s := range params.Sort {
			asc := s.IsAscending != nil && *s.IsAscending
			switch s.Property {
			case "name":
				if asc {
					sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
				} else {
					sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name > filtered[j].Name })
				}
			case "sortOrder":
				if asc {
					sort.Slice(filtered, func(i, j int) bool { return folderSortOrder(filtered[i].Name) < folderSortOrder(filtered[j].Name) })
				} else {
					sort.Slice(filtered, func(i, j int) bool { return folderSortOrder(filtered[i].Name) > folderSortOrder(filtered[j].Name) })
				}
			}
		}
	} else {
		sort.Slice(filtered, func(i, j int) bool {
			return folderSortOrder(filtered[i].Name) < folderSortOrder(filtered[j].Name)
		})
	}

	pos := params.Position
	if pos < 0 { pos = 0 }
	if pos >= len(filtered) { pos = len(filtered) }
	limit := 500
	if params.Limit != nil && *params.Limit > 0 && *params.Limit < limit {
		limit = *params.Limit
	}
	end := pos + limit
	if end > len(filtered) { end = len(filtered) }
	page := filtered[pos:end]

	ids := make([]string, len(page))
	for i, f := range page {
		ids[i] = fmt.Sprintf("%d", f.ID)
	}

	queryState := mailboxQueryState(folders)

	resp := MailboxQueryResponse{
		AccountID:           accountID,
		QueryState:          queryState,
		CanCalculateChanges: true,
		Position:            pos,
		IDs:                 ids,
	}

	if params.CalculateTotal {
		total := len(filtered)
		resp.Total = &total
	}

	return &MethodResponse{Name: "Mailbox/query", ID: mc.ID, Params: resp}
}

func matchMailboxFilter(f *storage.Folder, filter *MailboxQueryFilter) bool {
	if filter == nil {
		return true
	}
	if filter.Role != nil {
		role := folderRole(f.Name)
		if role != *filter.Role {
			return false
		}
	}
	if filter.ParentID != nil {
		pid := ""
		if f.ParentID != nil {
			pid = fmt.Sprintf("%d", *f.ParentID)
		}
		if pid != *filter.ParentID {
			return false
		}
	}
	if filter.Name != "" {
		if !strings.Contains(strings.ToLower(f.Name), strings.ToLower(filter.Name)) {
			return false
		}
	}
	return true
}

func mailboxQueryState(folders []storage.Folder) string {
	var maxID uint
	for _, f := range folders {
		if f.ID > maxID {
			maxID = f.ID
		}
	}
	return fmt.Sprintf("s%d-c%d", maxID, len(folders))
}
