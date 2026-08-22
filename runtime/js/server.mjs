#!/usr/bin/env node
/** Node sidecar: load a user .mjs/.js agent, wrap its tools, POST /v1/run. */

import http from "node:http";
import path from "node:path";
import fs from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { Callback, CallbackError, wrapTools, applyPlanHook } from "./intercept.mjs";
import * as httpio from "./httpio.mjs";

// A run request carries a spec and fixtures: kilobytes. Buffering whatever
// arrives lets one request take the heap.
const MAX_BODY = 4 * 1024 * 1024;

/** The chamber could not start this trial: no entry file, no run export. */
class EntryError extends Error {
  constructor(message) {
    super(message);
    this.name = "EntryError";
    this.chamber = true;
  }
}

function resolveEntry(entry) {
  if (!entry) throw new EntryError("empty entry");
  if (fs.existsSync(entry) && fs.statSync(entry).isFile()) return path.resolve(entry);
  // fileURLToPath, not new URL(...).pathname: the latter leaves percent-escapes
  // in place, so a checkout under a path with a space resolved to nothing.
  const here = path.dirname(fileURLToPath(import.meta.url));
  const roots = [
    process.cwd(),
    path.join(here, "..", ".."),
    path.join(here, "..", "..", "examples"),
    path.join(here, "..", "examples"),
  ];
  const names = [entry, path.basename(entry)];
  for (const root of roots) {
    for (const name of names) {
      const cand = path.normalize(path.join(root, name));
      if (fs.existsSync(cand) && fs.statSync(cand).isFile()) return cand;
      const cand2 = path.normalize(path.join(root, "examples", path.basename(name)));
      if (fs.existsSync(cand2) && fs.statSync(cand2).isFile()) return cand2;
    }
  }
  throw new EntryError("entry not found: " + entry);
}

// A counter, not Date.now(): two trials inside the same millisecond shared a
// cache entry, and with it the tool wrappers bound to the earlier trial.
let importSeq = 0;

async function loadModule(entry) {
  const abs = resolveEntry(entry);
  try {
    return await import(pathToFileURL(abs).href + "?crucible=" + ++importSeq);
  } catch (err) {
    throw new EntryError(`cannot load ${abs}: ${(err && err.message) || err}`);
  }
}

function write(res, code, body) {
  const raw = Buffer.from(JSON.stringify(body));
  res.writeHead(code, { "Content-Type": "application/json", "Content-Length": raw.length });
  res.end(raw);
}

async function handleRun(reqBody) {
  const spec = reqBody.spec || {};
  const entry = reqBody.entry || spec.entry;
  if (!entry) throw new EntryError("js runtime needs spec.entry");
  const cb = new Callback(reqBody.callback || "", reqBody.token || "");
  const mod = await loadModule(entry);
  if (typeof mod.run !== "function") throw new EntryError(entry + " has no run(req) export");
  const sess = httpio.install(cb, spec);
  return httpio.run(sess, async () => {
    if (mod.tools) wrapTools(mod.tools, cb);
    if (mod.DISPATCH) wrapTools(mod.DISPATCH, cb);
    if (mod.FUNCTIONS) wrapTools(mod.FUNCTIONS, cb);
    const hooked = applyPlanHook(reqBody, await cb.before("plan"));
    const out = await mod.run(hooked);
    if (out && typeof out === "object") {
      out.runtime = out.runtime || "js";
      out.checkpoint = true;
    }
    return out;
  });
}

async function readBody(req, res) {
  let size = 0;
  const chunks = [];
  for await (const c of req) {
    size += c.length;
    if (size > MAX_BODY) {
      write(res, 413, { error: `body over ${MAX_BODY} bytes` });
      req.destroy();
      return null;
    }
    chunks.push(c);
  }
  let body;
  try {
    body = JSON.parse(Buffer.concat(chunks).toString() || "{}");
  } catch (err) {
    write(res, 400, { error: "bad json: " + ((err && err.message) || err) });
    return null;
  }
  if (!body || typeof body !== "object" || Array.isArray(body)) {
    write(res, 400, { error: "body must be a JSON object" });
    return null;
  }
  return body;
}

function detail(err) {
  if (!err) return "unknown error";
  return String(err.stack || err.message || err);
}

function createServer() {
  return http.createServer(async (req, res) => {
    try {
      if (req.method === "GET" && (req.url === "/" || req.url === "/health" || req.url === "/v1/meta")) {
        write(res, 200, { ok: true, js: true, node: process.version });
        return;
      }
      if (req.method !== "POST" || req.url !== "/v1/run") {
        write(res, 404, { error: "not found" });
        return;
      }
      const body = await readBody(req, res);
      if (body === null) return;
      write(res, 200, await handleRun(body));
    } catch (err) {
      if (err instanceof EntryError || err instanceof CallbackError || (err && err.chamber)) {
        // The chamber could not run this trial. Say so explicitly so the Go
        // side counts it as errored rather than folding it into the score.
        write(res, 502, { chamber_error: true, error: detail(err) });
        return;
      }
      // The agent's own code threw. An agent that crashes has failed the
      // trial, so that is a verdict — with the stack attached as the reason.
      console.error("crucible-js: agent raised: " + detail(err));
      write(res, 200, {
        terminal: "abort",
        intent: {},
        claimed: { error: "agent raised " + ((err && err.name) || "Error") },
        steps: 0,
        runtime: "js",
        agent_error: detail(err),
      });
    }
  });
}

// An unhandled rejection inside an agent used to take the sidecar down with no
// explanation, which reads to the runner as every remaining trial failing.
process.on("unhandledRejection", (err) => {
  console.error("crucible-js: unhandled rejection: " + detail(err));
});
process.on("uncaughtException", (err) => {
  console.error("crucible-js: uncaught exception: " + detail(err));
});

const argv = process.argv.slice(2);
let addr = "127.0.0.1:8093";
const i = argv.indexOf("--addr");
if (i >= 0) addr = argv[i + 1];
const [host, port] = addr.split(":");
const server = createServer();
server.listen(Number(port), host, () => {
  console.log("crucible-js listening on " + addr);
});
