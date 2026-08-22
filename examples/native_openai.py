"""An unmodified OpenAI-tools agent. No chamber callback.

Tools are a chat.completions function schema plus a dispatch table of
plain callables. The chamber wraps DISPATCH after import. A scripted
planner emits tool_calls so the runner stays deterministic.
"""

from __future__ import annotations

import json

from ticket_logic import action_status, as_data, last_company, memory_id, note, parse_objective, transport


def _http_get(url: str, params: dict) -> dict:
    raise RuntimeError(f"live call to {url} was not intercepted: {params}")


def _http_post(url: str, body: dict) -> dict:
    raise RuntimeError(f"live call to {url} was not intercepted: {body}")


def search_ticket(query: str) -> dict:
    """Search the ticket HTTP API by company or free text."""
    return _http_get("https://tickets.example/search", {"q": query})


def update_ticket(id: str, status: str) -> dict:
    """Patch a ticket's status on the ticket HTTP API."""
    return _http_post(f"https://tickets.example/tickets/{id}", {"status": status})


TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "search_ticket",
            "description": "Search tickets by company or query.",
            "parameters": {
                "type": "object",
                "properties": {"query": {"type": "string"}},
                "required": ["query"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "update_ticket",
            "description": "Set a ticket status.",
            "parameters": {
                "type": "object",
                "properties": {
                    "id": {"type": "string"},
                    "status": {"type": "string"},
                },
                "required": ["id", "status"],
            },
        },
    },
]

DISPATCH = {"search_ticket": search_ticket, "update_ticket": update_ticket}


def _call(name: str, args: dict) -> dict:
    fn = DISPATCH.get(name)
    if fn is None:
        return {"ok": False, "error": "unknown_tool"}
    return fn(**args)


def _plan_tool_calls(state: dict) -> list[dict]:
    """Scripted stand-in for chat.completions tool_calls."""
    intent = state.get("intent") or {}
    if not state.get("ticket_id") and not state.get("searched"):
        query = intent.get("entity") or intent.get("company") or ""
        hijack = last_company(state.get("junk") or "", state.get("companies"))
        if hijack and hijack != query:
            query = hijack
            note("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
        return [{"id": "call-search", "type": "function", "function": {"name": "search_ticket", "arguments": json.dumps({"query": query})}}]
    status = action_status(intent)
    tid = state.get("ticket_id") or state.get("record_id") or state.get("deal_id") or ""
    return [{"id": "call-update", "type": "function", "function": {"name": "update_ticket", "arguments": json.dumps({"id": tid, "status": status})}}]


def run(req: dict) -> dict:
    state = {
        "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or ["Acme Corp", "Globex"],
        "steps": 0,
        "messages": [{"role": "user", "content": req.get("objective") or ""}],
    }
    state["intent"] = parse_objective(state["objective"], state["companies"])
    state["steps"] += 1

    for _ in range(4):
        if state.get("terminal") in ("end", "abort"):
            break
        calls = _plan_tool_calls(state)
        state["messages"].append({"role": "assistant", "content": None, "tool_calls": calls})
        for call in calls:
            fn = (call.get("function") or {})
            name = fn.get("name") or ""
            try:
                args = json.loads(fn.get("arguments") or "{}")
            except json.JSONDecodeError:
                args = {}
            res = _call(name, args)
            state["steps"] += 1
            state["messages"].append({"role": "tool", "tool_call_id": call.get("id"), "content": json.dumps(res)})
            if transport(res):
                state["last_error"] = res.get("error") or "timeout"
                state["terminal"] = "abort"
                break
            d = as_data(res)
            if name == "search_ticket":
                state["searched"] = True
                state["ticket_id"] = d.get("id") or ""
                state["deal_id"] = state["ticket_id"]
                state["record_id"] = state["ticket_id"]
                state["status"] = d.get("status") or ""
                mid = memory_id(state.get("memory"))
                if mid:
                    state["ticket_id"] = mid
                    state["deal_id"] = mid
                    state["record_id"] = mid
                    if (state.get("memory") or {}).get("deal_status"):
                        state["status"] = state["memory"]["deal_status"]
                    note("enrich trusted stale memory", {"deal_id": mid, "record_id": mid, "tool": "search_ticket"})
            elif name == "update_ticket":
                state["wrote"] = True
                state["status"] = args.get("status") or d.get("status") or state.get("status") or ""
                if d.get("id"):
                    state["ticket_id"] = d["id"]
                    state["deal_id"] = d["id"]
                    state["record_id"] = d["id"]
                state["terminal"] = "end"

    rid = state.get("record_id") or state.get("ticket_id") or state.get("deal_id") or ""
    return {
        "terminal": state.get("terminal") or "end",
        "intent": state.get("intent") or {},
        "claimed": {
            "wrote": bool(state.get("wrote")),
            "notified": False,
            "deal_id": rid,
            "record_id": rid,
            "status": state.get("status") or "",
            "error": state.get("last_error") or "",
        },
        "steps": int(state.get("steps") or 0),
        "checkpoint": True,
        "runtime": req.get("runtime") or "openai",
        "entry": "examples/native_openai.py",
        "intercepted": True,
        "openai": {"tools": [t["function"]["name"] for t in TOOLS], "format": "chat.completions"},
    }
