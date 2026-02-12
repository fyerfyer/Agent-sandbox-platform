package dispatcher

import (
	"context"
	"platform/internal/agentproto"
	"platform/internal/sandbox"
)

type IDispatcher interface {
	Configure(ctx context.Context, container *sandbox.Container, req *agentproto.ConfigureRequest) (*agentproto.ConfigureResponse, error)
	Dispatch(ctx context.Context, container *sandbox.Container, input string) error
	Stop(ctx context.Context, container *sandbox.Container, sessionID string) (*agentproto.StopResponse, error)
	CleanUp(sessionID string)
}
