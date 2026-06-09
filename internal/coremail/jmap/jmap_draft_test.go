package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestDraftCreate(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Ensure Drafts folder exists.
	ctx := context.Background()
	inbox, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	_ = inbox

	// Find Drafts folder ID.
	draftsFolder, _ := ms.Folders.GetByPath(ctx, 1, "Drafts", nil)
	if draftsFolder == nil {
		t.Fatal("Drafts folder should exist")
	}
	draftsID := draftsFolder.ID

	// Create draft with $draft keyword in Drafts folder.
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Draft Test",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Draft body",
			},
		},
	})

	if len(resp.Created) == 0 {
		t.Fatal("expected created response")
	}

	draftID := resp.Created["c1"]
	if draftID == "" {
		t.Fatal("expected draft ID")
	}

	// Verify draft exists in DB and has draft flag.
	msg, err := ms.Messages.GetByID(ctx, parseUintFromString(draftID), nil)
	if err != nil || msg == nil {
		t.Fatal("draft message not found in DB")
	}
	if !msg.Draft {
		t.Fatal("expected draft flag on saved draft")
	}
}

func TestDraftUpdate(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	ctx := context.Background()
	draftsFolder, _ := ms.Folders.GetByPath(ctx, 1, "Drafts", nil)
	if draftsFolder == nil {
		t.Fatal("Drafts folder should exist")
	}

	// Create draft.
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsFolder.ID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Original Draft",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Original body",
			},
		},
	})
	oldDraftID := resp.Created["c1"]

	// Update draft: create new + destroy old.
	updateResp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsFolder.ID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Updated Draft",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Updated body",
			},
		},
		"destroy": []string{oldDraftID},
	})

	if len(updateResp.Created) == 0 {
		t.Fatal("expected created response for draft update")
	}
	newDraftID := updateResp.Created["c1"]
	if newDraftID == oldDraftID {
		t.Fatal("draft update should create new message ID")
	}

	// Old draft should be destroyed.
	oldMsg, _ := ms.Messages.GetByID(ctx, parseUintFromString(oldDraftID), nil)
	if oldMsg != nil {
		t.Fatal("old draft should be purged")
	}

	// New draft should have updated content.
	newMsg, _ := ms.Messages.GetByID(ctx, parseUintFromString(newDraftID), nil)
	if newMsg == nil {
		t.Fatal("new draft not found")
	}
	if !newMsg.Draft {
		t.Fatal("new message should still be a draft")
	}
}

func TestDraftGet(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	ctx := context.Background()
	draftsFolder, _ := ms.Folders.GetByPath(ctx, 1, "Drafts", nil)

	// Create draft.
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsFolder.ID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Draft to Get",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Draft body for get test",
			},
		},
	})
	draftID := resp.Created["c1"]

	// Fetch via Email/get.
	_, body := jmapAPI(t, addr, "user@test.com", "pass", map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{[]interface{}{"Email/get", map[string]interface{}{"accountId": "1", "ids": []string{draftID}}, "c1"}},
	})

	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	for _, mr := range jmapResp.MethodResponses {
		if mr.Name == "Email/get" {
			var getResp EmailGetResponse
			json.Unmarshal(mr.Params, &getResp)
			if len(getResp.List) == 0 {
				t.Fatal("expected draft in get response")
			}
			entry := getResp.List[0]
			if !entry.Keywords["$draft"] {
				t.Fatal("expected $draft keyword in Email/get")
			}
			if entry.Subject != "Draft to Get" {
				t.Fatalf("expected subject 'Draft to Get', got '%s'", entry.Subject)
			}
			return
		}
	}
	t.Fatal("no Email/get response")
}

func TestDraftDelete(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	ctx := context.Background()
	draftsFolder, _ := ms.Folders.GetByPath(ctx, 1, "Drafts", nil)

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsFolder.ID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Delete Test",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Delete me",
			},
		},
	})
	draftID := resp.Created["c1"]

	// Destroy the draft.
	emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{draftID},
	})

	// Verify destroyed.
	msg, _ := ms.Messages.GetByID(ctx, parseUintFromString(draftID), nil)
	if msg != nil {
		t.Fatal("draft should be destroyed")
	}
}

func TestDraftSendMovesToSent(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	ctx := context.Background()
	draftsFolder, _ := ms.Folders.GetByPath(ctx, 1, "Drafts", nil)
	sentFolder, _ := ms.Folders.GetByPath(ctx, 1, "Sent", nil)

	// Create a draft.
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{fmt.Sprintf("%d", draftsFolder.ID): true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Send Draft",
				"from":       map[string]interface{}{"email": "user@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "Draft to send",
			},
		},
	})
	draftID := resp.Created["c1"]

	// Submit the draft via Submission/set.
	submissionSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{"emailId": draftID},
		},
	})

	// Verify draft moved from Drafts to Sent.
	msg, _ := ms.Messages.GetByID(ctx, parseUintFromString(draftID), nil)
	if msg == nil {
		t.Fatal("message should still exist after send")
	}
	if msg.FolderID != sentFolder.ID {
		t.Fatalf("expected message in Sent folder (ID %d), got folder %d", sentFolder.ID, msg.FolderID)
	}
	if msg.Draft {
		t.Fatal("message should no longer be a draft after send")
	}
	if !msg.Seen {
		t.Fatal("message should be marked seen after send")
	}
}

func TestDraftForeignRejected(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Try to submit a draft that doesn't exist.
	method, body := emailSetRaw(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Test",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})
	if method == "error" {
		t.Fatalf("create failed: %s", body)
	}

	// Try submission with wrong accountId.
	method2, _ := emailSetRaw(t, addr, map[string]interface{}{
		"accountId": "999",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{"999": true},
				"keywords":   map[string]bool{"$draft": true},
				"subject":    "Foreign",
				"from":       map[string]interface{}{"email": "other@test.com"},
				"to":         []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":       "test",
			},
		},
	})
	if method2 != "error" {
		t.Fatal("expected error for foreign account")
	}
}

func parseUintFromString(s string) uint {
	var n uint
	fmt.Sscanf(s, "%d", &n)
	return n
}
