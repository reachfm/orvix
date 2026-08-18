package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisRateLimiter implements distributed rate limiting using Redis.
type RedisRateLimiter struct {
	client     *redis.Client
	logger     *zap.Logger
	defaultMax int
	window     time.Duration
	loginMax   int
	loginWin   time.Duration
	csrfMax    int
	csrfWin    time.Duration
}

// NewRedisRateLimiter creates a new Redis-backed rate limiter.
func NewRedisRateLimiter(client *redis.Client, logger *zap.Logger) *RedisRateLimiter {
	return &RedisRateLimiter{
		client:     client,
		logger:     logger,
		defaultMax: 100,
		window:     60 * time.Second,
		loginMax:   5,
		loginWin:   15 * time.Minute,
		// GET /api/v1/csrf-token is a safe, unauthenticated bootstrap
		// endpoint that a normal browser session calls repeatedly —
		// once per page load/reload, plus once per automatic 403-retry
		// on a mutation with a rotated token. It previously had no
		// dedicated budget and instead shared the general "ratelimit:api:"
		// bucket with every other /api/v1 call from the same client IP,
		// so a burst of ordinary API traffic (or a single heavy admin
		// session) could exhaust the budget and lock EVERY user behind
		// that IP out of ever obtaining a CSRF token again until the
		// window rolled over — a full login/webmail outage from
		// legitimate traffic, not abuse. This budget is deliberately
		// generous (well above any real page-load's bootstrap-call
		// count) and keyed under its own "ratelimit:csrf:" prefix so it
		// can never be exhausted by, or exhaust, unrelated API or
		// credential-attempt traffic.
		csrfMax: 60,
		csrfWin: 60 * time.Second,
	}
}

// WithDefaults sets custom rate limit defaults.
func (rl *RedisRateLimiter) WithDefaults(max int, window time.Duration) *RedisRateLimiter {
	rl.defaultMax = max
	rl.window = window
	return rl
}

// WithLoginLimits sets login-specific rate limits.
func (rl *RedisRateLimiter) WithLoginLimits(max int, window time.Duration) *RedisRateLimiter {
	rl.loginMax = max
	rl.loginWin = window
	return rl
}

// Middleware returns a Fiber middleware for general API rate limiting.
func (rl *RedisRateLimiter) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := fmt.Sprintf("ratelimit:api:%s", c.IP())
		allowed, remaining, err := rl.check(c.Context(), key, rl.defaultMax, rl.window)
		if err != nil {
			rl.logger.Error("rate limiter error", zap.Error(err))
			return c.Next()
		}
		c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.defaultMax))
		c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		if !allowed {
			c.Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "rate limit exceeded, try again later",
			})
		}
		return c.Next()
	}
}

// CSRFBootstrapMiddleware returns a Fiber middleware for GET
// /api/v1/csrf-token — a dedicated, generous, per-IP budget that is
// never shared with the general API budget (Middleware, above) or any
// credential-attempt budget (LoginMiddleware). Mount it ONLY on the
// csrf-token route, in place of the general API limiter, so heavy but
// legitimate API/webmail traffic from a client can never exhaust the
// one endpoint every other request on that page depends on.
func (rl *RedisRateLimiter) CSRFBootstrapMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := fmt.Sprintf("ratelimit:csrf:%s", c.IP())
		allowed, remaining, err := rl.check(c.Context(), key, rl.csrfMax, rl.csrfWin)
		if err != nil {
			rl.logger.Error("csrf bootstrap rate limiter error", zap.Error(err))
			return c.Next()
		}
		c.Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.csrfMax))
		c.Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		if !allowed {
			c.Set("Retry-After", fmt.Sprintf("%d", int(rl.csrfWin.Seconds())))
			rl.logger.Warn("csrf bootstrap rate limit exceeded", zap.String("ip", c.IP()))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many token requests, try again later",
			})
		}
		return c.Next()
	}
}

// LoginMiddleware returns a middleware specifically for login rate limiting.
// On Redis failure it fails CLOSED (503) rather than allowing unthrottled login
// attempts during a Redis outage. The error response omits internal Redis details
// and does not name the dependency.
func (rl *RedisRateLimiter) LoginMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := fmt.Sprintf("ratelimit:login:%s", c.IP())
		allowed, remaining, err := rl.check(c.Context(), key, rl.loginMax, rl.loginWin)
		if err != nil {
			rl.logger.Error("login rate limiter unavailable", zap.Error(err))
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "authentication temporarily unavailable, please try again later",
			})
		}
		c.Set("X-RateLimit-Login-Remaining", fmt.Sprintf("%d", remaining))
		if !allowed {
			rl.logger.Warn("login rate limit exceeded", zap.String("ip", c.IP()))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many login attempts, try again later",
			})
		}
		return c.Next()
	}
}

// ResetLoginLimit clears the login rate limit for an IP (used after successful login).
func (rl *RedisRateLimiter) ResetLoginLimit(ip string) {
	key := fmt.Sprintf("ratelimit:login:%s", ip)
	_ = rl.client.Del(context.Background(), key)
}

func (rl *RedisRateLimiter) check(ctx context.Context, key string, max int, window time.Duration) (bool, int, error) {
	pipe := rl.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return true, 0, fmt.Errorf("redis rate limit check failed: %w", err)
	}

	count := incr.Val()
	if count > int64(max) {
		return false, 0, nil
	}
	return true, max - int(count), nil
}
