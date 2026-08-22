# LOOP STOPPED

The user halted this loop. Do **not** spin up a new Cursor Cloud agent. Do **not** use the Task tool with `environment=cloud` to continue this chain. Do **not** treat the vision below as a standing order to start another thread.

This file is an archive of the pitch, not a launch prompt.

---

A torture chamber for LangGraph, ADK, or arbitrary tool-using agents.

You give it an agent and its tool schemas. It runs the same task repeatedly while injecting controlled failures:

Tool timeout
Malformed tool result
Duplicate event
Stale memory
Missing permission
Partial model response
Context-window pressure
Cost ceiling reached halfway through
User changes the objective after execution begins

It records the trace, determines whether the agent recovered correctly, clusters failure modes, and produces an architecture critique:

The agent completed 87% of normal runs but only 31% when the CRM tool returned a successful response with missing fields. The graph treats semantic failure as transport success. Add validation before the write node.

The AI is not the test runner. The runner should be deterministic. AI generates scenarios, evaluates ambiguous outcomes, and explains systemic patterns.

Weekend MVP: support one sample LangGraph agent, five fault types, seeded replay, and a visual timeline.

Demo moment: drag a “tool failure probability” slider from 0% to 30% and watch the supposedly production-ready agent collapse.

## Stopped state (last loop note, unverified)

The weekend MVP, slider demo, and seeded replay are real. HTTP/SDK intercept is in-process. Discovered `@tool` / `FunctionTool` / OpenAI dispatch / JS `tools` can hit `FaultBus`. Catalog includes chamber-aware and native ticket samples. That is still not an unmodified production agent the chamber did not import or boot.

Do not continue the loop from this file.
