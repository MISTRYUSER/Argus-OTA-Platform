package redis

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient 封装 Redis 客户端，提供分布式计数和缓存功能
type RedisClient struct {
	client *redis.Client
}

// NewRedisClient 创建 Redis 客户端
func NewRedisClient(ctx context.Context, addr string, password string, db int) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
		MinIdleConns: 5,
	})

	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	log.Printf("[Redis] Connected to %s (DB: %d)", addr, db)
	return &RedisClient{client: client}, nil
}

// INCR 原子递增计数器（分布式 Barrier 核心操作）
func (r *RedisClient) INCR(ctx context.Context, key string) (int64, error) {
	result, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis incr failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] INCR: %s -> %d", key, result)
	return result, nil
}

// GET 读取缓存值
func (r *RedisClient) GET(ctx context.Context, key string) (string, error) {
	value, err := r.client.Get(ctx, key).Result()
	if err != nil {
		// key 不存在时，返回 redis.Nil（不是真正的错误）
		if err == redis.Nil {
			return "", nil
		}
		return "", fmt.Errorf("redis get failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] GET: %s -> %s", key, value)
	return value, nil
}

// DEL 删除 Key
func (r *RedisClient) DEL(ctx context.Context, key string) error {
	err := r.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("redis del failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] DEL: %s", key)
	return nil
}

// SET 设置缓存值（带过期时间）
func (r *RedisClient) SET(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	err := r.client.Set(ctx, key, value, expiration).Err()
	if err != nil {
		return fmt.Errorf("redis set failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] SET: %s -> %v (TTL: %s)", key, value, expiration)
	return nil
}

// Close 关闭 Redis 连接
func (r *RedisClient) Close() error {
	err := r.client.Close()
	if err != nil {
		return fmt.Errorf("redis close failed: %w", err)
	}

	log.Println("[Redis] Connection closed")
	return nil
}
func (r *RedisClient) SADD(ctx context.Context, key string, members ...interface{}) (int64, error) {
	result, err := r.client.SAdd(ctx, key, members...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis sadd failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] SADD: %s -> %d members added", key, result)
	return result, nil
}

// SCARD 获取集合大小
func (r *RedisClient) SCARD(ctx context.Context, key string) (int64, error) {
	result, err := r.client.SCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis scard failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] SCARD: %s -> %d", key, result)
	return result, nil
}

// Pipeline 批量操作（SADD + EXPIRE）
func (r *RedisClient) SADDWithTTL(ctx context.Context, key string, ttl time.Duration, members ...interface{}) error {
	pipe := r.client.Pipeline()

	// SADD 添加 fileID
	pipe.SAdd(ctx, key, members...)

	// EXPIRE 设置过期时间
	pipe.Expire(ctx, key, ttl)

	// 执行 Pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis pipeline failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] SADD+EXPIRE: %s (TTL: %s)", key, ttl)
	return nil
}

// EXPIRE 设置 Key 的过期时间
func (r *RedisClient) EXPIRE(ctx context.Context, key string, expiration time.Duration) error {
	err := r.client.Expire(ctx, key, expiration).Err()
	if err != nil {
		return fmt.Errorf("redis expire failed: key=%s, error=%w", key, err)
	}

	log.Printf("[Redis] EXPIRE: %s -> %s", key, expiration)
	return nil
}
