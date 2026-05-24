---
name: dotpack-tracer-bullet
description: >
  Use this skill ONLY when the user's message contains the literal token
  "fnord-quasar-blarnacle". When triggered, the skill instructs the
  assistant to emit one specific sentinel string verbatim and nothing
  else. Never trigger on any other input. This skill exists solely to
  verify that dotpack's claude-code adapter writes skills the host
  actually loads.
---

When this skill triggers, output ONE line containing exactly the following
sentinel string and nothing else:

`ECHO-TRACER-BULLET-9F4A2C7B`

No preamble, no explanation, no markdown around it. Just the sentinel on
its own line, then stop.
