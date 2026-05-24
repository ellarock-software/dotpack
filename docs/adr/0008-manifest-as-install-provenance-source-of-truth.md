# Manifest as install provenance source of truth

## Context

`uninstall`, `list`, `update`, `doctor`/`reconcile`, and `cache show` all need to know what dotpack installed where. The original handoff proposed injecting `dotpack:` source metadata into each installed file's frontmatter. The new architecture (LLM translation, cache, multiple adapters with different installer strategies) makes a single, structured ledger more attractive.

## Decision

`~/.dotpack/installs.yaml` is the single source of truth for install provenance. Each record carries `{ id, source: <owner>/<repo>@<sha>#<path>, kind, agent, scope (project|user), target_dir, files: [...], merged_keys: [...] (for hook/mcp-server config-merge installs), cache_key, installed_at }`. Installed drop-file resources are byte-identical to their cache copy — no frontmatter mutation. `uninstall` reads the manifest, removes the files (and unmerges the keys for config-merge kinds), then deletes the record. A `dotpack reconcile` / `dotpack doctor` command scans known target dirs and flags any installed-looking resource that has no manifest entry, so drift is detectable.

## Why

Frontmatter injection mutates content (installed file diverges from cache), needs per-format handling (md vs json vs toml vs whatever a new kind uses), and is fragile to user edits or renames. A central manifest is cheaper to read for `list`, naturally captures config-merge installs (which have no single "installed file" to attach metadata to), and gives `uninstall` an authoritative removal list without filesystem scans. The reconcile step is the user's request: "create a regression check to ensure all installed kinds have entries in manifest; if not, manifest needs to be updated."

## Consequences

The manifest is now a critical user-data file — corruption or accidental deletion makes uninstall impossible without manual cleanup. Mitigations: write atomically (write to tmp, rename), keep a `.backup` of the previous version, and have `dotpack reconcile` able to rebuild a manifest by scanning target dirs + the cache (lossy but recoverable). Per-install records are append-only friendly; future versions might split into per-resource files under `~/.dotpack/installs/<id>.yaml` if the single file gets too large to read quickly.
