# dotpack

dotpack is a package manager for AI-agent resources. It defines a portable schema and on-disk template for those resources, and uses adapters to install them into specific agent hosts (Claude Code, Cursor, GitHub Copilot, ...).

## Language

**Resource**:
A single unit dotpack installs — e.g., one skill, one agent, one command. A resource is schema-conformant and laid out per its kind's template.
_Avoid_: Package, artifact, item.

**Kind**:
The category of a resource (skill, agent, command, ...). Each kind has its own template and its own install behavior per adapter.
_Avoid_: Type, class, category.

**Schema**:
The metadata contract a resource must satisfy — frontmatter fields, their types, which are required, what each `kind`'s fields mean. dotpack keeps the schema minimal so adapters have less to map and authors have less to learn.
_Avoid_: Spec, manifest format, header.

**Template**:
The on-disk shape of a resource of a given kind — e.g., a skill is a directory containing `SKILL.md` plus optional sub-folders. Templates are kept minimal alongside the schema.
_Avoid_: Layout, structure, format.

**Adapter**:
The component that materializes a schema-and-template-conformant resource into a target agent host's native filesystem layout and configuration. An adapter may serve one host (e.g., `claude-code`) or several that converge on a shared convention (e.g., `agents-cli` writes to `.agents/` and `~/.agents/`, which both Gemini CLI and Codex CLI honor as a skills-dir alias).
_Avoid_: AgentHost, target, backend.

**File-drop adapter**:
The shared **adapter** implementation that handles **kinds** whose install is a file write (skill, agent, command, memory). The deep module lives in `internal/adapter/filedrop`; the per-host packages (`claudecode`, `gemini`, `codex`) shrink to per-host `Policy` declarations that wire the host's `HostID`, per-kind `Layout`, and `AgentToolsShape` into the deep impl. Distinguished from config-fragment adapters (future, for `hook` and `mcp-server` kinds per [ADR-0016](docs/adr/0016-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) §5–§7) which merge JSON/TOML fragments into existing host config files rather than writing standalone files.
_Avoid_: dropfile, write-file adapter, file adapter.

**Host support**:
Per-**adapter** declaration of which **kinds** the host can install. For the file-drop adapter this is `Policy.Layouts` membership — a kind present in `Layouts` is supported; a kind absent yields `Plan: kind X not yet supported`. Per-instance lossiness (a source field the target host can't honour) is computed by the orchestrator from schema aliases per ADR-0016 §8, not declared by the adapter. Default install policy refuses lossy installs unless the user passes `--allow-lossy`.
_Avoid_: Capability matrix, support table, compatibility map.

**Validator**:
A deterministic check that a candidate resource conforms to dotpack's **schema** and **template**. Runs on every install. Implemented in dotpack-the-binary (no LLM).
_Avoid_: Linter, checker.

**Translator**:
An LLM agent that rewrites a non-conformant in-the-wild resource (and its associated files) into a schema-and-template-conformant resource. Runs only when the **validator** rejects the source.
_Avoid_: Importer, converter.

**Reviewer**:
An LLM agent that checks a **translator** output for correctness against the source — does it faithfully express the same behavior, no drift, no fabricated capabilities. Advisory gate before persistence.
_Avoid_: Critic, verifier.

**Security agent**:
An LLM agent that scans a **translator** output for prompt-injection, exfiltration, hidden shell, and similar attacks introduced (or passed through) during translation. Runs in parallel with the **reviewer**. Advisory gate before persistence.
_Avoid_: Scanner, auditor.

## Example dialogue

> **Dev:** I want to install the `code-review` skill from `owner/repo` into Claude Code.
>
> **Designer:** OK — `code-review` is a **resource** of **kind** `skill`. dotpack will read its frontmatter against the **schema**, verify the directory matches the skill **template**, then hand it to the Claude Code **adapter** to drop into `.claude/skills/`.
>
> **Dev:** What if I also want it in Cursor?
>
> **Designer:** Same resource, same schema, same template — the Cursor **adapter** decides where it lands and how (if at all) any Cursor-specific fields get filled in.
