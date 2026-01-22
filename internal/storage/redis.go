// Package storage provides Redis client wrapper implementing RedisClient interface.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClientWrapper wraps go-redis client to implement RedisClient interface.
type RedisClientWrapper struct {
	client *redis.Client
}

// RedisConfig holds configuration for Redis connection.
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// NewRedisClient creates a new Redis client wrapper.
func NewRedisClient(cfg RedisConfig) (*RedisClientWrapper, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisClientWrapper{client: client}, nil
}

// Get retrieves a value from Redis.
func (r *RedisClientWrapper) Get(ctx context.Context, key string) (string, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", fmt.Errorf("key not found")
	}
	return val, err
}

// Set stores a value in Redis with expiration.
func (r *RedisClientWrapper) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// Del deletes keys from Redis.
func (r *RedisClientWrapper) Del(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// Keys returns all keys matching the pattern.
func (r *RedisClientWrapper) Keys(ctx context.Context, pattern string) ([]string, error) {
	return r.client.Keys(ctx, pattern).Result()
}

// Ping tests the Redis connection.
func (r *RedisClientWrapper) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close closes the Redis connection.
func (r *RedisClientWrapper) Close() error {
	return r.client.Close()
}
