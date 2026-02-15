from __future__ import annotations

import os
import shutil
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

import httpx

from .config import ClientConfig


@dataclass
class PlatformProcess:
  process: subprocess.Popen[str] | None = None
  log_file: Path | None = None

  def stop(self) -> None:
    if self.process is None:
      return
    if self.process.poll() is not None:
      return
    self.process.terminate()
    try:
      self.process.wait(timeout=8)
    except subprocess.TimeoutExpired:
      self.process.kill()


def _require_binary(name: str) -> None:
  if shutil.which(name) is None:
    raise RuntimeError(f"Required command not found: {name}")


def _run(command: list[str], cwd: Path | None = None, env: dict[str, str] | None = None) -> None:
  subprocess.run(command, cwd=str(cwd) if cwd else None, env=env, check=True)


def _docker_image_exists(image: str) -> bool:
  probe = subprocess.run(
    ["docker", "image", "inspect", image],
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
    check=False,
  )
  return probe.returncode == 0


def _wait_health(api_base: str, timeout_seconds: int) -> None:
  deadline = time.time() + timeout_seconds
  with httpx.Client(timeout=3.0) as client:
    while time.time() < deadline:
      try:
        resp = client.get(f"{api_base.rstrip('/')}/health")
        if resp.status_code == 200 and resp.json().get("status") == "ok":
          return
      except Exception:
        pass
      time.sleep(0.5)
  raise RuntimeError(f"Platform health check timed out after {timeout_seconds}s")


def _platform_env(config: ClientConfig) -> dict[str, str]:
  env = os.environ.copy()
  pool = config.platform.pool

  env.setdefault("POSTGRES_USER", "postgres")
  env.setdefault("POSTGRES_PASSWORD", "postgres")
  env.setdefault("POSTGRES_DB", "agent_platform")
  env.setdefault("POSTGRES_ADDR", "localhost:5432")
  env.setdefault("REDIS_ADDR", "localhost:6379")
  env.setdefault("POOL_MIN_IDLE", str(pool.min_idle))
  env.setdefault("POOL_MAX_BURST", str(pool.max_burst))
  env.setdefault("POOL_WARMUP_IMAGE", pool.warmup_image or config.runtime.image)
  env.setdefault("POOL_NETWORK_NAME", pool.network_name)
  env.setdefault("POOL_CONTAINER_MEM_MB", str(pool.container_mem_mb))
  env.setdefault("POOL_CONTAINER_CPU", str(pool.container_cpu))

  default_host_root = str((Path.home() / ".agent-platform/projects").resolve())
  host_root = pool.host_root or env.get("POOL_HOST_ROOT", default_host_root)
  env.setdefault("POOL_HOST_ROOT", host_root)
  env.setdefault("WORKER_PROJECT_DIR", host_root)
  Path(host_root).mkdir(parents=True, exist_ok=True)
  return env


def ensure_runtime_image(config: ClientConfig) -> None:
  if _docker_image_exists(config.runtime.image):
    return
  if not config.runtime.auto_build_image:
    raise RuntimeError(
      f"Runtime image {config.runtime.image} not found and auto_build_image=false"
    )
  if not config.runtime.root_dir:
    raise RuntimeError(
      "runtime.root_dir is required when runtime image is missing. "
      "Set runtime.root_dir in YAML or AGENT_RUNTIME_ROOT in env."
    )

  runtime_root = Path(config.runtime.root_dir)
  if not (runtime_root / "Dockerfile").is_file():
    raise RuntimeError(f"Invalid runtime.root_dir (Dockerfile not found): {runtime_root}")

  _require_binary("docker")
  _run(["docker", "build", "-t", config.runtime.image, "."], cwd=runtime_root)


def bootstrap_platform(config: ClientConfig) -> PlatformProcess:
  if not config.platform.auto_start:
    _wait_health(config.platform.api_base, config.platform.startup_timeout_seconds)
    return PlatformProcess()

  if not config.platform.root_dir:
    raise RuntimeError(
      "platform.root_dir is required when platform.auto_start=true. "
      "Set platform.root_dir in YAML or AGENT_PLATFORM_ROOT in env."
    )

  platform_root = Path(config.platform.root_dir)
  compose_file = platform_root / "docker-compose.yml"
  if not compose_file.is_file():
    raise RuntimeError(f"docker-compose.yml not found under platform.root_dir: {platform_root}")

  _require_binary("docker")
  _require_binary("go")

  env = _platform_env(config)

  _run(["docker", "compose", "up", "-d"], cwd=platform_root)
  _run(["go", "build", "-o", "bin/platform-server", "./cmd/server"], cwd=platform_root, env=env)

  log_file = platform_root / "platform.log"
  log_file.parent.mkdir(parents=True, exist_ok=True)
  log_fp = open(log_file, "w", encoding="utf-8")
  process = subprocess.Popen(
    [str(platform_root / "bin/platform-server")],
    cwd=str(platform_root),
    env=env,
    stdout=log_fp,
    stderr=subprocess.STDOUT,
    text=True,
  )

  try:
    _wait_health(config.platform.api_base, config.platform.startup_timeout_seconds)
  except Exception:
    if process.poll() is None:
      process.terminate()
    raise

  return PlatformProcess(process=process, log_file=log_file)
