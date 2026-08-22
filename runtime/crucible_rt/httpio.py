"""Intercept HTTP/SDK I/O so unmodified tool bodies hit FaultBus.

A graph that calls requests / httpx / urllib / simple_salesforce (or JS
fetch) inside a closure is not a discovered @tool object. After the
sidecar imports the agent this module patches those libraries so the
live call is rewritten into cb.tool. The chamber callback itself is
never intercepted.

URL → tool mapping uses spec.tools[].http when present, then the
current wrapped tool name, then host/path heuristics.

The library patches are process-wide and installed once. Which chamber a
patched call routes to is per-run: a session is registered for the thread
that installs it, and a thread the agent spawns resolves to it as long as
only one run is in flight. Two concurrent runs each keep their own tool
names, hit counts and snapshot, so one trial cannot read another's writes.
"""

from __future__ import annotations

import builtins
import io
import json
import threading
import urllib.error
import urllib.parse
import urllib.request
from contextlib import contextmanager
from typing import Any

_lock = threading.Lock()
_tls = threading.local()

_installed = False
_orig_urlopen = None
_orig_import = None
_patched: set[str] = set()

# Sessions in flight, keyed by the thread that installed them, and every
# callback base URL we have ever been handed. The URL set outlives the session
# on purpose: a worker thread with no session of its own must still recognise
# the chamber's own endpoints, or its callback POST would be intercepted as if
# it were a tool call and recurse.
_sessions: dict[int, "_Session"] = {}
_bases: set[str] = set()


class _Session:
    def __init__(self, cb, spec: dict | None):
        self.cb = cb
        self.spec = spec or {}
        self.hits = 0
        self.last: dict | None = None
        self.store = _empty_store()


def _empty_store() -> dict:
    return {"calls": [], "wrote": False, "record_id": "", "status": "", "error": ""}


def _session() -> "_Session | None":
    s = _sessions.get(threading.get_ident())
    if s is not None:
        return s
    # A thread the agent spawned. With one run in flight there is no ambiguity
    # about which chamber it belongs to; with several there is, and guessing
    # would credit one trial's write to another.
    with _lock:
        if len(_sessions) == 1:
            return next(iter(_sessions.values()))
    return None


def active() -> bool:
    return _installed and _session() is not None


def _guard(url: Any) -> None:
    """Refuse a call this process cannot attribute to a trial.

    A thread the agent spawned has no session of its own. With one run in flight
    _session falls back to it; with several, there is nothing to fall back to, and
    the call used to be forwarded to the real network — a scored trial reaching
    the internet, and a write nobody could see in the trace. This is the chamber
    failing to contain the run, so it is raised as a chamber error rather than
    handed to the agent as a tool failure.
    """
    if not _installed or _session() is not None or is_passthrough(str(url)):
        return
    with _lock:
        n = len(_sessions)
    if n < 2:
        # Nothing is running: no trial to attribute this to and no verdict to
        # corrupt, so it is left alone.
        return
    from .callback import CallbackError

    raise CallbackError(
        f"cannot tell which trial this call belongs to: {n} runs are in flight and "
        f"the thread that made it registered none. Refusing to send it to the real "
        f"network ({str(url)[:120]})"
    )


def hits() -> int:
    s = _session()
    return s.hits if s else 0


def last_result() -> dict | None:
    s = _session()
    return s.last if s else None


def current_tool() -> str | None:
    return getattr(_tls, "tool", None)


@contextmanager
def using_tool(name: str):
    prev = getattr(_tls, "tool", None)
    _tls.tool = name
    try:
        yield
    finally:
        _tls.tool = prev


def snapshot() -> dict:
    s = _session()
    store = s.store if s else _empty_store()
    return {
        "calls": list(store.get("calls") or []),
        "wrote": bool(store.get("wrote")),
        "record_id": store.get("record_id") or "",
        "status": store.get("status") or "",
        "error": store.get("error") or "",
        "hits": s.hits if s else 0,
    }


def reset_snapshot() -> None:
    s = _session()
    if s is not None:
        s.store = _empty_store()
        s.hits = 0
        s.last = None


def _store() -> dict:
    s = _session()
    return s.store if s else _empty_store()


def is_passthrough(url: str) -> bool:
    if not url:
        return False
    text = str(url)
    for base in tuple(_bases):
        if base and text.startswith(base):
            return True
    low = text.lower()
    if "/before_node" in low or low.endswith("/tool") or "/state" in low or "/v1/run" in low:
        if "127.0.0.1" in low or "localhost" in low or "[::1]" in low:
            return True
    return False


def install(cb, spec: dict | None = None) -> None:
    """Patch HTTP/SDK libraries so they call into FaultBus."""
    global _installed, _orig_urlopen, _orig_import
    base = str(getattr(cb, "url", "") or "").rstrip("/")
    with _lock:
        _sessions[threading.get_ident()] = _Session(cb, spec)
        if base:
            _bases.add(base)
        if _installed:
            _patch_optional()
            return
        _orig_urlopen = urllib.request.urlopen
        urllib.request.urlopen = _urlopen  # type: ignore[assignment]
        if _orig_import is None:
            _orig_import = builtins.__import__
            builtins.__import__ = _import  # type: ignore[assignment]
        _installed = True
        _patch_optional()


def install_from_env(cb=None, spec: dict | None = None) -> None:
    """Used by crucible_rt.boot for a foreign process."""
    import os

    from .callback import Callback

    if cb is None:
        cb = Callback(os.environ.get("CRUCIBLE_CALLBACK") or "", os.environ.get("CRUCIBLE_TOKEN") or "")
    if spec is None:
        path = os.environ.get("CRUCIBLE_REQ") or ""
        if path:
            try:
                req = json.loads(open(path, encoding="utf-8").read() or "{}")
                spec = req.get("spec") or {}
            except Exception:
                spec = {}
    install(cb, spec)
    hook = {}
    if hasattr(cb, "before"):
        try:
            hook = cb.before("plan") or {}
        except Exception:
            hook = {}
    path = os.environ.get("CRUCIBLE_REQ") or ""
    if path and hook:
        try:
            req = json.loads(open(path, encoding="utf-8").read() or "{}")
            if hook.get("objective"):
                req["objective"] = hook["objective"]
            if "partial" in hook:
                req["partial"] = hook["partial"]
            if hook.get("memory"):
                req["memory"] = hook["memory"]
            if hook.get("junk") is not None:
                req["junk"] = hook["junk"]
            open(path, "w", encoding="utf-8").write(json.dumps(req))
        except Exception:
            pass


def clear() -> None:
    """Drop this thread's session, leaving the library patches in place.

    A run ends by clearing, not uninstalling: the patched functions are shared
    with any other run in flight, and restoring the originals under it would let
    its next tool call out onto the real network.
    """
    with _lock:
        _sessions.pop(threading.get_ident(), None)


def uninstall() -> None:
    """Restore the real libraries. Only safe when no run is in flight.

    _patched is deliberately not cleared. The optional-library patches replace a
    method with a closure over the previous one, so re-patching an already
    patched method stacks another layer on every install — forty trials, forty
    nested wrappers.
    """
    global _installed, _orig_urlopen, _orig_import
    with _lock:
        if _orig_urlopen is not None:
            urllib.request.urlopen = _orig_urlopen
            _orig_urlopen = None
        if _orig_import is not None:
            builtins.__import__ = _orig_import
            _orig_import = None
        _installed = False
        _sessions.clear()


def _import(name, globals=None, locals=None, fromlist=(), level=0):
    mod = _orig_import(name, globals, locals, fromlist, level)
    root = (name or "").split(".", 1)[0]
    if root in ("requests", "httpx", "simple_salesforce") and _installed:
        _patch_optional()
    return mod


def _patch_optional() -> None:
    _patch_requests()
    _patch_httpx()
    _patch_salesforce()


def dispatch(method: str, url: str, headers: dict | None = None, body: Any = None) -> tuple[int, dict]:
    """Map one HTTP call onto FaultBus and return (status, json body)."""
    sess = _session()
    tool, args = map_request(method, url, body, sess.spec if sess else {})
    if sess is None or sess.cb is None:
        raise RuntimeError(f"live call to {url} was not intercepted: {args}")
    cb = sess.cb
    if current_tool() is None and hasattr(cb, "before"):
        try:
            cb.before(tool)
        except Exception:
            pass
    res = cb.tool(tool, args) if hasattr(cb, "tool") else {"ok": False, "error": "no_callback"}
    sess.hits += 1
    sess.last = res
    _note(tool, args, res)
    _emit(cb, tool, res)
    from .intercept import present

    payload = present(res if isinstance(res, dict) else {"ok": False, "error": "bad_result"})
    return _status(res), payload


def _status(res: dict | None) -> int:
    err = ((res or {}).get("error") or "")
    if err == "permission_denied":
        return 403
    if err in ("timeout", "cost_ceiling", "unavailable"):
        return 504
    if err and not (res or {}).get("ok"):
        return 400
    return 200


def _note(tool: str, args: dict, res: dict) -> None:
    store = _store()
    store["calls"].append({"tool": tool, "args": dict(args or {}), "ok": bool((res or {}).get("ok"))})
    err = (res or {}).get("error") or ""
    if err:
        store["error"] = err
    data = (res or {}).get("data")
    data = data if isinstance(data, dict) else {}
    rid = data.get("id") or (args or {}).get("id") or (args or {}).get("record_id") or ""
    if rid:
        store["record_id"] = str(rid)
    status = data.get("status") or (args or {}).get("status") or ""
    if status:
        store["status"] = str(status)
    if _writeish(tool) and err not in ("timeout", "cost_ceiling", "unavailable"):
        store["wrote"] = True


def _emit(cb, name: str, res: dict) -> None:
    if cb is None or not hasattr(cb, "state"):
        return
    err = (res or {}).get("error") or ""
    data = (res or {}).get("data")
    empty = data is None or data == {} or data == []
    if err == "permission_denied":
        try:
            cb.state("write ignored permission_denied", {"tool": name})
        except Exception:
            pass
    if (res or {}).get("ok") and empty and _writeish(name):
        try:
            cb.state("write accepted empty success payload", {"tool": name})
        except Exception:
            pass


def _writeish(name: str) -> bool:
    n = (name or "").lower()
    return any(p in n for p in ("write", "update", "patch", "create", "delete", "refund", "upsert"))


def map_request(method: str, url: str, body: Any, spec: dict | None) -> tuple[str, dict]:
    parsed = urllib.parse.urlparse(url or "")
    path = parsed.path or "/"
    query = dict(urllib.parse.parse_qsl(parsed.query))
    payload = _decode_body(body)
    tool = current_tool() or match_spec(spec, method, parsed, path) or heuristic(method, parsed.netloc, path, spec)
    args = extract_args(tool, spec, parsed, path, query, payload)
    return tool, args


def match_spec(spec: dict | None, method: str, parsed: urllib.parse.ParseResult, path: str) -> str:
    host = (parsed.netloc or "").lower()
    url = (parsed.geturl() if hasattr(parsed, "geturl") else "") or (host + path)
    for t in (spec or {}).get("tools") or []:
        if not isinstance(t, dict):
            continue
        bind = t.get("http") or {}
        if not bind:
            continue
        m = (bind.get("method") or "").upper()
        if m and m != (method or "").upper():
            continue
        bh = (bind.get("host") or "").lower()
        if bh and bh not in host:
            continue
        needle = bind.get("match") or bind.get("path") or ""
        if needle and needle not in path and needle not in url:
            continue
        if needle or bh or m:
            return t.get("name") or ""
    return ""


def heuristic(method: str, host: str, path: str, spec: dict | None = None) -> str:
    host = (host or "").lower()
    path = (path or "").lower()
    method = (method or "GET").upper()
    names = _spec_names(spec)
    if "tickets.example" in host or "ticket" in host:
        if method in ("POST", "PUT", "PATCH", "DELETE") or "/tickets/" in path:
            return _prefer(names, "update_ticket", write=True)
        return _prefer(names, "search_ticket", write=False)
    if "salesforce" in host or "force.com" in host or host.endswith("crm.example"):
        if method in ("POST", "PUT", "PATCH", "DELETE"):
            return _prefer(names, "write_deal", write=True)
        return _prefer(names, "lookup_contact", write=False)
    if any(p in path for p in ("/search", "/query", "/lookup", "/find")):
        return _prefer(names, "search_ticket", write=False)
    if method in ("POST", "PUT", "PATCH", "DELETE"):
        return _prefer(names, "update_ticket", write=True)
    return _prefer(names, "search_ticket", write=False)


def _spec_names(spec: dict | None) -> list[str]:
    out = []
    for t in (spec or {}).get("tools") or []:
        n = t.get("name") if isinstance(t, dict) else str(t)
        if n:
            out.append(n)
    return out


def _prefer(names: list[str], default: str, write: bool) -> str:
    if default in names:
        return default
    for n in names:
        if _writeish(n) == write:
            return n
    return names[0] if names else default


def extract_args(tool: str, spec: dict | None, parsed: urllib.parse.ParseResult, path: str, query: dict, payload: dict) -> dict:
    args: dict[str, Any] = {}
    args.update(query)
    if "q" in args and "query" not in args:
        args["query"] = args["q"]
    if "company" in args and "query" not in args:
        args["query"] = args["company"]
    if isinstance(payload, dict):
        args.update(payload)
    parts = [p for p in (path or "").split("/") if p]
    if parts and parts[-1] not in ("search", "query", "lookup", "tickets", "deals", "sobjects"):
        if "id" not in args:
            args["id"] = parts[-1]
    bind = {}
    for t in (spec or {}).get("tools") or []:
        if isinstance(t, dict) and t.get("name") == tool:
            bind = t.get("http") or {}
            break
    mapping = bind.get("args") or {}
    remapped = dict(args)
    for dest, src in mapping.items():
        if src == "$path" or src == "path":
            if parts:
                remapped[dest] = parts[-1]
        elif src in args:
            remapped[dest] = args[src]
        elif src in query:
            remapped[dest] = query[src]
        elif isinstance(payload, dict) and src in payload:
            remapped[dest] = payload[src]
    return remapped


def _decode_body(body: Any) -> dict:
    if body is None:
        return {}
    if isinstance(body, dict):
        return body
    raw = body
    if isinstance(body, bytes):
        raw = body.decode("utf-8", "replace")
    if isinstance(raw, str):
        text = raw.strip()
        if not text:
            return {}
        try:
            data = json.loads(text)
            return data if isinstance(data, dict) else {"data": data}
        except json.JSONDecodeError:
            return dict(urllib.parse.parse_qsl(text))
    return {}


class _FakeResponse:
    def __init__(self, status: int, body: bytes, url: str):
        self.status = status
        self.code = status
        self._body = body
        self.fp = io.BytesIO(body)
        self.reason = "OK" if status < 400 else "Error"
        self.length = len(body)
        self.version = 11
        self.url = url
        self.headers = _Headers({"Content-Type": "application/json", "Content-Length": str(len(body))})
        self.msg = self.headers

    def read(self, amt=None):
        return self.fp.read() if amt is None else self.fp.read(amt)

    def read1(self, n=-1):
        return self.read(n)

    def readline(self, limit=-1):
        return self.fp.readline(limit)

    def getcode(self):
        return self.status

    def geturl(self):
        return self.url

    def info(self):
        return self.headers

    def getheader(self, name, default=None):
        return self.headers.get(name, default)

    def getheaders(self):
        return list(self.headers.items())

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def close(self):
        pass


class _Headers(dict):
    def get_content_type(self):
        return self.get("Content-Type", "application/json")

    def get_content_charset(self):
        return "utf-8"


_SENTINEL = object()


def _urlopen(url, data=None, timeout=_SENTINEL, *args, **kwargs):
    if isinstance(url, urllib.request.Request):
        method = url.get_method()
        full = url.full_url
        data = url.data if data is None else data
        headers = dict(url.header_items())
    else:
        method = "GET" if data is None else "POST"
        full = str(url)
        headers = {}
    _guard(full)
    if _orig_urlopen is not None and (is_passthrough(full) or not active()):
        # Forward positionally and only pass timeout when the caller did.
        # `_orig(url, data=data, timeout=timeout, *args)` looks like it forwards
        # everything and in fact binds args to data, and defaulting timeout to
        # None turns urlopen's default into "block forever".
        if timeout is _SENTINEL:
            return _orig_urlopen(url, data, *args, **kwargs)
        return _orig_urlopen(url, data, timeout, *args, **kwargs)
    status, payload = dispatch(method, full, headers, data)
    raw = json.dumps(payload).encode()
    if status >= 400:
        raise urllib.error.HTTPError(full, status, payload.get("error") or "error", _Headers({"Content-Type": "application/json"}), io.BytesIO(raw))
    resp = _FakeResponse(status, raw, full)
    return resp


def _patch_requests() -> None:
    if "requests" in _patched:
        return
    try:
        import requests
        import requests.sessions
    except Exception:
        return
    orig = requests.sessions.Session.request
    if getattr(orig, "_crucible_patched", False):
        _patched.add("requests")
        return

    def request(session, method, url, **kwargs):
        _guard(url)
        if not active() or is_passthrough(str(url)):
            return orig(session, method, url, **kwargs)
        body = kwargs.get("json")
        if body is None:
            body = kwargs.get("data")
        if kwargs.get("params"):
            url = str(url) + ("&" if "?" in str(url) else "?") + urllib.parse.urlencode(kwargs["params"], doseq=True)
        status, payload = dispatch(method, str(url), kwargs.get("headers"), body)
        return _requests_response(requests, status, payload, str(url))

    request._crucible_patched = True  # type: ignore[attr-defined]
    requests.sessions.Session.request = request  # type: ignore[assignment]
    _patched.add("requests")


def _requests_response(requests, status: int, payload: dict, url: str):
    raw = json.dumps(payload).encode()
    resp = requests.models.Response()
    resp.status_code = status
    resp._content = raw
    resp.url = url
    resp.headers["Content-Type"] = "application/json"
    resp.encoding = "utf-8"
    resp.reason = "OK" if status < 400 else "Error"
    return resp


def _patch_httpx() -> None:
    if "httpx" in _patched:
        return
    try:
        import httpx
    except Exception:
        return
    orig_sync = httpx.Client.send
    if getattr(orig_sync, "_crucible_patched", False):
        _patched.add("httpx")
        return

    def _send(self, request, **kwargs):
        url = str(request.url)
        _guard(url)
        if not active() or is_passthrough(url):
            return orig_sync(self, request, **kwargs)
        status, payload = dispatch(request.method, url, dict(request.headers), request.content)
        return httpx.Response(status, json=payload, request=request)

    try:
        _send._crucible_patched = True  # type: ignore[attr-defined]
        httpx.Client.send = _send  # type: ignore[assignment]
        _patched.add("httpx")
    except Exception:
        return
    try:
        orig_async = httpx.AsyncClient.send
        if getattr(orig_async, "_crucible_patched", False):
            return

        async def _asend(self, request, **kwargs):
            url = str(request.url)
            _guard(url)
            if not active() or is_passthrough(url):
                return await orig_async(self, request, **kwargs)
            status, payload = dispatch(request.method, url, dict(request.headers), request.content)
            return httpx.Response(status, json=payload, request=request)

        _asend._crucible_patched = True  # type: ignore[attr-defined]
        httpx.AsyncClient.send = _asend  # type: ignore[assignment]
    except Exception:
        pass


def _patch_salesforce() -> None:
    if "simple_salesforce" in _patched:
        return
    try:
        import simple_salesforce
    except Exception:
        return
    SF = getattr(simple_salesforce, "Salesforce", None)
    if SF is None or getattr(SF, "_crucible_patched", False):
        return
    orig_query = getattr(SF, "query", None)
    orig_restful = getattr(SF, "restful", None)

    def query(self, q, *a, **k):
        if not active() and callable(orig_query):
            return orig_query(self, q, *a, **k)
        status, payload = dispatch("GET", "https://example.my.salesforce.com/services/data/v59.0/query", None, {"q": q, "query": q})
        if "records" not in payload:
            rec = {k: v for k, v in payload.items() if k not in ("ok", "error")}
            payload = {"records": [rec] if rec else [], "totalSize": 1 if rec else 0, "done": True, **payload}
        return payload

    def restful(self, path="", method="GET", params=None, **kwargs):
        if not active() and callable(orig_restful):
            return orig_restful(self, path, method, params, **kwargs)
        url = "https://example.my.salesforce.com/services/data/v59.0/" + str(path or "")
        status, payload = dispatch(method, url, None, params or kwargs.get("json") or kwargs.get("data"))
        return payload

    try:
        SF.query = query
        if orig_restful is not None:
            SF.restful = restful
        SF._crucible_patched = True  # type: ignore[attr-defined]
        _patched.add("simple_salesforce")
    except Exception:
        return

    # SFType.update / get used as sf.Ticket.update(id, data)
    try:
        SFType = getattr(simple_salesforce, "SFType", None)
        if SFType is None:
            return
        orig_get = getattr(SFType, "get", None)
        orig_update = getattr(SFType, "update", None)

        def get(self, record_id, *a, **k):
            if not active() and callable(orig_get):
                return orig_get(self, record_id, *a, **k)
            url = f"https://example.my.salesforce.com/services/data/v59.0/sobjects/{getattr(self, 'name', 'Ticket')}/{record_id}"
            _, payload = dispatch("GET", url, None, {"id": record_id})
            return payload

        def update(self, record_id, data=None, *a, **k):
            if not active() and callable(orig_update):
                return orig_update(self, record_id, data, *a, **k)
            body = dict(data or {})
            body.setdefault("id", record_id)
            url = f"https://example.my.salesforce.com/services/data/v59.0/sobjects/{getattr(self, 'name', 'Ticket')}/{record_id}"
            _, payload = dispatch("PATCH", url, None, body)
            return payload

        SFType.get = get
        SFType.update = update
    except Exception:
        return
