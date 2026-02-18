import grpc
import json
import time
import logging
from typing import Dict

from src.pb import agent_pb2
from src.pb import agent_pb2_grpc
from src.core.base import BaseAgent
from src.core.agent import DefaultAgent
from src.registry import ensure_builtin_agents

logger = logging.getLogger(__name__)

# 只要实现了 AgentService 的接口方法，就可以通过 gRPC 提供服务
class AgentService(agent_pb2_grpc.AgentServiceServicer):
  def __init__(self, agent_factory=None):
    ensure_builtin_agents()
    self._agent_factory = agent_factory or (lambda: DefaultAgent())
    self._agents: Dict[str, BaseAgent] = {}

  def _get_or_create_agent(self, session_id: str) -> BaseAgent:
    if session_id not in self._agents:
      self._agents[session_id] = self._agent_factory()
    return self._agents[session_id]

  async def _cleanup_agent(self, session_id: str) -> None:
    """清理指定 session 的 Agent 资源。"""
    agent = self._agents.pop(session_id, None)
    if agent is not None:
      try:
        await agent.cleanup()
      except Exception as e:
        logger.warning("Agent cleanup failed for session %s: %s", session_id, e)

  async def Configure(self, request, context):
    logger.info("Configure request for session %s", request.session_id)

    agent = self._get_or_create_agent(request.session_id)

    extra_tools = []
    for td in request.tools:
      params = {}
      if td.parameters_json:
        try:
          params = json.loads(td.parameters_json)
        except json.JSONDecodeError:
          params = {}
      extra_tools.append({
        "type": "function",
        "function": {
          "name": td.name,
          "description": td.description,
          "parameters": params,
        },
      })

    try:
      available = await agent.configure(
        session_id=request.session_id,
        system_prompt=request.system_prompt,
        builtin_tools=list(request.builtin_tools),
        extra_tools=extra_tools if extra_tools else None,
        agent_config=dict(request.agent_config) if request.agent_config else None,
      )
      return agent_pb2.ConfigureResponse(
        success=True,
        message="Agent configured",
        available_tools=available,
      )
    except Exception as e:
      logger.error("Configure failed: %s", e)
      return agent_pb2.ConfigureResponse(
        success=False,
        message=str(e),
      )

  async def RunStep(self, request, context):
    logger.info("RunStep request for session %s", request.session_id)

    agent = self._get_or_create_agent(request.session_id)

    try:
      async for event_data in agent.step(request.input_text):
        yield agent_pb2.AgentEvent(
          type=event_data.get("type", agent_pb2.EventType.EVENT_TYPE_UNSPECIFIED),
          content=event_data.get("content", ""),
          source=event_data.get("source", "agent"),
          metadata_json=event_data.get("metadata_json", ""),
          timestamp=int(time.time()),
        )
    except Exception as e:
      logger.error("Error in RunStep: %s", e)
      yield agent_pb2.AgentEvent(
        type=agent_pb2.EventType.EVENT_TYPE_ERROR,
        content=str(e),
        source="service",
        timestamp=int(time.time()),
      )

  async def Stop(self, request, context):
    logger.info("Stop request for session %s", request.session_id)

    agent = self._agents.get(request.session_id)
    if agent is None:
      return agent_pb2.StopResponse(success=False, message="No agent for session")

    try:
      await agent.stop()
      # 清理 Agent 资源
      await self._cleanup_agent(request.session_id)
      return agent_pb2.StopResponse(success=True, message="Stop signal sent")
    except Exception as e:
      return agent_pb2.StopResponse(success=False, message=str(e))

  async def Health(self, request, context):
    return agent_pb2.Pong(status=agent_pb2.ServiceStatus.SERVICE_STATUS_OK)