"""Compile a pasted spec into a LangGraph StateGraph or an ADK-shaped walk.

The chamber stays on the other side of the tools. This module only
decides node order and which tool name to callback.
"""

from typing import Any

from . import patient
from .intent import last_company, parse_model_intent

CRM_TOOLS = {"lookup_contact", "get_deal", "write_deal", "send_email", "check_permission"}
CRM_NODES = {"plan", "lookup", "fetch", "enrich", "authorize", "write", "notify", "end", "abort"}


def should_compile(spec: dict | None) -> bool:
    """True when the sidecar should build from the pasted spec, not the CRM closer."""
    if not spec:
        return False
    tools = spec.get("tools") or []
    graph = spec.get("graph") or {}
    nodes = list(graph.get("nodes") or [])
    if looks_like_crm(tools) and (not nodes or set(nodes) <= CRM_NODES):
        return False
    return bool(tools) or bool(nodes)


def looks_like_crm(tools) -> bool:
    for t in tools or []:
        name = t.get("name") if isinstance(t, dict) else t
        if name in CRM_TOOLS:
            return True
    return False


def classify(name: str) -> str:
    n = (name or "").lower()
    if any(p in n for p in ("email", "notify", "mail", "send_message")):
        return "email"
    if any(p in n for p in ("permission", "authorize", "acl")):
        return "permission"
    if any(p in n for p in ("write", "update", "patch", "create", "delete", "refund", "upsert", "set_")):
        return "write"
    return "read"


def is_write(name: str) -> bool:
    return classify(name) == "write"


def is_email(name: str) -> bool:
    return classify(name) == "email"


def is_permission(name: str) -> bool:
    return classify(name) == "permission"


def action_status(action: str) -> str:
    return {
        "close_won": "Closed-Won",
        "on_hold": "On-Hold",
        "refund": "Refunded",
        "resolve": "Resolved",
    }.get(action or "", "")


def graph_from_spec(spec: dict) -> dict:
    graph = spec.get("graph") or {}
    if graph.get("start") or graph.get("nodes"):
        return {
            "start": graph.get("start") or "plan",
            "nodes": list(graph.get("nodes") or []),
            "edges": list(graph.get("edges") or []),
        }
    tools = spec.get("tools") or []
    nodes = ["plan"]
    edges = []
    prev = "plan"
    for t in tools:
        name = t.get("name") if isinstance(t, dict) else str(t)
        nodes.append(name)
        edges.append({"from": prev, "to": name})
        prev = name
    edges.append({"from": prev, "to": "end"})
    nodes += ["end", "abort"]
    return {"start": "plan", "nodes": nodes, "edges": edges}


def next_from(graph: dict, frm: str) -> str:
    for e in graph.get("edges") or []:
        if e.get("from") == frm and e.get("to") != "abort":
            return e.get("to") or "end"
    return "end"


def infer_bind(name: str, spec: dict) -> dict:
    binds = spec.get("node_tools") or {}
    if name in binds and isinstance(binds[name], dict):
        b = dict(binds[name])
        if b.get("kind") or b.get("tool"):
            return b
    if name == "plan":
        return {"kind": "plan"}
    if name == "lookup":
        return {"kind": "lookup", "tool": "lookup_contact"}
    if name == "fetch":
        return {"kind": "fetch", "tool": "get_deal"}
    if name == "enrich":
        return {"kind": "enrich"}
    if name == "authorize":
        return {"kind": "authorize", "tool": "check_permission"}
    if name == "write":
        return {"kind": "write", "tool": "write_deal"}
    if name == "notify":
        return {"kind": "notify", "tool": "send_email"}
    for t in spec.get("tools") or []:
        tn = t.get("name") if isinstance(t, dict) else str(t)
        if tn == name or name in tn or tn in name:
            return {"kind": "tool", "tool": tn}
    want = classify(name)
    if want != "read" or is_write(name) or is_email(name) or is_permission(name):
        for t in spec.get("tools") or []:
            tn = t.get("name") if isinstance(t, dict) else str(t)
            if classify(tn) == want:
                return {"kind": "tool", "tool": tn}
    return {"kind": "plan"}


def find_tool(spec: dict, name: str) -> dict:
    for t in spec.get("tools") or []:
        if isinstance(t, dict) and t.get("name") == name:
            return t
    return {}


def infer_args(state: dict, tool: str, spec: dict) -> dict:
    args: dict[str, Any] = {}
    t = find_tool(spec, tool)
    required = list(t.get("required") or [])
    if not required:
        required = default_arg_names(tool)
    kind = classify(tool)
    for name in required:
        v = arg_from_state(state, name, kind)
        if v is not None and v != "":
            args[name] = v
    if is_write(tool):
        intent = state.get("intent") or {}
        status = action_status(intent.get("action") or intent.get("deal_action") or "")
        if status and not args.get("status"):
            args["status"] = status
    return args


def default_arg_names(tool: str) -> list[str]:
    kind = classify(tool)
    if kind == "write":
        return ["id", "status"]
    if kind == "email":
        return ["to", "subject", "body"]
    if kind == "permission":
        return ["perm"]
    return ["query", "company", "id"]


def arg_from_state(state: dict, name: str, kind: str):
    intent = state.get("intent") or {}
    if name in ("company", "query", "name", "title"):
        return intent.get("entity") or intent.get("company") or None
    if name in ("id", "record_id", "ticket_id", "deal_id"):
        return state.get("record_id") or state.get("deal_id") or None
    if name == "contact_id":
        return state.get("contact_id") or None
    if name == "status":
        if kind == "write":
            s = action_status((intent.get("action") or intent.get("deal_action") or ""))
            if s:
                return s
        return state.get("status") or None
    if name in ("to", "email", "recipient"):
        return state.get("ae") or None
    if name in ("perm", "permission"):
        return "crm.write"
    if name == "amount":
        return state.get("amount") or 0
    if name == "owner_id":
        return state.get("owner_id") or None
    if name == "close_date":
        return state.get("close_date") or None
    if name == "subject":
        return "update: " + (intent.get("company") or "")
    if name in ("body", "text"):
        return "deal=" + str(state.get("deal_id") or "") + " status=" + str(state.get("status") or "")
    return None


def apply_saves(state: dict, data: dict | None, save: dict | None) -> None:
    save = save or {
        "id": "deal_id",
        "status": "status",
        "ae": "ae",
        "email": "ae",
        "amount": "amount",
        "owner_id": "owner_id",
        "contact_id": "contact_id",
    }
    if not data:
        return
    for src, dest in save.items():
        if src not in data:
            continue
        val = data.get(src)
        if dest == "amount":
            try:
                state["amount"] = int(val or 0)
            except (TypeError, ValueError):
                state["amount"] = 0
            continue
        state[dest if dest != "id" else "deal_id"] = "" if val is None else str(val)
        if dest in ("deal_id", "id", "record_id", "ticket_id"):
            state["deal_id"] = "" if val is None else str(val)


def state_value(state: dict, path: str):
    intent = state.get("intent") or {}
    if path in ("intent.company", "intent.entity", "company", "entity", "query", "name"):
        return intent.get("entity") or intent.get("company")
    if path == "contact_id":
        return state.get("contact_id")
    if path in ("deal_id", "id", "record_id", "ticket_id"):
        return state.get("record_id") or state.get("deal_id")
    if path == "ae":
        return state.get("ae")
    if path == "status":
        return state.get("status")
    if path == "amount":
        return state.get("amount")
    if path == "owner_id":
        return state.get("owner_id")
    if path == "close_date":
        return state.get("close_date")
    return path


def make_node_fn(cb, name: str, spec: dict):
    bind = infer_bind(name, spec)
    kind = bind.get("kind") or "tool"

    def fn(state: dict) -> dict:
        if kind == "plan":
            return _plan(cb, state, spec)
        if kind == "enrich":
            state = patient.merge_hook(state, cb.before(name))
            state["steps"] = int(state.get("steps") or 0) + 1
            return state
        return _tool(cb, state, name, bind, spec)

    return fn


def _plan(cb, state: dict, spec: dict) -> dict:
    from .model import CloserPlanner

    state = patient.merge_hook(state, cb.before("plan"))
    companies = list(state.get("companies") or spec.get("companies") or [])
    model = CloserPlanner(companies=companies, partial=bool(state.get("partial")))
    msg = model.invoke(state.get("objective") or "")
    state["intent"] = parse_model_intent(msg.content, state.get("objective") or "", companies)
    state["steps"] = int(state.get("steps") or 0) + 1
    if not state["intent"].get("company"):
        state["last_error"] = "empty_company"
        state["terminal"] = "abort"
    return state


def _tool(cb, state: dict, name: str, bind: dict, spec: dict) -> dict:
    state = patient.merge_hook(state, cb.before(name))
    tool = bind.get("tool") or name
    args = infer_args(state, tool, spec)
    for arg, path in (bind.get("args_from") or {}).items():
        args[arg] = state_value(state, path)
    if classify(tool) == "read" and state.get("junk"):
        hijack = last_company(state.get("junk") or "", state.get("companies"))
        intent = state.get("intent") or {}
        if hijack and hijack != (intent.get("company") or ""):
            for k in ("query", "company", "name", "title"):
                if k in args:
                    args[k] = hijack
            if hasattr(cb, "state"):
                cb.state("lookup hijacked by context ballast", {"company": hijack, "tool": tool})
    res = cb.retry_tool(tool, args)
    state["steps"] = int(state.get("steps") or 0) + 1
    if patient.transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    d = patient.data(res)
    apply_saves(state, d, bind.get("save"))
    mem = state.get("memory") or {}
    mid = (mem.get("record_id") or mem.get("deal_id") or "") if classify(tool) == "read" else ""
    if mid:
        state["deal_id"] = mid
        state["record_id"] = mid
        if mem.get("deal_status"):
            state["status"] = mem["deal_status"]
        if hasattr(cb, "state"):
            cb.state("enrich trusted stale memory", {"deal_id": mid, "record_id": mid, "tool": tool})
    if is_write(tool):
        # Same bug as the CRM write node: a non-timeout envelope is "done".
        state["wrote"] = True
        status = d.get("status") or args.get("status") or ""
        if status:
            state["status"] = status
        if res.get("error") == "permission_denied" and hasattr(cb, "state"):
            cb.state("write ignored permission_denied", {"tool": tool})
        if res.get("ok") and not d and hasattr(cb, "state"):
            cb.state("write accepted empty success payload", {"tool": tool})
    if is_email(tool):
        state["notified"] = bool(res.get("ok") or not res.get("error"))
    return state


def walk(cb, state: dict, spec: dict) -> dict:
    gspec = graph_from_spec(spec)
    node = gspec.get("start") or "plan"
    steps = 0
    while node and node not in ("end", "abort"):
        if state.get("terminal") == "abort":
            break
        fn = make_node_fn(cb, node, spec)
        state = fn(state)
        if state.get("terminal") == "abort":
            break
        node = next_from(gspec, node)
        steps += 1
        if steps >= 20:
            state["last_error"] = "max_steps"
            state["terminal"] = "abort"
            break
    if not state.get("terminal"):
        state["terminal"] = "end"
    return state


def run_adk(cb, req: dict) -> dict:
    spec = req.get("spec") or {}
    state = patient.seed(req)
    if spec.get("companies") and not state.get("companies"):
        state["companies"] = list(spec["companies"])
    state = walk(cb, state, spec)
    out = patient.finish(state, "adk")
    tools = []
    for t in spec.get("tools") or []:
        tools.append(t.get("name") if isinstance(t, dict) else str(t))
    out["adk"] = {
        "agent": spec.get("name") or "pasted",
        "instruction": spec.get("description") or "Follow the objective. Use the pasted tools in order.",
        "tools": tools,
        "generic": True,
        "session": req.get("thread_id") or "adk",
    }
    return out


def run_langgraph(cb, req: dict) -> dict:
    from typing import TypedDict

    from langgraph.graph import END, START, StateGraph

    try:
        from langgraph.checkpoint.memory import InMemorySaver
    except ImportError:  # pragma: no cover
        from langgraph.checkpoint.memory import MemorySaver as InMemorySaver

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

    spec = req.get("spec") or {}
    gspec = graph_from_spec(spec)

    def wrap(fn):
        def node(state: AgentState) -> dict:
            nxt = fn(dict(state))
            return {k: nxt[k] for k in nxt}

        return node

    def route_to(default: str):
        def _r(state: AgentState) -> str:
            if state.get("terminal") == "abort":
                return END
            return default

        return _r

    g = StateGraph(AgentState)
    usable = [n for n in gspec["nodes"] if n not in ("end", "abort")]
    if not usable:
        usable = ["plan"]
    for name in usable:
        g.add_node(name, wrap(make_node_fn(cb, name, spec)))
    start = gspec.get("start") or usable[0]
    g.add_edge(START, start)
    for name in usable:
        nxt = next_from(gspec, name)
        if nxt in ("", "end", "abort") or nxt not in usable:
            g.add_conditional_edges(name, route_to(END))
        else:
            g.add_conditional_edges(name, route_to(nxt))
    graph = g.compile(checkpointer=InMemorySaver())
    seed = patient.seed(req)
    if spec.get("companies") and not seed.get("companies"):
        seed["companies"] = list(spec["companies"])
    result = graph.invoke(seed, {"configurable": {"thread_id": req.get("thread_id") or "t"}})
    out = patient.finish(result, "langgraph")
    out["checkpoint"] = True
    out["generic"] = True
    return out
