# Run Claude Code in a kukeon cell

This guide walks a freshly-`kuke init`-ed host from zero to a live Claude Code prompt running inside a kukeon cell — first as a long-lived Attachable `Cell`, then as a parametrized `CellBlueprint` you can drive with `kuke run --from-blueprint` for one-shot prompts.

[Claude Code](https://docs.claude.com/en/docs/claude-code/overview) is named in the [Vision](../vision.md) as the canonical agent workload kukeon's isolation primitives are measured against. The companion artifacts under [`docs/examples/claude-code/`](https://github.com/eminwux/kukeon/tree/main/docs/examples/claude-code) are the smallest end-to-end shape: an image, a Cell, and a CellBlueprint.

The example does **not** bake API keys into the image and does not configure auth — bring your own per the upstream [setup docs](https://docs.claude.com/en/docs/claude-code/setup). It is the smallest end-to-end shape — an image, a Cell, and a CellBlueprint — with the broader fleet-runner plumbing (SSH bind, signed commits, agents/project-repo clones) deliberately stripped out.

## Prerequisites

- A host that has been bootstrapped with [`kuke init`](init-and-reset.md). `kuke get realms` shows both `default` and `kuke-system` Ready.
- The `kukebuild` binary on `PATH` — `kuke build` exec's it. It is not part of the release artifacts yet; build it from a checkout with `make kukebuild` (see [Build from source](../install/build-from-source.md#build-kukebuild-for-kuke-build)).
- A copy of the three files under `docs/examples/claude-code/`:
  - [`Dockerfile`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/Dockerfile)
  - [`cell.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/cell.yaml)
  - [`blueprint.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/blueprint.yaml) (the daemon-stored replacement for the pre-#626 `profile.yaml`)

## Step 1 — Build the image into the `default` realm

The example `Dockerfile` installs `nodejs` + `npm` + the `@anthropic-ai/claude-code` package as a non-root `claude` user on `debian:trixie-slim`. [`kuke build`](../cli/kuke-build.md) builds it with kukeon's native builder and writes the result straight into the `default` realm's containerd namespace — no docker daemon and no separate load step:

```bash
cd docs/examples/claude-code
sudo kuke build -t claude-code:latest .
```

`--realm default` is the default, so it can be omitted. The tag normalizes to `docker.io/library/claude-code:latest` — the exact reference `cell.yaml` and `blueprint.yaml` use. Verify:

```bash
sudo kuke get images | grep claude-code
```

If you already have a pre-built image tarball instead, load it with `kuke image load` — see [`kuke image`](../cli/kuke-image.md) for the `kuke image load --from-docker <name:tag>` and `… save | sudo kuke image load -` forms.

### What's in the image that the smoke path doesn't need

The `Dockerfile` carries a handful of fleet-runner extras the Claude Code smoke does not exercise; they are kept so this Dockerfile remains drop-in usable on a full agent-runner host:

- **`golang`, `gh`, `pre-commit`, `gnupg`, `ssh`, `jq`, `yq`, `make`, `gettext-base`** — the toolchain a full agent-runner needs to clone repos, sign commits, and run pre-commit. The smoke in this guide does not invoke any of them; trim them if you want a leaner image.
- **`groupadd -g 988 kukeon` / `usermod -aG kukeon claude`** — fleet-specific. On a fleet host where gid 988 matches the host containerd socket group, the `claude` user inside the cell can talk to a host-mounted socket. On a freshly-`kuke init`-ed host you do not bind-mount the containerd socket into the cell, so the supplementary group is inert. Leave it in if you ever expect to bind the socket; drop it for a strictly-Claude-Code image.

The smoke path needs only `nodejs`, `npm`, the `@anthropic-ai/claude-code` package, and the non-root `claude` user.

## Step 2 — Apply and attach the Cell

`cell.yaml` is a single Attachable `Cell` with two containers: a `busybox` root keeping the cell alive (`sleep infinity`) and a `work` container running the Claude Code image, marked `attachable: true` so `kuke attach` can connect a TTY to it.

```bash
sudo kuke apply -f docs/examples/claude-code/cell.yaml
```

`kuke apply` creates the cell. `kuke apply` has no in-process fallback (it always routes through `kukeond`); if you hit a daemon issue, bring the daemon back with `kuke daemon start` — see [`kuke apply` always requires the daemon](apply-manifests.md#kuke-apply-always-requires-the-daemon).

Then connect a terminal to the `work` container:

```bash
sudo kuke attach claude-code
```

You should land at a `claude> ` prompt inside the cell. From there, run `claude` to enter the upstream Claude Code REPL (you'll need to complete the upstream auth flow the first time — the example does not pre-bake an API key).

Press `^]^]` (two consecutive `Ctrl-]` keystrokes) to detach cleanly. The cell keeps running; re-attach later with the same command.

### Tear-down

```bash
sudo kuke delete cell claude-code --cascade
```

`--cascade` removes the cell's containers in the same call. `kuke delete` is a workload verb with no in-process fallback (same as `kuke apply`); if it fails because the daemon is down, bring the daemon back with `kuke daemon start` — see [`kuke apply` always requires the daemon](apply-manifests.md#kuke-apply-always-requires-the-daemon).

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

## Step 3 — One-shot prompts via a `CellBlueprint`

For "fire one prompt, tear the cell down on exit" jobs, apply the example blueprint to the daemon and drive it with `kuke run --from-blueprint`. A [CellBlueprint](../manifests/blueprint.md) is a daemon-stored, scoped cell template.

```bash
sudo kuke apply -f docs/examples/claude-code/blueprint.yaml
```

The blueprint declares two parameters: `IMAGE` (defaults to `docker.io/library/claude-code:latest`) and `PROMPT` (required). Its `tty.onInit` runs `claude --print "${PROMPT}" && exit`, so the attach loop closes after the prompt's reply prints. `spec.cell.autoDelete: true` then deletes the materialized cell — no `--rm` flag required.

Run it:

```bash
sudo kuke run --from-blueprint claude-code \
    --param PROMPT="explain the kukeon architecture in one sentence"
```

`kuke run --from-blueprint` materializes a cell named `claude-code-<6hex>`, attaches to the `work` container, runs the onInit script, prints Claude Code's reply, and exits. Override `IMAGE` to pin a tag:

```bash
sudo kuke run --from-blueprint claude-code \
    --param IMAGE=docker.io/library/claude-code:2026-05-14 \
    --param PROMPT="…"
```

See [`kuke run`](../cli/kuke-run.md) for the full flag surface, including `--name`, `--param-file`, and the `-d`/`--detach` mode. To additionally fill structural repo/secret slots, wrap the blueprint in a `kind: CellConfig` and use `kuke run --from-config <cfg>`.

## Where to go next

- **Cell teardown verbs.** [`docs/cli-use-cases.md`](https://github.com/eminwux/kukeon/blob/main/docs/cli-use-cases.md) — the full operator workflow reference, including `stop` / `kill` / `delete` / `purge --cascade` for cells.
- **Manifest reference.** [Applying manifests](apply-manifests.md) — multi-doc manifests, why `kuke apply` always requires the daemon, blueprint parameter resolution order.
