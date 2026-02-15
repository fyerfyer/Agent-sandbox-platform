# agent-client

`agent-client` 是一个 Python CLI，用来替代 `test/start.sh`：

- 在**当前项目目录**读取 `agent.yaml` + `.env`
- 自动准备 `agent-runtime` 镜像（可选）
- 自动启动/连接 Go Platform
- 创建 session、配置 agent，并进入交互式对话

## 1. 安装

在仓库根目录执行：

```bash
cd /path/to/agent-platform
python -m pip install -e ./agent-client
```

## 2. 准备项目目录

在需要开发 Agent 的目录里放两个文件：

### `agent.yaml`

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

### `.env`

```env
DEEPSEEK_API_KEY=sk-xxxx
```

## 3. 启动

在项目目录直接运行：

```bash
agent-client run
```

可选参数：

```bash
agent-client run --config ./agent.yaml --env-file ./.env
```

## 4. 交互命令

- `/files`：查看容器工作目录文件
- `/read <path>`：读取容器里的文件
- `/sync`：同步容器文件到主机项目目录
- `/status`：查看 session 状态
- `/quit`：结束会话并退出

## 5. 常见配置说明

- `platform.auto_start=true`：自动执行 `docker compose up -d` + `go build` + 启动 platform-server
- `platform.auto_start=false`：只连接已运行的平台（不会自动拉镜像）
- `runtime.auto_build_image=true`：找不到 `runtime.image` 时自动在 `runtime.root_dir` 执行 `docker build`

## 6. 示例

可直接参考：

- `examples/agent.yaml`
- `examples/.env.example`
