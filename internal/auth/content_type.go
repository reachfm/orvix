package auth

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Fiber v3's Bind().JSON() parses the raw request body as JSON regardless of
// the declared Content-Type. That makes every JSON-binding mutation reachable
// as a CORS "simple request": a cross-site page can POST
// `Content-Type: text/plain` (or an HTML <form> can POST
// application/x-www-form-urlencoded / multipart/form-data) with a JSON body,
// and the browser sends it WITHOUT a preflight — so the attacker never needs
// permission from CORS and never needs to read the response.
//
// CSRF tokens are the primary defence (an attacker cannot set the
// X-CSRF-Token header cross-site without triggering a preflight the server
// will refuse). RequireJSONContentType is the second, independent layer: it
// rejects exactly the three content types an HTML form can produce, so a
// body-optional mutation cannot be driven by a plain cross-site <form> even
// if the CSRF layer were ever misconfigured.
//
// It must be mounted alongside — never instead of — the CSRF middleware.

// jsonContentType is the only media type accepted for JSON mutations.
const jsonContentType = "application/json"

// multipartContentType is accepted only by routes that genuinely upload
// files (webmail send with attachments).
const multipartContentType = "multipart/form-data"

// ContentTypeOption configures RequireJSONContentType.
type ContentTypeOption func(*contentTypeConfig)

type contentTypeConfig struct {
	allowMultipart bool
}

// AllowMultipart permits multipart/form-data in addition to
// application/json. Use it only for routes that actually parse a multipart
// body (file upload); every other mutation must stay JSON-only so a plain
// cross-site HTML form can never reach the handler.
func AllowMultipart() ContentTypeOption {
	return func(cfg *contentTypeConfig) { cfg.allowMultipart = true }
}

// RequireJSONContentType rejects state-changing requests whose Content-Type is
// not an accepted media type, with a stable 415 envelope.
//
// Safe methods (GET/HEAD/OPTIONS) pass through untouched. A request that
// declares no Content-Type at all is allowed only when it carries no body:
// an HTML form submission ALWAYS declares one of the three form content
// types, so an absent Content-Type cannot originate from a <form>, while
// body-less POSTs (archive, delete, mark-read, unsubscribe) remain valid for
// legitimate clients.
func RequireJSONContentType(opts ...ContentTypeOption) fiber.Handler {
	cfg := &contentTypeConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}

		media := mediaType(c.Get(fiber.HeaderContentType))
		if media == "" {
			if len(c.Body()) == 0 {
				return c.Next()
			}
			return unsupportedMediaType(c)
		}

		if media == jsonContentType {
			return c.Next()
		}
		if cfg.allowMultipart && media == multipartContentType {
			return c.Next()
		}
		return unsupportedMediaType(c)
	}
}

// mediaType lowercases a Content-Type header and strips any parameters
// (charset, boundary) so `application/json; charset=utf-8` compares equal to
// `application/json`.
func mediaType(header string) string {
	value := strings.TrimSpace(header)
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, ';'); idx >= 0 {
		value = value[:idx]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func unsupportedMediaType(c fiber.Ctx) error {
	return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
		"error": "unsupported media type: this endpoint requires application/json",
		"code":  "unsupported_media_type",
	})
}
