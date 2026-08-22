import json
import urllib.error
import urllib.request


class Callback:
    def __init__(self, url: str, token: str):
        self.url = url.rstrip("/")
        self.token = token

    def _post(self, path: str, payload: dict) -> dict:
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
            with urllib.request.urlopen(req, timeout=10) as resp:
                return json.loads(resp.read().decode() or "{}")
        except urllib.error.HTTPError as e:
            body = e.read().decode() if e.fp else str(e)
            return {"ok": False, "error": f"callback {e.code}: {body}"}

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
