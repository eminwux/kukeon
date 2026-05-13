# kuke create

Imperative creation of a single resource. For anything more than a one-off, prefer [`kuke apply`](kuke-apply.md) against a manifest.

```
kuke create <resource> [NAME] [flags]
kuke c      <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`. Each subcommand also has a short alias (`r`, `sp`, `st`, `ce`, `co`).

## kuke create realm

```
kuke create realm [NAME] [--namespace <ns>]
```

| Flag          | Default                       | Description                        |
| ------------- | ----------------------------- | ---------------------------------- |
| `--namespace` | `<realm>.kukeon.io` (derived) | Containerd namespace for the realm |

```bash
sudo kuke create realm mytenant
sudo kuke create realm mytenant --namespace mytenant.kukeon.io
```

## kuke create space

```
kuke create space [NAME] --realm <realm>
```

| Flag      | Default   | Description                   |
| --------- | --------- | ----------------------------- |
| `--realm` | `default` | Realm that will own the space |

```bash
sudo kuke create space blog --realm default
```

## kuke create stack

```
kuke create stack [NAME] --realm <realm> --space <space>
```

| Flag      | Default   | Description               |
| --------- | --------- | ------------------------- |
| `--realm` | `default` | Realm that owns the stack |
| `--space` | `default` | Space that owns the stack |

```bash
sudo kuke create stack wordpress --realm default --space blog
```

## kuke create cell

```
kuke create cell [NAME] --realm <r> --space <s> --stack <t>
```

| Flag      | Default   | Description              |
| --------- | --------- | ------------------------ |
| `--realm` | `default` | Realm that owns the cell |
| `--space` | `default` | Space that owns the cell |
| `--stack` | `default` | Stack that owns the cell |

`create cell` creates an empty cell (no containers). Use [`kuke apply`](kuke-apply.md) with a manifest to create a cell with its containers in one step, or [`kuke run`](kuke-run.md) for a one-shot create+start.

```bash
sudo kuke create cell web --realm default --space blog --stack wordpress
```

## kuke create container

Adds a container to an existing cell.

```
kuke create container [NAME] --cell <cell> [--realm <r> --space <s> --stack <t>] [container flags]
```

| Flag                 | Default                           | Description                                                  |
| -------------------- | --------------------------------- | ------------------------------------------------------------ |
| `--realm`            | `default`                         | Realm of the parent cell                                     |
| `--space`            | `default`                         | Space of the parent cell                                     |
| `--stack`            | `default`                         | Stack of the parent cell                                     |
| `--cell`             | _(required)_                      | Cell that owns the container                                 |
| `--image`            | `docker.io/library/debian:latest` | Container image                                              |
| `--command`          | (empty)                           | Command to run                                               |
| `--args`             | (empty, repeatable)               | Arguments                                                    |
| `--env`              | (empty, repeatable)               | `KEY=VALUE` env var                                          |
| `--port`             | (empty, repeatable)               | Port mapping                                                 |
| `--volume`           | (empty, repeatable)               | Volume mount                                                 |
| `--tmpfs`            | (empty, repeatable)               | Tmpfs mount, `path[:opts]` (e.g. `/tmp:size=64m`)            |
| `--network`          | (empty, repeatable)               | Network to join                                              |
| `--network-alias`    | (empty, repeatable)               | Network alias                                                |
| `--user`             | (empty)                           | Run as `UID[:GID]` (e.g. `1000:1000`)                        |
| `--privileged`       | `false`                           | Run privileged                                               |
| `--read-only`        | `false`                           | Mount the root filesystem read-only                          |
| `--root`             | `false`                           | Mark this container as the cell's root container             |
| `--cap-add`          | (empty, repeatable)               | Linux capability to add (e.g. `NET_ADMIN`)                   |
| `--cap-drop`         | (empty, repeatable)               | Linux capability to drop (e.g. `ALL` or `NET_ADMIN`)         |
| `--security-opt`     | (empty, repeatable)               | Security option (e.g. `no-new-privileges`, `seccomp=unconfined`) |
| `--cpu-shares`       | `0`                               | Relative CPU weight (cgroup `cpu.shares`)                    |
| `--memory`           | (empty)                           | Hard memory limit (bytes, or with suffix `k`/`m`/`g`)        |
| `--pids-limit`       | `0`                               | Maximum number of PIDs (0 leaves unset)                      |
| `--cni-config-path`  | (empty)                           | Override CNI config dir for this container                   |
| `--restart-policy`   | (empty)                           | Restart policy for the container                             |
| `--label`            | (empty, repeatable)               | `KEY=VALUE` label                                            |

```bash
sudo kuke create container nginx \
    --cell web --realm default --space blog --stack wordpress \
    --image docker.io/library/nginx:alpine \
    --root
```

!!! warning "The `--image` default"
    If you don't pass `--image`, Kukeon uses `docker.io/library/debian:latest`. Always pass `--image` explicitly when you care what runs.

## Imperative vs. declarative

`kuke create` is useful for quick experiments and one-off resources. For anything you want to commit, diff, or apply repeatedly, write a YAML manifest and use [`kuke apply`](kuke-apply.md). Manifests are the unit of version control; imperative commands are not.

## Related

- [kuke apply](kuke-apply.md) — declarative alternative
- [kuke run](kuke-run.md) — create + start a single cell in one shot
- [Manifest Reference](../manifests/overview.md) — the schema for each resource
