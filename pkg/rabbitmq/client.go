package rabbitmq

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
)

const defaultHeartbeat = 10 * time.Second

// Open 建立并验证 RabbitMQ 连接。
//
// amqp091-go 的 DialConfig 本身没有 context 参数，因此这里通过自定义 Dial
// 把 context 传入 TCP 建连阶段，避免服务启动时无限等待。
func Open(ctx context.Context, cfg config.RabbitMQConfig) (*amqp.Connection, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("rabbitmq URL must not be empty")
	}

	dialer := &net.Dialer{}
	conn, err := amqp.DialConfig(cfg.URL, amqp.Config{
		Heartbeat: defaultHeartbeat,
		Dial: func(network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	return conn, nil
}

// Close 关闭 RabbitMQ 连接。
func Close(conn *amqp.Connection) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}
