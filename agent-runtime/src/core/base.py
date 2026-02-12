from abc import ABC, abstractmethod
from typing import AsyncGenerator, Any, Dict, List, Optional

class BaseAgent(ABC):
  @abstractmethod
  async def configure(
    self,
    session_id: str,
    system_prompt: str = "",
    builtin_tools: Optional[List[str]] = None,
    extra_tools: Optional[List[Dict[str, Any]]] = None,
    agent_config: Optional[Dict[str, str]] = None,
  ) -> List[str]:
    pass 

  @abstractmethod
  async def step(self, input_text: str) -> AsyncGenerator[dict, None]:
    pass 

  @abstractmethod
  async def stop(self) -> None:
    pass 

  @abstractmethod
  async def reset(self) -> None:
    pass