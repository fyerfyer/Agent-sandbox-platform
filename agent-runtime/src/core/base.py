"""
Agent Runtime 的核心抽象基类。

所有自定义 Agent 必须继承此类并实现必要的抽象方法。
可选方法提供了生命周期钩子和元数据声明能力。

最小实现示例：
    from src.core.base import BaseAgent
    from src.registry import register_agent

    @register_agent("my-agent")
    class MyAgent(BaseAgent):
        async def configure(self, session_id, **kwargs): ...
        async def step(self, input_text): ...
        async def stop(self): ...
        async def reset(self): ...
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from enum import Enum
from typing import AsyncGenerator, Any, Dict, List, Optional


class AgentCapability(str, Enum):
  """Agent 声明的能力标识。"""
  STREAMING = "streaming"            # 支持流式输出
  TOOL_CALLING = "tool_calling"      # 支持工具调用
  MULTI_TURN = "multi_turn"          # 支持多轮对话
  CODE_EXECUTION = "code_execution"  # 支持代码执行
  FILE_IO = "file_io"                # 支持文件读写
  COMPOSE_STACK = "compose_stack"    # 支持 Docker Compose 管理
  CUSTOM_TOOLS = "custom_tools"      # 支持自定义工具注入


@dataclass
class AgentMetadata:
  """Agent 元数据，用于描述 Agent 的基本信息和能力。"""
  name: str = "unnamed"
  version: str = "0.1.0"
  description: str = ""
  author: str = ""
  capabilities: List[AgentCapability] = field(default_factory=lambda: [
    AgentCapability.STREAMING,
    AgentCapability.TOOL_CALLING,
    AgentCapability.MULTI_TURN,
  ])
  # Agent 支持的额外配置键，供 CLI 与平台侧做参数校验
  supported_config_keys: List[str] = field(default_factory=lambda: ["max_loops"])


class BaseAgent(ABC):
  """
  Agent 抽象基类。

  生命周期：
    1. __init__()     — 实例化（由 registry 工厂调用）
    2. configure()    — 注入 session 上下文、工具和配置
    3. step()         — 处理用户输入，流式产出事件
    4. stop()         — 中止当前推理循环
    5. reset()        — 重置对话状态（保留 session 绑定）
    6. cleanup()      — 释放资源（session 结束时调用）

  可选钩子：
    - on_configure()  — configure 完成后的自定义逻辑
    - on_error()      — step 遇到异常时的自定义处理
    - health_check()  — 健康检查（给平台用）
    - get_state()     — 获取内部状态快照（调试用）
  """

  @classmethod
  def metadata(cls) -> AgentMetadata:
    """返回 Agent 元数据。子类可覆盖此方法声明自己的能力。"""
    return AgentMetadata(name=cls.__name__)

  @abstractmethod
  async def configure(
    self,
    session_id: str,
    system_prompt: str = "",
    builtin_tools: Optional[List[str]] = None,
    extra_tools: Optional[List[Dict[str, Any]]] = None,
    agent_config: Optional[Dict[str, str]] = None,
  ) -> List[str]:
    """
    配置 Agent。

    Args:
      session_id: 会话 ID
      system_prompt: 系统提示词
      builtin_tools: 内置工具名称列表（如 ["bash", "file_read"]）
      extra_tools: 额外工具定义（OpenAI function calling 格式）
      agent_config: 键值对配置（如 {"max_loops": "20"}）

    Returns:
      实际激活的工具名称列表。
    """
    ...

  @abstractmethod
  async def step(self, input_text: str) -> AsyncGenerator[dict, None]:
    """
    处理一轮用户输入，流式产出事件。

    每个 yield 的 dict 包含：
      - type: agent_pb2.EventType 枚举值
      - content: 文本内容
      - source: 事件来源 ("llm", "agent", "tool")
      - metadata_json: 可选，额外的 JSON 元数据

    Args:
      input_text: 用户输入文本

    Yields:
      事件字典
    """
    ...
    if False:
      yield  # pragma: no cover

  @abstractmethod
  async def stop(self) -> None:
    """发送中止信号，中断当前的推理循环。"""
    ...

  @abstractmethod
  async def reset(self) -> None:
    """重置 Agent 的对话状态，但保留 session 绑定和配置。"""
    ...

  async def cleanup(self) -> None:
    """
    释放 Agent 持有的资源。

    当 session 终止时由 Service 层调用。
    """
    pass

  async def on_configure(self) -> None:
    """
    configure() 成功完成后的钩子。

    可用于初始化外部客户端、预加载模型等。
    """
    pass

  async def on_error(self, error: Exception) -> Optional[str]:
    """
    step() 遇到异常时的钩子。

    Args:
      error: 捕获到的异常

    Returns:
      可选的用户可见错误消息。返回 None 则使用默认错误处理。
    """
    return None

  async def health_check(self) -> bool:
    """
    Agent 健康检查。

    Returns:
      True 表示 Agent 状态正常。
    """
    return True

  def get_state(self) -> Dict[str, Any]:
    meta = self.metadata()
    return {
      "agent_name": meta.name,
      "agent_version": meta.version,
      "capabilities": [c.value for c in meta.capabilities],
    }