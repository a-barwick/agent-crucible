"""Wrap an unmodified agent's own tools so they hit the chamber.

The user writes @tool / FunctionTool / OpenAI functions / JS callables
that call HTTP or Salesforce. After import this module:

1. Patches urllib / requests / httpx / simple_salesforce so live I/O
   inside a tool *or* an ordinary closure hits FaultBus.
2. Wraps discovered tool objects. The original body runs first so the
   HTTP patch is what fires; if the body never touches the network the
   wrap falls back to cb.tool (same as before).

The graph never imports the world and never has to call cb.retry_tool.
before() is fired on each wrapped tool (and once on "plan") so the
nine-fault catalog still applies.
"""

from __future__ import annotations

import inspect
import threading
from typing import Any

_tls = threading.local()


def current_cb():
    return getattr(_tls, "cb", None)


def bind_cb(cb) -> None:
    _tls.cb = cb


def clear_cb() -> None:
    _tls.cb = None


def is_langchain_tool(obj: Any) -> bool:
    if obj is None or inspect.isclass(obj):
        return False
    return (
        hasattr(obj, "invoke")
        and hasattr(obj, "name")
        and (hasattr(obj, "func") or hasattr(obj, "_run"))
    )


def is_adk_tool(obj: Any) -> bool:
    if obj is None or inspect.isclass(obj):
        return False
    cls = obj.__class__.__name__
    return cls in ("FunctionTool", "LongRunningFunctionTool") or (
        hasattr(obj, "func") and hasattr(obj, "name") and callable(obj.func) and not is_langchain_tool(obj)
    )


def takes_callback(fn) -> bool:
    try:
        sig = inspect.signature(fn)
    except (TypeError, ValueError):
        return False
    names = [
        p.name
        for p in sig.parameters.values()
        if p.kind not in (inspect.Parameter.VAR_KEYWORD, inspect.Parameter.VAR_POSITIONAL)
    ]
    if not names:
        return False
    return names[0] in ("cb", "callback", "chamber") or "cb" in names


def present(res: dict) -> dict:
    if not isinstance(res, dict):
        return {"ok": False, "error": "bad_result"}
    err = res.get("error") or ""
    data = res.get("data")
    out: dict[str, Any] = {}
    if isinstance(data, dict):
        out.update(data)
    elif data is not None:
        out["data"] = data
    out["ok"] = bool(res.get("ok")) and not err
    if err:
        out["error"] = err
        out["ok"] = False
    return out


def _bind_args(fn, args, kwargs) -> dict:
    if len(args) == 1 and isinstance(args[0], dict) and not kwargs:
        return dict(args[0])
    out = dict(kwargs)
    try:
        sig = inspect.signature(fn)
        names = [
            p.name
            for p in sig.parameters.values()
            if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD)
            and p.name not in ("self", "cls")
        ]
        for i, a in enumerate(args):
            if i < len(names):
                out[names[i]] = a
    except (TypeError, ValueError):
        if args and not out:
            out["arg"] = args[0] if len(args) == 1 else list(args)
    return out


def _writeish(name: str) -> bool:
    n = (name or "").lower()
    return any(p in n for p in ("write", "update", "patch", "create", "delete", "refund", "upsert"))


def _emit_evidence(cb, name: str, res: dict) -> None:
    if cb is None or not hasattr(cb, "state"):
        return
    err = (res or {}).get("error") or ""
    data = (res or {}).get("data")
    empty = data is None or data == {} or data == []
    # Evidence is a side note. If the chamber cannot be reached the tool result
    # still stands, and raising here would turn a lost log line into a failure.
    try:
        if err == "permission_denied":
            cb.state("write ignored permission_denied", {"tool": name})
        if (res or {}).get("ok") and empty and _writeish(name):
            cb.state("write accepted empty success payload", {"tool": name})
    except Exception:
        pass


def wrap_callable(fn, name: str, cb=None):
    if fn is None or getattr(fn, "_crucible_wrapped", False):
        return fn

    def wrapped(*args, **kwargs):
        from . import httpio

        # Resolve the chamber at call time, not at wrap time. A tool object that
        # lives in a module the entry file imports is wrapped once and cached by
        # sys.modules for the whole suite; a captured callback would send every
        # later trial's tool calls to the first trial's bus and token.
        chamber = current_cb() or cb
        payload = _bind_args(fn, args, kwargs)
        if hasattr(chamber, "before"):
            chamber.before(name)
        if httpio.active():
            before = httpio.hits()
            try:
                with httpio.using_tool(name):
                    out = fn(*args, **kwargs)
            except Exception as e:
                if httpio.hits() > before:
                    raise
                # The body failed before it reached the network, so the chamber
                # answers instead. Say so: silently substituting a synthetic
                # success made the agent's own crash invisible in the timeline
                # and scored the trial as though the tool had worked.
                if hasattr(chamber, "state"):
                    try:
                        chamber.state(
                            "tool body raised before any I/O; chamber answered instead",
                            {"tool": name, "error": f"{type(e).__name__}: {e}"},
                        )
                    except Exception:
                        pass
            else:
                if httpio.hits() > before:
                    return out if out is not None else present(httpio.last_result() or {})
        res = chamber.tool(name, payload) if hasattr(chamber, "tool") else {}
        _emit_evidence(chamber, name, res)
        return present(res)

    wrapped._crucible_wrapped = True  # type: ignore[attr-defined]
    wrapped._crucible_name = name  # type: ignore[attr-defined]
    wrapped.__name__ = getattr(fn, "__name__", name)
    wrapped.__doc__ = getattr(fn, "__doc__", "") or ""
    return wrapped


def wrap_tool(obj: Any, cb, name: str | None = None) -> Any:
    name = name or getattr(obj, "name", None) or getattr(obj, "__name__", "tool")
    if is_langchain_tool(obj) or (hasattr(obj, "func") and callable(getattr(obj, "func", None))):
        fn = getattr(obj, "func", None)
        if callable(fn) and not getattr(fn, "_crucible_wrapped", False):
            obj.func = wrap_callable(fn, name, cb)
        coro = getattr(obj, "coroutine", None)
        if callable(coro) and not getattr(coro, "_crucible_wrapped", False):
            sync = wrap_callable(coro, name, cb)

            async def acoro(*a, **k):
                return sync(*a, **k)

            acoro._crucible_wrapped = True  # type: ignore[attr-defined]
            obj.coroutine = acoro
        return obj
    if callable(obj):
        return wrap_callable(obj, name, cb)
    return obj


def spec_tool_names(spec: dict | None) -> set[str]:
    names: set[str] = set()
    for t in (spec or {}).get("tools") or []:
        n = t.get("name") if isinstance(t, dict) else str(t)
        if n:
            names.add(n)
    return names


def discover_tools(mod, spec: dict | None = None) -> list[tuple[str, Any]]:
    found: list[tuple[str, Any]] = []
    seen: set[int] = set()

    def add(obj: Any, name: str | None = None) -> None:
        if obj is None or inspect.isclass(obj):
            return
        oid = id(obj)
        if oid in seen:
            return
        seen.add(oid)
        found.append((name or getattr(obj, "name", None) or getattr(obj, "__name__", "") or "tool", obj))

    for attr in ("tools", "TOOLS", "tool_list"):
        obj = getattr(mod, attr, None)
        if isinstance(obj, (list, tuple)):
            for t in obj:
                add(t, getattr(t, "name", None))
        elif isinstance(obj, dict):
            for k, v in obj.items():
                add(v, k if isinstance(k, str) else None)

    for attr in ("FUNCTIONS", "DISPATCH", "TOOL_FUNCS", "handlers"):
        obj = getattr(mod, attr, None)
        if isinstance(obj, dict):
            for k, v in obj.items():
                if callable(v):
                    add(v, k if isinstance(k, str) else None)

    names = spec_tool_names(spec)
    for aname, val in vars(mod).items():
        if aname.startswith("_"):
            continue
        if is_langchain_tool(val) or is_adk_tool(val):
            add(val, getattr(val, "name", aname))
        elif aname in names and callable(val):
            add(val, aname)
    return found


def wrap_module(mod, cb, spec: dict | None = None) -> list[str]:
    bind_cb(cb)
    from . import httpio

    httpio.install(cb, spec)
    wrapped: list[str] = []
    for name, obj in discover_tools(mod, spec):
        new = wrap_tool(obj, cb, name)
        if new is not obj:
            for aname, aval in list(vars(mod).items()):
                if aval is obj:
                    setattr(mod, aname, new)
        wrapped.append(name)

    for attr in ("FUNCTIONS", "DISPATCH", "TOOL_FUNCS", "handlers", "tools", "TOOLS"):
        obj = getattr(mod, attr, None)
        if isinstance(obj, dict):
            for k, v in list(obj.items()):
                if callable(v) and not getattr(v, "_crucible_wrapped", False):
                    obj[k] = wrap_callable(v, k, cb)
                    wrapped.append(k)
        elif isinstance(obj, list):
            for i, v in enumerate(obj):
                wrap_tool(v, cb, getattr(v, "name", None))
    return wrapped


def apply_plan_hook(cb, req: dict) -> dict:
    hook = {}
    if hasattr(cb, "before"):
        hook = cb.before("plan") or {}
    out = dict(req)
    if hook.get("objective"):
        out["objective"] = hook["objective"]
    if "partial" in hook:
        out["partial"] = hook["partial"]
    if hook.get("memory"):
        out["memory"] = hook["memory"]
    if hook.get("junk") is not None:
        out["junk"] = hook["junk"]
    return out


def note(message: str, data: dict | None = None) -> None:
    """Optional evidence from a patient agent. No-op outside the chamber."""
    cb = current_cb()
    if cb is not None and hasattr(cb, "state") and message:
        try:
            cb.state(message, data or {})
        except Exception:
            pass


def claim_from_io(extra: dict | None = None) -> dict:
    """Build a claimed blob from intercepted HTTP/tool I/O."""
    from . import httpio

    snap = httpio.snapshot()
    extra = extra or {}
    rid = extra.get("record_id") or extra.get("deal_id") or snap.get("record_id") or ""
    err = extra.get("error") or snap.get("error") or ""
    return {
        "wrote": bool(extra.get("wrote") or snap.get("wrote")),
        "notified": bool(extra.get("notified")),
        "deal_id": rid,
        "record_id": rid,
        "status": extra.get("status") or snap.get("status") or "",
        "error": err,
    }
