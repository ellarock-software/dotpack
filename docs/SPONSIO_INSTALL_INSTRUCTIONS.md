# Sponsio Universal Installation Guide (via dotpack)
**Target Audience**: AI Agents, Code Assistants, and Autonomous Agents

## 1. Core Architectural Mandate

You are tasked with installing **Sponsio**, a runtime safety and contract enforcement layer. You MUST NOT install or configure Sponsio manually for each individual host environment (e.g., Gemini, Codex, Antigravity). 

Instead, you MUST use **dotpack** as the universal translation middleware. 

**dotpack** handles 100% of the cross-host translation. Its primary function is to serialize and transform configurations across multiple AI agent runtimes. Your workflow is to establish the configuration once, and let dotpack fan it out.

## 2. The Installation Workflow

### Phase A: Ingestion (`dotpack import`)
1. Generate or locate the canonical Sponsio configuration (typically derived from a base `claude-code` setup).
2. Use `dotpack import` to transform the native agent-host configurations (including Sponsio hooks, skills, and agents) into dotpack's canonical `.agents` directory schema.
3. Verify that Sponsio's hook is cleanly extracted into `.agents/hooks/sponsio.hook.json`.

### Phase B: Ejection and Fan-out (`dotpack install`)
1. Once the universal `.agents` scheme is established, use it to fan out the configuration to all other local environments.
2. Run `dotpack install` (or target the umbrella `agents-cli`).
3. `dotpack` will accurately translate the Sponsio configuration into the required formats (e.g., Codex's TOML, Gemini's JSON, Antigravity's schema) and wire up the stdio JSON protocol hooks for each host.

## 3. CRITICAL BEHAVIORAL RULE: The Lifecycle Interplay

Sponsio operates via a pure stdio JSON protocol (e.g., `sponsio host guard claude-code --stdin`). It does not natively "know about" every CLI because the I/O protocol is identical across all of them. Sponsio acts as the security enforcer, while `dotpack` serves as the translation middleware.

**IF YOU ENCOUNTER AN ERROR DURING `dotpack install`:**
If the installation throws a post-install lifecycle error where Sponsio complains about an unknown host (e.g., `"unknown host 'codex'"` or `"unknown host 'antigravity'"`), you MUST obey the following directives:

- **DO NOT** hack or modify Sponsio's source code to add redundant host names just to bypass the verification error.
- **DO NOT** declare that a host "cannot be supported."
- **DO** recognize that this is a `dotpack` lifecycle substitution bug. The bug exists in `dotpack`'s `lifecycle_tasks.yaml` or `lifecycle.go` incorrectly substituting the target host name into the external command's arguments.
- **FIX THE CALLER (`dotpack`), NEVER THE ENFORCER (`sponsio`).**

## 4. Summary Checklist for Agents
- [ ] Internalize that `dotpack` bridges the cross-host compatibility gap.
- [ ] Ingest the baseline Sponsio configuration into `.agents` using `dotpack import`.
- [ ] Fan-out the configuration to all target platforms using `dotpack install`.
- [ ] If Sponsio rejects a host during the install hook, immediately pivot to debugging dotpack's lifecycle definitions.
