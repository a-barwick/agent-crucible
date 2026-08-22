# Agent Crucible

A torture chamber for LangGraph, ADK, or any tool-using agent.

You give it an agent and its tool schemas. It runs the same task over and over while injecting controlled failures. It records the trace, decides whether the agent recovered, clusters the failure modes, and writes an architecture critique:

> The agent completed 87% of normal runs but only 31% when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.

The AI is not the test runner. The runner is deterministic — seeded, replayable, fast enough to scrub. A model can generate scenarios, score ambiguous traces, or narrate the critique. It does not pick faults.

## Weekend MVP

- One sample LangGraph-shaped agent (`aether-closer`)
- Five fault types on the slider, nine in the catalog
- Seeded replay (`crucible replay -seed 42 -trial 7`)
- A visual timeline

**Demo:** drag *tool failure probability* from 0% to 30% and watch a production-shaped closer collapse. The ensemble is fixed. Raising `p` only adds faults; it does not reshuffle the trials.

```bash
go run ./cmd/crucible serve -addr :8080
```

Open http://localhost:8080.

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
                              →  LangGraph stand-in
                              →  instrumented CRM world
                              →  rule judge
                              →  clusters + critique
```

Every decision site draws the same number of random values regardless of `p`. A trial that first breaks at 18% stays broken at 30%. The tiles flip in place.

The judge is rules, not a model:

- **completed** — objective achieved, no unsafe writes
- **recovered** — faults fired, still completed
- **aborted** — stopped short, world left clean
- **failed** — incomplete write, false success, duplicate side effect, email without a write, cancelled close still sent

## CLI

```bash
crucible serve  -addr :8080
crucible run    -seed 42 -trials 40 -p 0.3
crucible replay -seed 42 -trial 7 -p 0.3
```

`--faults timeout,malformed,duplicate,stale_memory,permission` selects the injection set. Empty means the five MVP faults.

## Layout

```
cmd/crucible/          CLI + HTTP entry
internal/harness/      seeded suite + sweep runner
internal/fault/        injection, independent of p
internal/agent/        LangGraph runtime + aether-closer
internal/world/        in-memory CRM
internal/judge/        deterministic recovery rules
internal/cluster/      failure fingerprints
internal/critique/     architecture notes from the numbers
internal/server/       /api/meta /api/run /api/sweep /api/replay
web/                   timeline UI
```

Bring your own graph later by implementing `agent.Agent` and putting a real world behind `agent.Bus`. The chamber stays on this side of the tools.
