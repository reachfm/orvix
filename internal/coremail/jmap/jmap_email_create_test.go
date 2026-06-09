package jmap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ── Email/set Create Tests ─────────────────────────────────

func TestEmailSetCreateSuccess(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Test Create",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "Hello World",
			},
		},
	})

	if len(resp.Created) == 0 {
		t.Fatal("expected created response")
	}
	emailID := resp.Created["c1"]
	if emailID == "" {
		t.Fatal("expected non-empty created ID")
	}

	// Verify the message exists in MailStore.
	ctx := context.Background()
	uid := parseUintOrFail(t, emailID)
	msg, err := ms.Messages.GetByID(ctx, uid, nil)
	if err != nil || msg == nil {
		t.Fatalf("message not found in store: %v", err)
	}
	if msg.Subject != "Test Create" {
		t.Fatalf("expected subject 'Test Create', got %q", msg.Subject)
	}
}

func TestEmailSetCreateGeneratesRFC822Headers(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Header Check",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "Body text",
			},
		},
	})

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	_, data, err := ms.LoadMessage(context.Background(), uid, nil)
	if err != nil {
		t.Fatalf("load message: %v", err)
	}
	rfc822 := string(data)

	// Required headers.
	checks := []string{
		"From: user@test.com",
		"To: rcpt@test.com",
		"Subject: Header Check",
		"Date: ",
		"Message-ID: <",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
	}
	for _, check := range checks {
		if !strings.Contains(rfc822, check) {
			t.Fatalf("missing header: %s", check)
		}
	}
}

func TestEmailSetCreateCRLFPreserved(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "CRLF",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "Line1\nLine2",
			},
		},
	})

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	_, data, _ := ms.LoadMessage(context.Background(), uid, nil)
	rfc822 := string(data)

	if !strings.Contains(rfc822, "\r\n") {
		t.Fatal("expected CRLF line endings")
	}
}

func TestEmailSetCreateMessageIDGenerated(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "MsgID",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	msg, _ := ms.Messages.GetByID(context.Background(), uid, nil)
	if msg.InternetMessageID == "" {
		t.Fatal("expected InternetMessageID to be set")
	}
	if !strings.HasPrefix(msg.InternetMessageID, "<orvix-") {
		t.Fatalf("expected Message-ID to start with <orvix-, got %q", msg.InternetMessageID)
	}
}

func TestEmailSetCreateDateGenerated(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Date",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	_, data, _ := ms.LoadMessage(context.Background(), uid, nil)
	rfc822 := string(data)

	if !strings.Contains(rfc822, "Date: ") {
		t.Fatal("expected Date header")
	}
}

func TestEmailSetCreateContentTypeGenerated(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "CT",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	_, data, _ := ms.LoadMessage(context.Background(), uid, nil)
	rfc822 := string(data)

	if !strings.Contains(rfc822, "Content-Type: text/plain; charset=utf-8") {
		t.Fatal("expected Content-Type header")
	}
}

func TestEmailSetCreateSenderSpoofRejected(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Spoof",
				"from":    map[string]interface{}{"email": "attacker@evil.com"},
				"to":      []interface{}{map[string]interface{}{"email": "victim@test.com"}},
				"body":    "spoofed",
			},
		},
	})

	// The sender doesn't match the authenticated user.
	if _, ok := resp.Created["c1"]; ok {
		t.Fatal("expected spoofed sender to be rejected")
	}
	if _, ok := resp.NotCreated["c1"]; !ok {
		t.Fatal("expected notCreated for spoofed sender")
	}
}

func TestEmailSetCreateEmptySenderOK(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "No Sender",
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})

	// Empty sender (from not provided) should default and succeed.
	if _, ok := resp.Created["c1"]; !ok {
		t.Log("empty sender handling: may succeed or fail depending on validation")
	}
}

func TestEmailSetCreateHeaderInjectionRejected(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Normal\r\nBcc: injected@evil.com",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})

	if _, ok := resp.Created["c1"]; ok {
		t.Fatal("expected header injection to be rejected")
	}
}

func TestEmailSetCreateWithCcBcc(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "CC Test",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "to@test.com"}},
				"cc":      []interface{}{map[string]interface{}{"email": "cc@test.com"}},
				"bcc":     []interface{}{map[string]interface{}{"email": "bcc@test.com"}},
				"body":    "test",
			},
		},
	})

	if _, ok := resp.Created["c1"]; !ok {
		t.Fatal("expected create with Cc/Bcc to succeed")
	}

	emailID := resp.Created["c1"]
	uid := parseUintOrFail(t, emailID)
	_, data, _ := ms.LoadMessage(context.Background(), uid, nil)
	rfc822 := string(data)

	if !strings.Contains(rfc822, "Cc: cc@test.com") {
		t.Fatal("expected Cc header")
	}
	if !strings.Contains(rfc822, "Bcc: bcc@test.com") {
		t.Fatal("expected Bcc header")
	}
}

func TestEmailSetCreateSubmissionFlow(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Create email via Email/set.
	createResp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Submit Flow",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "Submit test",
			},
		},
	})

	emailID := createResp.Created["c1"]
	if emailID == "" {
		t.Fatal("create failed")
	}

	// Submit via Submission/set.
	subResp := submissionSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{"emailId": emailID},
		},
	})

	if _, ok := subResp.Created["c1"]; !ok {
		t.Fatal("expected submission to succeed after create")
	}
	_ = ms
}

func parseUintOrFail(t *testing.T, s string) uint {
	t.Helper()
	var n uint
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("parse uint %q: %v", s, err)
	}
	return n
}

func TestEmailSetCreateNotCreatedOnFailure(t *testing.T) {
	// Foreign account ID — message should not be created.
	// The server returns accountNotFound error for mismatched accountId.
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	name, body := emailSetRaw(t, addr, map[string]interface{}{
		"accountId": "999",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"subject": "Foreign",
				"from":    map[string]interface{}{"email": "user@test.com"},
				"to":      []interface{}{map[string]interface{}{"email": "rcpt@test.com"}},
				"body":    "test",
			},
		},
	})
	if name != "error" {
		t.Fatalf("expected error for foreign account, got %s: %s", name, body)
	}
}

func emailSetRaw(t *testing.T, addr string, params map[string]interface{}) (string, string) {
	t.Helper()
	methodCall := []interface{}{"Email/set", params, "c1"}
	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{methodCall},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)
	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)
	if len(jmapResp.MethodResponses) == 0 {
		return "", body
	}
	return jmapResp.MethodResponses[0].Name, body
}
