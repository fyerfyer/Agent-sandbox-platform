package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

var _ EventBus = (*RedisBus)(nil)

type RedisBus struct {
	client redis.Cmdable
	logger *slog.Logger
}

func NewRedisBus(client redis.Cmdable, logger *slog.Logger) *RedisBus {
	return &RedisBus{client: client, logger: logger}
}

func (b *RedisBus) Publish(ctx context.Context, sessionID string, event Event) error {
	channelKey := SessionChannelKey(sessionID)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	return b.client.Publish(ctx, channelKey, data).Err()
}

func (b *RedisBus) Subscribe(ctx context.Context, sessionID string) (<-chan Event, error) {
	channelKey := SessionChannelKey(sessionID)
	client, ok := b.client.(*redis.Client)
	if !ok {
		return nil, fmt.Errorf("invalid redis client type")
	}

	pubSub := client.Subscribe(ctx, channelKey)

	ch := make(chan Event)

	go func() {
		defer close(ch)
		defer func(pubSub *redis.PubSub) {
			err := pubSub.Close()
			if err != nil {
				b.logger.Error("failed to close pubsub", "error", err)
			}
		}(pubSub)

		for msg := range pubSub.Channel() {
			var event Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				b.logger.Error("failed to unmarshal event", "error", err)
				continue
			}
			ch <- event
		}
	}()

	return ch, nil
}
