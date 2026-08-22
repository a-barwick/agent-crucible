"""Shared ticket-task helpers. No chamber imports at module level.

Native LangGraph / ADK / OpenAI / react agents reuse this so the patient
bugs stay in one place: transport success is a write, stale memory wins,
junk context hijacks lookup, and the planned intent is never re-parsed.

http_json is a real urllib call. Outside the chamber it hits the network
(or raises). Inside the chamber, crucible_rt.httpio rewrites it.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request

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


def http_json(method: str, url: str, params: dict | None = None, body: dict | None = None, headers: dict | None = None) -> dict:
    """Call a JSON HTTP API. The chamber intercepts this at urllib."""
    if params:
        url = url + ("&" if "?" in url else "?") + urllib.parse.urlencode(params)
    hdrs = {"Accept": "application/json", "Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            raw = resp.read().decode() or "{}"
            return json.loads(raw)
    except urllib.error.HTTPError as e:
        raw = e.read().decode() if e.fp else ""
        try:
            parsed = json.loads(raw or "{}")
        except json.JSONDecodeError:
            parsed = {"ok": False, "error": f"http_{e.code}"}
        if not parsed.get("error"):
            if e.code == 403:
                parsed["error"] = "permission_denied"
            elif e.code in (408, 504, 598):
                parsed["error"] = "timeout"
            else:
                parsed["error"] = f"http_{e.code}"
        parsed.setdefault("ok", False)
        return parsed
    except Exception as e:
        text = str(e).lower()
        err = "timeout" if "time" in text or "timeout" in type(e).__name__.lower() else "unavailable"
        return {"ok": False, "error": err}


def note(message: str, data: dict | None = None) -> None:
    try:
        from crucible_rt.intercept import note as _note

        _note(message, data)
    except Exception:
        pass
