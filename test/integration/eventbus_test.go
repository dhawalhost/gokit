//go:build integration
// +build integration

package integration_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/eventbus"
	"github.com/redis/go-redis/v9"
)

func TestEventBus_LiveRedisStreamsConsumerGroup(t *testing.T) {
	addr := getTestRedisAddr(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis ping failed: %v", err)
	}

	streamPrefix := "int-events:"
	topic := "orders.created"

	// Cleanup any existing stream from previous test runs
	_ = client.Del(ctx, streamPrefix+topic).Err()

	bus, err := eventbus.NewRedisBus(eventbus.RedisBusConfig{
		Client:       client,
		StreamPrefix: streamPrefix,
		DefaultGroup: "integration-workers",
		BlockTimeout: 50 * time.Millisecond,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("NewRedisBus failed: %v", err)
	}
	defer bus.Close()

	var receivedCount atomic.Int32
	receivedPayloads := make(chan string, 10)

	sub, err := bus.Subscribe(ctx, topic, func(ctx context.Context, msg *eventbus.Message) error {
		receivedCount.Add(1)
		receivedPayloads <- string(msg.Payload)
		return nil
	}, eventbus.WithConsumerGroup("order-processors"), eventbus.WithConcurrency(2))

	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	defer sub.Unsubscribe()

	// Publish test messages
	totalMessages := 5
	for i := 1; i <= totalMessages; i++ {
		msg := eventbus.NewMessage(topic, []byte(time.Now().Format(time.RFC3339Nano))).
			WithKey("tenant-live").
			WithHeader("test-run", "true")

		if err := bus.Publish(ctx, msg); err != nil {
			t.Fatalf("Publish failed: %v", err)
		}
	}

	// Verify all messages are processed by consumer group
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if receivedCount.Load() == int32(totalMessages) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := receivedCount.Load(); got != int32(totalMessages) {
		t.Fatalf("expected %d messages, got %d", totalMessages, got)
	}
}
