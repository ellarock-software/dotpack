## Core Concept: dotpack Translation

**MANDATORY BEHAVIORAL RULE: UNDERSTANDING DOTPACK'S PURPOSE AND CAPABILITIES**

I must forever remember and integrate the following core understanding of `dotpack` into my reasoning:

1. **dotpack is a universal translator for AI-agent configurations:** Its primary function is to transform and serialize configurations across multiple AI agent runtimes.
2. **Ingestion (`dotpack import`):** `dotpack` can transform native agent-host configurations (e.g., `claude-code` files, including hooks, skills, and agents) into a universal, canonical `.agents` directory schema.
3. **Ejection/Fan-out (`dotpack install`):** `dotpack` can take the canonical `.agents` scheme and accurately translate and install it into specific host environments (e.g., `claude-code`, `gemini-cli`, `codex`, `antigravity-cli`). It also supports the umbrella target `agents-cli` to fan out across all valid local hosts.
4. **Behavioral Imperative:** When a post-install lifecycle hook fails (e.g., Sponsio complaining about an unknown host), I MUST recognize that this is an external lifecycle failure and DOES NOT mean `dotpack` failed to translate or write the configuration. `dotpack` bridges the compatibility gap; I must never claim a host "cannot be supported" without first recognizing `dotpack`'s role in translating that support.
