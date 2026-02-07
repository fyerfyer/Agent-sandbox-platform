package orchestrator

import (
	"context"
	"platform/internal/sandbox"
)

var _ ContainerStrategy = (*WarmStrategy)(nil)
var _ ContainerStrategy = (*ColdStrategy)(nil)

// Warm Strategy 对应 Task Mode
type WarmStrategy struct {
	pool *Pool
}

func (w *WarmStrategy) Name() StrategyType {
	return "Warm-Strategy"
}

func (w *WarmStrategy) Get(ctx context.Context, pool IPool, opts ContainerOptions) (*sandbox.Container, error) {
	container, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	return container, nil
}

func (w *WarmStrategy) Release(ctx context.Context, pool IPool, c *sandbox.Container) {
	pool.Release(ctx, c)
}

type ColdStrategy struct{}

func (c *ColdStrategy) Name() StrategyType {
	return "Cold-Strategy"
}

func (c *ColdStrategy) Get(ctx context.Context, pool IPool, opts ContainerOptions) (*sandbox.Container, error) {
	return pool.CreateColdContainer(ctx, opts)
}

func (c *ColdStrategy) Release(ctx context.Context, pool IPool, con *sandbox.Container) {
	con.Remove(ctx)
}
