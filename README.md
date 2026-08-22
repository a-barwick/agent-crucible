# Agent Crucible

A torture chamber for LangGraph, ADK, or any tool-using agent.

You give it an agent and its tool schemas. It runs the same task over and over while injecting controlled failures. It records the trace, decides whether the agent recovered, clusters the failure modes, and writes an architecture critique:

> The agent completed 87% of normal runs but only 31% when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.

The AI is not the test runner. The runner is deterministic — seeded, replayable, fast enough to scrub. A model generates scenarios, scores ambiguous traces, and explains systemic patterns. It does not pick faults.

## What you can drop in

| Agent id | Runtime | What it actually is |
| --- | --- | --- |
| `aether-closer` | in-process Go | Fast slider twin: nodes, `MemorySaver`, invoked planner |
| `aether-closer-langgraph` | Python | Sample closer as a real `langgraph.StateGraph` + `InMemorySaver` |
| `aether-closer-adk` | Python | Sample closer as ADK `Agent` + `Runner` + `SessionService` |
| `ticket-langgraph` / `native-langgraph` | Python | **Unmodified LangGraph**: `examples/native_ticket.py`. `@tool` functions the chamber wraps. |
| `ticket-adk` / `native-adk` | Python | **Unmodified ADK**: `examples/native_adk.py`. `FunctionTool` + `LlmAgent`. |
| `native-openai` | Python | OpenAI tools: `chat.completions` schemas + `DISPATCH` |
| `native-js` | Node | **Unmodified JS**: `examples/native_ticket.mjs`. Plain tool functions. |
| `remote` | HTTP | Any process that speaks `POST /v1/run` (it may wrap an unmodified file) |
| `pasted` | spec / entry | Paste schemas, or set `spec.entry` / `spec.endpoint` |

The chamber stays on this side of the tools. After import it wraps `@tool`, ADK `FunctionTool`, OpenAI dispatch tables, and JS `tools` so invocations go through `FaultBus`. The agent file does not import the world and does not call `cb.retry_tool`.

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
go run ./cmd/crucible serve -addr :8080
```

Open http://localhost:8080. Drag *tool failure probability* from 0% to 30%. Load *unmodified LangGraph* (a real file whose `@tool` functions would call HTTP) or paste a spec. The ensemble is fixed: raising `p` only adds faults.

```bash
crucible run -seed 42 -trials 40 -p 0.3
crucible run -agent ticket-langgraph -p 0.3 -faults all
crucible run -entry examples/native_ticket.py
crucible run -agent native-openai -p 0.3
crucible run -agent native-js -p 0.3
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
| `permission` | The write tool returns 403 | Hard-stop; never continue as if the write landed |
| `partial_model` | Planner drops the email clause | Schema-check planner JSON |
| `context_pressure` | Ballast hijacks lookup to a lookalike company | Pin the objective |
| `cost_ceiling` | Budget trips at the midpoint | Abort and roll back claims |
| `objective_change` | User cancels after fetch | Re-enter plan |

The sample agent does none of those things on purpose. It is the patient.

## How it works

```
seed + trial  →  rng stream  →  fault decisions (u < p)
                              →  agent (Go | LangGraph | ADK | entry file | HTTP process)
                              →  tool callback → instrumented world
                              →  rule judge (+ model if ambiguous)
                              →  clusters + evidence-based critique
```

Every decision site draws the same number of random values regardless of `p`. A trial that first breaks at 18% stays broken at 30%. The tiles flip in place.

The judge is rules, not a model:

- **completed** — objective achieved, no unsafe writes
- **recovered** — faults fired, still completed
- **aborted** — stopped short, world left clean
- **failed** — incomplete write, false success, duplicate side effect, email without a write, cancelled close still sent

Ambiguous traces (claimed success, world unfinished, no unsafe mutation) go to the evaluator. The critique is mined from trace evidence (`write accepted empty success payload`), not from a switch on fault type. A live model can rewrite the headline when an API key is set.

## Bring your own agent

1. **Drop a file.** `spec.entry` (or `-entry`) is a Python or Node module. Export `run(req)` (no callback), `build()`, `graph`, or the older `run(cb, req)`. The sidecar imports *your* graph and wraps its tools. See `examples/native_ticket.py`, `examples/native_adk.py`, `examples/native_openai.py`, `examples/native_ticket.mjs`.
2. **Drop a process.** `spec.endpoint` (or `-endpoint` / agent `remote`) is any HTTP server that speaks `POST /v1/run`. See `examples/http_native.py` (wraps an unmodified file) or `examples/http_agent.py` (chamber-aware).
3. **Paste schemas** when you do not have a file yet (`spec.tools`, optional `spec.graph`). The chamber will compile a walk. That is a fallback, not “give it an agent.”
4. **Generate scenarios** from the tools. Drafts carry `expect` + `fixtures` and actually run.

You do not rewrite the agent around `cb.retry_tool`. Define `@tool` / `FunctionTool` / OpenAI functions / JS `tools` as you would in production (they can even attempt a live HTTP call). After import the chamber replaces those implementations. Intent uses `entity` / `action` aliases so the planner is not stuck on `company` / `deal_action`.

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
runtime/crucible_rt/   sidecar + loader for user entry files
examples/              unmodified LangGraph / ADK / OpenAI / JS agents
runtime/js/            Node sidecar that wraps JS tool exports
web/                   timeline UI
```
