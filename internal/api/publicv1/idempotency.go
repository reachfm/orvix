package publicv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/kernel"
)

const IdempotencyRetention = 24 * time.Hour

func Idempotent(store *kernel.IdempotencyStore) fiber.Handler {
	return func(c fiber.Ctx) error {
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		if key == "" || len(key) > 255 || strings.ContainsAny(key, "\r\n") {
			return WriteError(c, fiber.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "A valid Idempotency-Key header is required.")
		}
		tenantID, _ := c.Locals("api_key_tenant_id").(uint)
		keyID, _ := c.Locals("api_key_id").(uint)
		if tenantID == 0 || keyID == 0 {
			return WriteError(c, fiber.StatusForbidden, "TENANT_REQUIRED", "A tenant-bound API key is required.")
		}
		body := append([]byte(nil), c.Body()...)
		canonical, err := canonicalJSON(body)
		if err != nil {
			return WriteError(c, fiber.StatusBadRequest, "INVALID_REQUEST", "The request body must be valid JSON.")
		}
		scope := fmt.Sprintf("public:v1:tenant:%d:key:%d:%s:%s", tenantID, keyID, c.Method(), c.Path())
		stored, replay, err := store.Begin(c.Context(), scope, key, kernel.RequestHash(canonical), time.Now().UTC())
		if err != nil {
			if errors.Is(err, kernel.ErrIdempotencyInFlight) {
				return WriteError(c, fiber.StatusConflict, "IDEMPOTENCY_IN_FLIGHT", "An identical request is already in progress.")
			}
			var apiErr *kernel.Error
			if errors.As(err, &apiErr) && apiErr.Code == kernel.ErrCodeIdempotencyReuse {
				return WriteError(c, fiber.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "The idempotency key was used with a different request.")
			}
			return WriteError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed.")
		}
		if replay {
			c.Set("Idempotency-Replayed", "true")
			c.Type("json")
			return c.Status(stored.StatusCode).SendString(stored.ResponseBody)
		}

		handlerErr := c.Next()
		status := c.Response().StatusCode()
		if handlerErr != nil || status >= 500 {
			_ = store.Abandon(c.Context(), scope, key)
			return handlerErr
		}
		responseBody := append([]byte(nil), c.Response().Body()...)
		if !json.Valid(responseBody) {
			_ = store.Abandon(c.Context(), scope, key)
			return WriteError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed.")
		}
		if err := store.Complete(c.Context(), scope, key, status, json.RawMessage(responseBody), time.Now().UTC()); err != nil {
			_ = store.Abandon(c.Context(), scope, key)
			return WriteError(c, fiber.StatusInternalServerError, "INTERNAL_ERROR", "The request could not be completed.")
		}
		return nil
	}
}

func canonicalJSON(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}
