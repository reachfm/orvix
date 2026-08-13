package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimitStore is the distributed LimitStore. It shares the counter across
// every node, which is what makes the account dimension meaningful in a
// multi-node deployment: an attacker spreading attempts for one account over
// several app servers still hits one budget.
type RedisLimitStore struct {
	client *redis.Client
}

// NewRedisLimitStore wraps an existing Redis client.
func NewRedisLimitStore(client *redis.Client) *RedisLimitStore {
	return &RedisLimitStore{client: client}
}

// Incr increments key and arms its TTL.
//
// Expire is issued with the NX flag so the TTL is set only when the key has
// none. Re-arming on every hit would slide the window forward indefinitely and
// let a steady attacker keep the counter alive without it ever expiring —
// which changes the semantics away from the fixed window the in-memory store
// implements, breaking parity.
func (s *RedisLimitStore) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	if s.client == nil {
		return 0, fmt.Errorf("redis limit store: no client")
	}
	pipe := s.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("redis limit store: %w", err)
	}
	return incr.Val(), nil
}

// Reset clears key.
func (s *RedisLimitStore) Reset(ctx context.Context, key string) error {
	if s.client == nil {
		return fmt.Errorf("redis limit store: no client")
	}
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis limit store: %w", err)
	}
	return nil
}
