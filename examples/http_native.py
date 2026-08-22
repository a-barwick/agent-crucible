#!/usr/bin/env python3
"""A foreign process that loads an unmodified agent file.

The process speaks POST /v1/run. The agent (native_ticket.py) still
does not import Callback — the loader wraps its @tool objects.
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

from crucible_rt.callback import Callback  # noqa: E402
from crucible_rt.loader import run as load_run  # noqa: E402


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("http-native: " + (fmt % args) + "\n")

    def _write(self, code: int, body) -> None:
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path in ("/health", "/", "/v1/meta"):
            self._write(200, {"ok": True, "entry": "examples/native_ticket.py", "process": True, "intercepted": True})
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
        spec = dict(req.get("spec") or {})
        spec.setdefault("entry", os.path.join(HERE, "native_ticket.py"))
        req["spec"] = spec
        req.setdefault("entry", spec["entry"])
        cb = Callback(req.get("callback") or "", req.get("token") or "")
        try:
            out = load_run(cb, req)
            out["process"] = True
            self._write(200, out)
        except Exception as e:  # pragma: no cover
            self._write(500, {"error": str(e), "terminal": "abort", "claimed": {"error": str(e)}})


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    addr = "127.0.0.1:8094"
    if "--addr" in argv:
        addr = argv[argv.index("--addr") + 1]
    host, port = addr.rsplit(":", 1)
    httpd = HTTPServer((host, int(port)), Handler)
    print(f"http-native listening on {addr}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
