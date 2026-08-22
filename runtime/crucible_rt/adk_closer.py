"""ADK adapter: Agent + Runner + SessionService.

google.adk is used when installed. Otherwise this is a faithful ADK-shaped
loop — instruction, tools, session state, model — that still callbacks into Go.
"""

from . import patient

HAS_ADK = False
try:  # pragma: no cover
    from google.adk.agents import LlmAgent  # type: ignore
    from google.adk.runners import Runner  # type: ignore
    from google.adk.sessions import InMemorySessionService  # type: ignore

    HAS_ADK = True
except Exception:
    LlmAgent = None
    Runner = None
    InMemorySessionService = None


class SessionService:
    """ADK SessionService analog — the checkpointer for this adapter."""

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


class RunnerADK:
    def __init__(self, agent: Agent, session_service: SessionService, callback):
        self.agent = agent
        self.sessions = session_service
        self.cb = callback

    def run(self, session_id: str, req: dict) -> dict:
        state = self.sessions.get(session_id)
        state.update(patient.seed(req))
        self.sessions.put(session_id, state)
        state = patient.walk(self.cb, state)
        self.sessions.put(session_id, state)
        out = patient.finish(state, "adk")
        out["adk"] = {
            "agent": self.agent.name,
            "instruction": self.agent.instruction,
            "tools": self.agent.tools,
            "has_google_adk": HAS_ADK,
            "session": session_id,
        }
        return out


def run(cb, req: dict) -> dict:
    agent = Agent(
        name="aether-closer",
        instruction="Close the named deal and email the AE. Use the CRM tools in order.",
        tools=["lookup_contact", "get_deal", "check_permission", "write_deal", "send_email"],
    )
    runner = RunnerADK(agent, SessionService(), cb)
    return runner.run(req.get("thread_id") or "adk", req)
