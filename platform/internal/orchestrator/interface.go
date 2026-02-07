package orchestrator

import (
	"context"
	"platform/internal/sandbox"
)

type IPool interface {
	Acquire(ctx context.Context) (*sandbox.Container, error)
	Release(ctx context.Context, c *sandbox.Container)
	Shutdown(ctx context.Context, c *sandbox.Container)
	CreateColdContainer(ctx context.Context, opts ContainerOptions) (*sandbox.Container, error)
}

type ContainerStrategy interface {
	Name() StrategyType
	Get(ctx context.Context, pool IPool, opts ContainerOptions) (*sandbox.Container, error)
	Release(ctx context.Context, pool IPool, c *sandbox.Container)
}
