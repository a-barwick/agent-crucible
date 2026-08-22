/** Wrap an unmodified JS agent's own tools so they hit the chamber.

HTTP/SDK I/O is patched separately (httpio.mjs). wrapCallable runs the
original body first so fetch inside a tool is what FaultBus sees.
*/

export class Callback {
  constructor(url, token) {
    this.url = String(url || "").replace(/\/$/, "");
    this.token = token || "";
  }

  async post(path, payload) {
    const res = await fetch(this.url + path, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: "Bearer " + this.token,
      },
      body: JSON.stringify(payload || {}),
    });
    const text = await res.text();
    try {
      return JSON.parse(text || "{}");
    } catch {
      return { ok: false, error: text || "bad json" };
    }
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
  if (!cb) return;
  const err = (res && res.error) || "";
  const data = res && res.data;
  const empty = data == null || (typeof data === "object" && Object.keys(data).length === 0);
  if (err === "permission_denied") {
    await cb.state("write ignored permission_denied", { tool: name });
  }
  if (res && res.ok && empty && writeish(name)) {
    await cb.state("write accepted empty success payload", { tool: name });
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
    if (httpio) {
      const before = httpio.hits();
      try {
        const out = await httpio.usingTool(name, () => fn(payload));
        if (httpio.hits() > before) return out;
      } catch (err) {
        if (httpio.hits() > before) throw err;
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
