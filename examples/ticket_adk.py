"""A real ADK-shaped agent a user would drop into the chamber.

google.adk is used when installed. Otherwise this is a faithful
Agent + Runner + SessionService loop. Tools callback into the chamber.
"""

from __future__ import annotations

from ticket_graph import STATUS, apply_hook, data_of, finish, last_company, parse_objective, transport

HAS_ADK = False
GoogleLlmAgent = None
try:  # pragma: no cover
    from google.adk.agents import LlmAgent as GoogleLlmAgent  # type: ignore

    HAS_ADK = True
except Exception:
    GoogleLlmAgent = None


class LlmAgent:
    """Constructed when google.adk is missing so this file always builds an LlmAgent."""

    def __init__(self, name, instruction="", tools=None, model=None, **kwargs):
        self.name = name
        self.instruction = instruction
        self.tools = list(tools or [])
        self.model = model or "scripted"


INSTRUCTION = "Resolve the named ticket. Search, then update. Do not invent ids."
TOOLS = ["search_ticket", "update_ticket"]


def build_llm_agent():
    if GoogleLlmAgent is not None:
        try:
            return GoogleLlmAgent(name="ticket-bot", instruction=INSTRUCTION, tools=list(TOOLS), model="scripted"), True
        except Exception:
            try:
                return GoogleLlmAgent(name="ticket-bot", instruction=INSTRUCTION, tools=list(TOOLS)), True
            except Exception:
                pass
    return LlmAgent(name="ticket-bot", instruction=INSTRUCTION, tools=list(TOOLS), model="scripted"), False


root_agent, GOOGLE_ADK_CONSTRUCTED = build_llm_agent()


class SessionService:
    def __init__(self):
        self._sessions: dict[str, dict] = {}

    def get(self, session_id: str) -> dict:
        return dict(self._sessions.get(session_id) or {})

    def put(self, session_id: str, state: dict) -> None:
        self._sessions[session_id] = dict(state)


class Agent:
    def __init__(self, name: str, instruction: str, tools: list[str]):
        self.name = name
        self.instruction = instruction
        self.tools = tools


class Runner:
    def __init__(self, agent: Agent, sessions: SessionService, cb):
        self.agent = agent
        self.sessions = sessions
        self.cb = cb

    def run(self, session_id: str, req: dict) -> dict:
        state = self.sessions.get(session_id)
        state.update({
            "objective": req.get("objective") or "Resolve the Acme Corp ticket.",
            "memory": req.get("memory") or {},
            "junk": req.get("junk") or "",
            "companies": req.get("companies") or ["Acme Corp", "Globex"],
            "partial": bool(req.get("partial")),
            "steps": int(state.get("steps") or 0),
        })
        self.sessions.put(session_id, state)
        state = self._walk(state)
        self.sessions.put(session_id, state)
        out = finish(state, req.get("runtime") or "adk")
        out["adk"] = {
            "agent": self.agent.name,
            "instruction": self.agent.instruction,
            "tools": self.agent.tools,
            "has_google_adk": HAS_ADK,
            "session": session_id,
            "entry": "examples/ticket_adk.py",
        }
        return out

    def _walk(self, state: dict) -> dict:
        cb = self.cb
        state = apply_hook(state, cb.before("plan"))
        state["intent"] = parse_objective(state.get("objective") or "", state.get("companies"))
        state["query"] = (state.get("intent") or {}).get("company") or ""
        state["steps"] = int(state.get("steps") or 0) + 1
        if state.get("terminal") == "abort":
            return state

        state = apply_hook(state, cb.before("search_ticket"))
        query = state.get("query") or ""
        hijack = last_company(state.get("junk") or "", state.get("companies"))
        if hijack and hijack != query:
            query = hijack
            if hasattr(cb, "state"):
                cb.state("lookup hijacked by context ballast", {"company": query, "tool": "search_ticket"})
        res = cb.retry_tool("search_ticket", {"query": query})
        state["steps"] = int(state.get("steps") or 0) + 1
        if transport(res):
            state["last_error"] = res.get("error") or "timeout"
            state["terminal"] = "abort"
            return state
        d = data_of(res)
        state["ticket_id"] = d.get("id") or ""
        state["deal_id"] = state["ticket_id"]
        state["status"] = d.get("status") or ""
        mem = state.get("memory") or {}
        if mem.get("deal_id"):
            state["ticket_id"] = mem["deal_id"]
            state["deal_id"] = mem["deal_id"]
            if mem.get("deal_status"):
                state["status"] = mem["deal_status"]
            if hasattr(cb, "state"):
                cb.state("enrich trusted stale memory", {"deal_id": state["deal_id"], "tool": "search_ticket"})

        state = apply_hook(state, cb.before("update_ticket"))
        intent = state.get("intent") or {}
        status = STATUS.get(intent.get("deal_action") or "", "Resolved")
        res = cb.retry_tool("update_ticket", {
            "id": state.get("ticket_id") or state.get("deal_id") or "",
            "status": status,
        })
        state["steps"] = int(state.get("steps") or 0) + 1
        if transport(res):
            state["last_error"] = res.get("error") or "timeout"
            state["terminal"] = "abort"
            return state
        state["wrote"] = True
        state["status"] = status
        did = data_of(res).get("id")
        if did:
            state["ticket_id"] = did
            state["deal_id"] = did
        if res.get("error") == "permission_denied" and hasattr(cb, "state"):
            cb.state("write ignored permission_denied", {"tool": "update_ticket"})
        if res.get("ok") and not data_of(res) and hasattr(cb, "state"):
            cb.state("write accepted empty success payload", {"tool": "update_ticket"})
        state["terminal"] = "end"
        return state


def run(cb, req: dict) -> dict:
    agent = Agent(name=root_agent.name, instruction=root_agent.instruction, tools=list(TOOLS))
    runner = Runner(agent, SessionService(), cb)
    out = runner.run(req.get("thread_id") or "adk-ticket", req)
    adk = out.setdefault("adk", {})
    adk["llm_agent"] = type(root_agent).__name__
    adk["google_adk_constructed"] = GOOGLE_ADK_CONSTRUCTED
    return out
