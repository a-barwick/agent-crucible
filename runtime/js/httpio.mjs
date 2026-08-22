/** Intercept fetch so unmodified JS tool bodies hit FaultBus.
 *
 * globalThis.fetch is patched once. Which chamber a patched call routes to is
 * per-run, held in an AsyncLocalStorage: the sidecar awaits on every hop, so two
 * overlapping /v1/run requests interleave on the one event loop, and module-level
 * state would let one run read the other's hit count, tool name and writes.
 */

import { AsyncLocalStorage } from "node:async_hooks";

const origFetch = globalThis.fetch;
const runs = new AsyncLocalStorage();

// Every callback base URL we have been handed. This outlives the run on purpose:
// the chamber's own endpoints must be recognised even from a callback made after
// the run's context has gone, or the POST would be intercepted as a tool call.
const bases = new Set();

let patched = false;

function emptyStore() {
  return { calls: [], wrote: false, record_id: "", status: "", error: "" };
}

function session() {
  return runs.getStore() || null;
}

export function active() {
  return session() !== null;
}

export function hits() {
  const s = session();
  return s ? s.hits : 0;
}

export function lastResult() {
  const s = session();
  return s ? s.last : null;
}

export function snapshot() {
  const s = session();
  const store = s ? s.store : emptyStore();
  return { ...store, hits: s ? s.hits : 0, last: s ? s.last : null };
}

export function usingTool(name, fn) {
  const s = session();
  if (!s) return Promise.resolve().then(fn);
  const prev = s.tool;
  s.tool = name || "";
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      s.tool = prev;
    });
}

export function isPassthrough(url) {
  if (!url) return false;
  const text = String(url);
  for (const base of bases) {
    if (base && text.startsWith(base)) return true;
  }
  const low = text.toLowerCase();
  const chamberPath =
    low.includes("/before_node") || low.endsWith("/tool") || low.includes("/state") || low.includes("/v1/run");
  return chamberPath && (low.includes("127.0.0.1") || low.includes("localhost") || low.includes("[::1]"));
}

/** install patches fetch and returns the session to run the agent inside. */
export function install(callback, agentSpec) {
  const base = callback && callback.url ? String(callback.url).replace(/\/$/, "") : "";
  if (base) bases.add(base);
  if (!patched) {
    globalThis.fetch = patchedFetch;
    patched = true;
  }
  return { cb: callback, spec: agentSpec || {}, tool: "", hits: 0, last: null, store: emptyStore() };
}

/** run executes fn with this session bound to the async context. */
export function run(sess, fn) {
  return runs.run(sess, fn);
}

/** uninstall restores the real fetch. Only safe with no run in flight. */
export function uninstall() {
  if (patched) {
    globalThis.fetch = origFetch;
    patched = false;
  }
}

function requestShape(input, init) {
  // fetch accepts a string, a URL, or a Request. Reading `input.url` covers
  // only the last of those: a URL has no `url` property, so the request went
  // out as "undefined" and was mapped to whatever the heuristic guessed.
  if (typeof input === "string") return { url: input, method: null, request: null };
  if (input instanceof URL) return { url: input.href, method: null, request: null };
  if (input && typeof input.url === "string") {
    return { url: input.url, method: input.method || null, request: input };
  }
  return { url: String(input ?? ""), method: null, request: null };
}

async function patchedFetch(input, init = {}) {
  const { url, method: reqMethod, request } = requestShape(input, init);
  if (!active() || isPassthrough(url)) return origFetch(input, init);
  const method = String((init && init.method) || reqMethod || "GET").toUpperCase();
  let body = init && init.body;
  if (body == null && request && !["GET", "HEAD"].includes(method)) {
    // A Request carries its body on itself, not in init. Clone it: reading the
    // original would leave a used stream behind if the caller retries.
    try {
      body = await request.clone().text();
    } catch {
      body = null;
    }
  }
  const { status, payload } = await dispatch(method, url, body);
  return new Response(JSON.stringify(payload), {
    status,
    statusText: status < 400 ? "OK" : "Error",
    headers: { "Content-Type": "application/json" },
  });
}

export async function dispatch(method, url, body) {
  const s = session();
  const { tool, args } = mapRequest(method, url, body, s ? s.spec : {});
  if (!s || !s.cb) throw new Error("live call to " + url + " was not intercepted: " + JSON.stringify(args));
  const cb = s.cb;
  if (!s.tool && cb.before) {
    try {
      await cb.before(tool);
    } catch {
      /* a lost hook is not a tool failure */
    }
  }
  const res = await cb.tool(tool, args);
  s.hits += 1;
  s.last = res;
  note(s.store, tool, args, res);
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

function note(store, tool, args, res) {
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
  // Evidence is a side note. A lost log line must not turn into a tool failure.
  try {
    if (err === "permission_denied") {
      await callback.state("write ignored permission_denied", { tool: name });
    }
    if (res && res.ok && empty && writeish(name)) {
      await callback.state("write accepted empty success payload", { tool: name });
    }
  } catch {
    /* ignore */
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
  const s = session();
  const tool =
    (s && s.tool) || matchSpec(agentSpec, method, parsed, path) || heuristic(method, parsed.hostname || "", path, agentSpec);
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
  const writeMethod = ["POST", "PUT", "PATCH", "DELETE"].includes(method);
  if (host.includes("tickets.example") || host.includes("ticket")) {
    if (writeMethod || path.includes("/tickets/")) return prefer(names, "update_ticket", true);
    return prefer(names, "search_ticket", false);
  }
  // Kept in step with the Python side: the same spec must map the same way
  // whichever sidecar the agent happens to run in.
  if (host.includes("salesforce") || host.includes("force.com") || host.endsWith("crm.example")) {
    return writeMethod ? prefer(names, "write_deal", true) : prefer(names, "lookup_contact", false);
  }
  if (["/search", "/query", "/lookup", "/find"].some((p) => path.includes(p))) {
    return prefer(names, "search_ticket", false);
  }
  if (writeMethod) return prefer(names, "update_ticket", true);
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
  if (args.company && !args.query) args.query = args.company;
  const parts = String(path || "")
    .split("/")
    .filter(Boolean);
  const last = parts.length ? parts[parts.length - 1] : "";
  if (last && !["search", "query", "lookup", "tickets", "deals", "sobjects"].includes(last) && !args.id) {
    args.id = last;
  }
  let bind = {};
  for (const t of (agentSpec && agentSpec.tools) || []) {
    if (t && typeof t === "object" && t.name === tool) {
      bind = t.http || {};
      break;
    }
  }
  // spec.tools[].http.args renames a source field onto the argument the tool
  // declares. The Python side has honoured this from the start.
  const mapping = bind.args || {};
  const out = { ...args };
  for (const [dest, src] of Object.entries(mapping)) {
    if (src === "$path" || src === "path") {
      if (last) out[dest] = last;
    } else if (src in args) {
      out[dest] = args[src];
    } else if (src in query) {
      out[dest] = query[src];
    } else if (payload && typeof payload === "object" && src in payload) {
      out[dest] = payload[src];
    }
  }
  return out;
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
