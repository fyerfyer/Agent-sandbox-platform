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
  "- You can safely install packages, run code, and modify files — "
  "the sandbox is disposable.\n"
  "- After you finish your task, use the **export_files** tool to copy results "
  "back to the user's local machine.\n\n"
  "## Available Tools\n"
  "- **bash**: Execute shell commands in the workspace (install packages, run tests, etc.)\n"
  "- **file_read**: Read file contents in the workspace.\n"
  "- **file_write**: Create or overwrite files in the workspace.\n"
  "- **list_files**: List files and directories in the workspace.\n"
  "- **export_files**: Export files from the container to the user's local machine. "
  "Use this after completing your task so the user can access the results. "
  "You can export the entire workspace or specific files/directories.\n"
  "- **create_service**: Create a companion Docker container (e.g., PostgreSQL, Redis) "
  "in the same network. Use this when your task requires external services.\n"
  "- **remove_service**: Remove a previously created companion service container.\n\n"
  "## Workflow\n"
  "1. Analyze the user's request carefully.\n"
  "2. Plan your approach step by step.\n"
  "3. If you need external services (database, cache, etc.), use create_service first.\n"
  "4. Use tools to implement your solution.\n"
  "5. Verify correctness (run tests, check output).\n"
  "6. Use **export_files** to copy finished work to the user's local machine.\n"
  "7. Summarize what you've done.\n"
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
        message = await self.llm.complete(
          history,
          tools=self.tools if self.tools else None,
        )

        if message.tool_calls:
          self.memory.add_message(
            role="assistant",
            content=message.content,
            tool_calls=message.tool_calls,
          )

          if message.content:
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_THOUGHT,
              "content": message.content,
              "source": "llm",
            }

          for tool_call in message.tool_calls:
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
          self.memory.add_message("assistant", message.content)
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_ANSWER,
            "content": message.content,
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