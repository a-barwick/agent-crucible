#!/usr/bin/env node
/** Node sidecar: load a user .mjs/.js agent, wrap its tools, POST /v1/run. */

import http from "node:http";
import path from "node:path";
import fs from "node:fs";
import { pathToFileURL } from "node:url";
import { Callback, wrapTools, applyPlanHook } from "./intercept.mjs";
import { install as installHTTP } from "./httpio.mjs";

function resolveEntry(entry) {
  if (!entry) throw new Error("empty entry");
  if (fs.existsSync(entry) && fs.statSync(entry).isFile()) return path.resolve(entry);
  const here = path.dirname(new URL(import.meta.url).pathname);
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
  throw new Error("entry not found: " + entry);
}

async function loadModule(entry) {
  const abs = resolveEntry(entry);
  return import(pathToFileURL(abs).href + "?t=" + Date.now());
}

function write(res, code, body) {
  const raw = Buffer.from(JSON.stringify(body));
  res.writeHead(code, { "Content-Type": "application/json", "Content-Length": raw.length });
  res.end(raw);
}

async function handleRun(reqBody) {
  const spec = reqBody.spec || {};
  const entry = reqBody.entry || spec.entry;
  if (!entry) throw new Error("js runtime needs spec.entry");
  const cb = new Callback(reqBody.callback || "", reqBody.token || "");
  const mod = await loadModule(entry);
  installHTTP(cb, spec);
  if (mod.tools) wrapTools(mod.tools, cb);
  if (mod.DISPATCH) wrapTools(mod.DISPATCH, cb);
  if (mod.FUNCTIONS) wrapTools(mod.FUNCTIONS, cb);
  const hooked = applyPlanHook(reqBody, await cb.before("plan"));
  if (typeof mod.run !== "function") throw new Error(entry + " has no run(req) export");
  const out = await mod.run(hooked);
  if (out && typeof out === "object") {
    out.runtime = out.runtime || "js";
    out.checkpoint = true;
  }
  return out;
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
      const chunks = [];
      for await (const c of req) chunks.push(c);
      let body = {};
      try {
        body = JSON.parse(Buffer.concat(chunks).toString() || "{}");
      } catch {
        write(res, 400, { error: "bad json" });
        return;
      }
      const out = await handleRun(body);
      write(res, 200, out);
    } catch (err) {
      write(res, 500, { error: String(err && err.message ? err.message : err), terminal: "abort", claimed: { error: String(err) } });
    }
  });
}

const argv = process.argv.slice(2);
let addr = "127.0.0.1:8093";
const i = argv.indexOf("--addr");
if (i >= 0) addr = argv[i + 1];
const [host, port] = addr.split(":");
const server = createServer();
server.listen(Number(port), host, () => {
  console.log("crucible-js listening on " + addr);
});
