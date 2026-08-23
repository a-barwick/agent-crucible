"""An unmodified LangGraph prebuilt create_react_agent.

Tools are ordinary @tool functions that call the ticket HTTP API.
The planner is a scripted chat model so the runner stays deterministic.
This file never imports Callback and never calls retry_tool.
"""

from __future__ import annotations

import json
import threading

from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from langchain_core.tools import tool
from langgraph.prebuilt import create_react_agent

try:
    from langgraph.checkpoint.memory import InMemorySaver
except ImportError:  # pragma: no cover
    from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

from ticket_logic import action_status, http_json, last_company, memory_id, note, parse_objective

# Per-run, not per-process. The sidecar can have two runs in flight, and the
# scripted planner reads this on every turn: as a module global, one trial's
# objective, stale memory and ballast leaked into the other's plan.
_ctx = threading.local()


def _context() -> dict:
    return getattr(_ctx, "value", None) or {}


@tool
def search_ticket(query: str) -> dict:
    """Search the ticket HTTP API by company or free text."""
    return http_json("GET", "http://tickets.example/search", params={"q": query})


@tool
def update_ticket(id: str, status: str) -> dict:
    """Patch a ticket's status on the ticket HTTP API."""
    return http_json("POST", f"http://tickets.example/tickets/{id}", body={"status": status})


TOOLS = [search_ticket, update_ticket]


class ScriptedTicketModel(BaseChatModel):
    """Deterministic stand-in for a tool-calling chat model."""

    @property
    def _llm_type(self) -> str:
        return "scripted-ticket"

    def bind_tools(self, tools, **kwargs):
        return self

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        n = sum(1 for m in messages if isinstance(m, ToolMessage) or getattr(m, "type", "") == "tool")
        objective = _context().get("objective") or ""
        companies = _context().get("companies") or ["Acme Corp", "Globex"]
        intent = parse_objective(objective, companies)
        if n == 0:
            query = intent.get("entity") or intent.get("company") or ""
            hijack = last_company(_context().get("junk") or "", companies)
            if hijack and hijack != query:
                query = hijack
                note("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
            msg = AIMessage(
                content="",
                tool_calls=[{"name": "search_ticket", "args": {"query": query}, "id": "call-search"}],
            )
        elif n == 1:
            last = next((m for m in reversed(messages) if isinstance(m, ToolMessage) or getattr(m, "type", "") == "tool"), None)
            data = {}
            content = getattr(last, "content", "") if last is not None else ""
            if isinstance(content, str) and content.startswith("{"):
                try:
                    data = json.loads(content)
                except json.JSONDecodeError:
                    data = {}
            tid = data.get("id") or ""
            mid = memory_id(_context().get("memory"))
            if mid:
                tid = mid
                note("enrich trusted stale memory", {"record_id": mid, "tool": "search_ticket"})
            status = action_status(intent)
            msg = AIMessage(
                content="",
                tool_calls=[{"name": "update_ticket", "args": {"id": tid, "status": status}, "id": "call-update"}],
            )
        else:
            msg = AIMessage(content="updated the ticket")
        return ChatResult(generations=[ChatGeneration(message=msg)])


def build(req: dict | None = None):
    req = req or {}
    return create_react_agent(ScriptedTicketModel(), TOOLS, checkpointer=InMemorySaver(), name="ticket-react")


def run(req: dict) -> dict:
    ctx = {
        "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or ["Acme Corp", "Globex"],
    }
    _ctx.value = ctx
    graph = create_react_agent(ScriptedTicketModel(), TOOLS, checkpointer=InMemorySaver(), name="ticket-react")
    result = graph.invoke(
        {"messages": [("user", ctx["objective"])]},
        {"configurable": {"thread_id": req.get("thread_id") or "react"}},
    )
    return finish(result, req)


def finish(result: dict, req: dict) -> dict:
    wrote = False
    rid = ""
    status = ""
    error = ""
    steps = 0
    for m in result.get("messages") or []:
        steps += 1
        content = getattr(m, "content", None)
        tool_calls = getattr(m, "tool_calls", None) or []
        name = getattr(m, "name", None) or ""
        for call in tool_calls:
            fn = call.get("name") or ""
            args = call.get("args") or {}
            if "update" in fn or "write" in fn:
                wrote = True
                if isinstance(args, dict):
                    status = args.get("status") or status
                    rid = args.get("id") or rid
        if "update" in str(name):
            wrote = True
        if isinstance(content, str) and content.startswith("{"):
            try:
                data = json.loads(content)
            except json.JSONDecodeError:
                data = {}
            if isinstance(data, dict):
                rid = data.get("id") or rid
                status = data.get("status") or status
                if data.get("error") in ("timeout", "cost_ceiling", "unavailable"):
                    error = data["error"]
    intent = parse_objective(_context().get("objective") or "", _context().get("companies"))
    return {
        "terminal": "abort" if error and not wrote else "end",
        "intent": intent,
        "claimed": {
            "wrote": wrote,
            "notified": False,
            "deal_id": rid,
            "record_id": rid,
            "status": status,
            "error": error,
        },
        "steps": max(steps, 1),
        "checkpoint": True,
        "runtime": req.get("runtime") or "langgraph",
        "entry": "examples/native_react.py",
        "react": True,
    }
