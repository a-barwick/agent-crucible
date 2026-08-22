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
            })
            return
        self._write(404, {"error": "not found"})

    def do_POST(self):
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
        return
    addr = "127.0.0.1:8091"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    httpd = HTTPServer((host, int(port)), Handler)
    print(f"crucible-rt listening on {addr}", flush=True)
    httpd.serve_forever()
