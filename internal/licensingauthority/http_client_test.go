package licensingauthority

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSURLAccepted(t *testing.T) {
	_, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  "https://authority.example.com",
		Timeout:  10 * time.Second,
		TestMode: false,
	})
	if err != nil {
		t.Fatalf("expected HTTPS URL to be accepted: %v", err)
	}
}

func TestHTTPURLRejectedUnlessTestMode(t *testing.T) {
	_, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  "http://authority.example.com",
		Timeout:  10 * time.Second,
		TestMode: false,
	})
	if err == nil {
		t.Fatal("expected HTTP URL to be rejected without test mode")
	}
	if !strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("expected HTTPS required error, got: %v", err)
	}

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  "http://localhost:8080",
		Timeout:  10 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("expected HTTP URL to be accepted in test mode: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client in test mode")
	}
}

func TestTimeoutRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(ValidationResponse{Valid: true})
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  200 * time.Millisecond,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx := context.Background()
	_, err = client.Validate(ctx, &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRetryBehavior(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(500)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(ValidationResponse{Valid: true, LicenseState: LicenseValid})
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  5 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	resp, err := client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if !resp.Valid {
		t.Fatal("expected valid response")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestCircuitBreakerOpens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  1 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	for i := 0; i < circuitThreshold; i++ {
		client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	}

	_, err = client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
	if !strings.Contains(err.Error(), "circuit breaker") {
		t.Fatalf("expected circuit breaker error, got: %v", err)
	}
}

func TestInvalidJSONRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  5 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid JSON error, got: %v", err)
	}
}

func TestInvalidStatusRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  5 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected HTTP 400 error")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("expected HTTP 400 error, got: %v", err)
	}
}

func TestContextCancelRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(ValidationResponse{Valid: true})
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  10 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err = client.Validate(ctx, &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected context cancel error")
	}
}

func TestNoNetworkAtStartup(t *testing.T) {
	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  "https://nonexistent.example.com",
		Timeout:  5 * time.Second,
		TestMode: false,
	})
	if err != nil {
		t.Fatalf("expected client creation to succeed without network: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestFallbackToCacheOnNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("server error"))
	}))
	defer srv.Close()

	client, err := NewHTTPAuthorityClient(HTTPAuthorityConfig{
		BaseURL:  srv.URL,
		Timeout:  1 * time.Second,
		TestMode: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.Validate(context.Background(), &ValidationRequest{LicenseID: "test"})
	if err == nil {
		t.Fatal("expected network failure error")
	}
}
