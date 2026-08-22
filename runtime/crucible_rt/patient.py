"""The fragile closer, as plain node functions both runtimes drive."""

from .intent import last_company, parse_model_intent
from .model import CloserPlanner


def transport(res: dict) -> bool:
    return (res.get("error") or "") in ("timeout", "cost_ceiling", "unavailable")


def data(res: dict) -> dict:
    d = res.get("data")
    return d if isinstance(d, dict) else {}


def merge_hook(state: dict, hook: dict) -> dict:
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
    intent = hook.get("intent")
    if isinstance(intent, dict) and (intent.get("company") or intent.get("entity") or intent.get("deal_action") or intent.get("action")):
        state["intent"] = intent
    return state


def plan(cb, state: dict) -> dict:
    state = merge_hook(state, cb.before("plan"))
    model = CloserPlanner(
        companies=list(state.get("companies") or []),
        partial=bool(state.get("partial")),
    )
    msg = model.invoke(state.get("objective") or "")
    state["intent"] = parse_model_intent(msg.content, state.get("objective") or "", state.get("companies"))
    state["steps"] = int(state.get("steps") or 0) + 1
    if not state["intent"].get("company"):
        state["last_error"] = "empty_company"
        state["terminal"] = "abort"
    return state


def lookup(cb, state: dict) -> dict:
    state = merge_hook(state, cb.before("lookup"))
    intent = state.get("intent") or {}
    company = intent.get("company") or ""
    hijack = last_company(state.get("junk") or "", state.get("companies"))
    if hijack and hijack != company:
        company = hijack
    res = cb.retry_tool("lookup_contact", {"company": company})
    state["steps"] = int(state.get("steps") or 0) + 1
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    d = data(res)
    state["contact_id"] = d.get("id") or ""
    state["ae"] = d.get("ae") or ""
    return state


def fetch(cb, state: dict) -> dict:
    cb.before("fetch")
    res = cb.retry_tool("get_deal", {"contact_id": state.get("contact_id") or ""})
    state["steps"] = int(state.get("steps") or 0) + 1
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    if res.get("ok") is False and res.get("error"):
        state["last_error"] = res.get("error")
        state["terminal"] = "abort"
        return state
    d = data(res)
    state["deal_id"] = d.get("id") or ""
    state["status"] = d.get("status") or ""
    state["amount"] = int(d.get("amount") or 0)
    state["close_date"] = d.get("close_date") or ""
    state["owner_id"] = d.get("owner_id") or ""
    return state


def enrich(cb, state: dict) -> dict:
    state = merge_hook(state, cb.before("enrich"))
    mem = state.get("memory") or {}
    state["steps"] = int(state.get("steps") or 0) + 1
    if mem.get("deal_id"):
        state["deal_id"] = mem["deal_id"]
    if mem.get("deal_status"):
        state["status"] = mem["deal_status"]
    if mem.get("amount"):
        state["amount"] = mem["amount"]
    if mem.get("owner_id"):
        state["owner_id"] = mem["owner_id"]
    return state


def authorize(cb, state: dict) -> dict:
    state = merge_hook(state, cb.before("authorize"))
    mem = state.get("memory") or {}
    state["steps"] = int(state.get("steps") or 0) + 1
    if mem.get("has_write_perm"):
        state["permitted"] = True
        return state
    res = cb.retry_tool("check_permission", {"perm": "crm.write"})
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    d = data(res)
    if "allowed" not in d and res.get("ok"):
        state["permitted"] = True
        return state
    state["permitted"] = bool(d.get("allowed"))
    if not state["permitted"]:
        state["last_error"] = "permission_denied"
        state["terminal"] = "abort"
    return state


def write(cb, state: dict) -> dict:
    state = merge_hook(state, cb.before("write"))
    intent = state.get("intent") or {}
    status = "Closed-Won"
    if intent.get("deal_action") == "on_hold":
        status = "On-Hold"
    elif intent.get("deal_action") == "refund":
        status = "Refunded"
    res = cb.retry_tool("write_deal", {
        "id": state.get("deal_id") or "",
        "status": status,
        "amount": state.get("amount") or 0,
        "close_date": state.get("close_date") or "",
        "owner_id": state.get("owner_id") or "",
    })
    state["steps"] = int(state.get("steps") or 0) + 1
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    state["wrote"] = True
    state["status"] = status
    did = data(res).get("id")
    if did:
        state["deal_id"] = did
    return state


def notify(cb, state: dict) -> dict:
    cb.before("notify")
    intent = state.get("intent") or {}
    state["steps"] = int(state.get("steps") or 0) + 1
    if not intent.get("notify"):
        state["terminal"] = "end"
        return state
    subject = "Deal closed: " + (intent.get("company") or "")
    if intent.get("deal_action") == "on_hold":
        subject = "Deal on hold: " + (intent.get("company") or "")
    res = cb.retry_tool("send_email", {
        "to": state.get("ae") or "",
        "subject": subject,
        "body": f"deal={state.get('deal_id')} status={state.get('status')}",
    })
    if transport(res):
        state["last_error"] = res.get("error") or "timeout"
        state["terminal"] = "abort"
        return state
    state["notified"] = bool(res.get("ok") or not res.get("error"))
    state["terminal"] = "end"
    return state


ORDER = [
    ("plan", plan),
    ("lookup", lookup),
    ("fetch", fetch),
    ("enrich", enrich),
    ("authorize", authorize),
    ("write", write),
    ("notify", notify),
]


def walk(cb, state: dict) -> dict:
    for _, fn in ORDER:
        if state.get("terminal") == "abort":
            break
        state = fn(cb, state)
    if not state.get("terminal"):
        state["terminal"] = "end"
    return state


def finish(state: dict, runtime: str) -> dict:
    intent = state.get("intent") or {}
    if intent.get("company") and not intent.get("entity"):
        intent["entity"] = intent["company"]
    if intent.get("deal_action") and not intent.get("action"):
        intent["action"] = intent["deal_action"]
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
    }


def seed(req: dict) -> dict:
    return {
        "objective": req.get("objective") or "",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or ["Acme Corp", "Acme Supplies"],
        "partial": bool(req.get("partial")),
        "steps": 0,
    }
