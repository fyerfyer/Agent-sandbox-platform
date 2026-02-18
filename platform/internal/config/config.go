package config

import (
	"os"
	"path/filepath"
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
	Log      LogConfig
	Session  SessionCleanupConfig
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

type LogConfig struct {
	// Dir 日志文件存储目录。
	// 默认为 ~/.agent-platform/logs
	// 可设置为 "." 使日志生成在当前工作目录下。
	Dir string

	// ContainerLogDir 容器执行日志（.dockerlogs）的目录。
	// 默认使用 LogConfig.Dir。
	ContainerLogDir string

	// Level 日志级别：debug, info, warn, error
	Level string
}

type SessionCleanupConfig struct {
	Interval time.Duration
	// 超过此时间的 Initializing/Running 会话被视为过期，自动清理
	MaxAge time.Duration
	// 是否启用自动清理
	Enabled bool
}

// Load 加载配置
func Load() *Config {
	logDir := getEnv("LOG_DIR", defaultLogDir())
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
			HostRoot:            getEnv("POOL_HOST_ROOT", defaultHostRoot()),
			ContainerMem:        int64(getIntEnv("POOL_CONTAINER_MEM_MB", 512)),
			ContainerCPU:        getFloatEnv("POOL_CONTAINER_CPU", 0.5),
		},
		Worker: WorkerConfig{
			ProjectDir:  getEnv("WORKER_PROJECT_DIR", defaultProjectDir()),
			Concurrency: getIntEnv("WORKER_CONCURRENCY", 5),
		},
		Metrics: MetricsConfig{
			Addr: getEnv("METRICS_ADDR", ":9090"),
		},
		Log: LogConfig{
			Dir:             logDir,
			ContainerLogDir: getEnv("CONTAINER_LOG_DIR", filepath.Join(logDir, "containers")),
			Level:           getEnv("LOG_LEVEL", "info"),
		},
		Session: SessionCleanupConfig{
			Interval: getDurationEnv("SESSION_CLEANUP_INTERVAL", 2*time.Minute),
			MaxAge:   getDurationEnv("SESSION_MAX_AGE", 30*time.Minute),
			Enabled:  getBoolEnv("SESSION_CLEANUP_ENABLED", true),
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

func getBoolEnv(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		switch val {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return defaultVal
}

// defaultHostRoot 返回一个用户可写的默认路径，用于冷容器的绑定挂载。
func defaultHostRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/agent-platform/projects"
	}
	return filepath.Join(home, ".agent-platform", "projects")
}

// defaultProjectDir 返回一个用户可写的默认路径，用于 Worker 使用的项目文件。
func defaultProjectDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/agent-platform/projects"
	}
	return filepath.Join(home, ".agent-platform", "projects")
}

// defaultLogDir 返回默认的日志目录。
func defaultLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/agent-platform/logs"
	}
	return filepath.Join(home, ".agent-platform", "logs")
}

// defaultDataDir 返回默认的数据目录（session 历史等）。
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/agent-platform/data"
	}
	return filepath.Join(home, ".agent-platform", "data")
}
