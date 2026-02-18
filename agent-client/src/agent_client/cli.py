from __future__ import annotations

import json
import os
import threading
import time
from datetime import datetime
from typing import Any, Optional

import typer
from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.prompt import Prompt, Confirm

from .api import PlatformApiClient
from .config import load_client_config, ClientConfig
from .history import SessionHistory, SessionRecord, ChatLog
from .platform import bootstrap_platform, ensure_runtime_image, PlatformProcess


app = typer.Typer(no_args_is_help=True, add_completion=False)
console = Console()

def _render_event(event: dict[str, Any], ctx: dict[str, Any] | None = None) -> bool:
  evt_type = event.get("type", "")
  payload = event.get("payload", "")

  if evt_type == "agent.text_chunk":
    text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
    console.print(text, end="", highlight=False)
    if ctx is not None:
      ctx["streaming"] = True
    return False

  if ctx is not None and ctx.get("streaming"):
    console.print()
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


def _chat_once(
  api: PlatformApiClient,
  session_id: str,
  message: str,
  timeout: int = 300,
  chat_log: Optional[ChatLog] = None,
) -> None:
  """发送单条消息并流式接收 Agent 响应，同时保存聊天记录。"""
  done = threading.Event()
  agent_response: dict[str, str] = {"text": ""}

  def _stream() -> None:
    ctx: dict[str, Any] = {"streaming": False}
    try:
      for event in api.stream_events(session_id):
        # 收集 agent 文本用于历史记录
        evt_type = event.get("type", "")
        payload = event.get("payload", "")
        if evt_type == "agent.text_chunk":
          text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
          agent_response["text"] += text
        elif evt_type == "agent.answer":
          text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
          agent_response["text"] = text

        if _render_event(event, ctx):
          done.set()
          break
    except Exception as exc:
      if ctx.get("streaming"):
        console.print()
      console.print(f"[yellow]SSE stream ended: {exc}[/yellow]")
      done.set()

  # 保存用户消息
  if chat_log:
    chat_log.append(session_id, "user", message)

  stream_thread = threading.Thread(target=_stream, daemon=True)
  stream_thread.start()
  time.sleep(0.25)

  api.send_message(session_id, message)
  stream_thread.join(timeout=timeout)
  done.set()

  # 保存 agent 响应
  if chat_log and agent_response["text"]:
    chat_log.append(session_id, "assistant", agent_response["text"], msg_type="answer")


def _format_timestamp(ts: float) -> str:
  try:
    return datetime.fromtimestamp(ts).strftime("%Y-%m-%d %H:%M:%S")
  except Exception:
    return "unknown"


def _format_duration(start: float, end: Optional[float] = None) -> str:
  elapsed = (end or time.time()) - start
  if elapsed < 60:
    return f"{elapsed:.0f}s"
  if elapsed < 3600:
    return f"{elapsed / 60:.0f}m"
  return f"{elapsed / 3600:.1f}h"

def _display_sessions_table(
  records: list[SessionRecord],
  title: str = "Sessions",
) -> None:
  if not records:
    console.print("[dim]No sessions found.[/dim]")
    return

  table = Table(
    title=f"[bold cyan]{title}[/bold cyan]",
    show_header=True,
    header_style="bold magenta",
    border_style="bright_blue",
    title_style="bold cyan",
    show_lines=True,
    padding=(0, 1),
  )
  
  table.add_column("#", justify="right", style="dim", width=4)
  table.add_column("ID", style="bright_cyan", width=13)
  table.add_column("Status", justify="center", width=12)
  table.add_column("Messages", justify="right", width=10)
  table.add_column("Created", style="dim", width=17)
  table.add_column("Preview", style="white", max_width=50)

  for i, r in enumerate(records, 1):
    if r.status == "active":
      status_display = "[green]● Active[/green]"
    elif r.status == "stopped":
      status_display = "[yellow]⏸ Stopped[/yellow]"
    elif r.status == "ended":
      status_display = "[dim]✕ Ended[/dim]"
    else:
      status_display = f"[dim]{r.status}[/dim]"

    preview = r.summary or "[dim italic]No messages yet[/dim italic]"
    if len(preview) > 50:
      preview = preview[:47] + "..."
    
    msg_display = f"💬 {r.message_count}"
    
    time_display = _format_relative_time(r.created_at)

    table.add_row(
      f"[bold]{i}[/bold]",
      f"[cyan]{r.session_id[:12]}[/cyan]",
      status_display,
      msg_display,
      time_display,
      preview,
    )

  console.print()
  console.print(table)
  console.print()


def _format_relative_time(timestamp: float) -> str:
  import time
  now = time.time()
  diff = now - timestamp
  
  if diff < 60:
    return "[green]just now[/green]"
  elif diff < 3600:
    mins = int(diff / 60)
    return f"[green]{mins}m ago[/green]"
  elif diff < 86400:
    hours = int(diff / 3600)
    return f"[yellow]{hours}h ago[/yellow]"
  elif diff < 604800:
    days = int(diff / 86400)
    return f"[yellow]{days}d ago[/yellow]"
  else:
    from datetime import datetime
    dt = datetime.fromtimestamp(timestamp)
    return f"[dim]{dt.strftime('%b %d')}[/dim]"


def _pick_session(
  records: list[SessionRecord],
  title: str = "Select a session",
) -> Optional[SessionRecord]:
  if not records:
    console.print("\n[yellow]⚠[/yellow]  [dim]No sessions available.[/dim]\n")
    return None

  _display_sessions_table(records, title)

  choice = Prompt.ask(
    "[bold cyan]→[/bold cyan] Enter number or ID prefix [dim](empty to cancel)[/dim]",
    default="",
  )
  if not choice:
    console.print("[dim]Cancelled.[/dim]")
    return None

  # 按序号选择
  try:
    idx = int(choice)
    if 1 <= idx <= len(records):
      selected = records[idx - 1]
      console.print(f"[green]✓[/green] Selected: [cyan]{selected.session_id[:12]}[/cyan]")
      return selected
  except ValueError:
    pass

  # 按 ID 前缀匹配
  matches = [r for r in records if r.session_id.startswith(choice)]
  if len(matches) == 1:
    selected = matches[0]
    console.print(f"[green]✓[/green] Selected: [cyan]{selected.session_id[:12]}[/cyan]")
    return selected
  if len(matches) > 1:
    console.print(f"[yellow]⚠[/yellow]  Ambiguous prefix '{choice}', matched {len(matches)} sessions.")
    return None

  console.print(f"[red]✗[/red]  No session matching '{choice}'.")
  return None

def _display_chat_history(chat_log: ChatLog, session_id: str) -> None:
  messages = chat_log.load(session_id)
  if not messages:
    console.print("[dim]No chat history for this session.[/dim]")
    return

  console.print()
  console.print(f"[bold cyan]─── Chat History ({len(messages)} messages) ───[/bold cyan]")
  console.print()

  for msg in messages:
    if msg.role == "user":
      console.print(f"[bold green]You:[/bold green] {msg.content}")
    elif msg.role == "assistant":
      # 如果内容较长，用 Panel 展示
      if len(msg.content) > 200:
        console.print(Panel.fit(
          msg.content,
          title="Agent",
          border_style="blue",
        ))
      else:
        console.print(f"[bold blue]Agent:[/bold blue] {msg.content}")
    console.print()

  console.print(f"[bold cyan]─── End of History ───[/bold cyan]")
  console.print()


def _view_only_repl(
  cfg: ClientConfig,
  chat_log: ChatLog,
  record: SessionRecord,
) -> None:
  # View-only 模式：已 quit 的 session 只能查看聊天记录，不能发送消息
  # TODO：这个的实现有些 Bug，quit了之后聊天记录貌似找不到了，之后修改
  console.print(
    Panel.fit(
      f"[dim]Session [cyan]{record.session_id[:12]}[/cyan] was terminated. "
      f"This is a [yellow]view-only[/yellow] mode.[/dim]\n"
      "You can browse the chat history but cannot send new messages.\n"
      "Commands: [green]/history[/green]  [green]/clear[/green]  [green]/quit[/green]",
      title="📖 View-Only Mode",
      border_style="yellow",
    )
  )

  # 自动展示聊天记录
  _display_chat_history(chat_log, record.session_id)

  while True:
    try:
      user_input = typer.prompt(f"{cfg.cli.prompt_prefix} (view-only)").strip()
    except (KeyboardInterrupt, EOFError):
      console.print()
      break

    if not user_input:
      continue

    if user_input.startswith("/"):
      cmd = user_input.lower().split()[0]
      if cmd in ("/quit", "/exit", "/q"):
        break
      elif cmd == "/clear":
        console.clear()
        continue
      elif cmd == "/history":
        _display_chat_history(chat_log, record.session_id)
        continue
      elif cmd == "/help":
        console.print(
          "[dim]View-only mode. Available commands: "
          "[green]/history[/green]  [green]/clear[/green]  [green]/quit[/green][/dim]"
        )
        continue
      else:
        console.print(
          f"[yellow]Unknown command: {cmd}[/yellow] "
          f"Type [green]/help[/green] for available commands."
        )
        continue

    console.print(
      "[yellow]⚠ View-only mode — cannot send messages to a terminated session.[/yellow]"
    )

  console.print("[dim]Exiting view-only mode.[/dim]")


HELP_TEXT = """
[bold cyan]Chat Commands[/bold cyan]
  Just type your message and press Enter to chat with the agent.

[bold cyan]Session Management[/bold cyan]
  [green]/sessions[/green]         Interactive session picker — view and switch sessions
  [green]/switch[/green] [dim]<id>[/dim]     Switch to a different active/stopped session
  [green]/resume[/green] [dim][id][/dim]     Resume a stopped session (reconnect to its container)
  [green]/stop[/green]             Detach from current session (keeps container running)
  [green]/quit[/green]             Terminate session, destroy container, and exit
  [green]/status[/green]           Show current session status

[bold cyan]File Operations[/bold cyan]
  [green]/files[/green]            List files in the sandbox workspace
  [green]/read[/green] [dim]<path>[/dim]     Read a file from the sandbox
  [green]/sync[/green]             Copy sandbox files to your local machine

[bold cyan]History & Info[/bold cyan]
  [green]/history[/green]          Show recent session history
  [green]/clear[/green]            Clear the terminal screen
  [green]/help[/green]             Show this help message
  [green]/version[/green]          Show client version

[bold cyan]Aliases[/bold cyan]
  [green]/exit[/green] [green]/q[/green]        Same as /quit
"""



class SessionContext:
  """
  管理当前 CLI 运行的会话上下文。

  - 创建 / resume / switch session
  - 处理 CLI 命令路由
  - 管理退出清理策略（terminate vs. stop）
  """

  def __init__(
    self,
    cfg: ClientConfig,
    api: PlatformApiClient,
    history: SessionHistory,
    platform_proc: Optional[PlatformProcess] = None,
  ):
    self.cfg = cfg
    self.api = api
    self.history = history
    self.platform_proc = platform_proc
    self.session_id: str = ""
    self.container_id: str = ""
    self.chat_log: ChatLog = ChatLog()
    self._should_exit: bool = False
    self._terminate_on_exit: bool = True  # /quit: True, /stop: False

  def create_new_session(self) -> str:
    """创建新 session 并等待 ready。"""
    env_vars = [f"{k}={v}" for k, v in self.cfg.session.env_vars.items()]
    session = self.api.create_session(
      project_id=self.cfg.session.project_id,
      user_id=self.cfg.session.user_id,
      strategy=self.cfg.session.strategy,
      image=self.cfg.runtime.image,
      env_vars=env_vars,
    )
    self.session_id = session.id
    console.print(f"[green]✓ Session created[/green] [cyan]{self.session_id[:12]}...[/cyan]")

    ready = self.api.wait_ready(self.session_id)
    self.container_id = (ready.get("container_id") or "")[:12]
    console.print(f"[green]✓ Session ready[/green] container=[cyan]{self.container_id}[/cyan]")

    configured = self.api.configure(
      session_id=self.session_id,
      system_prompt=self.cfg.agent.system_prompt,
      builtin_tools=self.cfg.agent.builtin_tools,
      agent_config=self.cfg.agent.agent_config,
    )
    tools_list = ', '.join(configured.get('available_tools', []))
    console.print(f"[green]✓ Agent configured[/green] tools=[cyan]{tools_list}[/cyan]")

    # 记录历史
    self.history.add(SessionRecord(
      session_id=self.session_id,
      project_id=self.cfg.session.project_id,
      strategy=self.cfg.session.strategy,
      agent_type=self.cfg.session.agent_type,
      container_id=self.container_id,
      image=self.cfg.runtime.image,
    ))

    return self.session_id

  def resume_session(self, record: SessionRecord) -> bool:
    """恢复一个已停止或已结束的 session。

    1. 检查平台侧 session 状态和容器状态
    2. 如果容器是 stopped/exited -> 尝试重启
    3. 如果容器是 running -> 重新连接 + re-configure
    4. 如果容器不存在 -> 提示用户是否重建
    """
    sid = record.session_id
    console.print(f"[cyan]Attempting to resume session:[/cyan] {sid[:12]}…")

    # 查询 session 状态
    try:
      status_data = self.api.session_status(sid)
      platform_status = status_data.get("status", "unknown")
    except Exception:
      platform_status = "unreachable"

    # 检查容器健康和详细状态
    health = self.api.session_health(sid)
    container_healthy = health.get("status") == "healthy"
    container_state = health.get("container_state", "unknown")

    # Case 1: 容器是 stopped/exited 状态 -> 尝试重启
    if container_state in ("exited", "stopped", "created"):
      console.print(f"[yellow]Container is {container_state}. Attempting to restart...[/yellow]")
      try:
        self.api.restart_session(sid)
        console.print("[green]✓ Container restarted[/green]")
        
        # 等待容器和agent完全ready
        console.print("[dim]Waiting for agent to be ready...[/dim]")
        try:
          self.api.wait_ready(sid)
          console.print("[green]✓ Agent is ready[/green]")
        except Exception as wait_err:
          console.print(f"[yellow]⚠ Agent readiness check timed out: {wait_err}[/yellow]")
          console.print("[dim]Will attempt to reconnect anyway...[/dim]")

        # 容器重启后 gRPC 服务可能还在启动，额外等待
        time.sleep(2)

      except Exception as e:
        console.print(f"[red]✗ Failed to restart container: {e}[/red]")
        if not Confirm.ask(
          "[cyan]Would you like to create a new session instead?[/cyan]",
          default=True,
        ):
          return False
        self.history.mark_ended(sid, summary="Restart failed — recreated")
        self.create_new_session()
        return True

    # Case 2: 平台认为session已终止，或容器完全不可用
    elif platform_status == "terminated" or container_state in ("no_container", "unreachable"):
      if record.status == "ended":
        console.print(
          "[yellow]⚠[/yellow]  [dim]This session has been terminated and its container was destroyed.[/dim]"
        )
      else:
        console.print(
          "[yellow]⚠[/yellow]  [dim]The container for this session is no longer available.[/dim]"
        )
        console.print(
          "[dim]   This may happen if the container was cleaned up by Docker "
          "or the platform was restarted.[/dim]"
        )

      # 如果没有聊天记录，作为孤儿记录清理
      if not self.chat_log.has_messages(sid):
        console.print(
          "[dim]No chat history found for this session. Cleaning up orphan record...[/dim]"
        )
        self.history.remove_record(sid)
        self.chat_log.remove(sid)
        console.print("[green]✓ Orphan record removed.[/green]")
        return False

      if not Confirm.ask(
        "[cyan]Would you like to create a new session with the same configuration?[/cyan]",
        default=True,
      ):
        console.print("[dim]Cancelled.[/dim]")
        return False

      self.history.mark_ended(sid, summary="Container no longer available — recreated")
      self.create_new_session()
      return True

    # Case 3: 容器正在运行，但健康检查失败 
    elif not container_healthy and container_state != "running":
      console.print(
        f"[yellow]⚠[/yellow]  [dim]Container state is '{container_state}' and not healthy.[/dim]"
      )
      if not Confirm.ask(
        "[cyan]Would you like to create a new session?[/cyan]",
        default=True,
      ):
        console.print("[dim]Cancelled.[/dim]")
        return False
      self.history.mark_ended(sid, summary="Container unhealthy — recreated")
      self.create_new_session()
      return True

    # Case 4: 容器正常运行 -> 重新连接
    self.session_id = sid
    self.container_id = (status_data.get("container_id") or record.container_id)[:12]

    # re-configure agent
    if record.status in ("stopped", "active"):
      # 使用重试机制，因为容器可能刚重启，gRPC 服务可能还在启动
      max_retries = 5
      retry_delay = 2
      
      for attempt in range(max_retries):
        try:
          configured = self.api.configure(
            session_id=self.session_id,
            system_prompt=self.cfg.agent.system_prompt,
            builtin_tools=self.cfg.agent.builtin_tools,
            agent_config=self.cfg.agent.agent_config,
          )
          console.print(
            f"[green]✓ Agent configured[/green] "
            f"tools=[cyan]{', '.join(configured.get('available_tools', []))}[/cyan]"
          )
          break
        except Exception as e:
          if attempt < max_retries - 1:
            console.print(
              f"[yellow]⚠ Configure attempt {attempt + 1} failed, retrying in {retry_delay}s...[/yellow]"
            )
            time.sleep(retry_delay)
            retry_delay = min(retry_delay * 2, 8)  # Exponential backoff, cap at 8s
          else:
            console.print(
              f"[yellow]⚠ Could not re-configure agent after {max_retries} attempts.[/yellow]\n"
              f"[dim]Error: {e}[/dim]\n"
              f"[dim]You can try /stop and then resume again, or continue without reconfiguration.[/dim]"
            )

    self.history.mark_active(sid)
    console.print(f"[green]✓ Session resumed[/green] container=[cyan]{self.container_id}[/cyan]")

    # 展示之前的聊天记录
    if self.chat_log.has_messages(sid):
      _display_chat_history(self.chat_log, sid)

    return True

  def switch_to_session(self, target_record: SessionRecord) -> bool:
    """切换到另一个 session。当前 session 会被 stop（保留容器）。"""
    # TODO：这个功能还未测试
    if self.session_id == target_record.session_id:
      console.print("[yellow]Already in this session.[/yellow]")
      return False

    # 先 stop 当前 session（保留容器）
    if self.session_id:
      self._stop_current(silent=True)

    return self.resume_session(target_record)

  def _stop_current(self, silent: bool = False) -> None:
    """
    Stop 当前 session（保留容器）。
    API 调用在后台线程中执行，不阻塞 CLI 退出。
    """
    if not self.session_id:
      return

    sid = self.session_id

    def _bg_stop() -> None:
      try:
        self.api.stop_agent(sid)
      except Exception:
        pass

    t = threading.Thread(target=_bg_stop, daemon=True)
    t.start()

    self.history.mark_stopped(self.session_id)
    if not silent:
      console.print(
        f"[green]✓ Session stopped[/green] [cyan]{self.session_id[:12]}...[/cyan]\n"
        f"[dim]Container preserved. Resume with:[/dim] [cyan]agent-client sessions[/cyan]"
      )

    # 给 HTTP 请求一个短暂的发送窗口
    t.join(timeout=0.5)

  def handle_command(self, user_input: str) -> bool:
    parts = user_input.strip().split(maxsplit=1)
    cmd_name = parts[0].lower()
    cmd_arg = parts[1].strip() if len(parts) > 1 else ""

    # ── /quit, /exit, /q ──
    if cmd_name in ("/quit", "/exit", "/q"):
      self._terminate_on_exit = True
      self._should_exit = True
      return True

    # ── /stop ── detach without destroying container
    if cmd_name == "/stop":
      self._stop_current()
      self._terminate_on_exit = False
      self._should_exit = True
      return True

    # ── /help ──
    if cmd_name == "/help":
      console.print(Panel(HELP_TEXT, title="Agent Platform CLI", border_style="cyan"))
      return False

    # ── /clear ──
    if cmd_name == "/clear":
      console.clear()
      return False

    # ── /version ──
    if cmd_name == "/version":
      console.print("agent-client 0.1.0")
      return False

    # ── /status ──
    if cmd_name == "/status":
      try:
        data = self.api.session_status(self.session_id)
        console.print_json(json.dumps(data))
      except Exception as e:
        console.print(f"[red]Failed to get status: {e}[/red]")
      return False

    # ── /files ──
    if cmd_name == "/files":
      try:
        data = self.api.list_files(self.session_id)
        console.print(data.get("output", "(empty)"))
      except Exception as e:
        console.print(f"[red]{e}[/red]")
      return False

    # ── /read <path> ──
    if cmd_name == "/read":
      if not cmd_arg:
        console.print("[yellow]Usage: /read <file_path>[/yellow]")
        return False
      try:
        data = self.api.read_file(self.session_id, cmd_arg)
        console.print(Panel.fit(
          data.get("content", ""), title=cmd_arg, border_style="white",
        ))
      except Exception as e:
        console.print(f"[red]{e}[/red]")
      return False

    # ── /sync ──
    if cmd_name == "/sync":
      try:
        data = self.api.sync_files(self.session_id)
        console.print(f"[green]{data.get('message', 'synced')}[/green]")
      except Exception as e:
        console.print(f"[red]{e}[/red]")
      return False

    # ── /history ──
    if cmd_name == "/history":
      records = self.history.recent(20)
      _display_sessions_table(records, "Recent Sessions")
      return False

    # ── /sessions ── interactive session picker (like Claude Code's /chat)
    if cmd_name == "/sessions":
      records = self.history.resumable_sessions()
      records.sort(key=lambda r: r.created_at, reverse=True)

      if not records:
        console.print("[dim]No active or stopped sessions to switch to.[/dim]")
        return False

      selected = _pick_session(records, "Switch to Session")
      if selected:
        if self.switch_to_session(selected):
          console.print(f"[green]Now in session:[/green] {self.session_id[:12]}…")
      return False

    # ── /switch <id> ──
    if cmd_name == "/switch":
      if not cmd_arg:
        console.print("[yellow]Usage: /switch <session_id_or_prefix>[/yellow]")
        console.print("[dim]Tip: Use /sessions for interactive selection.[/dim]")
        return False

      record = self.history.find(cmd_arg)
      if not record:
        console.print(f"[red]Session not found: {cmd_arg}[/red]")
        return False

      if record.status == "ended":
        console.print(
          f"[yellow]Session {record.session_id[:12]}… is ended.[/yellow] "
          f"Use [green]/resume {cmd_arg}[/green] to attempt recovery."
        )
        return False

      if self.switch_to_session(record):
        console.print(f"[green]Switched to session:[/green] {self.session_id[:12]}…")
      return False

    # ── /resume [id] ──
    if cmd_name == "/resume":
      if cmd_arg:
        record = self.history.find(cmd_arg)
      else:
        # 无参数 → 交互式选择可恢复的 session
        stopped = self.history.stopped_sessions()
        stopped.sort(key=lambda r: r.created_at, reverse=True)
        # 也包含已 ended 的 session（可查看聊天记录）
        ended = [r for r in self.history.recent(10) if r.status == "ended"]
        candidates = stopped + ended
        if not candidates:
          console.print("[dim]No sessions to resume.[/dim]")
          return False
        record = _pick_session(candidates, "Resume Session")

      if not record:
        if cmd_arg:
          console.print(f"[red]Session not found: {cmd_arg}[/red]")
        return False

      if record.status == "active" and record.session_id == self.session_id:
        console.print("[yellow]Already in this session.[/yellow]")
        return False

      # Ended session → view-only 模式（查看聊天记录）
      if record.status == "ended":
        if self.chat_log.has_messages(record.session_id):
          _view_only_repl(self.cfg, self.chat_log, record)
        else:
          console.print(
            f"[dim]Session [cyan]{record.session_id[:12]}[/cyan] has ended "
            f"and has no chat history.[/dim]"
          )
          if Confirm.ask(
            "[cyan]Remove this orphan record?[/cyan]",
            default=True,
          ):
            self.history.remove_record(record.session_id)
            self.chat_log.remove(record.session_id)
            console.print("[green]✓ Orphan record removed.[/green]")
        return False

      # 切换前 stop 当前 session
      if self.session_id and self.session_id != record.session_id:
        self._stop_current(silent=True)

      if self.resume_session(record):
        console.print(f"[green]Resumed session:[/green] {self.session_id[:12]}…")
      return False

    # 未知命令
    console.print(f"[yellow]Unknown command: {cmd_name}[/yellow]  Type /help for available commands.")
    return False

  def cleanup(self) -> None:
    """
    terminate 调用在后台线程执行，CLI 不等待容器销毁完成。
    Go Platform 会在 goroutine 中异步完成清理。
    """
    if not self.session_id:
      return

    if self._terminate_on_exit:
      sid = self.session_id

      def _bg_terminate() -> None:
        try:
          self.api.terminate_session(sid)
        except Exception:
          pass

      t = threading.Thread(target=_bg_terminate, daemon=True)
      t.start()

      console.print("[green]✓ Session terminated[/green] [dim]container will be destroyed[/dim]")
      self.history.mark_ended(self.session_id)

      # 给 HTTP 请求一个短暂的发送窗口
      t.join(timeout=0.5)
    # /stop 时已在 _stop_current 中处理

# Console APP
@app.command("run")
def run(
  config: str | None = typer.Option(None, "--config", "-c", help="Path to yaml config file"),
  env_file: str | None = typer.Option(None, "--env-file", help="Path to .env file"),
  resume: str | None = typer.Option(None, "--resume", "-r", help="Resume a stopped session by ID/prefix"),
) -> None:
  platform_proc = None
  api = None
  ctx = None

  try:
    cfg, cfg_path, env_path = load_client_config(config_file=config, env_file=env_file)
    console.print(f"[green]Loaded config:[/green] {cfg_path}")
    if env_path:
      console.print(f"[green]Loaded env:[/green] {env_path}")

    # API key 注入
    if "DEEPSEEK_API_KEY" not in cfg.session.env_vars:
      api_key = os.environ.get("DEEPSEEK_API_KEY", "")
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

    history = SessionHistory(max_records=cfg.cli.history_max_records)

    ctx = SessionContext(
      cfg=cfg, api=api, history=history, platform_proc=platform_proc,
    )

    # 决定是新建 session 还是 resume 已有 session
    if resume:
      record = history.find(resume)
      if not record:
        console.print(f"[red]Session not found: {resume}[/red]")
        # 也许用户输入的是前缀，展示候选
        candidates = [r for r in history.recent(10) if r.session_id.startswith(resume)]
        if candidates:
          _display_sessions_table(candidates, "Did you mean?")
        raise typer.Exit(code=1)

      # Ended session → view-only 模式
      if record.status == "ended":
        chat_log = ChatLog()
        if chat_log.has_messages(record.session_id):
          _view_only_repl(cfg, chat_log, record)
          raise typer.Exit(code=0)
        else:
          console.print(
            f"[dim]Session [cyan]{record.session_id[:12]}[/cyan] has ended "
            f"and has no chat history.[/dim]"
          )
          if Confirm.ask("[cyan]Remove this orphan record?[/cyan]", default=True):
            history.remove_record(record.session_id)
            chat_log.remove(record.session_id)
            console.print("[green]✓ Orphan record removed.[/green]")
          raise typer.Exit(code=0)

      if not ctx.resume_session(record):
        raise typer.Exit(code=1)
    elif cfg.cli.auto_resume_last:
      # 自动恢复上一个 stopped session
      stopped = history.stopped_sessions()
      if stopped:
        last_stopped = max(stopped, key=lambda r: r.created_at)
        console.print(f"[cyan]Auto-resuming last stopped session:[/cyan] {last_stopped.session_id[:12]}…")
        if not ctx.resume_session(last_stopped):
          ctx.create_new_session()
      else:
        ctx.create_new_session()
    else:
      ctx.create_new_session()

    # ── 欢迎信息 ──
    console.print(
      Panel.fit(
        "Type a message to chat with the agent.\n"
        "Commands: [green]/help[/green]  [green]/sessions[/green]  "
        "[green]/stop[/green]  [green]/resume[/green]  [green]/quit[/green]",
        title="Agent Platform",
        border_style="cyan",
      )
    )

    # ── 主 REPL 循环 ──
    while True:
      try:
        user_input = typer.prompt(cfg.cli.prompt_prefix).strip()
      except (KeyboardInterrupt, EOFError):
        console.print()
        # Ctrl-C / EOF → 提供用户友好的退出选项
        if not ctx.session_id:
          break
        
        console.print("[yellow]Interrupted. What would you like to do?[/yellow]")
        console.print("  [cyan]1.[/cyan] [green]Stop[/green]      - Detach from session (keep container running)")
        console.print("  [cyan]2.[/cyan] [red]Terminate[/red] - Destroy container and exit")
        console.print("  [cyan]3.[/cyan] [dim]Continue[/dim]   - Return to session")
        
        try:
          choice = Prompt.ask("Your choice", choices=["1", "2", "3"], default="1")
        except (KeyboardInterrupt, EOFError):
          # 再次Ctrl-C，使用默认行为
          console.print()
          choice = "1"
        
        if choice == "1":
          # Stop: 保留容器
          ctx._stop_current()
          ctx._terminate_on_exit = False
          console.print(
            f"[green]✓ Session stopped.[/green] Container preserved.\n"
            f"Resume later with: [cyan]agent-ctl resume {ctx.session_id[:12]}[/cyan]"
          )
          break
        elif choice == "2":
          # Terminate: 销毁容器
          ctx._terminate_on_exit = True
          console.print("[red]Terminating session and destroying container...[/red]")
          break
        else:
          # Continue: 继续session
          console.print("[dim]Continuing session...[/dim]")
          continue

      if not user_input:
        continue

      # 命令处理
      if user_input.startswith("/"):
        if ctx.handle_command(user_input):
          break
        continue

      # 普通聊天
      _chat_once(api, ctx.session_id, user_input, timeout=cfg.cli.stream_timeout, chat_log=ctx.chat_log)
      ctx.history.increment_messages(
        ctx.session_id,
        summary=user_input[:100],
      )

  except typer.Exit:
    raise
  except Exception as exc:
    console.print(f"[red]Error:[/red] {exc}")
    raise typer.Exit(code=1)
  finally:
    if ctx:
      ctx.cleanup()
    if api:
      api.close()
    if platform_proc:
      # 异步停止平台进程，不阻塞 CLI 退出
      def _bg_stop_platform() -> None:
        try:
          platform_proc.stop()
        except Exception:
          pass
      t = threading.Thread(target=_bg_stop_platform, daemon=True)
      t.start()
      t.join(timeout=0.5)


@app.command("sessions")
def sessions_cmd(
  all: bool = typer.Option(False, "--all", "-a", help="Show all sessions including ended"),
  config: str | None = typer.Option(None, "--config", "-c", help="Path to yaml config file"),
  env_file: str | None = typer.Option(None, "--env-file", help="Path to .env file"),
  interactive: bool = typer.Option(True, "--interactive/--no-interactive", "-i/-I", help="Enable interactive session picker"),
) -> None:
  """List session history. In interactive mode (default), you can select and resume a session."""
  history = SessionHistory()
  records = history.recent(30)
  if not all:
    records = [r for r in records if r.status != "ended"]
  
  if not interactive:
    _display_sessions_table(records, "Sessions")
    return
  
  # Interactive mode: let user pick and resume
  selected = _pick_session(records, "Select a session to resume (or press Enter to cancel)")
  if selected:
    console.print(f"[cyan]Resuming session:[/cyan] {selected.session_id[:12]}…")
    run(config=config, env_file=env_file, resume=selected.session_id)
  else:
    console.print("[dim]Cancelled.[/dim]")


@app.command("resume")
def resume_cmd(
  session_id: str = typer.Argument(..., help="Session ID or prefix to resume"),
  config: str | None = typer.Option(None, "--config", "-c", help="Path to yaml config file"),
  env_file: str | None = typer.Option(None, "--env-file", help="Path to .env file"),
) -> None:
  """Resume a stopped session. Shortcut for: run --resume <id>"""
  run(config=config, env_file=env_file, resume=session_id)


@app.command("version")
def version() -> None:
  """Show client version."""
  console.print("agent-client 0.1.0")


if __name__ == "__main__":
  app()