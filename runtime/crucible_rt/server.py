import json
import sys
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
            # Report what is importable, not what the sidecar wishes were
            # installed. The Go side reads `adk` to decide whether to offer ADK
            # agents, and a hardcoded true offered agents that could not run.
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
        _smoke_callback_errors()
        return
    addr = "127.0.0.1:8091"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    # Threaded: an agent under test may call the sidecar's own OpenAI proxy while
    # /v1/run is still on the stack, and a single-threaded server would sit and
    # wait for itself.
    httpd = ThreadingHTTPServer((host, int(port)), Handler)
    httpd.daemon_threads = True
    print(f"crucible-rt listening on {addr}", flush=True)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.server_close()


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
