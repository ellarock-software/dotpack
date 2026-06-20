# Optional Sponsio Lifecycle Hardening

dotpack can run optional post-install lifecycle tasks after it materializes host
configuration. The bundled task is for teams that use Sponsio to verify runtime
guard wiring.

Lifecycle tasks are **not enabled by default**. A normal install writes host
files and exits without invoking Sponsio or any other external lifecycle tool.

## Enable Lifecycle Verification

Install and configure Sponsio separately, then pass `--run-lifecycle`:

```sh
dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project --run-lifecycle
dotpack install-all --from .agents --target . --agent agents-cli --scope project --run-lifecycle
```

dotpack resolves the Sponsio binary from `PATH` or `DOTPACK_SPONSIO_BINARY`.
When lifecycle verification is enabled, the bundled task:

1. Runs `sponsio host install claude-code --mode observe`.
2. Runs `sponsio host status claude-code`.
3. Fails the dotpack command if verification fails.

The install output is still materialized before lifecycle verification runs. If
verification fails, inspect the reported Sponsio command and fix the Sponsio
installation or hook wiring before treating the install as complete.

## Recommended Use

Use `--run-lifecycle` in team environments where Sponsio is part of your agent
runtime safety model. Leave it unset for plain translation, local experiments,
and public examples that should not require extra tooling.
