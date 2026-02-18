import asyncio
import logging
from typing import AsyncGenerator, Any, Dict, List, Optional

from src.core.base import BaseAgent, AgentMetadata, AgentCapability
from src.core.llm import LLMClient
from src.core.memory import Memory
from src.registry import register_agent
from src.pb import agent_pb2

logger = logging.getLogger(__name__)


@register_agent("simple")
class SimpleAgent(BaseAgent):
  """Simple Agent：纯 LLM 多轮对话，不支持工具调用。"""
  @classmethod
  def metadata(cls) -> AgentMetadata:
    return AgentMetadata(
      name="SimpleAgent",
      version="1.0.0",
      description="Lightweight conversational agent without tool support",
      capabilities=[
        AgentCapability.STREAMING,
        AgentCapability.MULTI_TURN,
      ],
      supported_config_keys=["max_history"],
    )

  def __init__(self):
    self.llm = LLMClient()
    self.memory = Memory()
    self._system_prompt: str = ""
    self._session_id: str = ""
    self._cancelled = asyncio.Event()
    self._max_history: int = 50  # 保留的最大历史消息数

  async def configure(
    self,
    session_id: str,
    system_prompt: str = "",
    builtin_tools: Optional[List[str]] = None,
    extra_tools: Optional[List[Dict[str, Any]]] = None,
    agent_config: Optional[Dict[str, str]] = None,
  ) -> List[str]:
    self._session_id = session_id
    self._system_prompt = system_prompt or "You are a helpful assistant."
    self._cancelled.clear()

    if agent_config:
      if "max_history" in agent_config:
        self._max_history = int(agent_config["max_history"])

    self.memory.clear()
    self.memory.add_message("system", self._system_prompt)

    logger.info("SimpleAgent configured: session=%s", session_id)
    # SimpleAgent 不使用工具
    return []

  async def step(self, input_text: str) -> AsyncGenerator[dict, None]:
    self._cancelled.clear()
    self.memory.add_message("user", input_text)

    # 截断历史（保留 system + 最新的 N 条）
    history = self.memory.get_history()
    if len(history) > self._max_history + 1:
      self.memory.history = [history[0]] + history[-(self._max_history):]
      history = self.memory.get_history()

    try:
      collected = []
      async for chunk in self.llm.stream_complete(history, tools=None):
        if self._cancelled.is_set():
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_STATUS,
            "content": "Generation cancelled",
            "source": "agent",
          }
          return

        if chunk["type"] == "content_delta":
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_TEXT_CHUNK,
            "content": chunk["delta"],
            "source": "llm",
          }
          collected.append(chunk["delta"])
        elif chunk["type"] == "done":
          content = chunk.get("content") or "".join(collected)
          self.memory.add_message("assistant", content)
          yield {
            "type": agent_pb2.EventType.EVENT_TYPE_ANSWER,
            "content": content,
            "source": "llm",
          }
          return

    except Exception as e:
      logger.error("SimpleAgent error: %s", e)
      error_msg = await self.on_error(e)
      yield {
        "type": agent_pb2.EventType.EVENT_TYPE_ERROR,
        "content": error_msg or str(e),
        "source": "agent",
      }

  async def stop(self) -> None:
    self._cancelled.set()

  async def reset(self) -> None:
    self.memory.clear()
    self._cancelled.clear()
    self.memory.add_message("system", self._system_prompt or "You are a helpful assistant.")

  def get_state(self) -> Dict[str, Any]:
    state = super().get_state()
    state.update({
      "session_id": self._session_id,
      "history_length": len(self.memory.get_history()),
      "max_history": self._max_history,
    })
    return state