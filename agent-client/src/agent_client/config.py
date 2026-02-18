from __future__ import annotations

import os
import re
from pathlib import Path
from typing import Any, Literal

import yaml
from dotenv import dotenv_values
from pydantic import BaseModel, Field, ValidationError


CONFIG_CANDIDATES = ("agent.yaml", "agent.yml", "config.yaml", "config.yml")
ENV_PATTERN = re.compile(r"\$\{([A-Z0-9_]+)\}")


class PoolConfig(BaseModel):
  min_idle: int = 0
  max_burst: int = 5
  warmup_image: str | None = None  # defaults to runtime.image
  network_name: str = "agent-platform-net"
  host_root: str | None = None  # defaults to ~/.agent-platform/projects
  container_mem_mb: int = 512
  container_cpu: float = 0.5


class PlatformConfig(BaseModel):
  api_base: str = "http://localhost:8080"
  auto_start: bool = True
  startup_timeout_seconds: int = 45
  root_dir: str | None = None
  log_dir: str | None = None  # 日志输出目录，None 则使用平台默认值
  pool: PoolConfig = Field(default_factory=PoolConfig)


class RuntimeConfig(BaseModel):
  image: str = "agent-runtime:latest"
  auto_build_image: bool = True
  root_dir: str | None = None


class SessionConfig(BaseModel):
  project_id: str = "interactive"
  user_id: str = "interactive-user"
  strategy: Literal["Cold-Strategy", "Warm-Strategy"] = "Cold-Strategy"
  agent_type: str = "default"  # agent 类型：default, langchain, openai-agents, simple, 或自定义
  env_vars: dict[str, str] = Field(default_factory=dict)


class AgentConfig(BaseModel):
  system_prompt: str = ""
  builtin_tools: list[str] = Field(
    default_factory=lambda: ["bash", "file_read", "file_write", "list_files", "export_files",
                             "create_compose_stack", "get_compose_stack", "teardown_compose_stack"]
  )
  agent_config: dict[str, str] = Field(default_factory=lambda: {"max_loops": "20"})


class CLIConfig(BaseModel):
  """CLI 行为配置。"""
  history_max_records: int = 100  # 本地 session 历史最大保留条数
  auto_resume_last: bool = False  # run 时自动恢复上一个 stopped session
  prompt_prefix: str = "You"  # 输入提示符前缀
  stream_timeout: int = 300  # SSE 流最长等待时间（秒）


class ClientConfig(BaseModel):
  platform: PlatformConfig = Field(default_factory=PlatformConfig)
  runtime: RuntimeConfig = Field(default_factory=RuntimeConfig)
  session: SessionConfig = Field(default_factory=SessionConfig)
  agent: AgentConfig = Field(default_factory=AgentConfig)
  cli: CLIConfig = Field(default_factory=CLIConfig)


def _resolve_env(value: Any) -> Any:
  if isinstance(value, dict):
    return {k: _resolve_env(v) for k, v in value.items()}
  if isinstance(value, list):
    return [_resolve_env(v) for v in value]
  if isinstance(value, str):
    return ENV_PATTERN.sub(lambda m: os.environ.get(m.group(1), ""), value)
  return value


def _find_config_file(explicit: str | None, cwd: Path) -> Path:
  if explicit:
    cfg = Path(explicit).expanduser().resolve()
    if not cfg.is_file():
      raise FileNotFoundError(f"Config file not found: {cfg}")
    return cfg

  for name in CONFIG_CANDIDATES:
    candidate = cwd / name
    if candidate.is_file():
      return candidate
  raise FileNotFoundError(
    "No config file found in current directory. Expected one of: "
    + ", ".join(CONFIG_CANDIDATES)
  )


def _load_env(env_file: str | None, cwd: Path) -> Path | None:
  if env_file:
    env_path = Path(env_file).expanduser().resolve()
  else:
    env_path = (cwd / ".env").resolve()

  if not env_path.is_file():
    return None

  for key, value in dotenv_values(env_path).items():
    if value is not None and key not in os.environ:
      os.environ[key] = value
  return env_path


def _normalize_paths(config: ClientConfig, config_file: Path) -> ClientConfig:
  base = config_file.parent

  platform_root = config.platform.root_dir or os.environ.get("AGENT_PLATFORM_ROOT")
  runtime_root = config.runtime.root_dir or os.environ.get("AGENT_RUNTIME_ROOT")

  if platform_root:
    config.platform.root_dir = str((base / platform_root).resolve()) if not os.path.isabs(platform_root) else platform_root
  if runtime_root:
    config.runtime.root_dir = str((base / runtime_root).resolve()) if not os.path.isabs(runtime_root) else runtime_root

  return config


def load_client_config(
  config_file: str | None = None,
  env_file: str | None = None,
  cwd: str | None = None,
) -> tuple[ClientConfig, Path, Path | None]:
  root = Path(cwd).expanduser().resolve() if cwd else Path.cwd().resolve()
  loaded_env = _load_env(env_file, root)
  loaded_config = _find_config_file(config_file, root)

  with open(loaded_config, "r", encoding="utf-8") as f:
    data = yaml.safe_load(f) or {}
  data = _resolve_env(data)

  try:
    config = ClientConfig.model_validate(data)
  except ValidationError as exc:
    raise ValueError(f"Invalid config file: {loaded_config}\n{exc}") from exc

  config = _normalize_paths(config, loaded_config)
  return config, loaded_config, loaded_env
