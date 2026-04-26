# Attaching to a claude-code cell

This guide takes a freshly-`kuke init`-ed host from zero to an attached
`claude-code` session in two commands. Apply a single manifest, then attach.

## Prerequisites

- `kuke init` has completed on the host (the `default/default/default`
  realm/space/stack tuple is in place — that's what `kuke init` provisions).
- `sbsh` and `sb` are on the host's `$PATH`. `kuke attach` is a thin client
  that hands the terminal off to `sb` via `syscall.Exec`; bytes never traverse
  `kukeond`.

If you haven't bootstrapped yet, see [Init and reset](init-and-reset.md).

## 1. Save the manifest

Save the following as `claude-code.yaml`. It declares one Cell with a single
attachable container running the claude-code image. Because no container is
explicitly marked `root: true`, the runner provisions a default root container
to hold the cell's network namespace; the `claude` container runs alongside it
and is the one `kuke attach` connects to.

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: claude-code
spec:
  id: claude-code
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: claude
      image: docker.io/anthropic/claude-code:latest
      command: claude
      attachable: true
```

The image is treated as self-contained — no API keys, env vars, or volumes are
declared here. Anything `claude-code` needs to configure itself (auth, model
selection, workspace) is the image's responsibility.

## 2. Apply it

```bash
sudo kuke apply -f claude-code.yaml
```

`apply` creates the cell and starts its containers. Once the command returns,
the workload task is running and the per-container `sbsh` socket is in place.

## 3. Attach

```bash
sudo kuke attach \
    --realm default \
    --space default \
    --stack default \
    --cell claude-code \
    --container claude
```

You'll land in the live `claude-code` prompt. Bytes flow PTY → `sb` → the
container's `sbsh terminal` wrapper → the `claude` process; `kukeond` is not
in the data path.

`--container claude` is shown for clarity; you can omit it — `kuke attach`
auto-picks the only non-root attachable container in the cell.

## 4. Detach

Press `Ctrl+]` twice in quick succession (the sbsh detach keystroke). The `sb`
client exits cleanly, but the `claude` process inside the container keeps
running. Re-attach later with the same `kuke attach` command and you're back
at the same session.

## Tear down

When you're done with the cell:

```bash
sudo kuke delete cell claude-code \
    --realm default --space default --stack default --cascade
```

`--cascade` removes every container in the cell along with the cell itself.

## See also

- [Apply manifests](apply-manifests.md) — the full `kuke apply` story.
- [CLI Reference → kuke](../cli/kuke.md) — every command, every flag.
