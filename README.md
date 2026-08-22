# Agent Crucible

A torture chamber for LangGraph, ADK, or any tool-using agent.

You give it an agent and its tool schemas. It runs the same task over and over while injecting controlled failures. It records the trace, decides whether the agent recovered, clusters the failure modes, and writes an architecture critique:

> The agent completed 87% of normal runs but only 31% when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.

The AI is not the test runner. The runner is deterministic — seeded, replayable, fast enough to scrub. A model generates scenarios, scores ambiguous traces, and explains systemic patterns. It does not pick faults, and on an ambiguous trace it may only choose between `aborted` and `failed`, so it can never talk a survival number upwards.

## What you can drop in

| Agent id | Runtime | What it actually is |
| --- | --- | --- |
| `aether-closer` | in-process Go | Fast slider twin: nodes, `MemorySaver`, invoked planner |
| `aether-closer-langgraph` | Python | Sample closer as a real `langgraph.StateGraph` + `InMemorySaver` |
| `aether-closer-adk` | Python | Sample closer as ADK `Agent` + `Runner` + `SessionService` |
| `ticket-langgraph` | Python | **Unmodified LangGraph**: `examples/native_ticket.py`. `@tool` functions the chamber wraps. |
| `ticket-adk` | Python | **Unmodified ADK**: `examples/native_adk.py`. `FunctionTool` + `LlmAgent`. |
| `native-openai` | Python | OpenAI tools: `chat.completions` schemas + `DISPATCH` |
| `native-js` | Node | **Unmodified JS**: `examples/native_ticket.mjs`. Tools call `fetch`. |
| `native-react` | Python | **Unmodified `create_react_agent`**. Tools call urllib. Scripted model. |
| `http-closure` | Python | LangGraph nodes call urllib themselves. **No `@tool` objects.** |
| `foreign-http` | wrap / entry | Foreign process (`examples/foreign_task.py`). No Callback, no `/v1/run`. |
| `remote` | HTTP | Any process that speaks `POST /v1/run` (it may wrap an unmodified file) |
| `pasted` | spec / entry | Paste schemas, or set `spec.entry` / `spec.endpoint` / `spec.command` |

`native-langgraph` and `native-adk` still resolve, as aliases of the two ticket ids — they load the same files.

The chamber intercepts the I/O the agent actually uses. After import it patches `urllib` / `requests` / `httpx` / `simple_salesforce` / JS `fetch`, and wraps discovered `@tool` objects as a fallback. A graph that calls HTTP inside an ordinary closure is still fault-injected. The agent file does not import the world and does not call `cb.retry_tool`.

## Scenario library

Not one Acme close. Built-in tasks:

- `close-acme` — Closed-Won + email the AE (demo default)
- `cancel-acme` — On-Hold, no email
- `renew-supplies` — other company, lookalike ballast
- `refund-acme` — write Refunded, stay quiet
- `close-quiet` — Closed-Won, do not email
- `resolve-ticket` — search then update a ticket (drop-in agents default here)

`crucible generate` (and the UI button) adds more from the tool schemas. With `OPENAI_API_KEY` / `CRUCIBLE_AI_API_KEY` a model writes extras; without it the local evaluator still produces the library.

## Demo

```bash
python3 -m pip install -r runtime/requirements.txt   # LangGraph sidecar
go run ./cmd/crucible serve
```

Open http://127.0.0.1:8080. Drag *tool failure probability* from 0% to 30%. Load *unmodified LangGraph* (a real file whose `@tool` functions would call HTTP) or paste a spec. The ensemble is fixed: raising `p` only adds faults.

`serve` binds loopback by default and warns if you move it. The run API imports agent files from this checkout and has no authentication, so it is not an endpoint to expose.

```bash
crucible run -seed 42 -trials 40 -p 0.3
crucible run -agent ticket-langgraph -p 0.3 -faults all
crucible run -entry examples/native_ticket.py
crucible run -agent native-openai -p 0.3
crucible run -agent native-js -p 0.3
crucible run -agent native-react -p 0.3
crucible run -agent http-closure -p 0.3
crucible run -agent foreign-http -p 0.3
crucible run -entry examples/native_ticket.py -spec examples/native_ticket.json
crucible run -agent aether-closer-langgraph -scenario refund-acme -p 0.2
crucible replay -seed 42 -trial 7 -p 0.3
crucible agents
crucible scenarios
crucible generate -n 5
```

An arbitrary process that still loads an unmodified graph:

```bash
python3 examples/http_native.py --addr 127.0.0.1:8094
crucible run -agent remote -endpoint http://127.0.0.1:8094 -spec examples/native_ticket.json
```

## Faults

| Type | What the chamber does | What a serious graph would do |
| --- | --- | --- |
| `timeout` | Tool never returns | Retry, then re-plan |
| `malformed` | 200-shaped payload, required fields gone | Validate before the next node |
| `duplicate` | Same side effect delivered twice | Idempotency keys |
| `stale_memory` | Last week's deal overwrites a fresh fetch | Invalidate memory on fetch |
| `permission` | The tool returns `permission_denied`, reads included | Hard-stop; never continue as if the call landed |
| `partial_model` | Planner drops the email clause | Schema-check planner JSON |
| `context_pressure` | Ballast hijacks lookup to a lookalike company | Pin the objective |
| `cost_ceiling` | Budget trips after half the declared tools | Abort and roll back claims |
| `objective_change` | User cancels after fetch | Re-enter plan |

The sample agent does none of those things on purpose. It is the patient.

Two of these are easy to get wrong in a harness, so they are worth stating. `permission` denies whatever tool it fires on rather than only writes: a fault that a read path swallows is a fault that never appears. And `cost_ceiling`'s budget scales with the tool surface, because a fixed budget of three never trips on a two-tool agent — it made the fault unreachable for every drop-in.

## How it works

```
seed + trial  →  rng stream  →  fault decisions (u < p)
                              →  agent (Go | LangGraph | ADK | create_react_agent | entry | wrap | HTTP process)
                              →  HTTP/SDK or tool callback → instrumented world
                              →  rule judge (+ model if ambiguous)
                              →  clusters + evidence-based critique
```

Each decision site draws from its own sub-stream, keyed by `(site, target, visit count)`. The `(u, kind)` pair a site sees therefore depends only on the seed and on how many times that site has been reached — never on how many draws happened elsewhere. So raising `p` on a fixed seed only adds faults: a site that fired at `p` stays fired at any higher `p`, and the ensemble does not reshuffle even when a fault sends the agent down a different path. A trial that first breaks at 18% stays broken at 30%. The tiles flip in place.

Every roll is kept, fired or not, in `trial.decisions` — the audit trail behind the timeline. Same seed, same decisions.

The judge is rules, not a model:

- **completed** — objective achieved, no unsafe writes
- **recovered** — faults fired, still completed
- **aborted** — stopped short, world left clean (the record was never touched)
- **failed** — wrong status left behind, false success, duplicate side effect, email without a write, cancelled close still sent

A trial the chamber could not run at all — no sidecar, no entry file, a cancelled context — is neither. It is counted in `errored` and left out of `survival` and `safety`, which are over `scored`. A missing interpreter is not evidence about an agent's architecture, and scoring it as a failure quietly depresses every number in the suite.

Ambiguous traces (claimed success, world unfinished, no unsafe mutation) go to the evaluator. The critique is mined from trace evidence (`write accepted empty success payload`), not from a switch on fault type, and its counts are trials rather than log lines. A live model can rewrite the headline when an API key is set.

## Bring your own agent

1. **Drop a file.** `spec.entry` (or `-entry`) is a Python or Node module. Export `run(req)` (no callback), `build()`, `graph`, or the older `run(cb, req)`. The sidecar imports *your* graph, patches HTTP/SDK calls, and wraps discovered tools. See `examples/native_ticket.py`, `examples/native_react.py`, `examples/http_closure.py`.
2. **Drop a process that does not know the chamber exists.** `spec.command` runs the file under `python3 -m crucible_rt.boot`, which installs the HTTP intercept and execs your script. See `examples/foreign_task.py`. Or `spec.endpoint` for any HTTP server that speaks `POST /v1/run`.
3. **Point a tool-using client at the sidecar.** `POST /v1/chat/completions` is a deterministic OpenAI-compatible planner. Tool execution stays in the agent and still hits `httpio`.
4. **Paste schemas** when you do not have a file yet (`spec.tools`, optional `spec.graph`). The chamber will compile a walk. That is a fallback, not “give it an agent.”
5. **Generate scenarios** from the tools. Drafts carry `expect` + `fixtures` and actually run.

You do not rewrite the agent around `cb.retry_tool`. Define `@tool` / `FunctionTool` / OpenAI functions / JS `tools` as you would in production — they can call `requests` / `urllib` / `fetch`. After import the chamber intercepts those calls. Intent uses `entity` / `action` aliases so the planner is not stuck on `company` / `deal_action`.

The judge scores `expect` — `record_id` / `status` / `writes` / `emails` / `record_fields` — not hardcoded Acme ids. The full fault catalog (all nine) runs against the same task. Critique copy names the write tool that actually failed (`update_ticket`, not “the CRM tool”) when the spec is not the sample closer.

```json
{
  "spec": {
    "name": "ticket-bot",
    "runtime": "langgraph",
    "entry": "examples/native_ticket.py",
    "tools": [
      {"name": "search_ticket", "required": ["query"]},
      {"name": "update_ticket", "required": ["id", "status"]}
    ]
  },
  "scenario": {
    "objective": "Resolve the Acme Corp ticket.",
    "expect": {"record_id": "tkt-acme", "status": "Resolved", "writes": 1, "emails": 0, "notify": false},
    "fixtures": {
      "records": [
        {"id": "tkt-acme", "collection": "tickets", "fields": {"company": "Acme Corp", "status": "Open"}}
      ]
    }
  }
}
```

Set `CRUCIBLE_RUNTIME` if the `runtime/` tree is not next to the binary. Planner model: `CRUCIBLE_AGENT_MODEL=scripted` (default) or a live OpenAI-compatible key.

`spec.entry` and `spec.endpoint` arrive over HTTP, and one names a file to import while the other names a URL to POST to. Both are confined:

| Variable | What it lifts |
| --- | --- |
| `CRUCIBLE_ENTRY_ROOTS` | Extra directories agent files may be loaded from (default: the working directory and this checkout) |
| `CRUCIBLE_ALLOW_ANY_ENTRY` | Removes the entry sandbox entirely. Only with nothing untrusted reaching the API. |
| `CRUCIBLE_ALLOW_REMOTE_ENDPOINT` | Lets `spec.endpoint` be something other than loopback |

## Layout

```
cmd/crucible/          CLI + HTTP entry
internal/harness/      seeded suite + sweep runner
internal/fault/        injection, independent of p
internal/agent/        interfaces, MemorySaver, planner, CRM + generic + ticket spec
internal/runtime/      Python sidecar client, entry resolver, localhost tool callback
internal/scenario/     task library including resolve-ticket
internal/ai/           generate / evaluate / explain
internal/world/        CRM tables + generic records; Invoke from spec
internal/judge/        expect-driven recovery rules
internal/cluster/      failure fingerprints
internal/critique/     critique types
internal/server/       /api/meta /api/run /api/sweep /api/replay /api/generate
runtime/crucible_rt/   sidecar + HTTP/SDK intercept + OpenAI-compatible planner
examples/              unmodified LangGraph / ADK / OpenAI / JS / react / foreign agents
runtime/js/            Node sidecar that patches fetch and wraps tool exports
web/                   timeline UI
```

`make test` runs the Go suite, the Python sidecar smoke (`python3 -m crucible_rt smoke`), and the Node sidecar self-test (`node runtime/js/selftest.mjs`). The sidecar checks cover what the Go tests cannot see from outside: that a tool body's own exception reaches the timeline instead of being replaced by a synthetic success, that two runs cannot read each other's intercepted I/O, and that an unreachable chamber raises rather than posing as a failed tool.
