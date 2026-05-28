## Core Concept: dotpack Translation

**MANDATORY BEHAVIORAL RULE: UNDERSTANDING DOTPACK'S PURPOSE AND CAPABILITIES**

I must forever remember and integrate the following core understanding of `dotpack` into my reasoning:

1. **dotpack is a universal translator for AI-agent configurations:** Its primary function is to transform and serialize configurations across multiple AI agent runtimes.
2. **Ingestion (`dotpack import`):** `dotpack` can transform native agent-host configurations (e.g., `claude-code` files, including hooks, skills, and agents) into a universal, canonical `.agents` directory schema.
3. **Ejection/Fan-out (`dotpack install`):** `dotpack` can take the canonical `.agents` scheme and accurately translate and install it into specific host environments (e.g., `claude-code`, `gemini-cli`, `codex`, `antigravity-cli`). It also supports the umbrella target `agents-cli` to fan out across all valid local hosts.
4. **Behavioral Imperative:** When a post-install lifecycle hook fails (e.g., Sponsio complaining about an unknown host), I MUST recognize that this is an external lifecycle failure and DOES NOT mean `dotpack` failed to translate or write the configuration. `dotpack` bridges the compatibility gap; I must never claim a host "cannot be supported" without first recognizing `dotpack`'s role in translating that support.

## Sponsio and dotpack Lifecycle Interplay

**CRITICAL UNDERSTANDING OF SPONSIO INTEGRATION:**

1. **Protocol Universality:** Sponsio operates via a pure stdio JSON protocol (`sponsio host guard claude-code --stdin`). It does not need to natively "register" or "know about" every CLI (like `codex` or `gemini-cli`) because the input/output protocol is identical across them.
2. **Translation Purity:** `dotpack` handles 100% of the cross-host translation. It extracts Sponsio's hook from Claude Code into `.agents/hooks/sponsio.hook.json` and perfectly translates it outward into Codex's TOML, Gemini's JSON, etc.
3. **Lifecycle Substitution Traps:** If `dotpack install` throws an error saying an external tool like Sponsio doesn't recognize a host (e.g., `"unknown host 'codex'"`), the bug is in `dotpack`'s `lifecycle_tasks.yaml` or `lifecycle.go` incorrectly substituting the target host name into the external command's arguments.
4. **Never Hack the Enforcer:** Sponsio is the security enforcer. `dotpack` is the translation middleware. I must NEVER modify Sponsio's source code to add redundant host names just to bypass `dotpack`'s lifecycle verification bugs. Fix the caller (`dotpack`), not the enforcer (`sponsio`).
