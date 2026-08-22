"""A real LangGraph a user would drop into the chamber.

The chamber injects `cb`. Tools never touch the world. This graph is
intentionally production-shaped and fragile: a non-timeout envelope is a
successful write, checkpoint memory overwrites a fresh search, and junk
context can hijack the query.
"""

from __future__ import annotations

from typing import Any, TypedDict

from langgraph.graph import END, START, StateGraph

try:
    from langgraph.checkpoint.memory import InMemorySaver
except ImportError:  # pragma: no cover
    from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

STATUS = {
    "close_won": "Closed-Won",
    "on_hold": "On-Hold",
    "refund": "Refunded",
    "resolve": "Resolved",
}


class TicketState(TypedDict, total=False):
    objective: str
    intent: dict
    memory: dict
    junk: str
    companies: list
    partial: bool
    query: str
    ticket_id: str
    deal_id: str
    status: str
    wrote: bool
    notified: bool
    last_error: str
    terminal: str
    steps: int


def parse_objective(objective: str, companies: list[str] | None) -> dict:
    companies = list(companies or ["Acme Corp", "Globex"])
    intent = {"company": companies[0], "deal_action": "resolve", "notify": False}
    low = (objective or "").lower()
    best, best_len = "", 0
    for c in companies:
        if c.lower() in low and len(c) > best_len:
            best, best_len = c, len(c)
    if best:
        intent["company"] = best
    if "refund" in low:
        intent["deal_action"] = "refund"
    elif "on-hold" in low or "on hold" in low or "stop." in low:
        intent["deal_action"] = "on_hold"
    elif "resolve" in low:
        intent["deal_action"] = "resolve"
    elif "closed-won" in low or "close" in low:
        intent["deal_action"] = "close_won"
    if "do not email" in low:
        intent["notify"] = False
    elif "email" in low:
        intent["notify"] = True
    return intent


def apply_hook(state: dict, hook: dict | None) -> dict:
    if not hook:
        return state
    if hook.get("objective"):
        state["objective"] = hook["objective"]
    if "partial" in hook:
        state["partial"] = hook["partial"]
    if hook.get("memory"):
        state["memory"] = hook["memory"]
    if hook.get("junk") is not None:
        state["junk"] = hook["junk"]
    return state


def last_company(junk: str, companies: list[str] | None) -> str:
    companies = list(companies or ["Acme Corp", "Globex"])
    last, idx = "", -1
    for c in companies:
        i = (junk or "").rfind(c)
        if i > idx:
            idx, last = i, c
    return last


def transport(res: dict) -> bool:
    return (res.get("error") or "") in ("timeout", "cost_ceiling", "unavailable")


def data_of(res: dict) -> dict:
    d = res.get("data")
    return d if isinstance(d, dict) else {}


def build(cb):
    def plan(state: TicketState) -> dict:
        st = apply_hook(dict(state), cb.before("plan"))
        st["intent"] = parse_objective(st.get("objective") or "", st.get("companies"))
        st["query"] = (st.get("intent") or {}).get("company") or ""
        st["steps"] = int(st.get("steps") or 0) + 1
        if not st["intent"].get("company"):
            st["last_error"] = "empty_company"
            st["terminal"] = "abort"
        return st

    def search(state: TicketState) -> dict:
        st = apply_hook(dict(state), cb.before("search_ticket"))
        query = st.get("query") or (st.get("intent") or {}).get("company") or ""
        hijack = last_company(st.get("junk") or "", st.get("companies"))
        if hijack and hijack != query:
            query = hijack
            if hasattr(cb, "state"):
                cb.state("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
        res = cb.retry_tool("search_ticket", {"query": query})
        st["steps"] = int(st.get("steps") or 0) + 1
        if transport(res):
            st["last_error"] = res.get("error") or "timeout"
            st["terminal"] = "abort"
            return st
        d = data_of(res)
        st["ticket_id"] = d.get("id") or ""
        st["deal_id"] = st["ticket_id"]
        st["status"] = d.get("status") or ""
        mem = st.get("memory") or {}
        if mem.get("deal_id"):
            st["ticket_id"] = mem["deal_id"]
            st["deal_id"] = mem["deal_id"]
            if mem.get("deal_status"):
                st["status"] = mem["deal_status"]
            if hasattr(cb, "state"):
                cb.state("enrich trusted stale memory", {"deal_id": st["deal_id"], "tool": "search_ticket"})
        return st

    def update(state: TicketState) -> dict:
        st = apply_hook(dict(state), cb.before("update_ticket"))
        intent = st.get("intent") or {}
        # BUG: planned intent wins. A mid-run objective change is ignored.
        status = STATUS.get(intent.get("deal_action") or "", "Resolved")
        res = cb.retry_tool("update_ticket", {
            "id": st.get("ticket_id") or st.get("deal_id") or "",
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
        did = data_of(res).get("id")
        if did:
            st["ticket_id"] = did
            st["deal_id"] = did
        if res.get("error") == "permission_denied" and hasattr(cb, "state"):
            cb.state("write ignored permission_denied", {"tool": "update_ticket"})
        if res.get("ok") and not data_of(res) and hasattr(cb, "state"):
            cb.state("write accepted empty success payload", {"tool": "update_ticket"})
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


def finish(state: dict, runtime: str = "langgraph") -> dict:
    intent = state.get("intent") or {}
    return {
        "terminal": state.get("terminal") or "end",
        "intent": intent,
        "claimed": {
            "wrote": bool(state.get("wrote")),
            "notified": bool(state.get("notified")),
            "deal_id": state.get("deal_id") or state.get("ticket_id") or "",
            "status": state.get("status") or "",
            "error": state.get("last_error") or "",
        },
        "steps": int(state.get("steps") or 0),
        "checkpoint": True,
        "runtime": runtime,
        "entry": "examples/ticket_graph.py",
    }


def run(cb, req: dict) -> dict:
    graph = build(cb)
    seed = {
        "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or ["Acme Corp", "Globex"],
        "partial": bool(req.get("partial")),
        "steps": 0,
    }
    result = graph.invoke(seed, {"configurable": {"thread_id": req.get("thread_id") or "ticket"}})
    return finish(result, req.get("runtime") or "langgraph")
