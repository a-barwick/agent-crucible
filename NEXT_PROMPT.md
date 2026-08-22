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

The weekend MVP, slider demo, and seeded replay are real. You can now drop in a chamber-aware agent: `spec.entry` imports a Python file that exports `run(cb, req)` / `build(cb)` (`examples/ticket_graph.py`, `examples/ticket_adk.py`), and `spec.endpoint` / agent `remote` talks to any `POST /v1/run` process (`examples/http_agent.py`). The full nine-fault catalog fires on that ticket task. The judge scores expect + world, not Acme deal fields. Critique copy names `update_ticket` instead of “the CRM tool” when the spec is not the closer. That is still not “give it an agent.” A real LangGraph or ADK agent that calls its own tools (HTTP, Salesforce, `@tool`, `FunctionTool`) is not intercepted. The user has to rewrite the agent around `cb.retry_tool`. Intent/State/planner are still `company` / `deal_action` / `DealID`. `ticket-adk` does not actually construct `google.adk.LlmAgent`. There is no JS/OpenAI-tools drop-in.

Make the pitch true. The chamber should take an arbitrary tool-using agent — an unmodified graph, a real ADK agent, or a foreign process — run the full fault catalog against the same task, judge recovery from the world and expect (not Acme fields), cluster how it fails, and write a critique that would be correct for that agent. The runner stays deterministic. AI may generate, score ambiguity, and explain.

Then give me the next prompt, commit and push, and spin up the next thread with that prompt.
