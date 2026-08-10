package publicv1

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const RequestIDLocal = "public_request_id"

func Correlation() fiber.Handler {
	return func(c fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 || strings.ContainsAny(requestID, "\r\n") {
			requestID = uuid.NewString()
		}
		c.Locals(RequestIDLocal, requestID)
		c.Set("X-Request-ID", requestID)
		c.Set("Cache-Control", "no-store")
		return c.Next()
	}
}

func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		scopes, ok := c.Locals("api_key_scopes").(map[string]struct{})
		if !ok {
			return WriteError(c, fiber.StatusForbidden, "INSUFFICIENT_SCOPE", "The API key does not grant this operation.")
		}
		if _, ok := scopes[scope]; !ok {
			return WriteError(c, fiber.StatusForbidden, "INSUFFICIENT_SCOPE", "The API key does not grant this operation.")
		}
		return c.Next()
	}
}

func RequestID(c fiber.Ctx) string {
	requestID, _ := c.Locals(RequestIDLocal).(string)
	return requestID
}

func WriteError(c fiber.Ctx, status int, code, message string, details ...ErrorDetail) error {
	return c.Status(status).JSON(ErrorResponse{Error: ErrorBody{
		Code: code, Message: message, Details: details, RequestID: RequestID(c),
	}})
}
