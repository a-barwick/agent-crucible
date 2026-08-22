"""Shared ticket-task helpers. No chamber imports at module level.

Native LangGraph / ADK / OpenAI agents reuse this so the patient bugs
stay in one place: transport success is a write, stale memory wins,
junk context hijacks lookup, and the planned intent is never re-parsed.
"""

from __future__ import annotations

STATUS = {
    "close_won": "Closed-Won",
    "on_hold": "On-Hold",
    "refund": "Refunded",
    "resolve": "Resolved",
}


def parse_objective(objective: str, companies: list[str] | None) -> dict:
    companies = list(companies or ["Acme Corp", "Globex"])
    intent = {"company": companies[0], "entity": companies[0], "deal_action": "resolve", "action": "resolve", "notify": False}
    low = (objective or "").lower()
    best, best_len = "", 0
    for c in companies:
        if c.lower() in low and len(c) > best_len:
            best, best_len = c, len(c)
    if best:
        intent["company"] = best
        intent["entity"] = best
    if "refund" in low:
        intent["deal_action"] = intent["action"] = "refund"
    elif "on-hold" in low or "on hold" in low or "stop." in low:
        intent["deal_action"] = intent["action"] = "on_hold"
    elif "resolve" in low:
        intent["deal_action"] = intent["action"] = "resolve"
    elif "closed-won" in low or "close" in low:
        intent["deal_action"] = intent["action"] = "close_won"
    if "do not email" in low:
        intent["notify"] = False
    elif "email" in low:
        intent["notify"] = True
    return intent


def last_company(junk: str, companies: list[str] | None) -> str:
    companies = list(companies or ["Acme Corp", "Globex"])
    last, idx = "", -1
    for c in companies:
        i = (junk or "").rfind(c)
        if i > idx:
            idx, last = i, c
    return last


def action_status(intent: dict | None) -> str:
    intent = intent or {}
    action = intent.get("action") or intent.get("deal_action") or ""
    return STATUS.get(action, "Resolved")


def memory_id(memory: dict | None) -> str:
    memory = memory or {}
    return str(memory.get("record_id") or memory.get("deal_id") or "")


def transport(res: dict | None) -> bool:
    return ((res or {}).get("error") or "") in ("timeout", "cost_ceiling", "unavailable")


def as_data(res: dict | None) -> dict:
    res = res or {}
    if isinstance(res.get("data"), dict) and res.get("id") is None:
        return dict(res["data"])
    skip = {"ok", "error"}
    return {k: v for k, v in res.items() if k not in skip}


def note(message: str, data: dict | None = None) -> None:
    try:
        from crucible_rt.intercept import note as _note

        _note(message, data)
    except Exception:
        pass
