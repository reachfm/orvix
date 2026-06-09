package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	username, mailboxID, ok := s.authenticate(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	_ = mailboxID

	apiBase := fmt.Sprintf("https://%s/jmap", s.Hostname)
	session := &Session{
		Capabilities: s.capabilities(),
		Accounts:     s.accounts(r.Context(), username),
		PrimaryAccounts: map[string]string{
			"urn:ietf:params:jmap:core": username,
		},
		Username:       username,
		APITURL:        apiBase + "/api",
		DownloadURL:    apiBase + "/download/{accountId}/{blobId}/{name}",
		UploadURL:      apiBase + "/upload/{accountId}",
		EventSourceURL: apiBase + "/event",
		State:          fmt.Sprintf("%d", time.Now().Unix()),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(session)
}

func (s *Server) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	apiBase := fmt.Sprintf("https://%s/jmap", s.Hostname)
	resp := map[string]string{
		"apiUrl":         apiBase + "/api",
		"sessionUrl":     apiBase + "/session",
		"uploadUrl":      apiBase + "/upload/{accountId}",
		"downloadUrl":    apiBase + "/download/{accountId}/{blobId}/{name}",
		"eventSourceUrl": apiBase + "/event",
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) capabilities() map[string]interface{} {
	return map[string]interface{}{
		"urn:ietf:params:jmap:core": map[string]interface{}{
			"maxSizeUpload":          50 * 1024 * 1024,
			"maxConcurrentUpload":    4,
			"maxSizeRequest":         10 * 1024 * 1024,
			"maxConcurrentRequests":  4,
			"maxCallsInRequest":      16,
			"maxObjectsInGet":        500,
			"maxObjectsInSet":        500,
			"collationAlgorithms":    []string{"i;unicode-casemap"},
		},
		"urn:ietf:params:jmap:mail": map[string]interface{}{
			"maxMailboxesPerEmail":    100,
			"maxMailboxDepth":         10,
			"maxSizeMailboxName":      200,
		},
	}
}

func (s *Server) accounts(ctx context.Context, username string) map[string]*Account {
	mbox, err := s.Engine.Mailboxes.GetByEmail(ctx, username, nil)
	if err != nil || mbox == nil {
		return nil
	}

	accountID := fmt.Sprintf("%d", mbox.ID)

	return map[string]*Account{
		accountID: {
			Name:       username,
			IsPersonal: true,
			IsReadOnly: false,
			AccountCapabilities: map[string]interface{}{
				"urn:ietf:params:jmap:core": map[string]interface{}{},
				"urn:ietf:params:jmap:mail": map[string]interface{}{},
			},
		},
	}
}
