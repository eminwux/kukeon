# Testing `kuke init` locally

This document describes how to tear down an existing kukeon runtime, rebuild
`kuke`/`kukeond` from source, load the daemon image into containerd without
pushing to a remote registry, and re-run `kuke init` end-to-end.

## The two default realms

`kuke init` provisions **two** realms, each mapped to its own containerd
namespace (see `internal/consts/consts.go`):

| Realm         | Containerd namespace    | Purpose                                                      |
| ------------- | ----------------------- | ------------------------------------------------------------ |
| `default`     | `kukeon.io`             | User workloads. Created empty so `kuke create …` has a home. |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself.                     |

The `kukeond` daemon runs inside the **`kuke-system`** realm — specifically
as a container inside the cell
`kuke-system / kukeon / kukeon / kukeond`
(realm / space / stack / cell). The `default` realm is deliberately left
user-owned so `kuke purge --cascade` on it can never take down the daemon.

## Tear down the existing runtime

Only the `kuke-system` cell needs to be removed; user-realm data under
`/opt/kukeon/default` is left intact. All commands below use `--no-daemon`
because the daemon itself is what we're tearing down.

```bash
# 1. Stop the kukeond cell containers (sends SIGKILL via the runner).
sudo kuke kill cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon \
    --no-daemon

# 2. Remove the cell metadata so bootstrap re-creates it fresh.
sudo kuke delete cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon \
    --no-daemon

# 3. Remove the stale socket and pid file left by the old daemon.
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
```

After these three commands: no containerd tasks in
`kuke-system.kukeon.io`, no cell metadata in
`/opt/kukeon/kuke-system/kukeon/kukeon/`, and `/run/kukeon/` is empty.

## Build the binaries

```bash
rm -f kuke kukeond
make kuke          # produces ./kuke
ln -sf kuke kukeond # kukeond is argv[0]-dispatched from the same binary
```

## Build and load the local `kukeond` image (no registry push)

The bootstrap resolves the kukeond container image from `--kukeond-image`.
For local iteration we build with docker, tag it, and import the tarball
directly into containerd's `kuke-system.kukeon.io` namespace — **no push to
ghcr.io or any other registry is required**.

```bash
# Build. VERSION only affects the embedded kuke --version string.
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .

# (Optional) re-tag for clarity — the import below uses whatever tag you pass.
docker tag kukeon-local:dev docker.io/library/kukeon-local:dev

# Load the image into containerd under the system realm's namespace so
# bootstrap can find it without a pull.
docker save kukeon-local:dev | \
    sudo ctr -n kuke-system.kukeon.io images import -

# Verify the image is present.
sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local
```

## Run `kuke init`

```bash
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

Expected tail of the output:

```
    - cell "kukeond": created (image docker.io/library/kukeon-local:dev)
    - cell cgroup: created
    - cell root container cgroup: created
    - cell containers cgroup: created
kukeond is ready (unix:///run/kukeon/kukeond.sock)
```

## Verify daemon parity with `--no-daemon`

Both commands must return identical output. This is the regression guard for
the run-path bind-mount — if the daemon sees a different view of `/opt/kukeon`
than the in-process controller, only the `--no-daemon` list will be populated.

```bash
# Goes through kukeond over the unix socket.
sudo ./kuke get realms

# Bypasses kukeond; reads /opt/kukeon in-process.
sudo ./kuke get realms --no-daemon
```

Expected (identical) output:

```
NAME         NAMESPACE              STATE  CGROUP
-----------  ---------------------  -----  -------------------
default      kukeon.io              Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system
```

## Inspecting the running daemon

Confirm the bind-mount and `--run-path` flag are wired up on the kukeond
container:

```bash
# Expect two bind mounts: /run/kukeon and /opt/kukeon (both host→container same path).
sudo ctr -n kuke-system.kukeon.io container info \
    kukeon_kukeon_kukeond_kukeond | \
    python3 -c "import sys,json; print(json.dumps([m for m in json.load(sys.stdin)['Spec']['mounts'] if m.get('type')=='bind'],indent=2))"

# Expect: /bin/kukeond serve --socket /run/kukeon/kukeond.sock --run-path /opt/kukeon
ps -ef | grep '[k]ukeond serve'
```

## Commit message style

Commit subjects in this repo are **single-line only** — no body, no
trailers. Match the prevailing `type: short imperative summary` shape
(see `git log --oneline`) and keep everything in the subject; do not
add a blank line + paragraph body, and do **not** append a
`Co-Authored-By:` trailer.

## Pushing PR fix-up commits

When the task is "address a review comment on PR #N", the fix is only
visible on GitHub after it's **pushed** to the PR's head branch. After
committing locally, push to the PR's `headRefName` (check with
`gh pr view N --json headRefName`) before reporting the task done. A
local commit on the correct branch is not the same as a commit on the
PR.

## Reviewing PR comments and addressing fixes

When the user asks to review a PR's comments and fix them, follow this
sequence end-to-end:

1. **Find the review feedback.** Check both channels — GitHub surfaces
   inline review comments and top-level discussion separately:
   ```bash
   gh pr view <N>                                  # summary, state
   gh api repos/<owner>/<repo>/pulls/<N>/comments  # inline/review comments
   gh api repos/<owner>/<repo>/issues/<N>/comments # top-level discussion
   ```
   Read every comment — don't act on only the first one.

2. **Check out the PR's head branch, not main.** Resolve it first so
   you push to the right place:
   ```bash
   gh pr view <N> --json headRefName,headRepository
   git fetch origin <headRefName> && git checkout <headRefName>
   ```
   A local commit on `main` or on a stale branch does **not** show up
   on the PR.

3. **Separate blocking items from non-blocking suggestions.** Reviewers
   often mark some items as "nits" or "non-blocking". Address the
   blocking ones; surface the non-blocking ones to the user with a
   short recommendation so they can choose.

4. **Verify each claim before acting.** If the reviewer says a symbol
   is dead code, `grep` for it. If they say a test fails, run it. Do
   not delete/edit code on the reviewer's word alone — their comment
   may be based on stale state.

5. **Make the fix, then validate locally.** Run the same checks the
   PR's test plan calls out (typically `go build ./...`, `go vet ./...`,
   and the affected package tests) before committing. Fix-up commits
   that break CI waste a round-trip.

6. **Commit in the repo's style** (see "Commit message style" above):
   single-line `type: summary`, no body, no trailers.

7. **Push to the PR branch and confirm.** After `git push`, verify the
   new commit appears on the PR:
   ```bash
   gh pr view <N> --json commits --jq '.commits[-1] | "\(.oid[:7]) \(.messageHeadline)"'
   ```
   Only then is the task done.

8. **Do not scope-creep.** Keep the fix-up commit narrowly focused on
   what the reviewer asked for. Unrelated cleanups, doc tweaks, or
   personal-notes files (like edits to this CLAUDE.md) do not belong
   on the PR branch — stash them or commit them separately on another
   branch.
