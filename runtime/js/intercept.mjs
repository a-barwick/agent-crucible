/** Wrap an unmodified JS agent's own tools so they hit the chamber.

HTTP/SDK I/O is patched separately (httpio.mjs). wrapCallable runs the
original body first so fetch inside a tool is what FaultBus sees.
*/

/** The chamber could not be reached, or refused the request.
 *
 * Not a tool result. Returning {ok: false, error: "unknown session"} for a
 * rejected callback handed the agent a plausible tool failure, and the trial was
 * then scored as the agent mishandling it — when in fact the harness broke.
 */
export class CallbackError extends Error {
  constructor(message) {
    super(message);
    this.name = "CallbackError";
    this.chamber = true;
  }
}

export class Callback {
  constructor(url, token, timeoutMs = 30000) {
    this.url = String(url || "").replace(/\/$/, "");
    this.token = token || "";
    this.timeoutMs = timeoutMs;
  }

  async post(path, payload) {
    if (!this.url) throw new CallbackError("no callback url: the chamber did not say where to call back");
    let res;
    try {
      res = await fetch(this.url + path, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: "Bearer " + this.token,
        },
        body: JSON.stringify(payload || {}),
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch (err) {
      throw new CallbackError(`chamber unreachable at ${this.url}${path}: ${err && err.message ? err.message : err}`);
    }
    const text = await res.text();
    if (!res.ok) {
      throw new CallbackError(`chamber returned ${res.status} for ${path}: ${text.slice(0, 300)}`);
    }
    let out;
    try {
      out = JSON.parse(text || "{}");
    } catch {
      throw new CallbackError(`chamber sent non-JSON for ${path}: ${text.slice(0, 200)}`);
    }
    if (!out || typeof out !== "object" || Array.isArray(out)) {
      throw new CallbackError(`chamber sent ${Array.isArray(out) ? "an array" : typeof out} for ${path}`);
    }
    return out;
  }

  before(name) {
    return this.post("/before_node", { name });
  }

  tool(name, args) {
    return this.post("/tool", { tool: name, args: args || {} });
  }

  state(message, data) {
    return this.post("/state", { message, data: data || {} });
  }
}

function present(res) {
  const err = (res && res.error) || "";
  const data = res && res.data;
  const out = data && typeof data === "object" && !Array.isArray(data) ? { ...data } : {};
  if (data != null && (typeof data !== "object" || Array.isArray(data))) out.data = data;
  out.ok = Boolean(res && res.ok) && !err;
  if (err) {
    out.error = err;
    out.ok = false;
  }
  return out;
}

function writeish(name) {
  const n = String(name || "").toLowerCase();
  return ["write", "update", "patch", "create", "delete", "refund", "upsert"].some((p) => n.includes(p));
}

async function emitEvidence(cb, name, res) {
  if (!cb || !cb.state) return;
  const err = (res && res.error) || "";
  const data = res && res.data;
  const empty = data == null || (typeof data === "object" && Object.keys(data).length === 0);
  // Evidence is a side note; a lost log line must not become a tool failure.
  try {
    if (err === "permission_denied") {
      await cb.state("write ignored permission_denied", { tool: name });
    }
    if (res && res.ok && empty && writeish(name)) {
      await cb.state("write accepted empty success payload", { tool: name });
    }
  } catch (err2) {
    if (err2 instanceof CallbackError) throw err2;
  }
}

export function wrapCallable(fn, name, cb) {
  if (!fn || fn._crucibleWrapped) return fn;
  const wrapped = async (args = {}) => {
    const payload = args && typeof args === "object" && !Array.isArray(args) ? args : { arg: args };
    await cb.before(name);
    let httpio;
    try {
      httpio = await import("./httpio.mjs");
    } catch {
      httpio = null;
    }
    if (httpio && httpio.active()) {
      const before = httpio.hits();
      try {
        const out = await httpio.usingTool(name, () => fn(payload));
        if (httpio.hits() > before) return out;
      } catch (err) {
        if (httpio.hits() > before) throw err;
        // A failure to reach the chamber is never the agent's verdict.
        if (err instanceof CallbackError) throw err;
        // The body failed before it reached the network, so the chamber answers
        // instead. Say so: quietly substituting a synthetic success made the
        // agent's own crash invisible and scored the trial as a working tool.
        try {
          await cb.state("tool body raised before any I/O; chamber answered instead", {
            tool: name,
            error: String((err && err.stack) || err).slice(0, 500),
          });
        } catch (err2) {
          if (err2 instanceof CallbackError) throw err2;
        }
      }
    }
    const res = await cb.tool(name, payload);
    await emitEvidence(cb, name, res);
    return present(res);
  };
  wrapped._crucibleWrapped = true;
  wrapped._crucibleName = name;
  return wrapped;
}

export function wrapTools(tools, cb) {
  if (!tools || typeof tools !== "object") return tools;
  for (const [name, fn] of Object.entries(tools)) {
    if (typeof fn === "function") tools[name] = wrapCallable(fn, name, cb);
  }
  return tools;
}

export function applyPlanHook(req, hook) {
  const out = { ...req };
  if (!hook) return out;
  if (hook.objective) out.objective = hook.objective;
  if ("partial" in hook) out.partial = hook.partial;
  if (hook.memory) out.memory = hook.memory;
  if (hook.junk != null) out.junk = hook.junk;
  return out;
}
