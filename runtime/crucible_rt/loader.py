"""Load a user-written agent file and run it.

Chamber-aware files still export run(cb, req). Unmodified files export
run(req), build(), graph, or an ADK agent — their @tool / FunctionTool /
OpenAI dispatch tables are wrapped so tools hit FaultBus.
"""

from __future__ import annotations

import importlib.util
import os
import sys
from typing import Any

from . import intercept


def has_entry(req: dict) -> bool:
    spec = req.get("spec") or {}
    return bool(req.get("entry") or spec.get("entry"))


def resolve_entry(entry: str) -> str:
    if not entry:
        raise FileNotFoundError("empty entry")
    if os.path.isfile(entry):
        return os.path.abspath(entry)
    here = os.path.dirname(os.path.abspath(__file__))
    roots = [
        os.getcwd(),
        os.path.join(here, "..", ".."),
        os.path.join(here, ".."),
        os.path.join(here, "..", "..", "examples"),
        os.path.join(here, "..", "examples"),
    ]
    names = [entry, os.path.basename(entry)]
    for root in roots:
        for name in names:
            cand = os.path.normpath(os.path.join(root, name))
            if os.path.isfile(cand):
                return cand
            cand2 = os.path.normpath(os.path.join(root, "examples", os.path.basename(name)))
            if os.path.isfile(cand2):
                return cand2
    raise FileNotFoundError(entry)


def load_module(entry: str):
    path = resolve_entry(entry)
    folder = os.path.dirname(path)
    if folder and folder not in sys.path:
        sys.path.insert(0, folder)
    name = "crucible_entry_" + os.path.splitext(os.path.basename(path))[0]
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise ImportError(f"cannot load {path}")
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


def run(cb, req: dict) -> dict:
    spec = req.get("spec") or {}
    entry = req.get("entry") or spec.get("entry")
    export = req.get("export") or spec.get("export") or ""
    mod = load_module(entry)
    intercept.wrap_module(mod, cb, spec)

    if export:
        obj = getattr(mod, export, None)
        if obj is None:
            raise AttributeError(f"{entry} has no export {export!r}")
        if export == "build" or getattr(obj, "__name__", "") == "build":
            return _invoke_graph(_call_build(obj, cb), req, cb)
        if callable(obj):
            return _call_run(obj, cb, req)

    if callable(getattr(mod, "run", None)):
        return _call_run(mod.run, cb, req)
    if callable(getattr(mod, "build", None)):
        return _invoke_graph(_call_build(mod.build, cb), req, cb)
    for attr in ("graph", "app", "compiled"):
        if getattr(mod, attr, None) is not None:
            return _invoke_graph(getattr(mod, attr), req, cb)
    if getattr(mod, "root_agent", None) is not None or getattr(mod, "agent", None) is not None:
        raise RuntimeError(f"{entry} exported an agent but no run() — add run(req)")
    raise RuntimeError(f"{entry} has no run/build/graph export")


def _call_build(fn, cb):
    if intercept.takes_callback(fn):
        try:
            return fn(cb)
        except TypeError:
            return fn()
    try:
        return fn()
    except TypeError:
        return fn(cb)


def _call_run(fn, cb, req: dict) -> dict:
    if intercept.takes_callback(fn):
        return _normalize(fn(cb, req), req)
    hooked = intercept.apply_plan_hook(cb, req)
    return _normalize(fn(hooked), hooked)


def _invoke_graph(graph: Any, req: dict, cb=None) -> dict:
    if cb is not None and not intercept.takes_callback(getattr(graph, "invoke", None) or (lambda: None)):
        req = intercept.apply_plan_hook(cb, req)
    thread = req.get("thread_id") or "t"
    config = {"configurable": {"thread_id": thread}}
    if _wants_messages(graph):
        seed = {"messages": [("user", req.get("objective") or "")]}
        try:
            result = graph.invoke(seed, config)
        except TypeError:
            result = graph.invoke(seed)
        return _normalize(result, req)
    seed = {
        "objective": req.get("objective") or "",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or [],
        "partial": bool(req.get("partial")),
        "steps": 0,
    }
    try:
        result = graph.invoke(seed, config)
    except TypeError:
        result = graph.invoke(seed)
    return _normalize(result, req)


def _wants_messages(graph: Any) -> bool:
    getter = getattr(graph, "get_input_jsonschema", None)
    if callable(getter):
        try:
            js = getter() or {}
            props = js.get("properties") or {}
            return "messages" in props and "objective" not in props
        except Exception:
            return False
    schema = getattr(graph, "input_schema", None)
    names = getattr(schema, "model_fields", None) or getattr(schema, "__annotations__", None) or {}
    return "messages" in names and "objective" not in names


def _normalize(out: Any, req: dict) -> dict:
    if isinstance(out, dict) and "claimed" in out:
        out.setdefault("runtime", req.get("runtime") or "langgraph")
        out.setdefault("checkpoint", True)
        intent = out.get("intent") or {}
        if isinstance(intent, dict):
            if intent.get("company") and not intent.get("entity"):
                intent["entity"] = intent["company"]
            if intent.get("deal_action") and not intent.get("action"):
                intent["action"] = intent["deal_action"]
            claimed = out.get("claimed") or {}
            rid = claimed.get("record_id") or claimed.get("deal_id") or ""
            if rid and not claimed.get("record_id"):
                claimed["record_id"] = rid
            if not claimed.get("record_id"):
                io = intercept.claim_from_io(claimed)
                claimed["record_id"] = claimed.get("record_id") or io.get("record_id") or ""
                claimed["deal_id"] = claimed.get("deal_id") or io.get("deal_id") or ""
                if not claimed.get("status"):
                    claimed["status"] = io.get("status") or ""
            out["intent"] = intent
            out["claimed"] = claimed
        return out
    if isinstance(out, dict) and out.get("messages") is not None:
        return finish_messages(out, req)
    from . import patient

    if not isinstance(out, dict):
        raise TypeError("agent entry must return a dict")
    filled = patient.finish(out, req.get("runtime") or "langgraph")
    claimed = filled.get("claimed") or {}
    if not claimed.get("record_id") and not claimed.get("wrote"):
        filled["claimed"] = intercept.claim_from_io(claimed)
    return filled


def finish_messages(out: dict, req: dict) -> dict:
    """Turn a create_react_agent {messages} blob into a chamber result."""
    import json as _json

    claimed = intercept.claim_from_io()
    steps = 0
    for m in out.get("messages") or []:
        steps += 1
        name = getattr(m, "name", None) or (m.get("name") if isinstance(m, dict) else "")
        content = getattr(m, "content", None)
        if content is None and isinstance(m, dict):
            content = m.get("content")
        tool_calls = getattr(m, "tool_calls", None) or (m.get("tool_calls") if isinstance(m, dict) else None) or []
        if tool_calls:
            for call in tool_calls:
                fn = call.get("name") or (call.get("function") or {}).get("name") or ""
                args = call.get("args") or {}
                if intercept._writeish(fn):
                    claimed["wrote"] = True
                    if isinstance(args, dict) and args.get("status"):
                        claimed["status"] = args.get("status") or claimed.get("status") or ""
                    if isinstance(args, dict) and args.get("id"):
                        claimed["record_id"] = args.get("id") or claimed.get("record_id") or ""
                        claimed["deal_id"] = claimed["record_id"]
        if name and intercept._writeish(str(name)):
            claimed["wrote"] = True
        if isinstance(content, str) and content.startswith("{"):
            try:
                data = _json.loads(content)
            except Exception:
                data = {}
            if isinstance(data, dict):
                if data.get("id"):
                    claimed["record_id"] = data["id"]
                    claimed["deal_id"] = data["id"]
                if data.get("status"):
                    claimed["status"] = data["status"]
                if data.get("error") in ("timeout", "cost_ceiling", "unavailable"):
                    claimed["error"] = data["error"]
    intent = out.get("intent") or {}
    if not intent:
        from .intent import parse_intent

        intent = parse_intent(req.get("objective") or "", req.get("companies"))
    err = claimed.get("error") or ""
    terminal = "abort" if err in ("timeout", "cost_ceiling", "unavailable") and not claimed.get("wrote") else "end"
    return {
        "terminal": terminal,
        "intent": intent,
        "claimed": claimed,
        "steps": max(steps, 1),
        "checkpoint": True,
        "runtime": req.get("runtime") or "langgraph",
        "react": True,
    }
