package dispatcher

import (
	"context"
	"platform/internal/sandbox"
)

type IDispatcher interface {
	Dispatch(ctx context.Context, container *sandbox.Container, input string) error
	CleanUp(sessionID string)
}
