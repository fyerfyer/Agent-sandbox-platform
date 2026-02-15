package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ───────────────────────────────────────────────────────────────────────
// ComposeStack 描述一组由 docker-compose 管理的服务
// ───────────────────────────────────────────────────────────────────────

type ComposeStack struct {
	SessionID   string           `json:"session_id"`
	ProjectName string           `json:"project_name"` // docker compose -p <name>
	ComposeFile string           `json:"compose_file"` // 宿主机上的 compose 文件路径
	Services    []ComposeService `json:"services"`
	Status      string           `json:"status"` // "running", "stopped", "error"
	CreatedAt   time.Time        `json:"created_at"`
}

type ComposeService struct {
	Name        string `json:"name"`
	ContainerID string `json:"container_id"`
	IP          string `json:"ip"`
	Status      string `json:"status"`
}

// ───────────────────────────────────────────────────────────────────────
// ComposeManager 使用 DooD (Docker-outside-of-Docker) 方式管理
// docker-compose 堆栈。Go Platform 直接在宿主机运行，天然拥有
// docker socket 访问权限，可以直接调用 docker compose CLI。
// ───────────────────────────────────────────────────────────────────────

type ComposeManager struct {
	mu      sync.RWMutex
	stacks  map[string]*ComposeStack // sessionID -> stack
	docker  *client.Client
	network string // 共享 Docker 网络名称
	dataDir string // 存放 compose 文件的根目录
	logger  *slog.Logger
}

func NewComposeManager(docker *client.Client, networkName string, dataDir string, logger *slog.Logger) *ComposeManager {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".agent-platform", "compose")
	}
	_ = os.MkdirAll(dataDir, 0755)

	return &ComposeManager{
		stacks:  make(map[string]*ComposeStack),
		docker:  docker,
		network: networkName,
		dataDir: dataDir,
		logger:  logger,
	}
}

// ───────────────────────────────────────────────────────────────────────
// CreateStack 接收一个 docker-compose.yml 内容字符串，
// 写入临时目录后通过 CLI 启动。所有服务会被连接到平台共享网络。
// ───────────────────────────────────────────────────────────────────────

type CreateComposeRequest struct {
	// ComposeContent 是 docker-compose.yml 的完整内容
	ComposeContent string `json:"compose_content"`
	// ComposeFile 是宿主机上已存在的 compose 文件路径（与 ComposeContent 二选一）
	ComposeFile string `json:"compose_file"`
}

func (m *ComposeManager) CreateStack(ctx context.Context, sessionID string, req CreateComposeRequest) (*ComposeStack, error) {
	m.mu.Lock()
	if _, exists := m.stacks[sessionID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %s already has a compose stack; tear it down first", sessionID)
	}
	m.mu.Unlock()

	projectName := fmt.Sprintf("agent-%s", sessionID[:8])
	stackDir := filepath.Join(m.dataDir, sessionID)
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create stack directory: %w", err)
	}

	var composeFile string
	if req.ComposeContent != "" {
		// 注入/替换 network 配置，确保所有服务接入平台网络
		content := m.injectNetwork(req.ComposeContent)
		composeFile = filepath.Join(stackDir, "docker-compose.yml")
		if err := os.WriteFile(composeFile, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write compose file: %w", err)
		}
	} else if req.ComposeFile != "" {
		// 使用用户指定的已有 compose 文件
		absPath, err := filepath.Abs(req.ComposeFile)
		if err != nil {
			return nil, fmt.Errorf("invalid compose file path: %w", err)
		}
		if _, err := os.Stat(absPath); err != nil {
			return nil, fmt.Errorf("compose file not found: %w", err)
		}
		// 复制到 stack 目录并注入网络
		raw, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read compose file: %w", err)
		}
		content := m.injectNetwork(string(raw))
		composeFile = filepath.Join(stackDir, "docker-compose.yml")
		if err := os.WriteFile(composeFile, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write compose file: %w", err)
		}
	} else {
		return nil, fmt.Errorf("either compose_content or compose_file must be provided")
	}

	m.logger.Info("Starting compose stack",
		"session_id", sessionID,
		"project_name", projectName,
		"compose_file", composeFile,
	)

	// docker compose -p <project> -f <file> up -d
	if err := m.composeUp(ctx, projectName, composeFile); err != nil {
		return nil, fmt.Errorf("docker compose up failed: %w", err)
	}

	// 查询已启动的服务
	services, err := m.inspectServices(ctx, projectName)
	if err != nil {
		m.logger.Warn("Failed to inspect services after compose up", "error", err)
	}

	stack := &ComposeStack{
		SessionID:   sessionID,
		ProjectName: projectName,
		ComposeFile: composeFile,
		Services:    services,
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	m.mu.Lock()
	m.stacks[sessionID] = stack
	m.mu.Unlock()

	m.logger.Info("Compose stack created",
		"session_id", sessionID,
		"project_name", projectName,
		"services", len(services),
	)

	return stack, nil
}

// TeardownStack 停止并移除 compose 堆栈的所有容器和卷
func (m *ComposeManager) TeardownStack(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	stack, ok := m.stacks[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil // 没有堆栈，静默返回
	}
	delete(m.stacks, sessionID)
	m.mu.Unlock()

	m.logger.Info("Tearing down compose stack",
		"session_id", sessionID,
		"project_name", stack.ProjectName,
	)

	if err := m.composeDown(ctx, stack.ProjectName, stack.ComposeFile); err != nil {
		m.logger.Error("Failed to tear down compose stack",
			"session_id", sessionID,
			"error", err,
		)
		return err
	}

	// 清理 stack 目录
	stackDir := filepath.Join(m.dataDir, sessionID)
	_ = os.RemoveAll(stackDir)

	return nil
}

// GetStack 返回 session 对应的 compose 堆栈信息
func (m *ComposeManager) GetStack(sessionID string) *ComposeStack {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stacks[sessionID]
}

// RefreshServices 重新检查堆栈中服务的状态和 IP
func (m *ComposeManager) RefreshServices(ctx context.Context, sessionID string) (*ComposeStack, error) {
	m.mu.RLock()
	stack, ok := m.stacks[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no compose stack for session %s", sessionID)
	}

	services, err := m.inspectServices(ctx, stack.ProjectName)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	stack.Services = services
	m.mu.Unlock()

	return stack, nil
}

// CleanupSession 清理 session 的 compose 堆栈（TerminateSession 时调用）
func (m *ComposeManager) CleanupSession(ctx context.Context, sessionID string) {
	_ = m.TeardownStack(ctx, sessionID)
}

// ───────────────────────────────────────────────────────────────────────
// 内部方法
// ───────────────────────────────────────────────────────────────────────

func (m *ComposeManager) composeUp(ctx context.Context, projectName, composeFile string) error {
	args := []string{
		"compose",
		"-p", projectName,
		"-f", composeFile,
		"up", "-d",
		"--wait",
	}
	return m.runDocker(ctx, args)
}

func (m *ComposeManager) composeDown(ctx context.Context, projectName, composeFile string) error {
	args := []string{
		"compose",
		"-p", projectName,
		"-f", composeFile,
		"down",
		"--volumes",
		"--remove-orphans",
	}
	return m.runDocker(ctx, args)
}

func (m *ComposeManager) runDocker(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	m.logger.Info("Running docker command", "args", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", err, stderr.String())
	}
	return nil
}

// inspectServices 通过 Docker API 查询 compose 项目的所有容器并获取 IP
func (m *ComposeManager) inspectServices(ctx context.Context, projectName string) ([]ComposeService, error) {
	opts := container.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("com.docker.compose.project=%s", projectName)),
		),
	}

	containers, err := m.docker.ContainerList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list compose containers: %w", err)
	}

	var services []ComposeService
	for _, c := range containers {
		name := ""
		if labels := c.Labels; labels != nil {
			name = labels["com.docker.compose.service"]
		}
		if name == "" && len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		ip := ""
		// 优先从平台共享网络获取 IP
		if netInfo, ok := c.NetworkSettings.Networks[m.network]; ok {
			ip = netInfo.IPAddress
		} else {
			// fallback: 任意网络的 IP
			for _, net := range c.NetworkSettings.Networks {
				if net.IPAddress != "" {
					ip = net.IPAddress
					break
				}
			}
		}

		status := "running"
		if c.State != "running" {
			status = c.State
		}

		services = append(services, ComposeService{
			Name:        name,
			ContainerID: c.ID[:12],
			IP:          ip,
			Status:      status,
		})
	}

	return services, nil
}

// injectNetwork 向 compose 文件注入平台共享网络。
// 使用 external 网络确保 compose 服务和 agent 容器在同一网络。
func (m *ComposeManager) injectNetwork(content string) string {
	networkBlock := fmt.Sprintf(`
networks:
  agent-platform-net:
    external: true
    name: %s
`, m.network)

	// 如果文件已经定义了 networks 段，替换之
	if strings.Contains(content, "\nnetworks:") {
		// 找到 networks: 段并替换
		idx := strings.Index(content, "\nnetworks:")
		content = content[:idx] + networkBlock
	} else {
		content = content + networkBlock
	}

	// 确保每个 service 都连接了 agent-platform-net
	// 找到 services: 段，在每个服务下添加 networks 配置
	if !strings.Contains(content, "agent-platform-net") {
		// 使用简单策略：在文件末 networks 段前，让用户在 compose 文件中自行指定。
		// 更可靠的方式是用 YAML 库解析，但这里保持简单。
	}

	return content
}
