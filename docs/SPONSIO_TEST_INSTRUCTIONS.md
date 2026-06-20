# Testing Sponsio Integration

These checks are useful when you opt into dotpack's Sponsio lifecycle task with
`--run-lifecycle`.

## Check the Binary

```sh
sponsio --version
```

If the binary is not on `PATH`, set `DOTPACK_SPONSIO_BINARY` to an absolute
binary path before running dotpack lifecycle verification:

```sh
export DOTPACK_SPONSIO_BINARY=/path/to/sponsio
```

## Check Sponsio Health

From the project root:

```sh
sponsio doctor
sponsio validate --config sponsio.yaml
```

If either command reports a failure, fix Sponsio before running dotpack with
`--run-lifecycle`.

## Check dotpack Lifecycle

Run a targeted install with lifecycle enabled:

```sh
dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project --run-lifecycle
```

Without `--run-lifecycle`, dotpack intentionally skips Sponsio verification.
