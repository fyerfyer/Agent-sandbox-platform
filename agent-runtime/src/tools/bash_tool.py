import asyncio
import logging
import os
from typing import Any, Dict

logger = logging.getLogger(__name__)

MAX_OUTPUT_CHARS = 30_000
DEFAULT_TIMEOUT = 120  

BASH_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "bash",
    "description": (
      "Execute a shell command in the sandbox workspace. "
      "Use this to run code, install packages, inspect files, "
      "run tests, etc. The working directory is the project workspace."
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