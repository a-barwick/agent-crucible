/** Intercept fetch / http so unmodified JS tool bodies hit FaultBus. */

let origFetch = globalThis.fetch;
let cb = null;
let spec = {};
let currentTool = "";
let hitCount = 0;
let lastResult = null;
const store = { calls: [], wrote: false, record_id: "", status: "", error: "" };

export function hits() {
  return hitCount;
}

export function snapshot() {
  return { ...store, hits: hitCount, last: lastResult };
}

export function usingTool(name, fn) {
  const prev = currentTool;
  currentTool = name || "";
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      currentTool = prev;
    });
}

export function isPassthrough(url) {
  if (!url) return false;
  const base = cb && cb.url ? String(cb.url).replace(/\/$/, "") : "";
  if (base && String(url).startsWith(base)) return true;
  const low = String(url).toLowerCase();
  if ((low.includes("/before_node") || low.endsWith("/tool") || low.includes("/v1/run")) && (low.includes("127.0.0.1") || low.includes("localhost"))) {
    return true;
  }
  return false;
}

export function install(callback, agentSpec) {
  cb = callback;
  spec = agentSpec || {};
  hitCount = 0;
  lastResult = null;
  store.calls = [];
  store.wrote = false;
  store.record_id = "";
  store.status = "";
  store.error = "";
  if (!origFetch) origFetch = globalThis.fetch;
  globalThis.fetch = patchedFetch;
}

async function patchedFetch(input, init = {}) {
  const url = typeof input === "string" ? input : input && input.url;
  if (isPassthrough(url)) return origFetch(input, init);
  const method = String((init && init.method) || (input && input.method) || "GET").toUpperCase();
  const body = init && init.body;
  const { status, payload } = await dispatch(method, url, body);
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export async function dispatch(method, url, body) {
  const { tool, args } = mapRequest(method, url, body, spec);
  if (!cb) throw new Error("live call to " + url + " was not intercepted");
  if (!currentTool && cb.before) {
    try {
      await cb.before(tool);
    } catch {
      /* ignore */
    }
  }
  const res = await cb.tool(tool, args);
  hitCount += 1;
  lastResult = res;
  note(tool, args, res);
  await emit(cb, tool, res);
  return { status: statusOf(res), payload: present(res) };
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

function statusOf(res) {
  const err = (res && res.error) || "";
  if (err === "permission_denied") return 403;
  if (err === "timeout" || err === "cost_ceiling" || err === "unavailable") return 504;
  if (err && !(res && res.ok)) return 400;
  return 200;
}

function writeish(name) {
  const n = String(name || "").toLowerCase();
  return ["write", "update", "patch", "create", "delete", "refund", "upsert"].some((p) => n.includes(p));
}

function note(tool, args, res) {
  store.calls.push({ tool, args: args || {}, ok: Boolean(res && res.ok) });
  const err = (res && res.error) || "";
  if (err) store.error = err;
  const data = res && res.data && typeof res.data === "object" ? res.data : {};
  const rid = data.id || (args && (args.id || args.record_id)) || "";
  if (rid) store.record_id = String(rid);
  const status = data.status || (args && args.status) || "";
  if (status) store.status = String(status);
  if (writeish(tool) && err !== "timeout" && err !== "cost_ceiling" && err !== "unavailable") {
    store.wrote = true;
  }
}

async function emit(callback, name, res) {
  if (!callback || !callback.state) return;
  const err = (res && res.error) || "";
  const data = res && res.data;
  const empty = data == null || (typeof data === "object" && Object.keys(data).length === 0);
  if (err === "permission_denied") {
    await callback.state("write ignored permission_denied", { tool: name });
  }
  if (res && res.ok && empty && writeish(name)) {
    await callback.state("write accepted empty success payload", { tool: name });
  }
}

export function mapRequest(method, url, body, agentSpec) {
  let parsed;
  try {
    parsed = new URL(url, "http://tickets.example");
  } catch {
    parsed = { hostname: "", pathname: String(url || "/"), searchParams: new URLSearchParams() };
  }
  const path = parsed.pathname || "/";
  const query = {};
  if (parsed.searchParams) {
    for (const [k, v] of parsed.searchParams.entries()) query[k] = v;
  }
  const payload = decodeBody(body);
  const tool = currentTool || matchSpec(agentSpec, method, parsed, path) || heuristic(method, parsed.hostname || "", path, agentSpec);
  const args = extractArgs(tool, agentSpec, path, query, payload);
  return { tool, args };
}

function matchSpec(agentSpec, method, parsed, path) {
  const host = String(parsed.hostname || "").toLowerCase();
  const url = host + path;
  for (const t of (agentSpec && agentSpec.tools) || []) {
    if (!t || typeof t !== "object") continue;
    const bind = t.http || {};
    if (!bind.match && !bind.host && !bind.method) continue;
    if (bind.method && String(bind.method).toUpperCase() !== String(method || "").toUpperCase()) continue;
    if (bind.host && !host.includes(String(bind.host).toLowerCase())) continue;
    const needle = bind.match || bind.path || "";
    if (needle && !path.includes(needle) && !url.includes(needle)) continue;
    if (t.name) return t.name;
  }
  return "";
}

function heuristic(method, host, path, agentSpec) {
  host = String(host || "").toLowerCase();
  path = String(path || "").toLowerCase();
  method = String(method || "GET").toUpperCase();
  const names = ((agentSpec && agentSpec.tools) || []).map((t) => (t && t.name) || t).filter(Boolean);
  if (host.includes("tickets.example") || host.includes("ticket")) {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(method) || path.includes("/tickets/")) {
      return prefer(names, "update_ticket", true);
    }
    return prefer(names, "search_ticket", false);
  }
  if (["POST", "PUT", "PATCH", "DELETE"].includes(method)) return prefer(names, "update_ticket", true);
  return prefer(names, "search_ticket", false);
}

function prefer(names, fallback, write) {
  if (names.includes(fallback)) return fallback;
  for (const n of names) {
    if (writeish(n) === write) return n;
  }
  return names[0] || fallback;
}

function extractArgs(tool, agentSpec, path, query, payload) {
  const args = { ...query, ...(payload || {}) };
  if (args.q && !args.query) args.query = args.q;
  const parts = String(path || "")
    .split("/")
    .filter(Boolean);
  if (parts.length && !["search", "query", "lookup", "tickets", "deals"].includes(parts[parts.length - 1]) && !args.id) {
    args.id = parts[parts.length - 1];
  }
  return args;
}

function decodeBody(body) {
  if (!body) return {};
  if (typeof body === "object" && !ArrayBuffer.isView(body) && !(body instanceof ArrayBuffer)) return body;
  const text = typeof body === "string" ? body : Buffer.from(body).toString();
  try {
    const data = JSON.parse(text || "{}");
    return data && typeof data === "object" ? data : { data };
  } catch {
    return Object.fromEntries(new URLSearchParams(text));
  }
}
