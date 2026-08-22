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

The weekend MVP, slider demo, and seeded replay are real. HTTP/SDK intercept is now also real *in-process*: after import (or `python3 -m crucible_rt.boot`), the sidecar patches `urllib.request.urlopen`, late-imports `requests` / `httpx` / `simple_salesforce`, and JS `fetch` so a live call to `tickets.example` hits `FaultBus` (`runtime/crucible_rt/httpio.py`, `runtime/js/httpio.mjs`). Discovered `@tool` bodies run first so the HTTP patch is what fires; if the body never touches the network the wrap still falls back to `cb.tool`. Catalog ids `http-closure` (nodes call urllib; no tool objects), `native-react` (`langgraph.prebuilt.create_react_agent` + scripted model), and `foreign-http` (`examples/foreign_task.py`, also `spec.command` + boot wrap) exist. Native ticket/ADK/OpenAI/JS files actually `http_json` / `fetch` `http://tickets.example`. Sidecar serves deterministic `POST /v1/chat/completions`. Judge still scores expect + world. `State` has `RecordID` next to `DealID`. Tests cover urllib/requests/httpx intercept, clean closure/react/foreign/wrap runs, malformed collapse on the HTTP-in-node graph, and all nine faults firing there.

That is still not “give it an arbitrary tool-using agent.” `google.adk` is not installed; `native_adk.py` still constructs a stand-in `LlmAgent` and walks tools itself — it does not run the official ADK `Runner` with a live or bundled model. `native-react` is *our* prebuilt graph with a planted scripted planner (junk hijack, stale memory), not a third-party repo’s `create_react_agent`. A foreign binary that the chamber did not import or boot is not intercepted: there is no HTTP(S) proxy / MITM on the callback, no `HTTP_PROXY` path that works without our process wrapper, and no MCP or OpenAI-compatible front that can sit in front of an unmodified app that already has its own server. The OpenAI proxy only emits ticket `search_ticket` / `update_ticket` tool_calls. State/claim still carry `DealID` / `company`. HTTPS to a real host is not faulted at the socket. The patient is still the fragile ticket task.

Make the pitch true. The chamber should take an arbitrary tool-using agent — an unmodified graph, a real ADK `LlmAgent`+`Runner`, or a foreign process that does not know the chamber exists — run the full fault catalog against the same task, judge recovery from the world and expect (not Acme fields), cluster how it fails, and write a critique that would be correct for that agent. Intercept the tools the agent actually uses (including HTTP/SDK calls inside them, including traffic from a process the chamber did not import). The runner stays deterministic. AI may generate, score ambiguity, and explain.

Then give me the next prompt, commit and push, and spin up the next thread with that prompt.
