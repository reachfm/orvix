package jmap

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestJMAPCORSDeniesWildcardByDefault(t *testing.T) {
	srv := NewServer(nil)
	handler := srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/jmap/api", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no wildcard origin, got %q", got)
	}
}

func TestJMAPCORSAllowsConfiguredOrigin(t *testing.T) {
	srv := NewServer(nil)
	srv.SetAllowedOrigins([]string{"*", "https://mail.example"})
	handler := srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/jmap/api", nil)
	req.Header.Set("Origin", "https://mail.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected no content preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://mail.example" {
		t.Fatalf("expected configured origin, got %q", got)
	}
}

func TestStreamUploadToFileEnforcesLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.bin")
	payload := bytes.NewReader(bytes.Repeat([]byte("x"), int(maxUploadBytes)+1))

	size, err := streamUploadToFile(payload, path)
	if err != errUploadTooLarge {
		t.Fatalf("expected errUploadTooLarge, got size=%d err=%v", size, err)
	}
}

func TestStreamUploadToFileStoresWithinLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.bin")
	payload := []byte("streamed attachment")

	size, err := streamUploadToFile(bytes.NewReader(payload), path)
	if err != nil {
		t.Fatalf("stream upload: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), size)
	}
}
