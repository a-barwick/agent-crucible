import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

from . import adk_closer, langgraph_closer, loader
from .callback import Callback


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("crucible-rt: " + (fmt % args) + "\n")

    def _write(self, code: int, body) -> None:
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path in ("/health", "/"):
            self._write(200, {"ok": True})
            return
        if self.path == "/v1/meta":
            self._write(200, {
                "langgraph": True,
                "adk": True,
                "google_adk": adk_closer.HAS_ADK,
                "intercept": True,
                "httpio": True,
                "openai": True,
            })
            return
        self._write(404, {"error": "not found"})

    def do_POST(self):
        if self.path in ("/v1/chat/completions", "/chat/completions"):
            n = int(self.headers.get("Content-Length") or 0)
            try:
                body = json.loads(self.rfile.read(n).decode() or "{}")
            except json.JSONDecodeError:
                self._write(400, {"error": "bad json"})
                return
            from . import openai_proxy

            self._write(200, openai_proxy.complete(body))
            return
        if self.path != "/v1/run":
            self._write(404, {"error": "not found"})
            return
        n = int(self.headers.get("Content-Length") or 0)
        try:
            req = json.loads(self.rfile.read(n).decode() or "{}")
        except json.JSONDecodeError:
            self._write(400, {"error": "bad json"})
            return
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
        except Exception as e:  # pragma: no cover
            self._write(500, {"error": str(e), "terminal": "abort", "claimed": {"error": str(e)}})


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
        return
    addr = "127.0.0.1:8091"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    httpd = HTTPServer((host, int(port)), Handler)
    print(f"crucible-rt listening on {addr}", flush=True)
    httpd.serve_forever()
