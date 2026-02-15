# Agent Platform

> 目前已经跑通了基本的 Agent 调用、docker-compose 依赖创建、宿主机——沙箱内容同步。

当前项目包含

- `platform/`：Go 平台服务（Session、容器编排、SSE、文件同步）
- `agent-runtime/`：Python gRPC agent 运行时镜像
- `agent-client/`：Python CLI（`yaml + .env + 命令行` 方式启动）

## 快速开始

## 1. 安装 Python Client

```bash
python -m pip install -e ./agent-client
```

## 2. 创建配置文件

创建 `agent.yaml`：

```yaml
platform:
  api_base: http://localhost:8080
  auto_start: true
  startup_timeout_seconds: 45
  root_dir: /absolute/path/to/agent-platform/platform

runtime:
  image: agent-runtime:latest
  auto_build_image: true
  root_dir: /absolute/path/to/agent-platform/agent-runtime

session:
  project_id: my-agent-project
  user_id: my-user
  strategy: Cold-Strategy
  env_vars:
    DEEPSEEK_API_KEY: ${DEEPSEEK_API_KEY}
    DEEPSEEK_BASE_URL: https://api.deepseek.com
    MODEL_NAME: deepseek-chat
    MAX_LOOPS: "20"

agent:
  system_prompt: "You are a practical coding assistant."
  builtin_tools: [bash, file_read, file_write, list_files, export_files]
  agent_config:
    max_loops: "20"
```

创建 `.env`：

```env
DEEPSEEK_API_KEY=sk-xxxx
```

## 3. 启动

在该项目目录执行：

```bash
agent-client run
```

它会自动：

1. 读取当前目录 `agent.yaml` + `.env`
2. （可选）自动构建 `agent-runtime` 镜像
3. （可选）自动拉起 Go Platform 依赖 + 启动 platform-server
4. 创建 Session 并配置 Agent
5. 进入交互式会话

## Go Platform Docker 化运行

除了本地 `go build` 启动，也可以把 Go 服务封装成镜像。

### 构建镜像

```bash
docker build -t agent-platform-server:latest ./platform
```

### 启动依赖（Postgres + Redis）

```bash
docker compose -f ./platform/docker-compose.yml up -d
```

### 启动 platform-server 容器

```bash
docker run --rm \
  --name agent-platform-server \
  -p 8080:8080 \
  -p 9090:9090 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e POSTGRES_ADDR=host.docker.internal:5432 \
  -e REDIS_ADDR=host.docker.internal:6379 \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=agent_platform \
  -e POOL_NETWORK_NAME=agent-platform-net \
  -e POOL_WARMUP_IMAGE=agent-runtime:latest \
  -e POOL_HOST_ROOT=/tmp/agent-platform/projects \
  -e WORKER_PROJECT_DIR=/tmp/agent-platform/projects \
  agent-platform-server:latest
```

> Linux 下 `host.docker.internal` 可能不可用；可替换为宿主机网关地址。

---

## 目录说明

- `test/`：早期验证脚本（含硬编码），保留作参考
- `agent-client/`：推荐入口（替代脚本化启动）

更多细节见：`agent-client/README.md`。
