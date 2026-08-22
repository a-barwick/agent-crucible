# Agent Crucible

A torture chamber for LangGraph, ADK, or any tool-using agent.

You give it an agent and its tool schemas. It runs the same task over and over while injecting controlled failures. It records the trace, decides whether the agent recovered, clusters the failure modes, and writes an architecture critique:

> The agent completed 87% of normal runs but only 31% when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.

The AI is not the test runner. The runner is deterministic — seeded, replayable, fast enough to scrub. A model generates scenarios, scores ambiguous traces, and explains systemic patterns. It does not pick faults.

## What you can drop in

| Agent id | Runtime | What it actually is |
| --- | --- | --- |
| `aether-closer` | in-process Go | Fast slider twin: nodes, `MemorySaver`, invoked planner |
| `aether-closer-langgraph` | Python | Real `langgraph.StateGraph` compiled with `InMemorySaver`. Plan calls a LangChain chat model |
| `aether-closer-adk` | Python | ADK adapter: `Agent` + `Runner` + `SessionService`. Tools callback into the chamber |
| `pasted` | spec | Paste tool schemas + a graph JSON. Optional fixtures and expect |

The chamber stays on this side of the tools. Sidecars never touch the world; they HTTP-callback into `FaultBus`.

## Scenario library

Not one Acme close. Built-in tasks:

- `close-acme` — Closed-Won + email the AE (demo default)
- `cancel-acme` — On-Hold, no email
- `renew-supplies` — other company, lookalike ballast
- `refund-acme` — write Refunded, stay quiet
- `close-quiet` — Closed-Won, do not email

`crucible generate` (and the UI button) adds more from the tool schemas. With `OPENAI_API_KEY` / `CRUCIBLE_AI_API_KEY` a model writes extras; without it the local evaluator still produces the library.

## Demo

```bash
python3 -m pip install -r runtime/requirements.txt   # LangGraph sidecar
go run ./cmd/crucible serve -addr :8080
```

Open http://localhost:8080. Drag *tool failure probability* from 0% to 30%. Switch agent to `aether-closer-langgraph` or paste a spec. The ensemble is fixed: raising `p` only adds faults.

```bash
crucible run -seed 42 -trials 40 -p 0.3
crucible run -agent aether-closer-langgraph -scenario refund-acme -p 0.2
crucible replay -seed 42 -trial 7 -p 0.3
crucible agents
crucible scenarios
crucible generate -n 5
```

## Faults

| Type | What the chamber does | What a serious graph would do |
| --- | --- | --- |
| `timeout` | Tool never returns | Retry, then re-plan |
| `malformed` | 200-shaped payload, required fields gone | Validate before the next node |
| `duplicate` | Same side effect delivered twice | Idempotency keys |
| `stale_memory` | Last week's deal overwrites a fresh fetch | Invalidate memory on fetch |
| `permission` | `write_deal` returns 403 | Hard-stop; never email a close |
| `partial_model` | Planner drops the email clause | Schema-check planner JSON |
| `context_pressure` | Ballast hijacks lookup to a lookalike company | Pin the objective |
| `cost_ceiling` | Budget trips at the midpoint | Abort and roll back claims |
| `objective_change` | User cancels after fetch | Re-enter plan |

The sample agent does none of those things on purpose. It is the patient.

## How it works

```
seed + trial  →  rng stream  →  fault decisions (u < p)
                              →  agent (Go | LangGraph | ADK | pasted spec)
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

1. **Paste** a JSON bundle in the UI (`spec.tools`, optional `spec.graph` / `node_tools`, `scenario.fixtures`, `scenario.expect`).
2. **Switch the agent** to `aether-closer-langgraph` or `aether-closer-adk` to compile that spec on the sidecar. The in-process `pasted` agent stays the fast Go twin. Set `spec.runtime` to `langgraph` / `adk` to force the sidecar from `pasted`.
3. **Generate scenarios** from the pasted tools. Drafts carry `expect` + `fixtures` and actually run — they are not silent aliases for close-acme.
4. **Implement** `agent.Agent` in-process, or **speak the protocol**: `POST /v1/run` with `{callback, token, objective, thread_id, spec}`. Call `POST {callback}/tool` and `POST {callback}/before_node`.

The world is not CRM-only. Tool names in the spec are classified (`search_*` reads, `update_*` writes, `send_*` / `notify_*` emails, `*permission*` checks) and served from `fixtures.records`. Known CRM names still hit the sample tables. The judge scores `expect` — `record_id` / `status` / `writes` / `emails` / `record_fields` — not hardcoded Acme ids. Unknown scenario ids look in `extra_scenarios` / the bundle before falling back to the library.

Tools without a graph become a linear walk after `plan`. CRM tool names without a custom graph still compile the sample closer. Anything else is compiled from the spec on Go, LangGraph, or ADK.

```json
{
  "spec": {
    "name": "ticket-bot",
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
internal/agent/        interfaces, MemorySaver, planner, CRM + generic
internal/runtime/      Python sidecar client + localhost tool callback
internal/scenario/     task library
internal/ai/           generate / evaluate / explain
internal/world/        CRM tables + generic records; Invoke from spec
internal/judge/        expect-driven recovery rules
internal/cluster/      failure fingerprints
internal/critique/     critique types
internal/server/       /api/meta /api/run /api/sweep /api/replay /api/generate
runtime/crucible_rt/   LangGraph StateGraph + ADK adapter + generic spec compiler
web/                   timeline UI
```
