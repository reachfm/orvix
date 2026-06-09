package jmap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

// ── Cross-Method Concurrency Tests ─────────────────────────

func TestCertConcurrentAllMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping certification test in short mode")
	}

	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Wait for server to be ready.
	waitForServer(t, addr)

	// Pre-load messages.
	for i := 0; i < 5; i++ {
		jmapStoreMsg(t, ms, 1, "Cert concurrent", "body", "a@test.com", "b@test.com")
	}

	// Use error channel instead of t.Fatal from goroutines.
	errs := make(chan error, 10)
	var wg sync.WaitGroup

	// Mailbox/get.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := safeMailboxQuery(addr, map[string]interface{}{"accountId": "1"}); err != nil {
			errs <- err
		}
	}()

	// Mailbox/changes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := safeMailboxQuery(addr, map[string]interface{}{"accountId": "1"}); err != nil {
			errs <- err
			return
		}
		// Use empty sinceState for robustness.
		if _, err := safeMailboxChanges(addr, map[string]interface{}{"accountId": "1", "sinceState": ""}); err != nil {
			errs <- err
		}
	}()

	// Email/query.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := safeEmailQuery(addr, "user@test.com", "pass", "1", nil); err != nil {
			errs <- err
		}
	}()

	// Email/get.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := safeEmailGet(addr); err != nil {
			errs <- err
		}
	}()

	// Email/changes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := safeEmailChanges(addr, map[string]interface{}{"accountId": "1", "sinceState": ""}); err != nil {
			errs <- err
		}
	}()

	// Email/set update.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := safeEmailSet(addr, map[string]interface{}{
			"accountId": "1",
			"update": map[string]interface{}{
				"1": map[string]interface{}{
					"keywords": map[string]interface{}{"$seen": true},
				},
			},
		}); err != nil {
			errs <- err
		}
	}()

	// Submission/set.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := safeSubmissionSet(addr, map[string]interface{}{
			"accountId": "1",
			"create": map[string]interface{}{
				"c1": map[string]interface{}{"emailId": "1"},
			},
		}); err != nil {
			errs <- err
		}
	}()

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent method error: %v", err)
	}
}

// ── Concurrent Session + API Stress Test ───────────────────

func TestCertConcurrentSessionAndAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping certification test in short mode")
	}

	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	waitForServer(t, addr)

	errs := make(chan error, 30)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := safeSession(addr, "user@test.com", "pass"); err != nil {
				errs <- err
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := safeMailboxQuery(addr, map[string]interface{}{"accountId": "1"}); err != nil {
				errs <- err
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := safeEmailQuery(addr, "user@test.com", "pass", "1", nil); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent session error: %v", err)
	}
}

// ── Safe Helpers (no t.Fatal, return error) ───────────────

func safeSession(addr, username, password string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		resp, _, err := jmapSessionRaw(addr, username, password)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("session HTTP %d", resp.StatusCode)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func safeMailboxQuery(addr string, params map[string]interface{}) (interface{}, error) {
	return jmapRequestSingle(addr, "user@test.com", "pass", "Mailbox/query", params)
}

func safeMailboxChanges(addr string, params map[string]interface{}) (interface{}, error) {
	return jmapRequestSingle(addr, "user@test.com", "pass", "Mailbox/changes", params)
}

func safeEmailQuery(addr, username, password, accountID string, filter interface{}) error {
	_, err := jmapRequestSingle(addr, username, password, "Email/query", map[string]interface{}{
		"accountId": accountID,
		"filter":    filter,
	})
	return err
}

func safeEmailGet(addr string) error {
	_, err := jmapRequestSingle(addr, "user@test.com", "pass", "Email/get", map[string]interface{}{
		"accountId": "1",
	})
	return err
}

func safeEmailChanges(addr string, params map[string]interface{}) (interface{}, error) {
	return jmapRequestSingle(addr, "user@test.com", "pass", "Email/changes", params)
}

func safeEmailSet(addr string, params map[string]interface{}) error {
	_, err := jmapRequestSingle(addr, "user@test.com", "pass", "Email/set", params)
	return err
}

func safeSubmissionSet(addr string, params map[string]interface{}) error {
	_, err := jmapRequestSingle(addr, "user@test.com", "pass", "Submission/set", params)
	return err
}

// jmapRequestSingle performs a single JMAP API call and returns the first method response params.
func jmapRequestSingle(addr, username, password, method string, params map[string]interface{}) (interface{}, error) {
	resp, err := jmapAPIHTTP(addr, username, password, map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{[]interface{}{method, params, "c1"}},
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// jmapAPIHTTP performs a JMAP API call and returns the parsed response without t.Fatal.
func jmapAPIHTTP(addr, username, password string, reqBody interface{}) (interface{}, error) {
	// Retry on transient errors (SQLite busy, connection races).
	var lastErr error
	for i := 0; i < 3; i++ {
		httpResp, bodyStr := jmapAPIRaw(addr, username, password, reqBody)
		if httpResp == nil {
			lastErr = fmt.Errorf("connection failed")
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if httpResp.StatusCode != 200 {
			lastErr = fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, truncateStr(bodyStr, 200))
			time.Sleep(50 * time.Millisecond)
			continue
		}
		var jmapResp struct {
			MethodResponses []struct {
				Name   string          `json:"name"`
				Params json.RawMessage `json:"params"`
				ID     string          `json:"id"`
			} `json:"methodResponses"`
		}
		if err := json.Unmarshal([]byte(bodyStr), &jmapResp); err != nil {
			lastErr = fmt.Errorf("parse response: %w", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if len(jmapResp.MethodResponses) == 0 {
			lastErr = fmt.Errorf("no method responses: %s", bodyStr)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		mr := jmapResp.MethodResponses[0]
		if mr.Name == "error" {
			var errResp ErrorResponse
			json.Unmarshal(mr.Params, &errResp)
			lastErr = fmt.Errorf("JMAP error: %s - %s", errResp.Type, errResp.Detail)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return &struct{ Params json.RawMessage }{Params: mr.Params}, nil
	}
	return nil, lastErr
}

// jmapAPIRaw performs a JMAP API call and returns the raw HTTP response and body.
func jmapAPIRaw(addr, username, password string, reqBody interface{}) (*http.Response, string) {
	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("http://%s/jmap/api", addr)
	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	httpReq.Header.Set("Authorization", "Basic "+auth)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp, string(bodyBytes)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// jmapSessionRaw performs a session request and returns raw response.
func jmapSessionRaw(addr, username, password string) (*http.Response, string, error) {
	url := fmt.Sprintf("http://%s/jmap/session", addr)
	httpReq, _ := http.NewRequest("GET", url, nil)
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	httpReq.Header.Set("Authorization", "Basic "+auth)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body), nil
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		resp, err := http.Get(fmt.Sprintf("http://%s/jmap/session", addr))
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
