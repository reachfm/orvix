package publicv1

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

func testIdempotencyStore(t *testing.T) *kernel.IdempotencyStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idempotency.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	store := kernel.NewIdempotencyStore(db, dbdialect.FromDriver("sqlite"))
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestIdempotentReplayMismatchAndConcurrency(t *testing.T) {
	store := testIdempotencyStore(t)
	app := fiber.New()
	var executions atomic.Int32
	app.Post("/api/v1/public/widgets/:id", func(c fiber.Ctx) error {
		c.Locals(RequestIDLocal, "req-1")
		c.Locals("api_key_tenant_id", uint(7))
		c.Locals("api_key_id", uint(9))
		return c.Next()
	}, Idempotent(store), func(c fiber.Ctx) error {
		n := executions.Add(1)
		time.Sleep(20 * time.Millisecond)
		return c.Status(201).JSON(struct {
			Execution int32 `json:"execution"`
		}{n})
	})

	do := func(key, body string) *http.Response {
		req := httptest.NewRequest("POST", "/api/v1/public/widgets/42", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := do("same", `{"name":"one","count":1}`)
	second := do("same", `{ "count": 1, "name": "one" }`)
	if first.StatusCode != 201 || second.StatusCode != 201 || executions.Load() != 1 {
		t.Fatalf("replay failed: first=%d second=%d executions=%d", first.StatusCode, second.StatusCode, executions.Load())
	}
	if second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("replay response missing Idempotency-Replayed header")
	}
	if mismatch := do("same", `{"name":"different"}`); mismatch.StatusCode != 409 {
		t.Fatalf("mismatch status=%d, want 409", mismatch.StatusCode)
	}

	var wg sync.WaitGroup
	type outcome struct {
		status int
		replay bool
	}
	statuses := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := do("concurrent", `{"name":"two"}`)
			statuses <- outcome{status: resp.StatusCode, replay: resp.Header.Get("Idempotency-Replayed") == "true"}
		}()
	}
	wg.Wait()
	close(statuses)
	created, conflict, replayed := 0, 0, 0
	for result := range statuses {
		if result.replay {
			replayed++
		}
		switch result.status {
		case 201:
			created++
		case 409:
			conflict++
		}
	}
	if executions.Load() != 2 || created+conflict != 2 || (conflict == 0 && replayed != 1) {
		t.Fatalf("concurrent created=%d conflict=%d replayed=%d executions=%d", created, conflict, replayed, executions.Load())
	}
}

func TestIdempotentAbandonsServerFailureAndRetention(t *testing.T) {
	store := testIdempotencyStore(t)
	app := fiber.New()
	var attempts atomic.Int32
	app.Post("/mutate", func(c fiber.Ctx) error {
		c.Locals(RequestIDLocal, "req-2")
		c.Locals("api_key_tenant_id", uint(1))
		c.Locals("api_key_id", uint(2))
		return c.Next()
	}, Idempotent(store), func(c fiber.Ctx) error {
		if attempts.Add(1) == 1 {
			return c.Status(500).JSON(ErrorResponse{Error: ErrorBody{Code: "FAILED", Message: "failed"}})
		}
		return c.JSON(struct {
			OK bool `json:"ok"`
		}{true})
	})
	do := func() int {
		req := httptest.NewRequest("POST", "/mutate", bytes.NewBufferString(`{"x":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "retry")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if do() != 500 || do() != 200 || attempts.Load() != 2 {
		t.Fatalf("failed request was not abandoned; attempts=%d", attempts.Load())
	}

	canonical, err := canonicalJSON([]byte(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]int
	if err := json.Unmarshal(canonical, &decoded); err != nil || decoded["a"] != 1 {
		t.Fatalf("canonical JSON invalid: %s", canonical)
	}
	if _, err := store.PurgeBefore(context.Background(), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}
