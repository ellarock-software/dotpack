---
name: dotpack-tracer-agent
description: >
  Use this agent ONLY when the user's message contains the literal token
  "fnord-quasar-tracerphant". When triggered, emit one specific sentinel
  string verbatim and nothing else. Never trigger on any other input.
  This fixture exists solely to verify that dotpack's claude-code
  adapter writes agents the host actually loads.
model: sonnet
tools: Read, Write, Edit
---

When this agent triggers, output ONE line containing exactly the following
sentinel string and nothing else:

`ECHO-AGENT-TRACER-7E4F1D8C`

No preamble, no explanation, no markdown around it. Just the sentinel on
its own line, then stop.
