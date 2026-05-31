package gateway

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisAdapter 将 go-redis/v9 客户端适配为 RedisClient 接口
type RedisAdapter struct {
	client *redis.Client
}

// NewRedisAdapter 创建 Redis 适配器
func NewRedisAdapter(client *redis.Client) *RedisAdapter {
	return &RedisAdapter{client: client}
}

// Get 获取字符串值
func (a *RedisAdapter) Get(key string) (string, error) {
	ctx := context.Background()
	return a.client.Get(ctx, key).Result()
}

// Set 设置键值
func (a *RedisAdapter) Set(key string, value interface{}, expiry time.Duration) error {
	ctx := context.Background()
	return a.client.Set(ctx, key, value, expiry).Err()
}

// Del 删除键
func (a *RedisAdapter) Del(key string) error {
	ctx := context.Background()
	return a.client.Del(ctx, key).Err()
}

// HGet 获取哈希字段值
func (a *RedisAdapter) HGet(key, field string) (string, error) {
	ctx := context.Background()
	return a.client.HGet(ctx, key, field).Result()
}

// HSet 设置哈希字段值
func (a *RedisAdapter) HSet(key, field string, value interface{}) error {
	ctx := context.Background()
	return a.client.HSet(ctx, key, field, value).Err()
}

// HDel 删除哈希字段
func (a *RedisAdapter) HDel(key, field string) error {
	ctx := context.Background()
	return a.client.HDel(ctx, key, field).Err()
}

// Expire 设置过期时间
func (a *RedisAdapter) Expire(key string, expiry time.Duration) error {
	ctx := context.Background()
	return a.client.Expire(ctx, key, expiry).Err()
}

// ZRangeWithScores 获取有序集合成员（带分数）
func (a *RedisAdapter) ZRangeWithScores(key string, start, stop int64) ([]redis.Z, error) {
	ctx := context.Background()
	return a.client.ZRangeWithScores(ctx, key, start, stop).Result()
}

// ZCard 获取有序集合成员数
func (a *RedisAdapter) ZCard(key string) (int64, error) {
	ctx := context.Background()
	return a.client.ZCard(ctx, key).Result()
}

// ZRem 移除有序集合成员
func (a *RedisAdapter) ZRem(key string, members ...interface{}) (int64, error) {
	ctx := context.Background()
	return a.client.ZRem(ctx, key, members...).Result()
}

// Eval 执行Lua脚本
func (a *RedisAdapter) Eval(script string, keys []string, args ...interface{}) (interface{}, error) {
	ctx := context.Background()
	return a.client.Eval(ctx, script, keys, args...).Result()
}
