"""An unmodified LangGraph agent. No chamber callback.

Tools are ordinary @tool functions that would call the ticket HTTP API.
The chamber intercepts those tool objects after import. This file never
imports Callback and never calls retry_tool.
"""

from __future__ import annotations

from typing import Any, TypedDict

from langchain_core.tools import tool
from langgraph.graph import END, START, StateGraph

try:
    from langgraph.checkpoint.memory import InMemorySaver
except ImportError:  # pragma: no cover
    from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

from ticket_logic import action_status, as_data, last_company, memory_id, note, parse_objective, transport


def _http_get(url: str, params: dict) -> dict:
    raise RuntimeError(f"live call to {url} was not intercepted: {params}")


def _http_post(url: str, body: dict) -> dict:
    raise RuntimeError(f"live call to {url} was not intercepted: {body}")


@tool
def search_ticket(query: str) -> dict:
    """Search the ticket HTTP API by company or free text."""
    return _http_get("https://tickets.example/search", {"q": query})


@tool
def update_ticket(id: str, status: str) -> dict:
    """Patch a ticket's status on the ticket HTTP API."""
    return _http_post(f"https://tickets.example/tickets/{id}", {"status": status})


TOOLS = [search_ticket, update_ticket]


class TicketState(TypedDict, total=False):
    objective: str
    intent: dict
    memory: dict
    junk: str
    companies: list
    query: str
    ticket_id: str
    deal_id: str
    record_id: str
    status: str
    wrote: bool
    notified: bool
    last_error: str
    terminal: str
    steps: int


def build():
    def plan(state: TicketState) -> dict:
        st = dict(state)
        st["intent"] = parse_objective(st.get("objective") or "", st.get("companies"))
        st["query"] = (st.get("intent") or {}).get("entity") or (st.get("intent") or {}).get("company") or ""
        st["steps"] = int(st.get("steps") or 0) + 1
        if not st["intent"].get("company"):
            st["last_error"] = "empty_company"
            st["terminal"] = "abort"
        return st

    def search(state: TicketState) -> dict:
        st = dict(state)
        query = st.get("query") or (st.get("intent") or {}).get("company") or ""
        hijack = last_company(st.get("junk") or "", st.get("companies"))
        if hijack and hijack != query:
            query = hijack
            note("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
        res = search_ticket.invoke({"query": query})
        st["steps"] = int(st.get("steps") or 0) + 1
        if transport(res):
            st["last_error"] = res.get("error") or "timeout"
            st["terminal"] = "abort"
            return st
        d = as_data(res)
        st["ticket_id"] = d.get("id") or ""
        st["deal_id"] = st["ticket_id"]
        st["record_id"] = st["ticket_id"]
        st["status"] = d.get("status") or ""
        mid = memory_id(st.get("memory"))
        if mid:
            st["ticket_id"] = mid
            st["deal_id"] = mid
            st["record_id"] = mid
            mem = st.get("memory") or {}
            if mem.get("deal_status"):
                st["status"] = mem["deal_status"]
            note("enrich trusted stale memory", {"deal_id": mid, "record_id": mid, "tool": "search_ticket"})
        return st

    def update(state: TicketState) -> dict:
        st = dict(state)
        # BUG: planned intent wins. A mid-run objective change is ignored.
        status = action_status(st.get("intent"))
        res = update_ticket.invoke({
            "id": st.get("ticket_id") or st.get("record_id") or st.get("deal_id") or "",
            "status": status,
        })
        st["steps"] = int(st.get("steps") or 0) + 1
        if transport(res):
            st["last_error"] = res.get("error") or "timeout"
            st["terminal"] = "abort"
            return st
        # BUG: any non-timeout envelope is a successful write.
        st["wrote"] = True
        st["status"] = status
        did = as_data(res).get("id")
        if did:
            st["ticket_id"] = did
            st["deal_id"] = did
            st["record_id"] = did
        st["terminal"] = "end"
        return st

    def route(default):
        def _r(state: TicketState) -> Any:
            if state.get("terminal") == "abort":
                return END
            return default

        return _r

    g = StateGraph(TicketState)
    g.add_node("plan", plan)
    g.add_node("search_ticket", search)
    g.add_node("update_ticket", update)
    g.add_edge(START, "plan")
    g.add_conditional_edges("plan", route("search_ticket"))
    g.add_conditional_edges("search_ticket", route("update_ticket"))
    g.add_conditional_edges("update_ticket", route(END))
    return g.compile(checkpointer=InMemorySaver())


graph = None


def finish(state: dict, runtime: str = "langgraph") -> dict:
    intent = state.get("intent") or {}
    rid = state.get("record_id") or state.get("ticket_id") or state.get("deal_id") or ""
    return {
        "terminal": state.get("terminal") or "end",
        "intent": intent,
        "claimed": {
            "wrote": bool(state.get("wrote")),
            "notified": bool(state.get("notified")),
            "deal_id": rid,
            "record_id": rid,
            "status": state.get("status") or "",
            "error": state.get("last_error") or "",
        },
        "steps": int(state.get("steps") or 0),
        "checkpoint": True,
        "runtime": runtime,
        "entry": "examples/native_ticket.py",
        "intercepted": True,
    }


def run(req: dict) -> dict:
    compiled = build()
    seed = {
        "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or ["Acme Corp", "Globex"],
        "steps": 0,
    }
    result = compiled.invoke(seed, {"configurable": {"thread_id": req.get("thread_id") or "ticket"}})
    return finish(result, req.get("runtime") or "langgraph")
