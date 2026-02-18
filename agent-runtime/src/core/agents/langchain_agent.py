import asyncio
import json
import logging
import os
from typing import AsyncGenerator, Any, Dict, List, Optional

from src.core.base import BaseAgent, AgentMetadata, AgentCapability
from src.core.memory import Memory
from src.config import settings
from src.pb import agent_pb2
from src.tools import TOOL_REGISTRY, get_tool_schemas, get_tool_executor
from src.registry import register_agent

logger = logging.getLogger(__name__)

# 延迟导入 LangChain，如果不可用则记录警告
_LANGCHAIN_AVAILABLE = False
try:
  from langchain_openai import ChatOpenAI
  from langchain_core.messages import (
    HumanMessage, SystemMessage, AIMessage, ToolMessage
  )
  from langchain_core.tools import StructuredTool
  _LANGCHAIN_AVAILABLE = True
except ImportError:
  logger.warning(
    "LangChain not installed. LangchainAgent will not be available. "
    "Install with: pip install langchain langchain-openai"
  )


def _make_langchain_tool(name: str, executor, schema: dict) -> Any:
  """将平台内置工具包装为 LangChain StructuredTool。"""
  func_def = schema.get("function", schema)
  description = func_def.get("description", f"Tool: {name}")
  parameters = func_def.get("parameters", {})

  async def _wrapper(**kwargs):
    return await executor(**kwargs)

  return StructuredTool.from_function(
    func=lambda **kw: asyncio.get_event_loop().run_until_complete(_wrapper(**kw)),
    coroutine=_wrapper,
    name=name,
    description=description,
  )


@register_agent("langchain")
class LangchainAgent(BaseAgent):
  """
  基于 LangChain 的 ReAct Agent。

  使用 LangChain 的 ChatOpenAI 作为 LLM 后端，
  并将平台内置工具映射为 LangChain Tools。
  """

  @classmethod
  def metadata(cls) -> AgentMetadata:
    return AgentMetadata(
      name="LangchainAgent",
      version="1.0.0",
      description="ReAct agent powered by LangChain framework",
      author="agent-platform",
      capabilities=[
        AgentCapability.STREAMING,
        AgentCapability.TOOL_CALLING,
        AgentCapability.MULTI_TURN,
        AgentCapability.CODE_EXECUTION,
        AgentCapability.FILE_IO,
      ],
      supported_config_keys=["max_loops", "temperature", "model_name"],
    )

  def __init__(self):
    if not _LANGCHAIN_AVAILABLE:
      raise ImportError(
        "LangChain is required for LangchainAgent. "
        "Install with: pip install langchain langchain-openai"
      )
    self._llm: Optional[ChatOpenAI] = None
    self._memory = Memory()
    self._tools: Dict[str, Any] = {}       # name -> langchain tool
    self._tool_schemas: list = []           # openai format schemas
    self._active_tool_names: list = []
    self._system_prompt: str = ""
    self._session_id: str = ""
    self._max_loops: int = settings.MAX_LOOPS
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

    os.environ["SESSION_ID"] = session_id

    # 解析配置
    temperature = 0.0
    model_name = settings.MODEL_NAME
    if agent_config:
      if "max_loops" in agent_config:
        self._max_loops = int(agent_config["max_loops"])
      if "temperature" in agent_config:
        temperature = float(agent_config["temperature"])
      if "model_name" in agent_config:
        model_name = agent_config["model_name"]

    # 初始化 LLM
    self._llm = ChatOpenAI(
      model=model_name,
      api_key=settings.DEEPSEEK_API_KEY,
      base_url=settings.DEEPSEEK_BASE_URL,
      temperature=temperature,
      streaming=True,
    )

    # 注册工具
    self._tools = {}
    self._active_tool_names = []
    self._tool_schemas = []

    if builtin_tools:
      for name in builtin_tools:
        entry = TOOL_REGISTRY.get(name)
        if entry:
          lc_tool = _make_langchain_tool(name, entry["executor"], entry["schema"])
          self._tools[name] = lc_tool
          self._active_tool_names.append(name)
          self._tool_schemas.append(entry["schema"])

    # LangChain 不直接使用 extra_tools 的 OpenAI 格式，但我们保留兼容
    if extra_tools:
      for td in extra_tools:
        fname = td.get("function", {}).get("name", td.get("name", "unknown"))
        self._active_tool_names.append(fname)
        self._tool_schemas.append(td)

    # 初始化对话历史
    self._memory.clear()
    self._memory.add_message("system", self._system_prompt or "You are a helpful assistant.")

    logger.info(
      "LangchainAgent configured: session=%s tools=%s",
      session_id, self._active_tool_names,
    )
    await self.on_configure()
    return list(self._active_tool_names)

  async def step(self, input_text: str) -> AsyncGenerator[dict, None]:
    self._cancelled.clear()
    self._memory.repair_history()
    self._memory.add_message("user", input_text)

    # 构建 LangChain messages
    messages = self._build_lc_messages()
    tools_for_bind = list(self._tools.values()) if self._tools else None

    loops = 0
    while loops < self._max_loops:
      if self._cancelled.is_set():
        yield {
          "type": agent_pb2.EventType.EVENT_TYPE_STATUS,
          "content": "Agent step cancelled",
          "source": "agent",
        }
        return

      try:
        # 使用 LangChain 的 bind_tools + stream
        llm = self._llm
        if tools_for_bind:
          llm = self._llm.bind_tools([t for t in tools_for_bind])

        collected_content = []
        tool_calls = []

        async for chunk in llm.astream(messages):
          if self._cancelled.is_set():
            break

          if hasattr(chunk, "content") and chunk.content:
            collected_content.append(chunk.content)
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TEXT_CHUNK,
              "content": chunk.content,
              "source": "llm",
            }

          if hasattr(chunk, "tool_calls") and chunk.tool_calls:
            tool_calls.extend(chunk.tool_calls)

          # 中途 chunk 可能携带 additional_kwargs 中的增量 tool_calls
          if hasattr(chunk, "additional_kwargs"):
            tc = chunk.additional_kwargs.get("tool_calls", [])
            if tc:
              tool_calls.extend(tc)

        full_content = "".join(collected_content)

        if tool_calls:
          # 记录 assistant 消息
          self._memory.add_message("assistant", full_content or None)
          messages.append(AIMessage(content=full_content or "", tool_calls=tool_calls))

          if full_content:
            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_THOUGHT,
              "content": full_content,
              "source": "llm",
            }

          for tc in tool_calls:
            tc_name = tc.get("name", tc.get("function", {}).get("name", "unknown"))
            tc_args = tc.get("args", {})
            tc_id = tc.get("id", "")

            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TOOL_CALL,
              "content": f"Calling {tc_name} with {json.dumps(tc_args)}",
              "source": "agent",
              "metadata_json": json.dumps({
                "tool_call_id": tc_id,
                "name": tc_name,
                "arguments": json.dumps(tc_args),
              }),
            }

            # 执行工具
            result = await self._execute_tool(tc_name, tc_args)

            yield {
              "type": agent_pb2.EventType.EVENT_TYPE_TOOL_RESULT,
              "content": result,
              "source": "tool",
              "metadata_json": json.dumps({
                "tool_call_id": tc_id,
                "name": tc_name,
              }),
            }

            messages.append(ToolMessage(content=result, tool_call_id=tc_id))
            self._memory.add_message("tool", result, tool_call_id=tc_id)
        else:
          # 无工具调用 → 最终回答
          self._memory.add_message("assistant", full_content or "")
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_ANSWER,
            "content": full_content or "",
            "source": "llm",
          }
          return

      except Exception as e:
        logger.error("LangchainAgent error: %s", e)
        error_msg = await self.on_error(e)
        yield {
          "type": agent_pb2.EventType.EVENT_TYPE_ERROR,
          "content": error_msg or str(e),
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
    self._memory.clear()
    self._cancelled.clear()
    self._memory.add_message("system", self._system_prompt or "You are a helpful assistant.")

  async def cleanup(self) -> None:
    self._tools.clear()
    self._llm = None

  def _build_lc_messages(self) -> list:
    """将 Memory 历史转为 LangChain Message 对象列表。"""
    lc_msgs = []
    for msg in self._memory.get_history():
      role = msg["role"]
      content = msg.get("content") or ""
      if role == "system":
        lc_msgs.append(SystemMessage(content=content))
      elif role == "user":
        lc_msgs.append(HumanMessage(content=content))
      elif role == "assistant":
        lc_msgs.append(AIMessage(content=content))
      elif role == "tool":
        tc_id = msg.get("tool_call_id", "")
        lc_msgs.append(ToolMessage(content=content, tool_call_id=tc_id))
    return lc_msgs

  async def _execute_tool(self, name: str, args: dict) -> str:
    """执行工具调用。优先使用 LangChain 封装的工具，回退到平台工具。"""
    # 尝试 LangChain tool
    if name in self._tools:
      try:
        return await self._tools[name].ainvoke(args)
      except Exception as e:
        logger.error("LangChain tool %s failed: %s", name, e)
        return f"[ERROR] Tool execution failed: {e}"

    # 回退到平台工具
    executor = get_tool_executor(name)
    if executor:
      try:
        return await executor(**args)
      except Exception as e:
        return f"[ERROR] Tool execution failed: {e}"

    return f"[ERROR] Unknown tool: {name}"

  def get_state(self) -> Dict[str, Any]:
    state = super().get_state()
    state.update({
      "session_id": self._session_id,
      "history_length": len(self._memory.get_history()),
      "max_loops": self._max_loops,
      "active_tools": self._active_tool_names,
      "langchain_available": _LANGCHAIN_AVAILABLE,
    })
    return state