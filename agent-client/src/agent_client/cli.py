from __future__ import annotations

import json
import threading
import time
from typing import Any

import typer
from rich.console import Console
from rich.panel import Panel

from .api import PlatformApiClient
from .config import load_client_config
from .platform import bootstrap_platform, ensure_runtime_image


app = typer.Typer(no_args_is_help=True, add_completion=False)
console = Console()


def _render_event(event: dict[str, Any], ctx: dict[str, Any] | None = None) -> bool:
  """Render a single SSE event.  Return True when the turn is done."""
  evt_type = event.get("type", "")
  payload = event.get("payload", "")

  # ── streaming text chunk ─────────────────────────────────────
  if evt_type == "agent.text_chunk":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    # Print text inline without a newline so chunks concatenate.
    console.print(text, end="", highlight=False)
    if ctx is not None:
      ctx["streaming"] = True
    return False

  # If we were streaming and a non-chunk event arrives, finish the line.
  if ctx is not None and ctx.get("streaming"):
    console.print()  # newline
    ctx["streaming"] = False

  if evt_type == "agent.thought":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    console.print(f"[dim]💭 {text}[/dim]")
    return False

  if evt_type == "agent.tool_call":
    if isinstance(payload, dict):
      tool_name = payload.get("tool_name", payload.get("toolName", "tool"))
      arguments = payload.get("arguments", payload.get("text", ""))
    else:
      tool_name = "tool"
      arguments = str(payload)
    console.print(f"[yellow]🔧 Tool:[/yellow] {tool_name}")
    if arguments:
      text = str(arguments)
      if len(text) > 500:
        text = text[:500] + "…"
      console.print(f"[dim]{text}[/dim]")
    return False

  if evt_type == "agent.tool_result":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    if len(text) > 300:
      text = text[:300] + "…"
    console.print(f"[dim]📋 {text}[/dim]")
    return False

  if evt_type == "agent.answer":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    console.print(Panel.fit(text, title="✅ Agent Answer", border_style="green"))
    return True

  if evt_type in ("agent.error", "session.error"):
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    console.print(f"[red]❌ {text}[/red]")
    return True

  if evt_type == "agent.status":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    console.print(f"[dim]📡 {text}[/dim]")
    return False

  return False


def _chat_once(api: PlatformApiClient, session_id: str, message: str) -> None:
  done = threading.Event()

  def _stream() -> None:
    ctx: dict[str, Any] = {"streaming": False}
    try:
      for event in api.stream_events(session_id):
        if _render_event(event, ctx):
          done.set()
          break
    except Exception as exc:
      if ctx.get("streaming"):
        console.print()          # finish the line
      console.print(f"[yellow]SSE stream ended: {exc}[/yellow]")
      done.set()

  stream_thread = threading.Thread(target=_stream, daemon=True)
  stream_thread.start()
  time.sleep(0.25)

  api.send_message(session_id, message)
  stream_thread.join(timeout=300)
  done.set()


@app.command("run")
def run(
  config: str | None = typer.Option(None, "--config", "-c", help="Path to yaml config file"),
  env_file: str | None = typer.Option(None, "--env-file", help="Path to .env file"),
) -> None:
  """Run interactive client using current directory yaml + .env."""
  platform_proc = None
  api = None
  session_id = ""

  try:
    cfg, cfg_path, env_path = load_client_config(config_file=config, env_file=env_file)
    console.print(f"[green]Loaded config:[/green] {cfg_path}")
    if env_path:
      console.print(f"[green]Loaded env:[/green] {env_path}")

    if "DEEPSEEK_API_KEY" not in cfg.session.env_vars:
      api_key = __import__("os").environ.get("DEEPSEEK_API_KEY", "")
      if api_key:
        cfg.session.env_vars["DEEPSEEK_API_KEY"] = api_key

    if not cfg.session.env_vars.get("DEEPSEEK_API_KEY"):
      raise RuntimeError(
        "DEEPSEEK_API_KEY is missing. Provide it in .env or session.env_vars in YAML."
      )

    ensure_runtime_image(cfg)
    platform_proc = bootstrap_platform(cfg)

    api = PlatformApiClient(cfg.platform.api_base)
    health = api.health()
    if health.get("status") != "ok":
      raise RuntimeError(f"Platform unhealthy: {health}")

    env_vars = [f"{k}={v}" for k, v in cfg.session.env_vars.items()]
    session = api.create_session(
      project_id=cfg.session.project_id,
      user_id=cfg.session.user_id,
      strategy=cfg.session.strategy,
      image=cfg.runtime.image,
      env_vars=env_vars,
    )
    session_id = session.id
    console.print(f"[cyan]Session created:[/cyan] {session_id}")

    ready = api.wait_ready(session_id)
    container_id = (ready.get("container_id") or "")[:12]
    console.print(f"[green]Session ready[/green] container={container_id}")

    configured = api.configure(
      session_id=session_id,
      system_prompt=cfg.agent.system_prompt,
      builtin_tools=cfg.agent.builtin_tools,
      agent_config=cfg.agent.agent_config,
    )
    console.print(f"[green]Agent configured[/green] tools={configured.get('available_tools', [])}")

    console.print(
      Panel.fit(
        "Commands: /files  /read <path>  /sync  /status  /quit",
        title="Agent Platform",
        border_style="cyan",
      )
    )

    while True:
      user_input = typer.prompt("You").strip()
      if not user_input:
        continue

      if user_input.lower() in {"/quit", "/exit", "/q"}:
        break

      if user_input.lower() == "/files":
        data = api.list_files(session_id)
        console.print(data.get("output", "(empty)"))
        continue

      if user_input.lower().startswith("/read "):
        file_path = user_input[6:].strip()
        data = api.read_file(session_id, file_path)
        console.print(Panel.fit(data.get("content", ""), title=file_path, border_style="white"))
        continue

      if user_input.lower() == "/sync":
        data = api.sync_files(session_id)
        console.print(f"[green]{data.get('message', 'synced')}[/green]")
        continue

      if user_input.lower() == "/status":
        data = api.session_status(session_id)
        console.print_json(json.dumps(data))
        continue

      _chat_once(api, session_id, user_input)

  except Exception as exc:
    console.print(f"[red]Error:[/red] {exc}")
    raise typer.Exit(code=1)
  finally:
    if api and session_id:
      try:
        api.terminate_session(session_id)
        console.print("[green]Session terminated[/green]")
      except Exception:
        pass
    if api:
      api.close()
    if platform_proc:
      platform_proc.stop()


@app.command("version")
def version() -> None:
  console.print("agent-client 0.1.0")


if __name__ == "__main__":
  app()
