# kuke version

Print client and daemon version strings; warn on mismatch.

```
kuke version [--no-daemon] [--strict]
```

## Output

By default, `kuke version` prints the client version, then queries the daemon for its version and prints that on a second line. If the two strings differ, a warning is emitted on stderr; with `--strict`, the command also exits non-zero.

```
$ kuke version
Client: v0.6.0
Daemon: v0.6.0
```

When the daemon is unreachable (socket missing, daemon down), the second line is replaced by a `Warning: daemon unreachable: <err>` on stderr; the client-version stdout line still prints and the exit code stays 0. Use `--no-daemon` to skip the daemon query entirely.

```
$ kuke version
Client: v0.6.0
Warning: daemon unreachable: dial unix:///run/kukeon/kukeond.sock: connect: no such file or directory

$ kuke version --no-daemon
Client: v0.6.0
```

For release builds, the version string is the semver tag. For dev builds, it's whatever `VERSION` was passed at build time (often `v0.0.0-dev` or empty).

## Flags

| Flag          | Default | Description                                                                                                              |
| ------------- | ------- | ------------------------------------------------------------------------------------------------------------------------ |
| `--no-daemon` | `false` | Skip the daemon query — print only `Client: <ver>`                                                                       |
| `--strict`    | `false` | Exit with non-zero status on a client/daemon version mismatch (the warning on stderr is emitted in both modes)           |

Plus all [global flags](kuke.md).

## Daemon mismatch behavior

`Warning: version mismatch (client: <c>, daemon: <d>)` is emitted on stderr whenever the two version strings differ. Without `--strict`, the exit code stays 0 (informational warning); with `--strict`, the exit code is non-zero. Pair `--strict` with CI guards on host-upgrade workflows where the operator wants a hard fail if `kuke` and `kukeond` drift.
