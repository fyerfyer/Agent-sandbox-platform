"""
内置 Agent 实现。

包含平台预置的 Agent 示例类型，每个 Agent 自动注册到全局注册表中。
导入此包即可完成所有内置 Agent 的注册。

可用的 Agent 类型：
    - "simple"         : 精简版 Agent，无工具调用，纯 LLM 对话
    - "langchain"      : 基于 LangChain 的 ReAct Agent
    - "openai-agents"  : 基于 OpenAI Agents SDK 的 Agent

默认 Agent （"default"）定义在 src.core.agent 中，不在此包内。
"""

from src.core.agents.simple_agent import SimpleAgent
from src.core.agents.langchain_agent import LangchainAgent

__all__ = ["SimpleAgent", "LangchainAgent"]
