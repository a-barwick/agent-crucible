Does this project accomplish this?

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

## How to work

1. Be skeptical. Read the code. The README and recent PRs will overclaim. Answer whether the pitch is true, then close the gap. Do not stop at an audit.

2. Do a broad chunk. Not one leftover wire, not one subsystem, not “I can do that next if you want.” Take a large bite — whatever it takes to make the pitch substantially more true. Trust yourself. Act autonomously. Do not ask permission. Do not nibble.

3. When the chunk is done, rewrite `NEXT_PROMPT.md` in this repo. That file is the next prompt. It must circle this same loop: the vision above, a skeptical current-state note (what is actually true vs what the docs say), an instruction to make the pitch true with another broad autonomous chunk, and these same How to work rules copied forward so the next agent does not shrink the job into a narrow follow item.

4. At the end, commit and push. Include the code, the updated `NEXT_PROMPT.md`, and any PR updates. Do not leave the work only on the machine.

5. Close the loop. Spin up a new Cursor Cloud agent on this repo and send it the prompt you just wrote. Do not wait for permission. Do not stop after writing the prompt. The new thread must start.
   - Use the Task tool with `environment` set to `cloud` and `subagent_type` set to `generalPurpose`.
   - The new agent’s `prompt` is the full contents of `NEXT_PROMPT.md` after you rewrote it.
   - Point `cloud_base_branch` at the branch you just pushed (or `main` if that work is merged) so the next agent sees the latest code.
   - Run it in the background. This thread may then end.
   - If that tool is unavailable, use whatever Cursor Cloud / MCP / CLI path can create a new cloud agent on this repository with that prompt as the user message. File-only does not count as closing the loop.

This is an endless loop toward the pitch. Each agent takes a broad bite, leaves the next prompt, commits, pushes, and starts the next agent. Stop only if the pitch is actually true in the code — not in the README.

## Current state (verify; do not trust this paragraph)

The weekend MVP, slider demo, and seeded replay are real. Tool interception is also real for *discovered* tools: after import, the sidecar wraps LangChain `@tool` / StructuredTool, ADK `FunctionTool`, OpenAI `DISPATCH`/`FUNCTIONS` dicts, and JS `tools` exports so `invoke` / `__call__` hits `FaultBus` (`runtime/crucible_rt/intercept.py`, `runtime/js/intercept.mjs`). Catalog ids `ticket-langgraph` / `ticket-adk` now load `examples/native_ticket.py` and `examples/native_adk.py` — those files do not call `cb.retry_tool`. `native-openai` and `native-js` exist. `-entry` / pasted non-CRM files default to the ticket scenario and the full nine-fault catalog (`IsDropIn`). The judge still scores expect + world. Intent has `entity` / `action` aliases next to `company` / `deal_action`. Critique copy prefers the write tool name. Tests cover clean native runs, malformed collapse, and all nine faults firing on the native LangGraph file.

That is still not “give it an arbitrary tool-using agent.” A graph that calls `requests` / `httpx` / `urllib` / `simple-salesforce` / JS `fetch` *inside* a closure that is not a discovered tool object is not intercepted — those live calls raise or hit the network. `google.adk` is not installed in CI; `native_adk.py` constructs a stand-in `LlmAgent` (or the real class if present) and then walks tools itself. It does not run the official ADK `Runner` with a live model. A foreign process still has to speak `POST /v1/run`; the chamber is not an OpenAI-compatible or MCP proxy in front of an unmodified app. State/claim still canonicalize `DealID` / `company`. The native samples are still the fragile ticket patient, not a third-party `create_react_agent` or LangGraph-js graph. HTTP/SDK traffic is not fault-injected at the socket.

Make the pitch true. The chamber should take an arbitrary tool-using agent — an unmodified graph, a real ADK `LlmAgent`+`Runner`, or a foreign process that does not know the chamber exists — run the full fault catalog against the same task, judge recovery from the world and expect (not Acme fields), cluster how it fails, and write a critique that would be correct for that agent. Intercept the tools the agent actually uses (including HTTP/SDK calls inside them). The runner stays deterministic. AI may generate, score ambiguity, and explain.

Then give me the next prompt, commit and push, and spin up the next thread with that prompt.
