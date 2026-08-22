"""An unmodified LangGraph whose nodes call HTTP themselves.

The graph is ordinary closures that urllib the ticket API. Nothing is
exported as a LangChain or ADK tool object. The chamber intercepts at
the socket — not by wrapping a discovered callable.
"""

from __future__ import annotations

from typing import Any, TypedDict

from langgraph.graph import END, START, StateGraph

try:
    from langgraph.checkpoint.memory import InMemorySaver
except ImportError:  # pragma: no cover
    from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

from ticket_logic import action_status, as_data, http_json, last_company, memory_id, note, parse_objective, transport


class TicketState(TypedDict, total=False):
    objective: str
    intent: dict
    memory: dict
    junk: str
    companies: list
    query: str
    ticket_id: str
    record_id: str
    status: str
    wrote: bool
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
        res = http_json("GET", "http://tickets.example/search", params={"q": query})
        st["steps"] = int(st.get("steps") or 0) + 1
        if transport(res):
            st["last_error"] = res.get("error") or "timeout"
            st["terminal"] = "abort"
            return st
        d = as_data(res)
        st["ticket_id"] = d.get("id") or ""
        st["record_id"] = st["ticket_id"]
        st["status"] = d.get("status") or ""
        mid = memory_id(st.get("memory"))
        if mid:
            st["ticket_id"] = mid
            st["record_id"] = mid
            mem = st.get("memory") or {}
            if mem.get("deal_status"):
                st["status"] = mem["deal_status"]
            note("enrich trusted stale memory", {"record_id": mid, "tool": "search_ticket"})
        return st

    def update(state: TicketState) -> dict:
        st = dict(state)
        status = action_status(st.get("intent"))
        res = http_json(
            "POST",
            "http://tickets.example/tickets/" + str(st.get("ticket_id") or st.get("record_id") or ""),
            body={"status": status},
        )
        st["steps"] = int(st.get("steps") or 0) + 1
        if transport(res):
            st["last_error"] = res.get("error") or "timeout"
            st["terminal"] = "abort"
            return st
        st["wrote"] = True
        st["status"] = status
        did = as_data(res).get("id")
        if did:
            st["ticket_id"] = did
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
    g.add_node("search", search)
    g.add_node("update", update)
    g.add_edge(START, "plan")
    g.add_conditional_edges("plan", route("search"))
    g.add_conditional_edges("search", route("update"))
    g.add_conditional_edges("update", route(END))
    return g.compile(checkpointer=InMemorySaver())


def finish(state: dict, runtime: str = "langgraph") -> dict:
    intent = state.get("intent") or {}
    rid = state.get("record_id") or state.get("ticket_id") or ""
    return {
        "terminal": state.get("terminal") or "end",
        "intent": intent,
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
        "runtime": runtime,
        "entry": "examples/http_closure.py",
        "http_intercept": True,
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
    result = compiled.invoke(seed, {"configurable": {"thread_id": req.get("thread_id") or "closure"}})
    return finish(result, req.get("runtime") or "langgraph")
