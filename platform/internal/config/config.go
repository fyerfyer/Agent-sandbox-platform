package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server   ServerConfig
	Redis    RedisConfig
	Postgres PostgresConfig
	Pool     PoolConfig
	Worker   WorkerConfig
	Metrics  MetricsConfig
}

type ServerConfig struct {
	Addr         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type PostgresConfig struct {
	Addr     string
	User     string
	Password string
	Database string
}

type PoolConfig struct {
	MinIdle             int
	MaxBurst            int
	WarmupImage         string
	HealthCheckInterval time.Duration
	NetworkName         string
	HostRoot            string
	ContainerMem        int64
	ContainerCPU        float64
}

type WorkerConfig struct {
	ProjectDir  string
	Concurrency int
}

type MetricsConfig struct {
	Addr string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:         getEnv("SERVER_ADDR", ":8080"),
			ReadTimeout:  getDurationEnv("SERVER_READ_TIMEOUT", 30*time.Second),
			WriteTimeout: getDurationEnv("SERVER_WRITE_TIMEOUT", 120*time.Second),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getIntEnv("REDIS_DB", 0),
		},
		Postgres: PostgresConfig{
			Addr:     getEnv("POSTGRES_ADDR", "localhost:5432"),
			User:     getEnv("POSTGRES_USER", "postgres"),
			Password: getEnv("POSTGRES_PASSWORD", "postgres"),
			Database: getEnv("POSTGRES_DB", "agent_platform"),
		},
		Pool: PoolConfig{
			MinIdle:             getIntEnv("POOL_MIN_IDLE", 2),
			MaxBurst:            getIntEnv("POOL_MAX_BURST", 10),
			WarmupImage:         getEnv("POOL_WARMUP_IMAGE", "agent-runtime:latest"),
			HealthCheckInterval: getDurationEnv("POOL_HEALTH_CHECK_INTERVAL", 30*time.Second),
			NetworkName:         getEnv("POOL_NETWORK_NAME", "agent-platform-net"),
			HostRoot:            getEnv("POOL_HOST_ROOT", "/var/agent-platform/projects"),
			ContainerMem:        int64(getIntEnv("POOL_CONTAINER_MEM_MB", 512)),
			ContainerCPU:        getFloatEnv("POOL_CONTAINER_CPU", 0.5),
		},
		Worker: WorkerConfig{
			ProjectDir:  getEnv("WORKER_PROJECT_DIR", "/var/agent-platform/projects"),
			Concurrency: getIntEnv("WORKER_CONCURRENCY", 5),
		},
		Metrics: MetricsConfig{
			Addr: getEnv("METRICS_ADDR", ":9090"),
		},
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getIntEnv(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getFloatEnv(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}
