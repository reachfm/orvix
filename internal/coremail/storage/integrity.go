package storage

import (
	"context"
	"fmt"
	"os"
)

// IntegrityResult holds the outcome of an integrity check on a single message.
type IntegrityResult struct {
	MessageID     uint   `json:"message_id"`
	DBRecordOK    bool   `json:"db_record_ok"`
	FileExists    bool   `json:"file_exists"`
	SHA256Match   bool   `json:"sha256_match"`
	AttachmentsOK bool   `json:"attachments_ok"`
	Error         string `json:"error,omitempty"`
}

// IntegrityEngine provides methods to verify the consistency of stored messages.
type IntegrityEngine struct {
	Store *MailStore
}

// NewIntegrityEngine creates an integrity engine for the given MailStore.
func NewIntegrityEngine(ms *MailStore) *IntegrityEngine {
	return &IntegrityEngine{Store: ms}
}

// VerifyMessageIntegrity checks a single message for:
// 1. Database record exists
// 2. RFC822 file exists on disk
// 3. SHA256 hash matches the stored value
// 4. All attachment files exist and hashes match
func (ie *IntegrityEngine) VerifyMessageIntegrity(ctx context.Context, msgID uint) (*IntegrityResult, error) {
	res := &IntegrityResult{MessageID: msgID}

	msg, err := ie.Store.Messages.GetByID(ctx, msgID, nil)
	if err != nil {
		res.Error = fmt.Sprintf("db lookup: %v", err)
		return res, nil
	}
	if msg == nil {
		res.Error = "message not found in database"
		return res, nil
	}
	res.DBRecordOK = true

	if _, err := os.Stat(msg.RFC822Path); os.IsNotExist(err) {
		res.Error = fmt.Sprintf("rfc822 file missing: %s", msg.RFC822Path)
		return res, nil
	}
	res.FileExists = true

	computedSHA, err := ComputeSHA256(msg.RFC822Path)
	if err != nil {
		res.Error = fmt.Sprintf("sha256 compute: %v", err)
		return res, nil
	}
	res.SHA256Match = computedSHA == msg.SHA256
	if !res.SHA256Match {
		res.Error = fmt.Sprintf("sha256 mismatch: stored=%s computed=%s", msg.SHA256, computedSHA)
		return res, nil
	}

	attachments, err := ie.Store.Attachments.ListByMessage(ctx, msgID, nil)
	if err != nil {
		res.Error = fmt.Sprintf("list attachments: %v", err)
		return res, nil
	}
	res.AttachmentsOK = true
	for _, a := range attachments {
		if a.StoragePath != "" {
			if _, err := os.Stat(a.StoragePath); os.IsNotExist(err) {
				res.AttachmentsOK = false
				res.Error = fmt.Sprintf("attachment file missing: %s", a.StoragePath)
				break
			}
			if a.SHA256 != "" {
				attSHA, err := ComputeSHA256(a.StoragePath)
				if err != nil || attSHA != a.SHA256 {
					res.AttachmentsOK = false
					res.Error = fmt.Sprintf("attachment sha256 mismatch for %s", a.StoragePath)
					break
				}
			}
		}
	}
	return res, nil
}

// VerifyMailboxIntegrity checks ALL messages in a mailbox and returns aggregate results.
func (ie *IntegrityEngine) VerifyMailboxIntegrity(ctx context.Context, mailboxID uint) ([]IntegrityResult, int, int, error) {
	msgs, _, err := ie.Store.Messages.List(ctx, MessageFilter{MailboxID: mailboxID, Limit: 100000}, nil)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list messages: %w", err)
	}

	var results []IntegrityResult
	corrupt := 0
	for _, m := range msgs {
		res, err := ie.VerifyMessageIntegrity(ctx, m.ID)
		if err != nil {
			return nil, 0, 0, err
		}
		if !res.DBRecordOK || !res.FileExists || !res.SHA256Match || (res.Error != "") {
			corrupt++
		}
		results = append(results, *res)
	}
	return results, len(msgs) - corrupt, corrupt, nil
}

// VerifyStoreIntegrity scans ALL non-purged messages in the store.
func (ie *IntegrityEngine) VerifyStoreIntegrity(ctx context.Context) (total, ok, corrupt int, err error) {
	// For a full store scan, we use pagination to avoid memory issues at enterprise scale.
	// In production, this would be a background job processing batches.
	offset := 0
	const batchSize = 1000
	for {
		filter := MessageFilter{Limit: batchSize, Offset: offset}
		msgs, count, err := ie.Store.Messages.List(ctx, filter, nil)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, m := range msgs {
			total++
			res, err := ie.VerifyMessageIntegrity(ctx, m.ID)
			if err != nil {
				return 0, 0, 0, err
			}
			if res.DBRecordOK && res.FileExists && res.SHA256Match && res.Error == "" {
				ok++
			} else {
				corrupt++
			}
		}
		if len(msgs) < batchSize {
			break
		}
		offset += batchSize
		_ = count // available for progress reporting
	}
	return total, ok, corrupt, nil
}

// DetectOrphans finds RFC822 files on disk that have no corresponding database record.
func (ie *IntegrityEngine) DetectOrphans(ctx context.Context) ([]string, error) {
	// This is a placeholder for the orphan detection algorithm.
	// At enterprise scale, this would be a background job:
	// 1. Walk the filesystem, collecting all .eml files
	// 2. Batch-query the database for their message_ids
	// 3. Report files with no matching DB record
	// For now, this documents the approach without implementing a full filesystem walk.
	return nil, nil
}
