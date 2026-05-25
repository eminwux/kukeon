# `kuke init` / `make dev-init` troubleshooting

## `kuke init --run-path <path>` derives the wrong kukeond socket

`kuke init --run-path X` derives `<X>/kukeond.sock` for the daemon socket (PR #569). If the resulting socket is `/run/kukeon/kukeond.sock` regardless of `X`, check for a leftover `/etc/kukeon/kukeond.yaml`:

```bash
ls -l /etc/kukeon/kukeond.yaml
```

`kukeond serve` writes this file on its first start via `internal/serverconfig.WriteDefault` (O_EXCL — first writer wins forever, no rewrite on subsequent starts). The file's `spec.socket` field flows back through `applyServerConfiguration`'s `viper.Set` on every subsequent `kuke init`, which (intentionally) trips `viper.IsSet(KUKEOND_SOCKET)` in `applyRunPathImpliesKukeondSocket` and skips the per-`--run-path` derivation. The behavior is correct for operators who pinned a YAML config; it surprises agents iterating on a fix when a prior `make dev-init` or e2e run left the file behind.

Move it aside before re-running the local repro:

```bash
sudo mv /etc/kukeon/kukeond.yaml /tmp/kukeond.yaml.bak
```

`make dev-init` itself is unaffected because its expected socket is exactly the YAML's pinned `/run/kukeon/kukeond.sock`.
