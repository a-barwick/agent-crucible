#!/usr/bin/env python3
"""A foreign process that does not know the chamber exists.

No Callback import. No @tool. No /v1/run server. It reads a JSON
request (argv or OBJECTIVE), urllibs the ticket HTTP API, and prints
a result. The chamber wraps the process with crucible_rt.boot so those
HTTP calls hit FaultBus.
"""

from __future__ import annotations

import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
if HERE not in sys.path:
    sys.path.insert(0, HERE)

from ticket_logic import action_status, as_data, http_json, last_company, memory_id, parse_objective, transport


def execute(req: dict) -> dict:
    objective = req.get("objective") or os.environ.get("OBJECTIVE") or "Resolve the Acme Corp ticket."
    companies = req.get("companies") or ["Acme Corp", "Globex"]
    memory = req.get("memory") or {}
    junk = req.get("junk") or ""
    intent = parse_objective(objective, companies)
    query = intent.get("entity") or intent.get("company") or ""
    hijack = last_company(junk, companies)
    if hijack and hijack != query:
        query = hijack
    found = http_json("GET", "http://tickets.example/search", params={"q": query})
    if transport(found):
        return _result(intent, error=found.get("error") or "timeout")
    data = as_data(found)
    tid = data.get("id") or ""
    status = data.get("status") or ""
    mid = memory_id(memory)
    if mid:
        tid = mid
        if memory.get("deal_status"):
            status = memory["deal_status"]
    want = action_status(intent)
    wrote = http_json("POST", "http://tickets.example/tickets/" + str(tid), body={"status": want})
    if transport(wrote):
        return _result(intent, error=wrote.get("error") or "timeout", record_id=tid, status=status)
    did = as_data(wrote).get("id") or tid
    return _result(intent, wrote=True, record_id=did, status=want)


def _result(intent, wrote=False, record_id="", status="", error=""):
    return {
        "terminal": "abort" if error and not wrote else "end",
        "intent": intent,
        "claimed": {
            "wrote": bool(wrote),
            "notified": False,
            "deal_id": record_id,
            "record_id": record_id,
            "status": status,
            "error": error,
        },
        "steps": 3,
        "checkpoint": True,
        "runtime": "wrap",
        "entry": "examples/foreign_task.py",
    }


def run(req: dict) -> dict:
    """Sidecar entry. Same body as the process — HTTP only."""
    return execute(req)


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    req = {}
    path = argv[0] if argv and os.path.isfile(argv[0]) else os.environ.get("CRUCIBLE_REQ") or ""
    if path and os.path.isfile(path):
        req = json.loads(open(path, encoding="utf-8").read() or "{}")
    out = execute(req)
    dest = os.environ.get("CRUCIBLE_RESULT") or ""
    raw = json.dumps(out)
    if dest:
        open(dest, "w", encoding="utf-8").write(raw)
    else:
        sys.stdout.write(raw + "\n")


if __name__ == "__main__":
    main()
