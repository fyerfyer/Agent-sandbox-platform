package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"platform/internal/monitor"
	"platform/internal/sandbox"
	"sync"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

var _ IPool = (*Pool)(nil)

type Pool struct {
	mu             sync.Mutex
	availableCh    chan struct{} // 使用+创建空闲容器的名额，当前总名额为 MaxBurst - (Idle + Active)
	client         *client.Client
	logger         *slog.Logger
	config         PoolConfig
	idleContainers []*sandbox.Container
	managedCount   int // Pool 当前所有管理的容器数量（包括空闲和正在使用的）
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
		availableCh:    make(chan struct{}, cfg.MaxBurst),
		stopCh:         make(chan struct{}),
	}

	// 初始化 availableCh，装 cfg.MaxBurst 个空闲容器
	for i := 0; i < cfg.MaxBurst; i++ {
		p.availableCh <- struct{}{}
	}

	// 清理孤儿容器
	// 扫描所有带有 managed_by=agent-platform 和 project_id=pool 标签的容器
	opts := container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(),
	}
	opts.Filters.Add("label", "managed_by=agent-platform")
	opts.Filters.Add("label", "project_id=pool")

	containers, err := client.ContainerList(context.Background(), opts)
	if err != nil {
		logger.Error("Failed to list orphaned containers", "error", err)
	} else {
		for _, c := range containers {
			if c.State == "running" {
				logger.Info("Adopting orphaned container", "id", c.ID)
				inspect, err := client.ContainerInspect(context.Background(), c.ID)
				if err != nil {
					logger.Error("Failed to inspect orphaned container", "id", c.ID, "error", err)
					continue
				}

				// 重建 Container
				sc := sandbox.NewContainer(client, sandbox.ContainerConfig{
					Image:           c.Image,
					SessionID:       c.Labels["session_id"],
					ProjectID:       c.Labels["project_id"],
					NetworkName:     cfg.NetworkName,
					MemoryLimit:     inspect.HostConfig.Memory,
					CPULimit:        float64(inspect.HostConfig.NanoCPUs) / 1e9,
					UseAnonymousVol: true, // Pool 容器是匿名卷
				}, "", logger)
				sc.ID = c.ID

				// 获取 IP
				if net, ok := c.NetworkSettings.Networks[cfg.NetworkName]; ok {
					sc.IP = net.IPAddress
				}

				p.idleContainers = append(p.idleContainers, sc)
				p.managedCount++
				// 消耗一个 availableCh token
				select {
				case <-p.availableCh:
				default:
					logger.Warn("Pool overflow during adoption")
				}
			} else {
				logger.Info("Removing stopped orphaned container", "id", c.ID)
				client.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
			}
		}
	}

	monitor.PoolIdleCount.Set(float64(len(p.idleContainers)))

	go p.worker()

	return p
}

func (p *Pool) Acquire(ctx context.Context) (*sandbox.Container, error) {
	start := time.Now()
	for {
		// 等待有空闲容器
		select {
		case <-p.availableCh:
			// 有空闲容器，继续
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.stopCh:
			return nil, fmt.Errorf("pool is shutting down")
		}

		p.mu.Lock()

		if len(p.idleContainers) > 0 {
			idx := len(p.idleContainers) - 1
			c := p.idleContainers[idx]
			p.idleContainers = p.idleContainers[:idx]
			p.mu.Unlock()

			// 检验容器状态
			if c.IsRunning(ctx) {
				p.logger.Info("Acquired warm container", "id", c.ID)
				monitor.PoolIdleCount.Dec()
				monitor.PoolAcquisitionLatency.Observe(time.Since(start).Seconds())
				return c, nil
			}

			// 清理
			p.logger.Warn("Pooled container is dead, discarding", "id", c.ID)
			go func() {
				c.Remove(context.Background())
				// 加锁更新容器总数
				p.mu.Lock()
				p.managedCount--
				p.mu.Unlock()
				p.availableCh <- struct{}{}
			}()

			// 尝试下一个容器
			continue
		}

		// 没有空闲容器，创建一个新容器
		// 已经消耗了一个使用+创建名额，可以创建
		p.managedCount++
		p.mu.Unlock()

		c, err := p.createWarmContainer(ctx)
		if err != nil {
			p.logger.Error("Failed to create burst container", "error", err)
			monitor.ContainerCreationErrors.Inc()
			p.mu.Lock()
			p.managedCount-- // 回退
			p.mu.Unlock()
			// 返回一个使用+创建名额，因为创建失败了
			p.availableCh <- struct{}{}
			return nil, err
		}

		p.logger.Info("Created burst container", "id", c.ID)
		monitor.PoolAcquisitionLatency.Observe(time.Since(start).Seconds())
		return c, nil
	}
}

func (p *Pool) Release(ctx context.Context, c *sandbox.Container) {
	// 直接更新
	// API 行为保持同步，清理流程异步
	p.mu.Lock()
	p.managedCount--
	p.mu.Unlock()

	// 返回一个使用+创建名额
	select {
	case p.availableCh <- struct{}{}:
	default:
		p.logger.Warn("Failed to return capacity token (channel full), this should not happen")
	}

	// 异步清理
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := c.Stop(ctx, 2); err != nil {
			p.logger.Error("Failed to stop container", "id", c.ID, "error", err)
		}

		if err := c.Remove(ctx); err != nil {
			p.logger.Error("Failed to remove container", "id", c.ID, "error", err)
		}

		p.logger.Info("Released and removed container", "id", c.ID)
	}()
}

func (p *Pool) Shutdown(ctx context.Context, c *sandbox.Container) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 关闭 stopCh 以停止 worker
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}

	// 异步移除容器
	for _, c := range p.idleContainers {
		go func(c *sandbox.Container) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.Stop(ctx, 10)
			c.Remove(ctx)
		}(c)
	}
	p.idleContainers = nil
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
		// 增加 TCP Dial 检查
		if c.IsRunning(ctx) && p.checkAgentHealth(c.IP) {
			alive = append(alive, c)
		} else {
			p.logger.Warn("Removing dead container from pool", "id", c.ID)
			go func(c *sandbox.Container) {
				c.Remove(context.Background())
				p.mu.Lock()
				p.managedCount--
				p.mu.Unlock()
				// 返还一个使用+创建名额
				p.availableCh <- struct{}{}
			}(c)
		}
	}

	p.idleContainers = alive
	monitor.PoolIdleCount.Set(float64(len(p.idleContainers)))
}

// 通过 TCP 探活 agent
func (p *Pool) checkAgentHealth(ip string) bool {
	if p.config.DisableHealthCheck {
		return true
	}
	if ip == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", ip+":50051", 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (p *Pool) maintainPool() {
	p.mu.Lock()
	if time.Now().Before(p.cooldownUntil) {
		p.mu.Unlock()
		return
	}

	currentIdle := len(p.idleContainers)
	needed := p.config.MinIdle - currentIdle

	// 限制最大创建数量
	maxAllowed := p.config.MaxBurst - p.managedCount

	if needed > maxAllowed {
		needed = maxAllowed
	}

	if needed <= 0 {
		p.mu.Unlock()
		return
	}

	// 预占名额
	// 如果预占成功，增加 managedCount
	tokensConsumed := 0
TokenLoop:
	for i := 0; i < needed; i++ {
		select {
		case <-p.availableCh:
			tokensConsumed++
			p.managedCount++ // 预占名额
		default:
			// 没有名额，结束循环
			break TokenLoop
		}
	}

	p.mu.Unlock()

	if tokensConsumed == 0 {
		return
	}

	// 限制并发创建数量
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	// 失败计数器
	var failureCount int32 = 0

	for range tokensConsumed {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			container, err := p.createWarmContainer(ctx)
			if err != nil {
				p.logger.Error("Failed to replenish pool", "error", err)
				monitor.ContainerCreationErrors.Inc()
				newCount := atomic.AddInt32(&failureCount, 1)
				if newCount >= 3 {
					p.mu.Lock()
					p.cooldownUntil = time.Now().Add(1 * time.Minute)
					p.mu.Unlock()
				}

				// 创建失败，回滚
				p.mu.Lock()
				p.managedCount--
				p.mu.Unlock()
				p.availableCh <- struct{}{}
				return
			}

			// 加入空闲池
			p.mu.Lock()
			// 二次检查
			if len(p.idleContainers) < p.config.MinIdle &&
				p.managedCount <= p.config.MaxBurst {
				p.idleContainers = append(p.idleContainers, container)
				monitor.PoolIdleCount.Inc()
				p.mu.Unlock()
				// 返回一个使用+创建名额
				p.availableCh <- struct{}{}
			} else {
				// 池子已满，回滚
				p.managedCount--
				p.mu.Unlock()
				go func() {
					container.Stop(ctx, 10)
					container.Remove(ctx)
				}()
				// 返回一个使用+创建名额
				p.availableCh <- struct{}{}
			}
		}()
	}

	wg.Wait()
}

func (p *Pool) createWarmContainer(ctx context.Context) (*sandbox.Container, error) {
	// 生成唯一 session ID
	sessionID := fmt.Sprintf("warmup-%d", time.Now().UnixNano())

	cfg := sandbox.ContainerConfig{
		Image:           p.config.WarmupImage,
		MemoryLimit:     p.config.ContainerMem * 1024 * 1024,
		CPULimit:        p.config.ContainerCPU,
		UseAnonymousVol: true,
		NetworkName:     p.config.NetworkName,
		SessionID:       sessionID,
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
