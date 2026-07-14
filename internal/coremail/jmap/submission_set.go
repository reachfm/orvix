package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/policy"
)

func (s *Server) SetQueueEngine(qe interface {
	Enqueue(ctx context.Context, entry *queue.QueueEntry) error
}) {
	s.queueEngine = qe
}

func (s *Server) SetTrustEngine(te interface{ IsLockedOut(key string) bool }) {
	s.trustEngine = te
}

func (s *Server) SetPolicyEngine(pe interface {
	Evaluate(req *policy.EvaluationRequest) *policy.EvaluationResult
}) {
	s.policyEngine = pe
}

func (s *Server) handleSubmissionSet(ctx context.Context, mc *MethodCall, mailboxID uint, accountID string, username string) *MethodResponse {
	if s.Observability != nil {
		s.Observability.Metrics.IncJMAPSubmission()
	}

	var params SubmissionSetRequest
	if err := json.Unmarshal([]byte(mc.Params), &params); err != nil {
		return &MethodResponse{Name: "error", ID: mc.ID, Params: ErrorResponse{Type: "invalidArguments"}}
	}

	resp := SubmissionSetResponse{
		AccountID:  accountID,
		Created:    make(map[string]*SubmissionCreated),
		NotCreated: make(map[string]string),
	}

	for clientID, create := range params.Create {
		if create == nil {
			resp.NotCreated[clientID] = "invalidArguments"
			continue
		}
		if create.EmailID == "" {
			resp.NotCreated[clientID] = "invalidArguments"
			continue
		}

		emailID, idErr := parseUint(create.EmailID)
		if idErr != nil {
			resp.NotCreated[clientID] = "invalidArguments"
			continue
		}

		msg, err := s.MailStore.Messages.GetByID(ctx, emailID, nil)
		if err != nil || msg == nil || msg.MailboxID != mailboxID {
			resp.NotCreated[clientID] = "notFound"
			if s.Observability != nil {
				s.Observability.Metrics.IncJMAPSubmissionFailed()
			}
			continue
		}

		sender := create.Sender
		if sender == "" {
			sender = username
		}

		if sender != username {
			resp.NotCreated[clientID] = "forbidden"
			if s.Observability != nil {
				s.Observability.Metrics.IncJMAPSubmissionFailed()
			}
			continue
		}

		if s.policyEngine != nil {
			senderDomain := extractDomainFromEmail(sender)
			peReq := &policy.EvaluationRequest{
				Direction: policy.Send,
				Scope:     policy.External,
				TenantID:  0,
				Domain:    senderDomain,
			}
			peResult := s.policyEngine.Evaluate(peReq)
			if peResult.Action != policy.ActionAllow {
				resp.NotCreated[clientID] = "forbidden"
				if s.Observability != nil {
					s.Observability.Metrics.IncJMAPSubmissionFailed()
				}
				continue
			}
		}

		if s.trustEngine != nil {
			if s.trustEngine.IsLockedOut("submission:" + sender) {
				resp.NotCreated[clientID] = "forbidden"
				if s.Observability != nil {
					s.Observability.Metrics.IncJMAPSubmissionFailed()
				}
				continue
			}
		}

		recipientDomain := extractDomainFromEmail(msg.ToAddresses)
		entry := &queue.QueueEntry{
			TenantID:        msg.TenantID,
			DomainID:        msg.DomainID,
			MailboxID:       &msg.MailboxID,
			MessageID:       msg.MessageID,
			FromAddress:     sender,
			ToAddress:       msg.ToAddresses,
			RecipientDomain: recipientDomain,
			Direction:       queue.DirectionOutbound,
			DeliveryMode:    queue.DeliveryRemoteSMTP,
			Status:          queue.StatusPending,
		}

		if s.queueEngine != nil {
			if err := s.queueEngine.Enqueue(ctx, entry); err != nil {
				resp.NotCreated[clientID] = "serverFail"
				if s.Observability != nil {
					s.Observability.Metrics.IncJMAPSubmissionFailed()
				}
				continue
			}
		}

		// After successful enqueue, move draft to Sent folder.
		if msg.Draft {
			sentFolder, findErr := findSentFolder(ctx, s.MailStore.Folders, mailboxID)
			if findErr == nil && sentFolder != nil {
				seen := true
				draft := false
				s.MailStore.Messages.UpdateFlags(ctx, msg.ID, &seen, nil, nil, &draft, nil, nil, nil)
				s.MailStore.MoveMessage(ctx, msg.ID, sentFolder.ID, nil)
			}
		}

		submissionID := fmt.Sprintf("sub-%s-%d", msg.MessageID[:8], msg.ID)

		resp.Created[clientID] = &SubmissionCreated{ID: submissionID}
		if s.Observability != nil {
			s.Observability.Metrics.IncJMAPSubmissionQueued()
		}
	}

	return &MethodResponse{Name: "Submission/set", ID: mc.ID, Params: resp}
}

func extractDomainFromEmail(email string) string {
	idx := strings.LastIndexByte(email, '@')
	if idx < 0 {
		return email
	}
	return email[idx+1:]
}

func findSentFolder(ctx context.Context, repo storage.FolderRepository, mailboxID uint) (*storage.Folder, error) {
	folders, err := repo.ListByMailbox(ctx, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	for _, f := range folders {
		if f.FolderType == storage.FolderSent || strings.EqualFold(f.Name, "Sent") {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("sent folder not found")
}
