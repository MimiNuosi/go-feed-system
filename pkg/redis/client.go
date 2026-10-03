package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"go-feed-system/pkg/config"
)

// Open 创建并验证 Redis 客户端。
//
// 与 C++ 项目里的 RedisManager::GetInstance() 不同，这里不使用全局单例，
// 而是显式创建 *redis.Client，由 main 注入给需要它的 Service。
func Open(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// 核心：创建客户端后必须 Ping 一次，确保连接真实有效
	if err := client.Ping(ctx).Err(); err != nil {
		// 连接失败，清理资源，返回包装后的错误
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}

// Close 关闭 Redis 客户端。
//
// 相当于 C++ 里的析构函数，在 main 退出时手动调用。
func Close(client *redis.Client) error {
	if client == nil {
		return nil
	}
	return client.Close()
}
