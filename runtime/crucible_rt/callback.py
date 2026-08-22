import json
import socket
import urllib.error
import urllib.request


class CallbackError(RuntimeError):
    """The chamber could not be reached, or refused the request.

    This is not a tool result. Returning {"ok": false, "error": ...} for a dead
    socket would hand the agent a plausible tool failure and let the trial be
    scored as though the agent had mishandled it, when in fact the harness
    itself broke. It propagates so /v1/run answers with an error the Go side
    classifies as a chamber failure.
    """


class Callback:
    def __init__(self, url: str, token: str, timeout: float = 30.0):
        self.url = url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def _post(self, path: str, payload: dict) -> dict:
        if not self.url:
            raise CallbackError("no callback url: the chamber did not say where to call back")
        req = urllib.request.Request(
            self.url + path,
            data=json.dumps(payload).encode(),
            headers={
                "Content-Type": "application/json",
                "Authorization": "Bearer " + self.token,
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read().decode() or "{}"
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace") if e.fp else str(e)
            raise CallbackError(f"chamber returned {e.code} for {path}: {body[:300]}") from e
        except (urllib.error.URLError, socket.timeout, OSError) as e:
            raise CallbackError(f"chamber unreachable at {self.url}{path}: {e}") from e
        try:
            out = json.loads(raw)
        except json.JSONDecodeError as e:
            raise CallbackError(f"chamber sent non-JSON for {path}: {raw[:200]!r}") from e
        if not isinstance(out, dict):
            raise CallbackError(f"chamber sent {type(out).__name__} for {path}, expected an object")
        return out

    def before(self, name: str) -> dict:
        return self._post("/before_node", {"name": name})

    def tool(self, name: str, args: dict) -> dict:
        return self._post("/tool", {"tool": name, "args": args or {}})

    def retry_tool(self, name: str, args: dict) -> dict:
        res = self.tool(name, args)
        if (res.get("error") or "") == "timeout":
            res = self.tool(name, args)
        return res

    def state(self, message: str, data: dict | None = None) -> dict:
        return self._post("/state", {"message": message, "data": data or {}})
