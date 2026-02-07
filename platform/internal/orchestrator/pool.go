package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"platform/internal/sandbox"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/client"
)

var _ IPool = (*Pool)(nil)

type Pool struct {
	mu             sync.Mutex
	client         *client.Client
	logger         *slog.Logger
	config         PoolConfig
	idleContainers []*sandbox.Container
	factory        func(ctx context.Context) (*sandbox.Container, error)
	cooldownUntil  time.Time
	stopCh         chan struct{}
}

func NewPool(client *client.Client, logger *slog.Logger, cfg PoolConfig) *Pool {
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = 2 * time.Second
	}

	if cfg.MaxBurst == 0 {
		cfg.MaxBurst = 5
	}

	if cfg.MaxBurst < cfg.MinIdle {
		cfg.MaxBurst = cfg.MinIdle
	}

	p := &Pool{
		client:         client,
		logger:         logger,
		config:         cfg,
		idleContainers: make([]*sandbox.Container, 0),
		stopCh:         make(chan struct{}),
	}

	go p.worker()

	return p
}

func (p *Pool) Acquire(ctx context.Context) (*sandbox.Container, error) {
	const maxRetries = 3
	for range maxRetries {
		p.mu.Lock()
		if len(p.idleContainers) == 0 {
			p.mu.Unlock()
			p.logger.Info("Pool empty")
			return nil, fmt.Errorf("pool is empty")
		}

		idx := len(p.idleContainers) - 1
		c := p.idleContainers[idx]
		p.idleContainers = p.idleContainers[:idx]
		p.mu.Unlock()

		if c.IsRunning(ctx) {
			p.logger.Info("Acquired warm container", "id", c.ID)
			return c, nil
		}

		p.logger.Warn("Pooled container is dead, discarding", "id", c.ID)
		go c.Remove(context.Background())
	}

	return nil, fmt.Errorf("failed to acquire valid container after %d retries", maxRetries)
}

// TODO：之后可以考虑清空 container,当前先简单删除
func (p *Pool) Release(ctx context.Context, c *sandbox.Container) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := c.Stop(ctx, 10); err != nil {
			p.logger.Error("Failed to stop container", "id", c.ID, "error", err)
		}

		if err := c.Remove(ctx); err != nil {
			p.logger.Error("Failed to remove container", "id", c.ID, "error", err)
		}

		p.logger.Info("Released container", "id", c.ID)
	}()
}

func (p *Pool) Shutdown(ctx context.Context, c *sandbox.Container) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		for _, c := range p.idleContainers {
			if err := c.Stop(ctx, 10); err != nil {
				p.logger.Error("Failed to stop container", "id", c.ID, "error", err)
			}
			if err := c.Remove(ctx); err != nil {
				p.logger.Error("Failed to remove container", "id", c.ID, "error", err)
			}
		}

		p.idleContainers = nil
	}()
}

func (p *Pool) worker() {
	ticker := time.NewTicker(p.config.HealthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return

		case <-ticker.C:
			p.healthCheck()
			p.maintainPool()
		}
	}
}

func (p *Pool) healthCheck() {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	alive := make([]*sandbox.Container, 0, len(p.idleContainers))
	for _, c := range p.idleContainers {
		if c.IsRunning(ctx) {
			alive = append(alive, c)
		} else {
			p.logger.Warn("Removing dead container from pool", "id", c.ID)
			go c.Remove(context.Background())
		}
	}

	p.idleContainers = alive
}

func (p *Pool) maintainPool() {
	p.mu.Lock()
	if time.Now().Before(p.cooldownUntil) {
		p.mu.Unlock()
		return
	}

	needed := p.config.MinIdle - len(p.idleContainers)
	p.mu.Unlock()

	if needed <= 0 {
		return
	}

	// 限制一次最多发送 3 个请求
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	// 熔断器计数
	var failureCount int32 = 1

	for range needed {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			container, err := p.createWarmContainer(ctx)
			if err != nil {
				p.logger.Error("Failed to replenish pool", "error", err)
				newCount := atomic.AddInt32(&failureCount, 1)
				if newCount >= 3 {
					p.cooldownUntil = time.Now().Add(1 * time.Minute)
				}
				return
			}

			// 二次校验
			p.mu.Lock()
			if len(p.idleContainers) < p.config.MinIdle {
				p.idleContainers = append(p.idleContainers, container)
			}
			p.mu.Unlock()
		})
	}

	wg.Wait()
}

// Warm Container 创建通用容器
func (p *Pool) createWarmContainer(ctx context.Context) (*sandbox.Container, error) {
	cfg := sandbox.ContainerConfig{
		Image:           p.config.WarmupImage,
		MemoryLimit:     p.config.ContainerMem * 1024 * 1024,
		CPULimit:        p.config.ContainerCPU,
		UseAnonymousVol: true,
		NetworkName:     p.config.NetworkName,
		SessionID:       "warmup",
		ProjectID:       "pool",
	}

	c := sandbox.NewContainer(p.client, cfg, "", p.logger)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start warm container: %w", err)
	}

	return c, nil
}

func (p *Pool) CreateColdContainer(ctx context.Context, opts ContainerOptions) (*sandbox.Container, error) {
	cfg := sandbox.ContainerConfig{
		Image:           opts.Image,
		EnvVars:         opts.EnvVars,
		MemoryLimit:     p.config.ContainerMem * 1024 * 1024,
		CPULimit:        p.config.ContainerCPU,
		UseAnonymousVol: false,
		NetworkName:     p.config.NetworkName,
		SessionID:       opts.SessionID,
		ProjectID:       opts.ProjectID,
	}

	c := sandbox.NewContainer(p.client, cfg, p.config.HostRoot, p.logger)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start cold container: %w", err)
	}

	return c, nil
}
