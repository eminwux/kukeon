# kukeon

Build, test, and smoke-test conventions for anyone — human or agent — working on this repo.

## Conventions

- **License header:** Apache 2.0 + SPDX copyright header on every new Go file.
- **Shared error sentinels:** defined in `internal/errdefs/`. Don't declare new `errors.New` in package code when the concept is reusable — add it to `errdefs` instead.
- **Tests colocated with source:** `foo.go` + `foo_test.go`.

## The two default realms

`kuke init` provisions **two** realms, each mapped to its own containerd namespace (see `internal/consts/consts.go`):

| Realm         | Containerd namespace    | Purpose                                                      |
| ------------- | ----------------------- | ------------------------------------------------------------ |
| `default`     | `kukeon.io`             | User workloads. Created empty so `kuke create …` has a home. |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself.                     |

The `kukeond` daemon runs inside the **`kuke-system`** realm — specifically as a container inside the cell `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell). The `default` realm is deliberately left user-owned so `kuke purge --cascade` on it can never take down the daemon.

## Local smoke test: rebuild and re-run `kuke init`

When a change touches the build path, the daemon, or anything under `/opt/kukeon`, run this end-to-end before opening a PR.

### 1. Tear down the existing runtime

Only the `kuke-system` cell needs to be removed; user-realm data under `/opt/kukeon/default` is left intact. `--no-daemon` because the daemon is what we're tearing down.

```bash
sudo kuke kill cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon \
    --no-daemon

sudo kuke delete cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon \
    --no-daemon

sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
```

After these: no containerd tasks in `kuke-system.kukeon.io`, no cell metadata in `/opt/kukeon/kuke-system/kukeon/kukeon/`, `/run/kukeon/` empty.

### 2. Build the binaries

```bash
rm -f kuke kukeond
make kuke           # produces ./kuke
ln -sf kuke kukeond # kukeond is argv[0]-dispatched from the same binary
```

### 3. Build and load the local `kukeond` image (no registry push)

```bash
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker tag kukeon-local:dev docker.io/library/kukeon-local:dev

docker save kukeon-local:dev | \
    sudo ctr -n kuke-system.kukeon.io images import -

sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local
```

### 4. Run `kuke init`

```bash
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

Expected tail of output:

```
    - cell "kukeond": created (image docker.io/library/kukeon-local:dev)
    - cell cgroup: created
    - cell root container cgroup: created
    - cell containers cgroup: created
kukeond is ready (unix:///run/kukeon/kukeond.sock)
```

### 5. Daemon-parity check (the regression guard)

Both commands must return identical output. If the daemon sees a different view of `/opt/kukeon` than the in-process controller, only the `--no-daemon` list will be populated — that's the bind-mount regression this check catches.

```bash
sudo ./kuke get realms              # goes through kukeond over the unix socket
sudo ./kuke get realms --no-daemon  # bypasses kukeond; reads /opt/kukeon in-process
```

Expected (identical) output:

```
NAME         NAMESPACE              STATE  CGROUP
-----------  ---------------------  -----  -------------------
default      kukeon.io              Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system
```

**If your change touches anything the daemon reads, this check must be in the PR's test plan.** Reviewer agent will flag PRs that miss it.

### Inspecting the running daemon

```bash
# Two bind mounts expected: /run/kukeon and /opt/kukeon (host→container, same path).
sudo ctr -n kuke-system.kukeon.io container info \
    kukeon_kukeon_kukeond_kukeond | \
    python3 -c "import sys,json; print(json.dumps([m for m in json.load(sys.stdin)['Spec']['mounts'] if m.get('type')=='bind'],indent=2))"

# Expected process line:
# /bin/kukeond serve --socket /run/kukeon/kukeond.sock --run-path /opt/kukeon
ps -ef | grep '[k]ukeond serve'
```
