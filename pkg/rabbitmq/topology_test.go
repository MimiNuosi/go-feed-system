package rabbitmq

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
)

func TestDeclareTopologyIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:          url,
		Exchange:     "test.feed.events." + suffix,
		ExchangeType: "topic",
		RoutingKey:   "test.video.published." + suffix,
		Queue:        "test.feed.queue." + suffix,
		DLX:          "test.feed.dlx." + suffix,
		DLQ:          "test.feed.dlq." + suffix,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open rabbitmq connection: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close rabbitmq connection: %v", err)
		}
	})

	channel, err := conn.Channel()
	if err != nil {
		t.Fatalf("create rabbitmq channel: %v", err)
	}
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("close rabbitmq channel: %v", err)
		}
	})

	t.Cleanup(func() {
		for _, queue := range []string{cfg.Queue, cfg.DLQ} {
			if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
				t.Errorf("delete queue %s: %v", queue, err)
			}
		}
		for _, exchange := range []string{cfg.Exchange, cfg.DLX} {
			if err := channel.ExchangeDelete(exchange, false, false); err != nil {
				t.Errorf("delete exchange %s: %v", exchange, err)
			}
		}
	})

	// 连续声明两次，验证声明操作是幂等的。
	for i := 0; i < 2; i++ {
		if err := DeclareTopology(ctx, channel, cfg); err != nil {
			t.Fatalf("declare topology attempt %d: %v", i+1, err)
		}
	}

	// Passive 声明用于检查 Broker 上已有资源的参数是否与期望一致。
	if err := channel.ExchangeDeclarePassive(
		cfg.Exchange,
		cfg.ExchangeType,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		t.Fatalf("verify main exchange: %v", err)
	}
	if err := channel.ExchangeDeclarePassive(
		cfg.DLX,
		cfg.ExchangeType,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		t.Fatalf("verify dead letter exchange: %v", err)
	}

	mainQueueArgs := amqp.Table{
		"x-dead-letter-exchange": cfg.DLX,
	}
	if _, err := channel.QueueDeclarePassive(
		cfg.Queue,
		true,
		false,
		false,
		false,
		mainQueueArgs,
	); err != nil {
		t.Fatalf("verify main queue: %v", err)
	}
	if _, err := channel.QueueDeclarePassive(
		cfg.DLQ,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		t.Fatalf("verify dead letter queue: %v", err)
	}

	body := []byte("topology-test")
	if err := channel.PublishWithContext(
		ctx,
		cfg.Exchange,
		cfg.RoutingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	); err != nil {
		t.Fatalf("publish test message: %v", err)
	}

	mainDelivery := getUnackedMessage(t, channel, cfg.Queue)
	if !bytes.Equal(mainDelivery.Body, body) {
		t.Fatalf("expected main queue body %s, got %s", body, mainDelivery.Body)
	}
	if err := mainDelivery.Nack(false, false); err != nil {
		t.Fatalf("nack main delivery: %v", err)
	}

	deadLetter := getMessage(t, channel, cfg.DLQ)
	if !bytes.Equal(deadLetter.Body, body) {
		t.Fatalf("expected DLQ body %s, got %s", body, deadLetter.Body)
	}
}

func getUnackedMessage(t *testing.T, channel *amqp.Channel, queueName string) amqp.Delivery {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		message, ok, err := channel.Get(queueName, false)
		if err != nil {
			t.Fatalf("get unacked message: %v", err)
		}
		if ok {
			return message
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for unacked message")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
