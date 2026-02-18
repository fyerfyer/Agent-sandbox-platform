"""
Agent 注册与发现机制。

第三方 Agent 只需实现 BaseAgent 接口，
并通过以下方式之一注册：

1. 使用 @register_agent 装饰器
2. 通过环境变量 AGENT_MODULE / AGENT_CLASS 指定自定义 Agent 类
3. 在 config.yaml 中指定 agent_type

内置 Agent 类型会在 ensure_builtin_agents() 时自动注册。

e.g.
@register_agent("langchain")
class LangchainAgent(BaseAgent):
  ...

# 运行时自动加载：
AGENT_TYPE=langchain python -m src.main
"""

from __future__ import annotations

import importlib
import logging
from typing import Callable, Dict, List, Optional, Type

from src.core.base import BaseAgent, AgentMetadata

logger = logging.getLogger(__name__)

# 全局 Agent 注册表：name -> factory 
_AGENT_REGISTRY: Dict[str, Callable[[], BaseAgent]] = {}
_BUILTIN_LOADED: bool = False


def register_agent(name: str):
  """
  装饰器：将 Agent 类注册到全局注册表中。
  """
  def decorator(cls: Type[BaseAgent]):
    if not issubclass(cls, BaseAgent):
      raise TypeError(
        f"register_agent: {cls.__name__} must be a subclass of BaseAgent"
      )
    _AGENT_REGISTRY[name] = cls
    logger.info("Registered agent type: %s -> %s", name, cls.__name__)
    return cls
  return decorator


def ensure_builtin_agents() -> None:
  """
  确保内置 Agent 已注册。

  通过导入 src.core.agents 包来触发所有 @register_agent 装饰器。
  幂等，多次调用无副作用。
  """
  global _BUILTIN_LOADED
  if _BUILTIN_LOADED:
    return
  try:
    import src.core.agents  # noqa: F401 — 触发注册
    _BUILTIN_LOADED = True
    logger.info("Built-in agents loaded: %s", list(_AGENT_REGISTRY.keys()))
  except Exception as e:
    logger.warning("Failed to load built-in agents: %s", e)


def get_agent_factory(agent_type: Optional[str] = None) -> Callable[[], BaseAgent]:
  """
  根据 agent_type 返回对应的 Agent 工厂函数。

  查找顺序：
  1. agent_type 参数（显式指定）
  2. 注册表中已注册的类型
  3. 默认 DefaultAgent
  """
  ensure_builtin_agents()

  # 默认 fallback
  if not agent_type or agent_type == "default":
    from src.core.agent import DefaultAgent
    return DefaultAgent

  # 从注册表查找
  if agent_type in _AGENT_REGISTRY:
    logger.info("Using registered agent type: %s", agent_type)
    return _AGENT_REGISTRY[agent_type]

  raise ValueError(
    f"Unknown agent type: '{agent_type}'. "
    f"Available types: {list(_AGENT_REGISTRY.keys()) + ['default']}"
  )


def load_agent_from_module(module_path: str, class_name: str) -> Callable[[], BaseAgent]:
  """
  从指定的 Python 模块路径动态加载 Agent 类。

  这适用于需要在运行时加载自定义 Agent 的场景，
  如用户在 Dockerfile 中安装了自己的 Agent 包。
  """
  logger.info("Loading agent from module: %s.%s", module_path, class_name)
  module = importlib.import_module(module_path)
  cls = getattr(module, class_name)

  if not issubclass(cls, BaseAgent):
    raise TypeError(
      f"{class_name} in {module_path} is not a subclass of BaseAgent"
    )

  # 自动注册
  _AGENT_REGISTRY[class_name.lower()] = cls
  return cls


def list_registered_agents() -> List[str]:
  ensure_builtin_agents()
  return list(_AGENT_REGISTRY.keys())


def get_agent_metadata(agent_type: str) -> Optional[AgentMetadata]:
  ensure_builtin_agents()
  factory = _AGENT_REGISTRY.get(agent_type)
  if factory is None:
    return None
  if hasattr(factory, "metadata"):
    return factory.metadata()
  return AgentMetadata(name=agent_type)