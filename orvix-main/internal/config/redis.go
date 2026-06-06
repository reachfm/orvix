package config

import (
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// NewRedisClient creates a Redis client from config.
func NewRedisClient(cfg *RedisConfig, logger *zap.Logger) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	logger.Info("redis client created",
		zap.String("addr", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)),
	)

	return client
}
