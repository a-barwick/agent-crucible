"""An unmodified ADK agent. No chamber callback.

Constructs google.adk.LlmAgent when the package is installed, otherwise
a matching LlmAgent + FunctionTool surface. Tools are plain functions
the chamber intercepts. This file never calls retry_tool.
"""

from __future__ import annotations

from ticket_logic import action_status, as_data, http_json, last_company, memory_id, note, parse_objective, transport

INSTRUCTION = "Resolve the named ticket. Search, then update. Do not invent ids."


def search_ticket(query: str) -> dict:
    """Search the ticket HTTP API by company or free text."""
    return http_json("GET", "http://tickets.example/search", params={"q": query})


def update_ticket(id: str, status: str) -> dict:
    """Patch a ticket's status on the ticket HTTP API."""
    return http_json("POST", f"http://tickets.example/tickets/{id}", body={"status": status})


HAS_GOOGLE_ADK = False
try:  # pragma: no cover
    from google.adk.agents import LlmAgent as GoogleLlmAgent  # type: ignore
    from google.adk.tools import FunctionTool as GoogleFunctionTool  # type: ignore

    HAS_GOOGLE_ADK = True
except Exception:
    GoogleLlmAgent = None
    GoogleFunctionTool = None


class FunctionTool:
    """ADK FunctionTool stand-in. google.adk uses FunctionTool(func)."""

    def __init__(self, func):
        self.func = func
        self.name = getattr(func, "__name__", "tool")
        self.description = (getattr(func, "__doc__", "") or "").strip()


class LlmAgent:
    """ADK LlmAgent stand-in used when google.adk is not installed."""

    def __init__(self, name, instruction="", tools=None, model=None, **kwargs):
        self.name = name
        self.instruction = instruction
        self.tools = list(tools or [])
        self.model = model or "scripted"
        self.kwargs = kwargs


def _function_tool(fn):
    if GoogleFunctionTool is not None:
        try:
            return GoogleFunctionTool(fn)
        except Exception:
            try:
                return GoogleFunctionTool(func=fn)
            except Exception:
                pass
    return FunctionTool(fn)


TOOLS = [_function_tool(search_ticket), _function_tool(update_ticket)]
DISPATCH = {"search_ticket": search_ticket, "update_ticket": update_ticket}


def build_llm_agent():
    tools = [search_ticket, update_ticket]
    if GoogleLlmAgent is not None:
        try:
            return GoogleLlmAgent(
                name="ticket-bot",
                instruction=INSTRUCTION,
                tools=tools,
                model="scripted",
            ), True
        except Exception:
            try:
                return GoogleLlmAgent(name="ticket-bot", instruction=INSTRUCTION, tools=tools), True
            except Exception:
                pass
    return LlmAgent(name="ticket-bot", instruction=INSTRUCTION, tools=TOOLS, model="scripted"), False


root_agent, GOOGLE_ADK_CONSTRUCTED = build_llm_agent()


class SessionService:
    def __init__(self):
        self._sessions: dict[str, dict] = {}

    def get(self, session_id: str) -> dict:
        return dict(self._sessions.get(session_id) or {})

    def put(self, session_id: str, state: dict) -> None:
        self._sessions[session_id] = dict(state)


class Runner:
    def __init__(self, agent, sessions: SessionService):
        self.agent = agent
        self.sessions = sessions

    def run(self, session_id: str, req: dict) -> dict:
        state = self.sessions.get(session_id)
        state.update({
            "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
            "memory": req.get("memory") or {},
            "junk": req.get("junk") or "",
            "companies": req.get("companies") or ["Acme Corp", "Globex"],
            "steps": int(state.get("steps") or 0),
        })
        self.sessions.put(session_id, state)
        state = _walk(state)
        self.sessions.put(session_id, state)
        return _finish(state, req, session_id, self.agent)


def _call(name: str, args: dict) -> dict:
    for t in TOOLS:
        if getattr(t, "name", None) == name and callable(getattr(t, "func", None)):
            return t.func(**args)
    fn = DISPATCH.get(name)
    if fn is None:
        return {"ok": False, "error": "unknown_tool"}
    return fn(**args)


def _walk(state: dict) -> dict:
    state["intent"] = parse_objective(state.get("objective") or "", state.get("companies"))
    state["query"] = (state.get("intent") or {}).get("entity") or (state.get("intent") or {}).get("company") or ""
    state["steps"] = int(state.get("steps") or 0) + 1
    if state.get("terminal") == "abort":
        return state

    query = state.get("query") or ""
    hijack = last_company(state.get("junk") or "", state.get("companies"))
    if hijack and hijack != query:
        query = hijack
        note("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
    res = _call("search_ticket", {"query": query})
    state["steps"] = int(state.get("steps") or 0) + 1
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    d = as_data(res)
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

    status = action_status(state.get("intent"))
    res = _call("update_ticket", {
        "id": state.get("ticket_id") or state.get("record_id") or state.get("deal_id") or "",
        "status": status,
    })
    state["steps"] = int(state.get("steps") or 0) + 1
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    state["wrote"] = True
    state["status"] = status
    did = as_data(res).get("id")
    if did:
        state["ticket_id"] = did
        state["deal_id"] = did
        state["record_id"] = did
    state["terminal"] = "end"
    return state


def _finish(state: dict, req: dict, session_id: str, agent) -> dict:
    intent = state.get("intent") or {}
    rid = state.get("record_id") or state.get("ticket_id") or state.get("deal_id") or ""
    tools = []
    for t in getattr(agent, "tools", None) or TOOLS:
        tools.append(getattr(t, "name", None) or getattr(t, "__name__", str(t)))
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
        "runtime": req.get("runtime") or "adk",
        "entry": "examples/native_adk.py",
        "intercepted": True,
        "adk": {
            "agent": getattr(agent, "name", "ticket-bot"),
            "instruction": getattr(agent, "instruction", INSTRUCTION),
            "tools": tools,
            "has_google_adk": HAS_GOOGLE_ADK,
            "google_adk_constructed": GOOGLE_ADK_CONSTRUCTED,
            "llm_agent": type(agent).__name__,
            "session": session_id,
        },
    }


def run(req: dict) -> dict:
    runner = Runner(root_agent, SessionService())
    return runner.run(req.get("thread_id") or "adk-ticket", req)
