import json
import os
import sys
import threading
import time
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from . import adk_closer, langgraph_closer, loader
from .callback import Callback, CallbackError

# A pasted spec with fixtures is a few kilobytes. Reading Content-Length
# unbounded lets one request buy the whole heap.
MAX_BODY = 4 << 20


def _detail(e: BaseException) -> str:
    """Type, message, and the last frames. `str(e)` alone is empty for a bare
    KeyError and says nothing about where a drop-in agent broke."""
    tb = traceback.format_exception(type(e), e, e.__traceback__)
    tail = "".join(tb[-4:]).strip()
    return f"{type(e).__name__}: {e}\n{tail}" if tail else f"{type(e).__name__}: {e}"


def _have(mod: str) -> bool:
    import importlib.util

    try:
        return importlib.util.find_spec(mod) is not None
    except (ImportError, ValueError):
        return False


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("crucible-rt: " + (fmt % args) + "\n")

    def _write(self, code: int, body) -> None:
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _read_json(self):
        """Return the decoded body, or None after answering with an error."""
        try:
            n = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            self._write(400, {"error": "bad content-length"})
            return None
        if n < 0 or n > MAX_BODY:
            self._write(413, {"error": f"body over {MAX_BODY} bytes"})
            return None
        raw = self.rfile.read(n) if n else b""
        try:
            body = json.loads(raw.decode("utf-8", "replace") or "{}")
        except json.JSONDecodeError as e:
            self._write(400, {"error": f"bad json: {e}"})
            return None
        if not isinstance(body, dict):
            self._write(400, {"error": "body must be a JSON object"})
            return None
        return body

    def do_GET(self):
        if self.path in ("/health", "/"):
            self._write(200, {"ok": True})
            return
        if self.path == "/v1/meta":
            # Each key answers "can this sidecar serve that?", which is not the
            # same question as "is the third-party package installed". The Go
            # side reads them to decide which agents to offer, so a key that
            # reported the package would hide agents this sidecar can run.
            #
            # adk is a capability: adk_closer runs the ADK-shaped loop whether or
            # not google-adk is importable, and google_adk reports which of the
            # two is in use. Optional HTTP clients are reported as found, since
            # an agent written against requests genuinely cannot run without it.
            self._write(200, {
                "langgraph": _have("langgraph"),
                "adk": True,
                "google_adk": adk_closer.HAS_ADK,
                "intercept": True,
                "httpio": True,
                "openai": True,
                "requests": _have("requests"),
                "httpx": _have("httpx"),
            })
            return
        self._write(404, {"error": "not found"})

    def do_POST(self):
        if self.path in ("/v1/chat/completions", "/chat/completions"):
            body = self._read_json()
            if body is None:
                return
            from . import openai_proxy

            try:
                self._write(200, openai_proxy.complete(body))
            except Exception as e:
                self._write(500, {"error": _detail(e)})
            return
        if self.path != "/v1/run":
            self._write(404, {"error": "not found"})
            return
        req = self._read_json()
        if req is None:
            return
        self._run(req)

    def _run(self, req: dict) -> None:
        from . import httpio, intercept

        cb = Callback(req.get("callback") or "", req.get("token") or "")
        kind = (req.get("runtime") or "langgraph").lower()
        try:
            if loader.has_entry(req):
                out = loader.run(cb, req)
            elif kind == "adk":
                out = adk_closer.run(cb, req)
            else:
                out = langgraph_closer.run(cb, req)
            self._write(200, out)
        except (loader.EntryError, CallbackError) as e:
            # The chamber could not run this trial. Say so explicitly: the Go
            # side must not fold it into the agent's score.
            self._write(502, {"chamber_error": True, "error": _detail(e)})
        except Exception as e:
            # The agent's own code raised. That is a verdict, not a chamber
            # failure — an agent that crashes mid-write has failed the trial —
            # so it comes back as an abort with the reason attached.
            detail = _detail(e)
            sys.stderr.write("crucible-rt: agent raised: " + detail + "\n")
            self._write(200, {
                "terminal": "abort",
                "intent": {},
                "claimed": {"error": "agent raised " + type(e).__name__},
                "steps": 0,
                "runtime": kind,
                "agent_error": detail,
            })
        finally:
            # Whatever happened, this thread is no longer running an agent.
            # Leaving the session registered would let the next thing the
            # sidecar does over HTTP be charged to a finished trial.
            httpio.clear()
            intercept.clear_cb()


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    if argv and argv[0] == "smoke":
        from langgraph.graph import StateGraph  # noqa: F401
        from . import generic, loader
        from .intent import parse_intent
        from .model import CloserPlanner

        m = CloserPlanner(companies=["Acme Corp"], partial=False)
        msg = m.invoke("Update the Acme Corp deal to Closed-Won and email the account executive.")
        print("langgraph-ok", msg.content)
        spec = {
            "name": "ticket-bot",
            "tools": [
                {"name": "search_ticket", "required": ["query"]},
                {"name": "update_ticket", "required": ["id", "status"]},
            ],
            "companies": ["Acme Corp", "Globex"],
        }
        if not generic.should_compile(spec):
            raise SystemExit("generic should compile ticket spec")
        g = generic.graph_from_spec(spec)
        if "search_ticket" not in g["nodes"] or "update_ticket" not in g["nodes"]:
            raise SystemExit("generic graph missing tools")
        intent = parse_intent("Resolve the Acme Corp ticket.", ["Acme Corp", "Globex"])
        if intent.get("deal_action") != "resolve":
            raise SystemExit("intent resolve: %s" % intent)

        class _Fake:
            def before(self, name):
                return {}

            def retry_tool(self, name, args):
                if name == "search_ticket":
                    return {"ok": True, "data": {"id": "tkt-acme", "status": "Open", "company": "Acme Corp"}}
                if name == "update_ticket":
                    return {"ok": True, "data": {"id": args.get("id"), "status": args.get("status")}}
                return {"ok": False, "error": "unknown"}

        from .patient import seed

        state = generic.walk(
            _Fake(),
            seed({"objective": "Resolve the Acme Corp ticket.", "companies": ["Acme Corp", "Globex"]}),
            spec,
        )
        if not state.get("wrote") or state.get("status") != "Resolved":
            raise SystemExit("generic walk: %s" % state)
        print("generic-ok", state.get("deal_id"), state.get("status"))
        path = loader.resolve_entry("examples/ticket_graph.py")
        if not path.endswith("ticket_graph.py"):
            raise SystemExit("entry resolve: %s" % path)
        print("loader-ok", path)

        from . import intercept
        from langchain_core.tools import tool as lc_tool

        hits = []

        class _Spy:
            def before(self, name):
                hits.append(("before", name))
                return {}

            def tool(self, name, args):
                hits.append(("tool", name, args))
                if name == "search_ticket":
                    return {"ok": True, "data": {"id": "tkt-acme", "status": "Open", "company": "Acme Corp"}}
                if name == "update_ticket":
                    return {"ok": True, "data": {"id": args.get("id"), "status": args.get("status")}}
                return {"ok": False, "error": "unknown"}

            def state(self, message, data=None):
                hits.append(("state", message))
                return {"ok": True}

        @lc_tool
        def search_ticket(query: str) -> dict:
            """Search tickets. Used only to prove intercept wraps @tool."""
            raise RuntimeError("live search was not intercepted")

        spy = _Spy()
        intercept.wrap_tool(search_ticket, spy, "search_ticket")
        got = search_ticket.invoke({"query": "Acme Corp"})
        if got.get("id") != "tkt-acme":
            raise SystemExit("intercept invoke: %s" % got)
        if not any(h[0] == "tool" and h[1] == "search_ticket" for h in hits):
            raise SystemExit("intercept missed tool call: %s" % hits)
        print("intercept-ok", got.get("id"))

        native = loader.resolve_entry("examples/native_ticket.py")
        if not native.endswith("native_ticket.py"):
            raise SystemExit("native resolve: %s" % native)
        raw = open(native, encoding="utf-8").read()
        if "cb.retry_tool" in raw or "cb.before" in raw or "from crucible_rt.callback" in raw:
            raise SystemExit("native_ticket.py is still chamber-aware")
        if "tickets.example" not in raw or "http_json" not in raw:
            raise SystemExit("native_ticket.py does not call the ticket HTTP API")
        print("native-ok", native)

        from . import httpio
        from . import openai_proxy

        hits = []

        class _HTTPSpy:
            def before(self, name):
                hits.append(("before", name))
                return {}

            def tool(self, name, args):
                hits.append(("http", name, args))
                if name == "search_ticket":
                    return {"ok": True, "data": {"id": "tkt-acme", "status": "Open", "company": "Acme Corp"}}
                if name == "update_ticket":
                    return {"ok": True, "data": {"id": args.get("id"), "status": args.get("status")}}
                return {"ok": False, "error": "unknown"}

            def state(self, message, data=None):
                hits.append(("state", message))
                return {"ok": True}

        spy = _HTTPSpy()
        spec = {"tools": [{"name": "search_ticket", "http": {"host": "tickets.example", "match": "/search", "method": "GET"}}, {"name": "update_ticket", "http": {"host": "tickets.example", "match": "/tickets/", "method": "POST"}}]}
        httpio.install(spy, spec)
        try:
            import urllib.request

            with urllib.request.urlopen("http://tickets.example/search?q=Acme+Corp", timeout=2) as resp:
                body = json.loads(resp.read().decode())
            if body.get("id") != "tkt-acme":
                raise SystemExit("httpio urlopen: %s" % body)
            if not any(h[0] == "http" and h[1] == "search_ticket" for h in hits):
                raise SystemExit("httpio missed urllib: %s" % hits)
            try:
                import requests

                r = requests.get("http://tickets.example/search", params={"q": "Acme Corp"}, timeout=2)
                if r.json().get("id") != "tkt-acme":
                    raise SystemExit("httpio requests: %s" % r.text)
                print("httpio-requests-ok", r.json().get("id"))
            except ImportError:
                print("httpio-requests-skip")
            try:
                import httpx

                r = httpx.get("http://tickets.example/search", params={"q": "Acme Corp"}, timeout=2)
                if r.json().get("id") != "tkt-acme":
                    raise SystemExit("httpio httpx: %s" % r.text)
                print("httpio-httpx-ok", r.json().get("id"))
            except ImportError:
                print("httpio-httpx-skip")
            print("httpio-ok", body.get("id"))
        finally:
            httpio.uninstall()

        chat = openai_proxy.complete({
            "messages": [{"role": "user", "content": "Resolve the Acme Corp ticket."}],
            "tools": [{"type": "function", "function": {"name": "search_ticket"}}],
        })
        calls = (((chat.get("choices") or [{}])[0].get("message") or {}).get("tool_calls") or [])
        if not calls or calls[0]["function"]["name"] != "search_ticket":
            raise SystemExit("openai proxy: %s" % chat)
        # The same request must answer identically, ids included. uuid4 and
        # time.time() gave the agent a different call id and timestamp on every
        # replay of the same trial.
        again = openai_proxy.complete({
            "messages": [{"role": "user", "content": "Resolve the Acme Corp ticket."}],
            "tools": [{"type": "function", "function": {"name": "search_ticket"}}],
        })
        if again != chat:
            raise SystemExit("openai proxy is not deterministic:\n%s\n%s" % (chat, again))
        print("openai-proxy-ok", calls[0]["function"]["name"])

        closure = loader.resolve_entry("examples/http_closure.py")
        raw_c = open(closure, encoding="utf-8").read()
        if "\n@tool" in raw_c or "TOOLS =" in raw_c or "cb.retry_tool" in raw_c:
            raise SystemExit("http_closure.py still has tool objects or a callback")
        print("closure-ok", closure)

        react = loader.resolve_entry("examples/native_react.py")
        raw_r = open(react, encoding="utf-8").read()
        if "create_react_agent" not in raw_r or "cb.retry_tool" in raw_r:
            raise SystemExit("native_react.py is not a create_react_agent drop-in")
        print("react-ok", react)

        _smoke_wrapper_honesty()
        _smoke_no_escape_to_the_network()
        _smoke_chamber_error_propagates()
        _smoke_tools_list()
        _smoke_async_tools()
        _smoke_callback_errors()
        return
    addr = "127.0.0.1:8091"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    parent = 0
    if "--parent-pid" in argv:
        parent = int(argv[argv.index("--parent-pid") + 1])
    host, port = addr.rsplit(":", 1)
    # Threaded: an agent under test may call the sidecar's own OpenAI proxy while
    # /v1/run is still on the stack, and a single-threaded server would sit and
    # wait for itself.
    httpd = ThreadingHTTPServer((host, int(port)), Handler)
    httpd.daemon_threads = True
    if parent > 0:
        threading.Thread(target=_watch_parent, args=(parent, httpd), daemon=True).start()
    print(f"crucible-rt listening on {addr}", flush=True)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.server_close()


def _watch_parent(parent: int, httpd) -> None:
    """Exit when the runner that started us does.

    The runner kills its sidecars on the way out, but it does not always get
    the chance: SIGKILL, a panic, or a Ctrl-C that skips the cleanup path all
    leave this process reparented to init, holding a port and serving the code
    it happened to start with. Comparing getppid() to the pid we were handed is
    immune to pid reuse -- once it differs, our parent is gone whoever adopted
    us."""
    while True:
        time.sleep(1.0)
        if os.getppid() == parent:
            continue
        # Announce it only if anyone can still hear. stdout is a pipe to the
        # parent, so by now writing it usually raises BrokenPipeError, and an
        # exception here used to kill this thread before the shutdown below --
        # leaving exactly the orphan it exists to prevent.
        try:
            print(f"crucible-rt: parent {parent} exited, shutting down", flush=True)
        except OSError:
            pass
        httpd.shutdown()
        return


def _smoke_wrapper_honesty() -> None:
    """A wrapped tool must not hide the agent's own crash, and must route to
    whichever chamber is live now rather than the one present when it was
    wrapped. Both were silent before: a tool body that raised produced a
    synthetic success, and a tool cached in sys.modules kept trial 1's token
    for the whole suite."""
    from . import intercept

    class _Chamber:
        def __init__(self, tag):
            self.tag = tag
            self.states: list[tuple[str, dict]] = []
            self.calls: list[str] = []

        def before(self, name):
            return {}

        def tool(self, name, args):
            self.calls.append(name)
            return {"ok": True, "data": {"id": self.tag}}

        def state(self, message, data=None):
            self.states.append((message, data or {}))
            return {"ok": True}

    def broken(query: str) -> dict:
        raise KeyError("credentials")

    first, second = _Chamber("one"), _Chamber("two")
    wrapped = intercept.wrap_callable(broken, "search_ticket", first)

    from . import httpio

    httpio.install(first, {"tools": [{"name": "search_ticket"}]})
    try:
        intercept.bind_cb(first)
        got = wrapped(query="Acme Corp")
        if got.get("id") != "one":
            raise SystemExit("wrapper did not fall back to the chamber: %s" % got)
        if not any("tool body raised" in m for m, _ in first.states):
            raise SystemExit("wrapper swallowed the tool body's exception: %s" % first.states)

        intercept.bind_cb(second)
        got = wrapped(query="Acme Corp")
        if got.get("id") != "two":
            raise SystemExit("wrapper kept a stale chamber: %s" % got)
    finally:
        intercept.clear_cb()
        httpio.uninstall()
    print("wrapper-ok", first.calls, second.calls)


def _smoke_no_escape_to_the_network() -> None:
    """Two suites can be in flight against one sidecar at once — two browser tabs
    are enough. A thread the agent spawned registers no session of its own, and
    with more than one to choose from the patched libraries used to hand the call
    to the real network: a scored trial talking to the internet, with the call
    absent from the trace. It has to refuse instead."""
    import threading

    from . import httpio
    from .callback import CallbackError

    class _Chamber:
        def __init__(self, tag):
            self.url = "http://127.0.0.1:9/" + tag

        def before(self, name):
            return {}

        def tool(self, name, args):
            return {"ok": True, "data": {"id": "tkt-" + name}}

        def state(self, message, data=None):
            return {"ok": True}

    spec = {"tools": [{
        "name": "search_ticket",
        "http": {"host": "tickets.example", "match": "/search", "method": "GET"},
    }]}
    outcome: dict[str, object] = {}

    def worker():
        """A thread with no session of its own, like one an agent spawns."""
        import urllib.request

        try:
            with urllib.request.urlopen("http://tickets.example/search?q=Acme", timeout=2) as r:
                outcome["body"] = json.loads(r.read().decode())
        except BaseException as e:  # noqa: BLE001 - the class is the assertion
            outcome["raised"] = e

    def hold(sess_ready, release, tag):
        httpio.install(_Chamber(tag), spec)
        sess_ready.set()
        release.wait(10)
        httpio.clear()

    # One run in flight: a spawned thread is unambiguous and is intercepted.
    httpio.install(_Chamber("one"), spec)
    try:
        t = threading.Thread(target=worker)
        t.start()
        t.join(10)
        if outcome.get("raised") is not None or (outcome.get("body") or {}).get("id") != "tkt-search_ticket":
            raise SystemExit("single run: spawned thread was not intercepted: %s" % outcome)

        # A second run in flight: now there is nothing to fall back to.
        ready, release = threading.Event(), threading.Event()
        other = threading.Thread(target=hold, args=(ready, release, "two"))
        other.start()
        try:
            if not ready.wait(10):
                raise SystemExit("second session never registered")
            outcome.clear()
            t = threading.Thread(target=worker)
            t.start()
            t.join(10)
        finally:
            release.set()
            other.join(10)
        if not isinstance(outcome.get("raised"), CallbackError):
            raise SystemExit("two runs in flight: call escaped to the network: %s" % outcome)
    finally:
        httpio.clear()
        httpio.uninstall()
    print("no-escape-ok")


def _smoke_chamber_error_propagates() -> None:
    """An unreachable chamber must not come back as a tool failure, at any layer.
    ticket_logic.http_json caught everything urllib raised and returned
    {"ok": false, "error": "unavailable"}, and the tool wrapper answered for a
    body that raised, so a dead chamber was scored as the agent mishandling a
    tool at exactly the moment the harness had broken."""
    import os
    import sys

    from . import httpio, intercept
    from .callback import Callback, CallbackError

    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ex = os.path.join(root, "examples")
    if ex not in sys.path:
        sys.path.insert(0, ex)
    import ticket_logic

    # A chamber at a closed port: every callback raises CallbackError.
    dead = Callback("http://127.0.0.1:1", "tok", timeout=1.0)
    spec = {"tools": [{
        "name": "search_ticket",
        "http": {"host": "tickets.example", "match": "/search", "method": "GET"},
    }]}
    httpio.install(dead, spec)
    try:
        intercept.bind_cb(dead)
        try:
            ticket_logic.http_json("GET", "http://tickets.example/search?q=Acme")
        except CallbackError:
            pass
        else:
            raise SystemExit("http_json turned a dead chamber into a tool result")

        def search_ticket(query: str) -> dict:
            """Tool whose body talks to the intercepted API."""
            return ticket_logic.http_json("GET", "http://tickets.example/search?q=" + query)

        wrapped = intercept.wrap_callable(search_ticket, "search_ticket", dead)
        try:
            wrapped(query="Acme Corp")
        except CallbackError:
            pass
        else:
            raise SystemExit("the tool wrapper answered for an unreachable chamber")
    finally:
        intercept.clear_cb()
        httpio.clear()
        httpio.uninstall()
    print("chamber-error-propagates-ok")


def _smoke_tools_list() -> None:
    """An agent may export plain functions in a `tools` list rather than @tool
    objects. wrap_module patched the tool objects in place but threw away the
    wrapper it built for a plain function, so the list still held the raw body
    and those tools ran with no chamber at all."""
    import types

    from . import httpio, intercept

    calls: list[str] = []

    def search_ticket(query: str) -> dict:
        """Plain function, no decorator."""
        return {"id": "tkt-acme"}

    class _Chamber:
        def before(self, name):
            calls.append(name)
            return {}

        def tool(self, name, args):
            calls.append("tool:" + name)
            return {"ok": True, "data": {"id": "tkt-acme"}}

        def state(self, message, data=None):
            return {"ok": True}

    mod = types.ModuleType("smoke_tools_list")
    mod.tools = [search_ticket]
    try:
        names = intercept.wrap_module(mod, _Chamber(), {"tools": [{"name": "search_ticket"}]})
        entry = mod.tools[0]
        if not getattr(entry, "_crucible_wrapped", False):
            raise SystemExit("tools list still holds the unwrapped body")
        entry("Acme Corp")
    finally:
        httpio.clear()
        intercept.clear_cb()
        httpio.uninstall()
    if "search_ticket" not in names:
        raise SystemExit("wrapped tool not reported: %s" % names)
    if "search_ticket" not in calls:
        raise SystemExit("wrapped tool did not reach the chamber: %s" % calls)
    print("tools-list-ok", names)


def _smoke_async_tools() -> None:
    """An async tool body has to be awaited. Calling a coroutine function from a
    synchronous wrapper only built a coroutine object: the body never ran, no I/O
    was recorded, and so every async tool fell through to the chamber's synthetic
    answer while appearing to have worked."""
    import asyncio

    from . import httpio, intercept

    try:
        from langchain_core.tools import tool
    except ImportError:
        print("async-tools-skip")
        return

    ran: list[str] = []

    @tool
    async def search_ticket(query: str) -> dict:
        """Search tickets over HTTP."""
        ran.append(query)
        import urllib.parse
        import urllib.request

        url = "http://tickets.example/search?q=" + urllib.parse.quote(query)
        with urllib.request.urlopen(url, timeout=2) as resp:
            return json.loads(resp.read().decode())

    class _Chamber:
        def __init__(self):
            self.calls: list[str] = []

        def before(self, name):
            return {}

        def tool(self, name, args):
            self.calls.append(name)
            return {"ok": True, "data": {"id": "tkt-acme", "status": "Open"}}

        def state(self, message, data=None):
            return {"ok": True}

    cb = _Chamber()
    spec = {"tools": [{
        "name": "search_ticket",
        "http": {"host": "tickets.example", "match": "/search", "method": "GET"},
    }]}
    httpio.install(cb, spec)
    try:
        intercept.bind_cb(cb)
        wrapped = intercept.wrap_tool(search_ticket, cb, "search_ticket")
        got = asyncio.run(wrapped.ainvoke({"query": "Acme Corp"}))
    finally:
        intercept.clear_cb()
        httpio.clear()
        httpio.uninstall()
    if not ran:
        raise SystemExit("async tool body never ran; the chamber answered for it")
    if (got or {}).get("id") != "tkt-acme":
        raise SystemExit("async tool did not return its own intercepted result: %s" % got)
    if cb.calls != ["search_ticket"]:
        raise SystemExit("async tool I/O did not reach the chamber once: %s" % cb.calls)
    print("async-tools-ok", ran, got.get("id"))


def _smoke_callback_errors() -> None:
    """An unreachable chamber must raise, not return a tool error. Handing the
    agent {"ok": false} for a dead socket let a harness failure be scored as
    the agent mishandling a tool."""
    from .callback import Callback, CallbackError

    cb = Callback("http://127.0.0.1:1", "tok", timeout=1.0)
    try:
        cb.tool("search_ticket", {"query": "Acme Corp"})
    except CallbackError as e:
        print("callback-error-ok", str(e)[:60])
    else:
        raise SystemExit("unreachable chamber returned a tool result")
    try:
        Callback("", "").before("plan")
    except CallbackError:
        print("callback-nourl-ok")
    else:
        raise SystemExit("empty callback url did not raise")
