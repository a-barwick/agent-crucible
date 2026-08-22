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
    seed = {
        "objective": req.get("objective") or "",
        "memory": req.get("memory") or {},
        "junk": req.get("junk") or "",
        "companies": req.get("companies") or [],
        "partial": bool(req.get("partial")),
        "steps": 0,
    }
    thread = req.get("thread_id") or "t"
    try:
        result = graph.invoke(seed, {"configurable": {"thread_id": thread}})
    except TypeError:
        result = graph.invoke(seed)
    return _normalize(result, req)


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
            out["intent"] = intent
            out["claimed"] = claimed
        return out
    from . import patient

    if not isinstance(out, dict):
        raise TypeError("agent entry must return a dict")
    return patient.finish(out, req.get("runtime") or "langgraph")
