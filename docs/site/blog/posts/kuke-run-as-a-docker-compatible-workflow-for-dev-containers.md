---
date: 2026-05-18
categories:
  - comparison
---

# Dev containers without the flag-soup: from `docker run -it` to `kuke run -f`

If you've spent any time using Docker as your local dev environment, you know the shape of it: a long `docker run -it --name workspace ...` invocation, a moment of "wait, did I want `-d` or not?", and a flag-soup that lives only in your shell history until you forget which terminal you started it from. Turning that one-shot incantation into a persistent reattachable workspace is the friction `kuke run -f` is built to remove — by treating the cell as a declarative YAML spec instead of a command line.

<!-- more -->

## The `docker run -it` flag-soup problem

The first time you run a dev container with Docker, it's easy:

```bash
docker run -it --name workspace ubuntu:24.04 bash
```

You land at a shell. You install your tools, edit some files, and exit. The container stops. The next time you want to come back, you reach for `docker start -ai workspace`, then wonder whether you wanted `docker exec -it workspace bash` instead because you forgot whether you'd stopped it. Want to add a volume mount? You `docker rm` the container and rebuild the invocation from scratch, because there's no spec to edit — the flags _are_ the spec, and they live in your terminal scrollback.

That's the friction. The container is real and persistent on disk, but its _definition_ isn't.

"What about `docker compose`?" is the natural next thought, and for stacks of related services it's the right answer — the spec lives in a file you can commit, edit, and re-apply. The trade is structure: compose projects are flat and directory-scoped, so `docker compose down` run from the wrong directory can take out unrelated containers when names collide. For a single reattachable dev workspace you don't need a whole stack; you need one cell. `kuke run -f` is the verb that runs one, and the `Realm → Space → Stack → Cell` hierarchy you'd reach for when you outgrow compose-shaped flatness stays out of the way for the single-cell case here.

## Setup: load the image, write the spec

A kukeon **cell** is the smallest scheduled unit — a YAML document describing one or more containers that run together. For a dev workspace, the smallest interesting cell is two containers: a root container holding the cell open, and an attachable container running your shell.

First, pull the base image into your local Docker daemon, then load it into the `default` realm (kukeon's per-realm containerd namespace — the realm `kuke init` provisions for user workloads):

```bash
docker pull ubuntu:24.04
sudo kuke image load --from-docker ubuntu:24.04 --realm default
```

`kuke image load --from-docker` shells out to `docker save` under the hood, so the image has to be in your local Docker daemon's content store first — that's what the `docker pull` is for. `--realm default` is the default and can be omitted; we spell it out here to make the namespace explicit.

Now write the cell spec as `workspace.yaml`:

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: workspace
spec:
  id: workspace
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: root
      root: true
      image: docker.io/library/busybox:latest
      command: sleep
      args:
        - "infinity"
    - id: shell
      attachable: true
      image: docker.io/library/ubuntu:24.04
      command: /bin/bash
      tty:
        prompt: "workspace> "
```

Compare that to the equivalent `docker run -it` invocation:

```bash
docker run -it --name workspace ubuntu:24.04 bash
```

The Docker command is shorter, but it's the only place that definition exists. The YAML is longer because it spells out the two-container shape — a root container keeping the cell alive, plus an attachable shell container — but it's a file. You can commit it next to your dotfiles, diff it across machines, and read it without typing `docker inspect`.

## The walkthrough: run, detach, reattach, reconcile

Materialize the cell and attach to its shell in one command:

```bash
sudo kuke run -f workspace.yaml
```

`kuke run -f` creates the cell, starts its containers, and attaches your terminal to the `shell` container by default. You land at a `workspace> ` prompt, run whatever you came to run, and when you want to step away:

```text
^]^]    # press Ctrl-] twice to detach
```

This is the part that surprises operators coming from `docker run -it`. There, exiting the foreground process kills the container — you have to remember `-d` up-front, then add `docker exec -it workspace bash` afterward to come back. With `kuke run -f`, the cell keeps running once you detach; only workload termination or a peer hangup tears it down. The same command does both jobs: first-time creation _and_ attach. There's no "did I want detached mode or not?" decision to make at start time.

Confirm the cell is still alive:

```bash
kuke get cells
```

It shows up in the `Ready` state. To come back, reattach explicitly:

```bash
sudo kuke attach workspace --container shell
```

`--container shell` is the explicit form. (When a cell has exactly one non-root attachable container, `kuke attach workspace` alone suffices — `--container` becomes required only when there's more than one.)

Now suppose you want to add an environment variable, change the working directory, or pin a new image tag. With Docker, that means `docker rm workspace` plus a fresh `docker run -it ...` with the new flags. With kukeon, you edit `workspace.yaml` and reconcile:

```bash
sudo kuke apply -f workspace.yaml
```

`kuke apply -f` updates the cell to match the file. If you instead re-ran `kuke run -f workspace.yaml` against a cell whose on-disk spec diverged from the file, the CLI would refuse cleanly with a message pointing you at `kuke apply -f` — `run` is the "first-time materialize" verb, `apply` is the "reconcile to spec" verb, and the CLI keeps them straight so you can't accidentally clobber state.

When you're done with the workspace:

```bash
sudo kuke delete -f workspace.yaml
```

The same file that `run` and `apply` consumed also drives teardown — `kuke delete -f` removes every resource the manifest declares, so the spec stays the single source of truth across the cell's whole lifecycle. (`kuke delete cell workspace` is the by-name equivalent if you don't have the file handy.)

`kuke purge cell` exists too, but it's the recovery verb for _broken_ state — for example, if an image-pull failure leaves the cell half-created and a subsequent `kuke kill <name>` errors out with `no RootContainerID set`. For routine teardown of a healthy workspace, `kuke delete` is the right verb; `purge` is the heavier hammer you reach for when something in kukeon's state is wedged.

## What you get: spec as source of truth

The same workflow you've been building one flag at a time in your shell history is, in `kuke run -f`'s world, a file. Four things follow from that:

- **Versionable, diffable.** `workspace.yaml` sits in your dotfiles repo. You can `git diff` it, share it, restore it on a new machine.
- **Structured persistence.** The cell outlives every attach session. Detaching is cheap; reattaching is one command. No more "wait, did I leave that running with `-d`?".
- **Symmetric attach.** `kuke run -f` is the first-time materialize-and-attach verb; `kuke attach` is the reattach verb. One mental model for "start" and "come back," not two.
- **A real update path.** `kuke apply -f` reconciles the cell to match the spec. `kuke run -f` against a divergent cell refuses cleanly rather than silently overwriting — the two verbs encode the difference between "create" and "update" so you can't conflate them.

The trade is honest: you write more YAML up-front than you write `docker run` flags. In exchange, the workspace stops being a thing that lives in your terminal scrollback and becomes a thing that lives in your repo.

## Where to go next

- For the full agent-runner shape — building a custom image, the Attachable cell pattern, and the parametrized `CellBlueprint` for one-shot prompts — see [Run Claude Code in a kukeon cell](../../guides/run-claude-code.md). The cell-spec pattern this post uses is the same one that guide walks through end-to-end.
- For the full surface of `kuke run -f` (including `-b` blueprint mode, `<config>` positional for CellConfig, `--rm` auto-delete, and the `-d/--detach` flag), see the [`kuke run` reference](../../cli/kuke-run.md).
- For everything `kuke apply`, `kuke attach`, `kuke delete`, and `kuke purge` will and won't do — exit codes, side effects, error paths — `docs/cli-use-cases.md` in the repo is the workflow-oriented source of truth.
