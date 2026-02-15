import asyncio
import json
import logging
import os
from typing import AsyncGenerator, Any, Dict, List, Optional

from src.core.base import BaseAgent
from src.core.llm import LLMClient
from src.core.memory import Memory
from src.config import settings
from src.pb import agent_pb2
from src.tools import TOOL_REGISTRY, get_tool_schemas, get_tool_executor

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# 沙盒上下文：始终注入，让 Agent 知道自己在容器中运行
SANDBOX_CONTEXT = (
  "## Environment\n"
  "You are operating inside a secure, isolated Docker sandbox.\n"
  "- Your workspace directory is: /app/workspace\n"
  "- All file operations are relative to this workspace.\n"
  "- The sandbox is disposable — you can freely run code and modify files.\n"
  "- After you finish your task, use **export_files** to copy results "
  "back to the user's local machine.\n\n"

  "## Pre-installed Runtimes & Tools\n"
  "The following are **already installed** in this sandbox — do NOT attempt to install them:\n"
  "- **Python 3.11** — `python3`, `pip`\n"
  "- **Node.js 20 LTS** — `node`, `npm`, `npx`, `yarn`, `pnpm`\n"
  "- **TypeScript** — `tsc`, `ts-node`\n"
  "- **Build tools** — `gcc`, `g++`, `make`, `cmake`\n"
  "- **Database client libraries** — `libpq` (PostgreSQL), `libmysqlclient` (MySQL/MariaDB)\n"
  "- **Utilities** — `git`, `curl`, `wget`, `jq`, `tree`, `zip`/`unzip`\n\n"

  "## Package Installation Rules\n"
  "**CRITICAL: NEVER use `apt-get`, `apt`, `dpkg`, or `sudo apt` commands.**\n"
  "System package management is disabled in this sandbox. All system libraries\n"
  "you might need are already pre-installed (see above).\n\n"
  "For language-specific packages, use:\n"
  "- **Python** → `pip install <package>` (e.g., `pip install flask pandas`)\n"
  "- **Node.js** → `npm install <package>` (e.g., `npm install express`)\n"
  "- **Go** (if needed) → download the binary directly with `curl`\n\n"

  "## External Dependencies (Databases, Caches, Message Queues, etc.)\n"
  "**ALL external infrastructure MUST be defined via docker-compose.yml so that the user "
  "can reproduce the entire environment with a single `docker compose up`.**\n\n"
  "Follow these steps:\n"
  "1. **Write a `docker-compose.yml`** in the workspace that declares every service "
  "the project needs (e.g., PostgreSQL, Redis, MySQL, MongoDB, RabbitMQ, Kafka, etc.). "
  "Every service definition MUST include:\n"
  "   - `networks: [agent-platform-net]` so the service is reachable from this sandbox.\n"
  "   - A `healthcheck` so the platform can verify readiness.\n"
  "   - Sensible default environment variables (user, password, database name, etc.).\n"
  "   Example:\n"
  "   ```yaml\n"
  "   services:\n"
  "     db:\n"
  "       image: postgres:15-alpine\n"
  "       environment:\n"
  "         POSTGRES_USER: appuser\n"
  "         POSTGRES_PASSWORD: apppass\n"
  "         POSTGRES_DB: appdb\n"
  "       healthcheck:\n"
  "         test: ['CMD-SHELL', 'pg_isready -U appuser -d appdb']\n"
  "         interval: 2s\n"
  "         timeout: 3s\n"
  "         retries: 5\n"
  "       networks:\n"
  "         - agent-platform-net\n"
  "     cache:\n"
  "       image: redis:7-alpine\n"
  "       healthcheck:\n"
  "         test: ['CMD', 'redis-cli', 'ping']\n"
  "         interval: 2s\n"
  "         timeout: 3s\n"
  "         retries: 5\n"
  "       networks:\n"
  "         - agent-platform-net\n"
  "   networks:\n"
  "     agent-platform-net:\n"
  "       external: true\n"
  "   ```\n\n"
  "2. **Use `create_compose_stack`** to launch the docker-compose.yml. "
  "The tool returns the IP addresses of every service.\n"
  "3. **Use the returned IPs** (not `localhost`) to connect to services from your code.\n"
  "4. **Test your application** end-to-end against the running services to verify correctness.\n"
  "5. **Use `get_compose_stack`** if you need to re-check service IPs or status.\n"
  "6. **Do NOT install service daemons locally** "
  "(e.g., no `apt-get install postgresql`, no `brew install redis`).\n"
  "7. **Do NOT use `teardown_compose_stack`** unless you are completely done with the task "
  "and no longer need the services. The stack is automatically cleaned up when the session ends.\n\n"
  "**Why docker-compose?** The user receives the entire project (including docker-compose.yml) "
  "so they can reproduce the full environment with `docker compose up` — no manual setup needed.\n\n"

  "## Available Tools\n"
  "- **bash**: Execute shell commands (run code, pip/npm install, run tests, etc.)\n"
  "- **file_read**: Read file contents in the workspace.\n"
  "- **file_write**: Create or overwrite files in the workspace.\n"
  "- **list_files**: List files and directories in the workspace.\n"
  "- **export_files**: Copy finished work to the user's local machine.\n"
  "- **create_compose_stack**: Launch a docker-compose.yml to start infrastructure services.\n"
  "- **get_compose_stack**: Check status & IPs of running compose services.\n"
  "- **teardown_compose_stack**: Stop and remove all compose services (usually not needed).\n\n"

  "## Workflow\n"
  "1. Analyze the user's request carefully.\n"
  "2. Plan your approach step by step.\n"
  "3. If external services are needed, **write a docker-compose.yml first**, then "
  "use **create_compose_stack** to launch it.\n"
  "4. Use `pip install` or `npm install` for any missing language packages.\n"
  "5. Implement your solution using the available tools.\n"
  "6. **Verify correctness** — run your application against the compose services, run tests, "
  "check output. Fix any issues before proceeding.\n"
  "7. Ensure the project includes the docker-compose.yml so the user can reproduce it.\n"
  "8. Use **export_files** to copy finished work to the user's local machine.\n"
  "9. Summarize what you've done, including how to start the project "
  "(`docker compose up` + run command).\n"
)

# 默认任务提示（当用户未提供 system_prompt 时使用）
DEFAULT_TASK_PROMPT = (
  "You are a helpful and capable AI coding assistant. "
  "Please answer the user's questions step by step. "
  "If you use tools, interpret the results based on the user's original intent."
)

# 默认简单 ReAct Agent 实现
class DefaultAgent(BaseAgent):
  def __init__(self):
    self.llm = LLMClient()
    self.memory = Memory()
    self.tools: list[dict] = []
    self._active_tool_names: list[str] = []
    self._system_prompt: str = ""
    self._max_loops: int = settings.MAX_LOOPS
    self._session_id: str = ""
    self._cancelled = asyncio.Event()

  async def configure(
    self,
    session_id: str,
    system_prompt: str = "",
    builtin_tools: Optional[List[str]] = None,
    extra_tools: Optional[List[Dict[str, Any]]] = None,
    agent_config: Optional[Dict[str, str]] = None,
  ) -> List[str]:
    self._session_id = session_id
    self._system_prompt = system_prompt
    self._cancelled.clear()

    # 将 Session 写入环境变量，供 Go 后端使用
    # TODO：有没有别的更优雅的实现？
    os.environ["SESSION_ID"] = session_id

    if agent_config:
      if "max_loops" in agent_config:
        self._max_loops = int(agent_config["max_loops"])

    self.tools = []
    self._active_tool_names = []

    if builtin_tools:
      schemas = get_tool_schemas(builtin_tools)
      self.tools.extend(schemas)
      self._active_tool_names.extend(
        [n for n in builtin_tools if n in TOOL_REGISTRY]
      )

    if extra_tools:
      for td in extra_tools:
        self.tools.append(td)
        fname = td.get("function", {}).get("name", td.get("name", "unknown"))
        self._active_tool_names.append(fname)

    self.memory.clear()
    # 始终注入沙盒上下文 + 用户自定义/默认任务提示
    task_prompt = self._system_prompt if self._system_prompt else DEFAULT_TASK_PROMPT
    full_system_prompt = SANDBOX_CONTEXT + task_prompt
    self.memory.add_message("system", full_system_prompt)

    logger.info(
      "Agent configured: session=%s tools=%s max_loops=%d",
      session_id,
      self._active_tool_names,
      self._max_loops,
    )
    return list(self._active_tool_names)

  async def step(self, input_text: str) -> AsyncGenerator[dict, None]:
    self._cancelled.clear()

    # 修复 Tool Call 中断历史记录
    self.memory.repair_history()

    self.memory.add_message("user", input_text)

    loops = 0
    while loops < self._max_loops:
      if self._cancelled.is_set():
        yield {
          "type": agent_pb2.EventType.EVENT_TYPE_STATUS,
          "content": "Agent step cancelled",
          "source": "agent",
        }
        return

      history = self.memory.get_history()
      try:
        collected_content = None
        tool_calls = None

        async for chunk in self.llm.stream_complete(
          history,
          tools=self.tools if self.tools else None,
        ):
          if chunk["type"] == "content_delta":
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TEXT_CHUNK,
              "content": chunk["delta"],
              "source": "llm",
            }
          elif chunk["type"] == "done":
            collected_content = chunk["content"]
            tool_calls = chunk["tool_calls"]

        if tool_calls:
          self.memory.add_message(
            role="assistant",
            content=collected_content,
            tool_calls=tool_calls,
          )

          if collected_content:
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_THOUGHT,
              "content": collected_content,
              "source": "llm",
            }

          for tool_call in tool_calls:
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TOOL_CALL,
              "content": f"Calling {tool_call.function.name} with {tool_call.function.arguments}",
              "source": "agent",
              "metadata_json": json.dumps({
                "tool_call_id": tool_call.id,
                "name": tool_call.function.name,
                "arguments": tool_call.function.arguments,
              }),
            }

            result = await self._execute_tool(
              tool_call.function.name,
              tool_call.function.arguments,
            )

            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TOOL_RESULT,
              "content": result,
              "source": "tool",
              "metadata_json": json.dumps({
                "tool_call_id": tool_call.id,
                "name": tool_call.function.name,
              }),
            }

            self.memory.add_message(
              role="tool",
              content=result,
              tool_call_id=tool_call.id,
            )
        else:
          self.memory.add_message("assistant", collected_content or "")
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_ANSWER,
            "content": collected_content or "",
            "source": "llm",
          }
          return

      except Exception as e:
        logger.error("Error in agent step: %s", e)
        yield {
          "type": agent_pb2.EventType.EVENT_TYPE_ERROR,
          "content": str(e),
          "source": "agent",
        }
        return

      loops += 1

    yield {
      "type": agent_pb2.EventType.EVENT_TYPE_ERROR,
      "content": f"Agent exceeded max loops ({self._max_loops})",
      "source": "agent",
    }

  async def stop(self) -> None:
    self._cancelled.set()

  async def reset(self) -> None:
    self.memory.clear()
    self._cancelled.clear()
    task_prompt = self._system_prompt if self._system_prompt else DEFAULT_TASK_PROMPT
    full_system_prompt = SANDBOX_CONTEXT + task_prompt
    self.memory.add_message("system", full_system_prompt)

  async def _execute_tool(self, name: str, arguments_json: str) -> str:
    executor = get_tool_executor(name)
    if executor is None:
      return f"[ERROR] Unknown tool: {name}"

    try:
      args: dict = json.loads(arguments_json) if arguments_json else {}
    except json.JSONDecodeError as exc:
      return f"[ERROR] Invalid tool arguments JSON: {exc}"

    try:
      return await executor(**args)
    except Exception as exc:
      logger.error("Tool %s failed: %s", name, exc)
      return f"[ERROR] Tool execution failed: {exc}"