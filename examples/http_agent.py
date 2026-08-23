#!/usr/bin/env python3
"""An arbitrary tool-using process the chamber can torture.

Speaks POST /v1/run. Tools callback into the chamber. This is not the
sidecar compiler — it loads examples/ticket_graph.py and runs that graph.
"""

from __future__ import annotations

import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(ROOT, "runtime"))

from crucible_rt.callback import Callback, CallbackError  # noqa: E402
from ticket_graph import run as run_graph  # noqa: E402

# A run request carries a spec and fixtures: kilobytes. Reading whatever
# Content-Length claims lets one request buy the whole heap.
MAX_BODY = 4 << 20


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("http-agent: " + (fmt % args) + "\n")

    def _write(self, code: int, body) -> None:
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path in ("/health", "/", "/v1/meta"):
            self._write(200, {"ok": True, "entry": "examples/ticket_graph.py", "process": True})
            return
        self._write(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/v1/run":
            self._write(404, {"error": "not found"})
            return
        try:
            n = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            self._write(400, {"error": "bad content-length"})
            return
        if n > MAX_BODY:
            self._write(413, {"error": "body too large"})
            return
        try:
            req = json.loads(self.rfile.read(n).decode() or "{}")
        except (json.JSONDecodeError, UnicodeDecodeError):
            self._write(400, {"error": "bad json"})
            return
        cb = Callback(req.get("callback") or "", req.get("token") or "")
        try:
            out = run_graph(cb, req)
            out["process"] = True
            self._write(200, out)
        except CallbackError as e:  # pragma: no cover
            # The chamber is unreachable, which is not a verdict on this agent.
            # Reporting it as an abort let a broken harness be scored as an
            # agent that gave up. 502 plus chamber_error is what the runner
            # reads as errored.
            self._write(502, {"chamber_error": True, "error": str(e)})
        except Exception as e:  # pragma: no cover
            # The agent's own code raised, which *is* a verdict: an agent that
            # crashes mid-run has failed the trial.
            self._write(200, {
                "terminal": "abort",
                "intent": {},
                "claimed": {"error": "agent raised " + type(e).__name__},
                "steps": 0,
                "process": True,
                "agent_error": str(e),
            })


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    addr = "127.0.0.1:8092"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    httpd = HTTPServer((host, int(port)), Handler)
    print(f"http-agent listening on {addr}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
