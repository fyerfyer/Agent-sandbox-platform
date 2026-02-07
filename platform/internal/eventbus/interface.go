package eventbus

import "context"

type EventBus interface {
	Publish(ctx context.Context, sessionID string, event Event) error
	Subscribe(ctx context.Context, sessionID string) (<-chan Event, error)
}
