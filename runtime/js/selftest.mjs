#!/usr/bin/env node
/** Checks for the parts of the Node sidecar the Go tests cannot see.
 *
 * Run with `node runtime/js/selftest.mjs`. Exits non-zero on the first failure.
 */

import * as httpio from "./httpio.mjs";
import { Callback, CallbackError, wrapCallable } from "./intercept.mjs";

function fail(msg) {
  console.error("selftest: " + msg);
  process.exit(1);
}

function chamber(tag) {
  return {
    url: "http://127.0.0.1:9/callback",
    tag,
    calls: [],
    states: [],
    before() {
      return {};
    },
    tool(name, args) {
      this.calls.push({ name, args });
      return { ok: true, data: { id: tag, status: args.status || "Open" } };
    },
    state(message, data) {
      this.states.push({ message, data });
      return { ok: true };
    },
  };
}

const spec = {
  tools: [
    { name: "search_ticket", http: { host: "tickets.example", match: "/search", method: "GET" } },
    { name: "update_ticket", http: { host: "tickets.example", match: "/tickets/", method: "POST" } },
  ],
};

async function fetchShapes() {
  // fetch takes a string, a URL, or a Request. Reading input.url covered only
  // the last, so a URL went out as "undefined" and was mapped by guesswork.
  const cb = chamber("tkt-shapes");
  const sess = httpio.install(cb, spec);
  await httpio.run(sess, async () => {
    const asString = await (await fetch("http://tickets.example/search?q=Acme+Corp")).json();
    if (asString.id !== "tkt-shapes") fail("string url: " + JSON.stringify(asString));

    await fetch(new URL("http://tickets.example/search?q=Acme+Corp"));
    await fetch(
      new Request("http://tickets.example/tickets/tkt-shapes", {
        method: "POST",
        body: JSON.stringify({ status: "Resolved" }),
        headers: { "Content-Type": "application/json" },
      }),
    );
  });
  const names = cb.calls.map((c) => c.name);
  if (names.length !== 3) fail("expected three intercepted calls, got " + JSON.stringify(names));
  if (names[1] !== "search_ticket") fail("URL object mapped to " + names[1]);
  if (names[2] !== "update_ticket") fail("Request object mapped to " + names[2]);
  const wrote = cb.calls[2].args;
  if (wrote.status !== "Resolved") fail("Request body was dropped: " + JSON.stringify(wrote));
  if (wrote.id !== "tkt-shapes") fail("path id was dropped: " + JSON.stringify(wrote));
  console.log("fetch-shapes-ok", names.join(","));
}

async function sessionsDoNotLeak() {
  // Two runs overlap on the one event loop. Each must see only its own hits and
  // its own store, or a trial reads another trial's write.
  const one = chamber("tkt-one");
  const two = chamber("tkt-two");
  const s1 = httpio.install(one, spec);
  const s2 = httpio.install(two, spec);
  let mid = null;
  const first = httpio.run(s1, async () => {
    await fetch("http://tickets.example/search?q=One");
    await new Promise((r) => setTimeout(r, 5));
    mid = httpio.hits();
    return httpio.snapshot();
  });
  const second = httpio.run(s2, async () => {
    await fetch("http://tickets.example/search?q=Two");
    await fetch("http://tickets.example/tickets/tkt-two", { method: "POST", body: '{"status":"Resolved"}' });
    return httpio.snapshot();
  });
  const [a, b] = await Promise.all([first, second]);
  if (mid !== 1) fail("first run saw " + mid + " hits after the second ran");
  if (a.hits !== 1 || b.hits !== 2) fail(`hit counts crossed: ${a.hits} and ${b.hits}`);
  if (a.wrote) fail("first run inherited the second run's write");
  if (!b.wrote) fail("second run lost its own write");
  console.log("session-isolation-ok", a.hits, b.hits);
}

async function bodyRaiseIsReported() {
  // A tool body that fails before any I/O falls back to the chamber. That is
  // fine; doing it silently is not, because the crash then never appears in the
  // timeline and the trial scores as though the tool had worked.
  const cb = chamber("tkt-raise");
  const sess = httpio.install(cb, spec);
  const wrapped = wrapCallable(async () => {
    throw new TypeError("credentials is not a function");
  }, "search_ticket", cb);
  const out = await httpio.run(sess, () => wrapped({ query: "Acme Corp" }));
  if (out.id !== "tkt-raise") fail("wrapper did not fall back to the chamber: " + JSON.stringify(out));
  if (!cb.states.some((s) => s.message.includes("tool body raised"))) {
    fail("wrapper swallowed the tool body's exception: " + JSON.stringify(cb.states));
  }
  console.log("body-raise-ok");
}

async function callbackErrorsThrow() {
  // An unreachable or refusing chamber must throw, not hand the agent a
  // plausible tool failure for the judge to score.
  const cb = new Callback("http://127.0.0.1:1", "tok", 500);
  await cb
    .tool("search_ticket", { query: "Acme Corp" })
    .then(() => fail("unreachable chamber returned a tool result"))
    .catch((err) => {
      if (!(err instanceof CallbackError)) fail("unreachable chamber threw " + err);
    });
  await new Callback("", "")
    .before("plan")
    .then(() => fail("empty callback url did not throw"))
    .catch((err) => {
      if (!(err instanceof CallbackError)) fail("empty url threw " + err);
    });
  console.log("callback-error-ok");
}

await fetchShapes();
await sessionsDoNotLeak();
await bodyRaiseIsReported();
await callbackErrorsThrow();
httpio.uninstall();
console.log("js-selftest-ok");
