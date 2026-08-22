"""Run a foreign Python file with HTTP/SDK intercept installed.

The agent script does not import the chamber. This module is the
process wrapper: it reads CRUCIBLE_* env (or argv), patches urllib /
requests / httpx, then execs the user file as __main__.
"""

from __future__ import annotations

import json
import os
import runpy
import sys


def main(argv=None):
    argv = list(argv if argv is not None else sys.argv[1:])
    if not argv:
        raise SystemExit("usage: python3 -m crucible_rt.boot <script> [req.json]")
    script = argv[0]
    req_path = argv[1] if len(argv) > 1 else os.environ.get("CRUCIBLE_REQ") or ""
    if req_path:
        os.environ["CRUCIBLE_REQ"] = req_path
    from . import httpio

    httpio.install_from_env()
    sys.argv = [script] + argv[1:]
    runpy.run_path(script, run_name="__main__")


def write_result(payload: dict) -> None:
    path = os.environ.get("CRUCIBLE_RESULT") or ""
    raw = json.dumps(payload)
    if path:
        with open(path, "w", encoding="utf-8") as f:
            f.write(raw)
    else:
        sys.stdout.write(raw + "\n")


if __name__ == "__main__":
    main()
