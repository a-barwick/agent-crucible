import json
import re


def parse_intent(objective: str, companies: list[str] | None = None) -> dict:
    companies = companies or ["Acme Corp", "Acme Supplies"]
    intent = {"company": companies[0], "deal_action": "close_won", "notify": True}
    low = objective.lower()
    best, best_len = "", 0
    for c in companies:
        if c.lower() in low and len(c) > best_len:
            best, best_len = c, len(c)
    if best:
        intent["company"] = best
    elif "acme" in low:
        for c in companies:
            if c == "Acme Corp":
                intent["company"] = c
                break
    if "refund" in low:
        intent["deal_action"] = "refund"
        intent["notify"] = False
    elif "on-hold" in low or "on hold" in low or "do not close" in low or "stop." in low:
        intent["deal_action"] = "on_hold"
    elif "closed-won" in low or "close" in low:
        intent["deal_action"] = "close_won"
    else:
        intent["deal_action"] = "none"
    if "do not email" in low:
        intent["notify"] = False
    elif "email" in low:
        intent["notify"] = True
    return intent


def parse_model_intent(text: str, fallback: str, companies: list[str] | None) -> dict:
    text = (text or "").strip()
    i, j = text.find("{"), text.rfind("}")
    if i >= 0 and j > i:
        text = text[i : j + 1]
    try:
        data = json.loads(text)
        if isinstance(data, dict) and (data.get("company") or data.get("deal_action")):
            if not data.get("company"):
                data["company"] = parse_intent(fallback, companies)["company"]
            data.setdefault("notify", False)
            return data
    except json.JSONDecodeError:
        pass
    return parse_intent(fallback, companies)


def last_company(junk: str, companies: list[str] | None) -> str:
    companies = companies or ["Acme Corp", "Acme Supplies"]
    last, idx = "", -1
    for c in companies:
        i = junk.rfind(c)
        if i > idx:
            idx, last = i, c
    return last
