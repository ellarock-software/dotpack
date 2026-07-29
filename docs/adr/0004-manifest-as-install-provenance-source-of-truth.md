# Manifest as install provenance source of truth

## Context

`uninstall`, `list`, `update`, `doctor`/`reconcile`, and `cache show` all need to know what dotpack installed where. The original handoff proposed injecting `dotpack:` source metadata into each installed file's frontmatter. The new architecture (LLM translation, cache, multiple adapters with different installer strategies) makes a single, structured ledger more attractive.

## Decision

`~/.dotpack/installs.yaml` is the single source of truth for install provenance. Each record carries `{ id, source, kind, agent, scope (project|user), canonical_root, target_root, source_path, source_rel_path, source_sha256, target_dir, files: [...], file_claims: [...], merged_keys: [...] (for hook/mcp-server config-merge installs), cache_key, installed_at }`. Installed drop-file resources are byte-identical to their cache copy — no frontmatter mutation. `file_claims` records the per-output `sha256` so `reconcile` and `inventory` can distinguish "present and unchanged" from "present but user-edited." `target_root` scopes records for daily canonical/materialized workflows, while the user-facing `id` remains `host:kind:name`.

`uninstall` reads the manifest, removes the files (and unmerges the keys for config-merge kinds), then deletes the record. When the global manifest contains the same display ID under multiple project roots, uninstall selects the current project root or an explicit `--target`. Reinstalling the same manifest identity reconciles the owned file set by removing prior claimed files that the replacement plan no longer claims. `dotpack inventory` scans known file-drop target dirs and classifies outputs as tracked, drifted, missing, canonical-untracked, or foreign-untracked. `dotpack sync-back` promotes changed/untracked file-drop outputs into canonical `.agents`, `dotpack reset-materialized` clears manifest-owned materialized output for a target, and `dotpack install-all` rebuilds materialized host config from canonical `.agents`.

## Why

Frontmatter injection mutates content (installed file diverges from cache), needs per-format handling (md vs json vs toml vs whatever a new kind uses), and is fragile to user edits or renames. A central manifest is cheaper to read for `list`, naturally captures config-merge installs (which have no single "installed file" to attach metadata to), and gives `uninstall` an authoritative removal list without filesystem scans. The reconcile step is the user's request: "create a regression check to ensure all installed kinds have entries in manifest; if not, manifest needs to be updated."

## Consequences

The manifest is now a critical user-data file — corruption or accidental deletion makes uninstall impossible without manual cleanup. Mitigations: write atomically (write to tmp, rename), keep a `.backup` of the previous version, and have `dotpack inventory` able to recover file-drop state by scanning target dirs + canonical `.agents` (lossy but recoverable). Per-install records are append-only friendly; future versions might split into per-resource files under `~/.dotpack/installs/{target}/{id}.yaml` if the single file gets too large to read quickly.

Untracked config fragments inside host settings files are not automatically promoted by `sync-back`; they require a format-aware importer that can preserve intent without guessing ownership. `reset-materialized` removes manifest-owned merged keys and leaves untracked merged settings in place so a subsequent install collides loudly instead of silently deleting user-authored configuration.
