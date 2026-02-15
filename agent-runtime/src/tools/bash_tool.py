import asyncio
import logging
import os
import re
from typing import Any, Dict

logger = logging.getLogger(__name__)

MAX_OUTPUT_CHARS = 30_000
DEFAULT_TIMEOUT = 120

# ── apt / system-package-manager guard ──────────────────────────────
# Matches commands like: apt-get install, apt install, sudo apt-get,
# dpkg -i, etc.  We block these because the sandbox image is
# pre-built with all necessary system libraries; running apt at
# runtime is slow, unreliable (network restrictions), and unnecessary.

# TODO：可能需要引入Safe Guard Layer来禁止一些指令执行。
_APT_PATTERN = re.compile(
  r"(?:^|&&|;|\|)\s*(?:sudo\s+)?"
  r"(?:apt-get|apt|dpkg)\s+(?:install|update|upgrade|add|remove)",
  re.IGNORECASE,
)

_APT_BLOCKED_MSG = (
  "[BLOCKED] System package commands (apt-get, apt, dpkg) are disabled in this sandbox.\n"
  "\n"
  "The sandbox already has common system libraries pre-installed:\n"
  "  - Python 3.11, Node.js 20, TypeScript, gcc/g++/make/cmake\n"
  "  - libpq (PostgreSQL), libmysqlclient (MySQL/MariaDB)\n"
  "  - git, curl, wget, jq, tree, zip/unzip\n"
  "\n"
  "For language packages use:\n"
  "  - Python:  pip install <package>\n"
  "  - Node.js: npm install <package>\n"
  "\n"
  "For databases/services, write a docker-compose.yml in the workspace and use \n"
  "the create_compose_stack tool to launch infrastructure services.\n"
)

BASH_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "bash",
    "description": (
      "Execute a shell command in the sandbox workspace. "
      "Use this to run code, install language packages (pip install / npm install), "
      "inspect files, run tests, etc. The working directory is the project workspace. "
      "NOTE: System package commands (apt-get, apt, dpkg) are blocked — all system "
      "libraries are pre-installed. Use 'pip install' for Python packages or "
      "'npm install' for Node.js packages."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "command": {
          "type": "string",
          "description": "The shell command to execute.",
        },
        "timeout": {
          "type": "integer",
          "description": f"Timeout in seconds (default {DEFAULT_TIMEOUT}).",
        },
      },
      "required": ["command"],
    },
  },
}


async def bash_execute(
  command: str,
  timeout: int = DEFAULT_TIMEOUT,
  **_kwargs: Any,
) -> str:
  """Run *command* via ``/bin/bash -c`` and return combined output."""
  if _APT_PATTERN.search(command):
    logger.warning("bash: BLOCKED apt command: %r", command)
    return _APT_BLOCKED_MSG

  workspace = os.environ.get("WORKSPACE_DIR", "/app/workspace")
  logger.info("bash: executing %r (timeout=%ds)", command, timeout)

  try:
    proc = await asyncio.create_subprocess_shell(
      command,
      stdout=asyncio.subprocess.PIPE,
      stderr=asyncio.subprocess.PIPE,
      cwd=workspace,
      env={**os.environ},
    )
    stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=timeout)
  except asyncio.TimeoutError:
    proc.kill()
    return f"[ERROR] Command timed out after {timeout}s"
  except Exception as exc:
    return f"[ERROR] {exc}"

  out = stdout.decode(errors="replace")
  err = stderr.decode(errors="replace")

  parts = []
  if out:
    parts.append(out)
  if err:
    parts.append(f"[STDERR]\n{err}")

  result = "\n".join(parts) if parts else "(no output)"

  if len(result) > MAX_OUTPUT_CHARS:
    result = result[:MAX_OUTPUT_CHARS] + f"\n... [truncated, {len(result)} chars total]"

  exit_code = proc.returncode
  if exit_code != 0:
    result += f"\n[exit code: {exit_code}]"

  return result