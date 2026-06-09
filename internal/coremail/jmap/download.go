package jmap

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	username, mailboxID, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = username

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/jmap/download/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid download URL", http.StatusBadRequest)
		return
	}
	accountID := parts[0]
	attachmentID := parts[1]

	_ = accountID

	var attID uint
	if _, err := fmt.Sscanf(attachmentID, "%d", &attID); err != nil {
		http.Error(w, "invalid attachment id", http.StatusBadRequest)
		return
	}

	att, err := s.MailStore.Attachments.GetByID(r.Context(), attID, nil)
	if err != nil || att == nil {
		http.Error(w, "attachment not found", http.StatusNotFound)
		return
	}

	msg, err := s.MailStore.Messages.GetByID(r.Context(), att.MessageID, nil)
	if err != nil || msg == nil || msg.MailboxID != mailboxID {
		http.Error(w, "attachment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, att.Filename))
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", att.SizeBytes))
	http.ServeFile(w, r, att.StoragePath)
}
