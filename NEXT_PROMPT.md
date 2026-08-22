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

3. When you are done, give me the next prompt. That prompt must circle this same loop: the vision above, a skeptical current-state note (what is actually true vs what the docs say), an instruction to make the pitch true with another broad autonomous chunk, and these same How to work rules copied forward so the next agent does not shrink the job into a narrow follow item.

## Current state (verify; do not trust this paragraph)

The weekend MVP and the slider demo are real. Pasted JSON schemas can compile on LangGraph/ADK into a chamber-built walk, and generated scenarios can carry expect + fixtures. That is not “give it an agent.” You still cannot drop in a real graph, a real ADK agent, or an arbitrary tool-using process. Several faults, the planner, and the critique are still CRM-shaped.

Make the pitch true. The chamber should take an arbitrary tool-using agent, run the full fault catalog against the same task, judge recovery from the world and expect (not Acme fields), cluster how it fails, and write a critique that would be correct for that agent. The runner stays deterministic. AI may generate, score ambiguity, and explain.

Then give me the next prompt (that will circle this loop again like you just experienced and past threads).
