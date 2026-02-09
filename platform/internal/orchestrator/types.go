package orchestrator

import "time"

// TODO: 冷容器和热容器的配置在哪些地方需要区分？
type ContainerOptions struct {
	Image     string
	EnvVars   []string
	SessionID string
	ProjectID string
}

type StrategyType string

const (
	WarmStrategyType StrategyType = "Warm-Strategy"
	ColdStrategyType StrategyType = "Cold-Strategy"
)

type PoolConfig struct {
	MinIdle             int
	MaxBurst            int // Idle + Active = MaxBurst
	WarmupImage         string
	HealthCheckInterval time.Duration
	NetworkName         string  // 容器使用的网络
	HostRoot            string  // 冷容器挂载目录
	ContainerMem        int64   // MB
	ContainerCPU        float64 // CPU 核心数（如 0.5, 1, 2）
}
