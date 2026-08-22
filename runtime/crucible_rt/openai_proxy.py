"""OpenAI-compatible chat.completions for a foreign tool-using process.

A process that does not speak POST /v1/run can still point
OPENAI_BASE_URL at the sidecar. The runner stays deterministic: this
module emits scripted tool_calls from the conversation, it does not
call a model. Tool execution stays in the agent (and hits httpio).
"""

from __future__ import annotations

import hashlib
import json


def complete(body: dict) -> dict:
    messages = list(body.get("messages") or [])
    n_tools = 0
    last_tool = ""
    last_content = ""
    for m in messages:
        if (m.get("role") or "") == "tool":
            n_tools += 1
            last_tool = m.get("name") or last_tool
            last_content = m.get("content") or last_content
        if m.get("tool_calls"):
            pass
    if n_tools == 0:
        query = _last_user(messages)
        calls = [_call("search_ticket", {"query": _entity(query)})]
        return _msg(body, calls=calls)
    if n_tools == 1:
        data = {}
        try:
            data = json.loads(last_content or "{}")
        except json.JSONDecodeError:
            data = {}
        tid = data.get("id") or ""
        calls = [_call("update_ticket", {"id": tid, "status": "Resolved"})]
        return _msg(body, calls=calls)
    return _msg(body, content="updated the ticket")


def _last_user(messages: list) -> str:
    for m in reversed(messages):
        if m.get("role") == "user":
            return m.get("content") or ""
    return ""


def _entity(text: str) -> str:
    low = (text or "").lower()
    for name in ("Acme Corp", "Globex", "Acme Supplies"):
        if name.lower() in low:
            return name
    return "Acme Corp"


def _digest(payload) -> str:
    """A stable id for a request. uuid4 and time.time() made two replays of the
    same trial differ in the ids and the timestamp they handed the agent, which
    is exactly the wall-clock dependence the rest of the harness refuses: an
    agent that echoes a call id, logs it, or keys anything off it produced a
    different trace each run."""
    raw = json.dumps(payload, sort_keys=True, default=str)
    return hashlib.sha256(raw.encode()).hexdigest()


def _call(name: str, args: dict) -> dict:
    return {
        "id": "call-" + _digest({"tool": name, "args": args})[:8],
        "type": "function",
        "function": {"name": name, "arguments": json.dumps(args)},
    }


def _msg(body: dict, content: str = "", calls: list | None = None) -> dict:
    msg = {"role": "assistant", "content": content or None}
    if calls:
        msg["tool_calls"] = calls
    turn = {
        "messages": body.get("messages") or [],
        "tools": body.get("tools") or [],
        "model": body.get("model") or "",
        "content": content,
        "calls": calls or [],
    }
    return {
        "id": "chatcmpl-" + _digest(turn)[:10],
        "object": "chat.completion",
        # The chamber's clock is a tick counter that starts at zero; there is no
        # wall time in a trial, and inventing one here only made replays differ.
        "created": 0,
        "model": body.get("model") or "scripted",
        "choices": [{"index": 0, "message": msg, "finish_reason": "tool_calls" if calls else "stop"}],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
    }
