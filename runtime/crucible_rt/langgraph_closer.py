"""Real LangGraph closer: StateGraph + InMemorySaver + chat model."""

from typing import Any, TypedDict

from langgraph.graph import END, START, StateGraph

try:
    from langgraph.checkpoint.memory import InMemorySaver
except ImportError:  # pragma: no cover
    from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

from . import generic, patient


class AgentState(TypedDict, total=False):
    objective: str
    intent: dict
    memory: dict
    junk: str
    companies: list
    partial: bool
    contact_id: str
    ae: str
    deal_id: str
    status: str
    amount: int
    close_date: str
    owner_id: str
    permitted: bool
    wrote: bool
    notified: bool
    last_error: str
    terminal: str
    steps: int


def build(cb) -> Any:
    def wrap(fn):
        def node(state: AgentState) -> dict:
            nxt = fn(cb, dict(state))
            return {k: nxt[k] for k in nxt}

        return node

    def route(default: str):
        def _r(state: AgentState) -> str:
            if state.get("terminal") == "abort":
                return END
            return default

        return _r

    g = StateGraph(AgentState)
    for name, fn in patient.ORDER:
        g.add_node(name, wrap(fn))
    g.add_edge(START, "plan")
    g.add_conditional_edges("plan", route("lookup"))
    g.add_conditional_edges("lookup", route("fetch"))
    g.add_conditional_edges("fetch", route("enrich"))
    g.add_edge("enrich", "authorize")
    g.add_conditional_edges("authorize", route("write"))
    g.add_conditional_edges("write", route("notify"))
    g.add_edge("notify", END)
    return g.compile(checkpointer=InMemorySaver())


def run(cb, req: dict) -> dict:
    spec = req.get("spec") or {}
    if generic.should_compile(spec):
        return generic.run_langgraph(cb, req)
    graph = build(cb)
    thread = req.get("thread_id") or "t"
    result = graph.invoke(patient.seed(req), {"configurable": {"thread_id": thread}})
    out = patient.finish(result, "langgraph")
    out["checkpoint"] = True
    return out
