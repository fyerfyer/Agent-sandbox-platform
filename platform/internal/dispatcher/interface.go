package dispatcher

import (
	"context"
	"platform/internal/sandbox"
)

type IDispatcher interface {
	Diapatch(ctx context.Context, container *sandbox.Container, input string) error
	CleanUp(sessionID string)
}
