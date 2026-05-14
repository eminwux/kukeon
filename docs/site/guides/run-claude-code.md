# Run Claude Code in a kukeon cell

This guide walks a freshly-`kuke init`-ed host from zero to a live Claude Code prompt running inside a kukeon cell — first as a long-lived Attachable `Cell`, then as a parametrized `CellProfile` you can drive with `kuke run -p` for one-shot prompts.

[Claude Code](https://docs.claude.com/en/docs/claude-code/overview) is named in the [Vision](../vision.md) as the canonical agent workload kukeon's isolation primitives are measured against. The companion artifacts under [`docs/examples/claude-code/`](https://github.com/eminwux/kukeon/tree/main/docs/examples/claude-code) are the smallest end-to-end shape: an image, a Cell, and a CellProfile.

The example does **not** bake API keys into the image and does not configure auth — bring your own per the upstream [setup docs](https://docs.claude.com/en/docs/claude-code/setup). For the full agent-runner shape (SSH bind, signed commits, agents/project-repo clones), see [`eminwux/crew`](https://github.com/eminwux/crew) — this guide is derived from `crew/images/claude/Dockerfile` and `crew/agents/pm/profiles/pm.yaml` with that plumbing stripped out.

## Prerequisites

- A host that has been bootstrapped with [`kuke init`](init-and-reset.md). `kuke get realms` shows both `default` and `kuke-system` Ready.
- A local Docker (or `nerdctl build`) install for building the image.
- A copy of the three files under `docs/examples/claude-code/`:
  - [`Dockerfile`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/Dockerfile)
  - [`cell.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/cell.yaml)
  - [`profile.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/profile.yaml)

## Step 1 — Build the image

The example `Dockerfile` is the upstream `crew` image verbatim. It installs `nodejs` + `npm` + the `@anthropic-ai/claude-code` package as a non-root `claude` user on `debian:trixie-slim`:

```bash
cd docs/examples/claude-code
docker build -t claude-code:latest .
```

`nerdctl build -t claude-code:latest .` works the same way if you don't run Docker.

### What's in the upstream image that the smoke path doesn't need

The crew Dockerfile carries a handful of fleet-runner extras the Claude Code smoke does not exercise; they are kept verbatim so this Dockerfile remains drop-in usable on a crew-style host:

- **`golang`, `gh`, `pre-commit`, `gnupg`, `ssh`, `jq`, `yq`, `make`, `gettext-base`** — the toolchain crew's `/dispatch` agents need to clone repos, sign commits, and run pre-commit. The smoke in this guide does not invoke any of them; trim them if you want a leaner image.
- **`groupadd -g 988 kukeon` / `usermod -aG kukeon claude`** — fleet-specific. On crew's hosts gid 988 matches the host containerd socket group, so the `claude` user inside the cell can talk to a host-mounted socket. On a freshly-`kuke init`-ed host you do not bind-mount the containerd socket into the cell, so the supplementary group is inert. Leave it in if you ever expect to bind the socket; drop it for a strictly-Claude-Code image.

The smoke path needs only `nodejs`, `npm`, the `@anthropic-ai/claude-code` package, and the non-root `claude` user.

## Step 2 — Load the image into the `default` realm

Every realm maps to its own containerd namespace ([Containerd namespaces](../concepts/containerd-namespaces.md)). User workloads live in `default.kukeon.io`, so the cell's image has to be loaded into that namespace before `kuke apply` / `kuke run` can pull it:

```bash
sudo kuke image load --from-docker claude-code:latest
```

`--realm default` is the default, so it can be omitted. Verify:

```bash
sudo kuke image get | grep claude-code
```

If you don't run Docker, use the `nerdctl save … | sudo kuke image load -` form documented in [`kuke image`](../cli/kuke-image.md).

## Step 3 — Apply and attach the Cell

`cell.yaml` is a single Attachable `Cell` with two containers: a `busybox` root keeping the cell alive (`sleep infinity`) and a `work` container running the Claude Code image, marked `attachable: true` so `kuke attach` can connect a TTY to it.

```bash
sudo kuke apply -f docs/examples/claude-code/cell.yaml
```

`kuke apply` creates the cell. If you hit a daemon issue, [retry under `--no-daemon`](apply-manifests.md#--no-daemon-caveat).

Then connect a terminal to the `work` container:

```bash
sudo kuke attach claude-code
```

You should land at a `claude> ` prompt inside the cell. From there, run `claude` to enter the upstream Claude Code REPL (you'll need to complete the upstream auth flow the first time — the example does not pre-bake an API key).

Press `^]^]` (two consecutive `Ctrl-]` keystrokes) to detach cleanly. The cell keeps running; re-attach later with the same command.

### Tear-down

```bash
sudo kuke delete cell claude-code --cascade --no-daemon
```

`--cascade` removes the cell's containers in the same call. The `--no-daemon` flag is the same caveat as in `kuke apply` — see [Applying manifests](apply-manifests.md#--no-daemon-caveat).

### Optional: persist `~/.claude` across restarts

The smoke path above keeps Claude Code's per-user state inside the cell's overlay. To survive `kuke delete cell` + re-apply, bind-mount a host directory under the `work` container:

```yaml
- id: work
  attachable: true
  image: docker.io/library/claude-code:latest
  command: /bin/bash
  workingDir: /home/claude
  volumes:
    - source: /var/lib/claude-code/state
      target: /home/claude/.claude
  tty:
    prompt: "claude> "
```

This is optional — the smoke flow above does not need it.

## Step 4 — One-shot prompts via a `CellProfile`

For "fire one prompt, tear the cell down on exit" jobs, install the example profile and drive it with `kuke run -p`. A [CellProfile](apply-manifests.md#parameterized-cell-profiles) is a per-user reusable cell template loaded from `$HOME/.kuke/profiles.d/<name>.yaml` (or `$KUKE_PROFILES_DIR`).

```bash
mkdir -p ~/.kuke/profiles.d
cp docs/examples/claude-code/profile.yaml ~/.kuke/profiles.d/claude-code.yaml
```

The profile declares two parameters: `IMAGE` (defaults to `docker.io/library/claude-code:latest`) and `PROMPT` (required). Its `tty.onInit` runs `claude --print "${PROMPT}" && exit`, so the attach loop closes after the prompt's reply prints. `spec.cell.autoDelete: true` then deletes the materialized cell — no `--rm` flag required.

Run it:

```bash
sudo kuke run -p claude-code \
    --param PROMPT="explain the kukeon architecture in one sentence"
```

`kuke run -p` materializes a cell named `claude-code-<6hex>`, attaches to the `work` container, runs the onInit script, prints Claude Code's reply, and exits. Override `IMAGE` to pin a tag:

```bash
sudo kuke run -p claude-code \
    --param IMAGE=docker.io/library/claude-code:2026-05-14 \
    --param PROMPT="…"
```

See [`kuke run`](../cli/kuke-run.md) for the full flag surface, including `--name`, `--param-file`, and the `-d`/`--detach` mode.

## Where to go next

- **The full crew agent-runner shape.** [`eminwux/crew`](https://github.com/eminwux/crew) — SSH bind, GPG-signed commits, agents-repo clone, project-repo clone, the orchestrator that dispatches per-call cells. Two layers up from this guide.
- **Cell teardown verbs.** [`docs/cli-use-cases.md`](https://github.com/eminwux/kukeon/blob/main/docs/cli-use-cases.md) — the full operator workflow reference, including `stop` / `kill` / `delete` / `purge --cascade` for cells.
- **Manifest reference.** [Applying manifests](apply-manifests.md) — multi-doc manifests, the `--no-daemon` caveat, profile parameter resolution order.
