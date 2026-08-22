/** An unmodified JS tool-using agent. No chamber callback.

Tools hit the ticket HTTP API with fetch. The Node sidecar patches
fetch (and wraps `tools`) so invocations go through FaultBus.
*/

async function httpJson(url, opts = {}) {
  const res = await fetch(url, opts);
  return res.json();
}

export const tools = {
  async search_ticket({ query }) {
    return httpJson("http://tickets.example/search?q=" + encodeURIComponent(query || ""));
  },
  async update_ticket({ id, status }) {
    return httpJson("http://tickets.example/tickets/" + encodeURIComponent(id || ""), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status }),
    });
  },
};

const STATUS = {
  close_won: "Closed-Won",
  on_hold: "On-Hold",
  refund: "Refunded",
  resolve: "Resolved",
};

function parseObjective(objective, companies) {
  companies = companies && companies.length ? companies : ["Acme Corp", "Globex"];
  const intent = { company: companies[0], entity: companies[0], deal_action: "resolve", action: "resolve", notify: false };
  const low = String(objective || "").toLowerCase();
  let best = "", bestLen = 0;
  for (const c of companies) {
    if (low.includes(c.toLowerCase()) && c.length > bestLen) {
      best = c;
      bestLen = c.length;
    }
  }
  if (best) {
    intent.company = best;
    intent.entity = best;
  }
  if (low.includes("refund")) intent.deal_action = intent.action = "refund";
  else if (low.includes("on-hold") || low.includes("on hold") || low.includes("stop.")) intent.deal_action = intent.action = "on_hold";
  else if (low.includes("resolve")) intent.deal_action = intent.action = "resolve";
  else if (low.includes("closed-won") || low.includes("close")) intent.deal_action = intent.action = "close_won";
  if (low.includes("do not email")) intent.notify = false;
  else if (low.includes("email")) intent.notify = true;
  return intent;
}

function lastCompany(junk, companies) {
  companies = companies && companies.length ? companies : ["Acme Corp", "Globex"];
  let last = "", idx = -1;
  for (const c of companies) {
    const i = String(junk || "").lastIndexOf(c);
    if (i > idx) {
      idx = i;
      last = c;
    }
  }
  return last;
}

function transport(res) {
  return ["timeout", "cost_ceiling", "unavailable"].includes((res && res.error) || "");
}

export async function run(req) {
  const state = {
    objective: req.objective || "Resolve the Acme Corp ticket.",
    memory: req.memory || {},
    junk: req.junk || "",
    companies: req.companies || ["Acme Corp", "Globex"],
    steps: 0,
  };
  state.intent = parseObjective(state.objective, state.companies);
  state.steps += 1;
  let query = state.intent.entity || state.intent.company || "";
  const hijack = lastCompany(state.junk, state.companies);
  if (hijack && hijack !== query) query = hijack;
  const found = await tools.search_ticket({ query });
  state.steps += 1;
  if (transport(found)) {
    return finish(state, found.error || "timeout");
  }
  state.ticket_id = found.id || "";
  state.deal_id = state.ticket_id;
  state.record_id = state.ticket_id;
  state.status = found.status || "";
  const mid = (state.memory && (state.memory.record_id || state.memory.deal_id)) || "";
  if (mid) {
    state.ticket_id = mid;
    state.deal_id = mid;
    state.record_id = mid;
    if (state.memory.deal_status) state.status = state.memory.deal_status;
  }
  const status = STATUS[state.intent.action || state.intent.deal_action] || "Resolved";
  const wrote = await tools.update_ticket({ id: state.ticket_id || "", status });
  state.steps += 1;
  if (transport(wrote)) {
    return finish(state, wrote.error || "timeout");
  }
  state.wrote = true;
  state.status = status;
  if (wrote.id) {
    state.ticket_id = wrote.id;
    state.deal_id = wrote.id;
    state.record_id = wrote.id;
  }
  state.terminal = "end";
  return finish(state, "");
}

function finish(state, err) {
  const rid = state.record_id || state.ticket_id || state.deal_id || "";
  return {
    terminal: err ? "abort" : state.terminal || "end",
    intent: state.intent || {},
    claimed: {
      wrote: Boolean(state.wrote),
      notified: false,
      deal_id: rid,
      record_id: rid,
      status: state.status || "",
      error: err || state.last_error || "",
    },
    steps: Number(state.steps || 0),
    checkpoint: true,
    runtime: "js",
    entry: "examples/native_ticket.mjs",
    intercepted: true,
  };
}
