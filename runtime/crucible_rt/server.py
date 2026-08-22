import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

from . import adk_closer, langgraph_closer
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
            if kind == "adk":
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
        from .model import CloserPlanner

        m = CloserPlanner(companies=["Acme Corp"], partial=False)
        msg = m.invoke("Update the Acme Corp deal to Closed-Won and email the account executive.")
        print("langgraph-ok", msg.content)
        return
    addr = "127.0.0.1:8091"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    httpd = HTTPServer((host, int(port)), Handler)
    print(f"crucible-rt listening on {addr}", flush=True)
    httpd.serve_forever()
