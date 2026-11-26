# Your first agent

This guide takes a host that has been through [`kuke init`](init-and-reset.md)
to a running Claude or Codex agent: a cell with the harness's terminal open,
which knows it runs in kukeon and needs no project. It covers the CLI and the
web UI, which do the same thing.

## What you get

`kuke team init` applies two system teams. `kuke` is majordomo, the agent that
operates kukeon. `default` is the one this guide uses: its `agent` role, with
the Claude and Codex harnesses, stores two **agent templates** in
`default/default/default`:

| Template               | Harness | Blueprint |
| ---------------------- | ------- | --------- |
| `default-agent-claude` | Claude  | `claude`  |
| `default-agent-codex`  | Codex   | `codex`   |

A template is a CellConfig labelled `kukeon.io/template: "true"`. You do not
run it; **New agent** copies it. See
[Teams → The default team](../concepts/team.md#the-default-team) for what the
role knows and how the team is applied.

## Prerequisites

- A host bootstrapped with [`kuke init`](init-and-reset.md), with `kukeond`
  running.
- The system teams applied: `sudo kuke team init`. Check the templates are
  stored:

  ```bash
  sudo kuke get configs --realm default --space default --stack default
  ```

  `default-agent-claude` and `default-agent-codex` are listed. On a host
  installed before the `default` team existed, `sudo kuke team init --upgrade`
  creates it.

## From the CLI

```bash
sudo kuke create agent --harness claude --name "Reviewer"
```

```
Config "default-agent-claude-3f9a1c" (Reviewer) stored in default/default/default, from template default-agent-claude
Cell "default-agent-claude-3f9a1c-8b2e4d" created and started in default/default/default
Attach with: kuke attach default-agent-claude-3f9a1c-8b2e4d --realm default --space default --stack default
```

The command does three things:

1. It copies the template into a Config of its own, named
   `<template>-<6 hex>`. The copy has the template's spec unchanged, the
   annotations `kukeon.io/agent-template` and `kukeon.io/agent-team` naming
   the template and its team, and, with
   `--name`, the display name in `kukeon.io/display-name`. It has no
   `kukeon.io/team` label, so applying a team never removes it.
2. It creates the Config's first cell, with a generated name.
3. It starts the cell.

Add `-a` to attach as soon as the cell is started, as `kuke run -a` does;
detach with `^]^]`:

```bash
sudo kuke create agent --harness codex -a
```

`--realm`, `--space` and `--stack` store the Config somewhere other than the
template's scope. See [`kuke create agent`](../cli/kuke-create.md#kuke-create-agent)
for every flag.

### Find your agents

```bash
sudo kuke get configs -o wide
sudo kuke get cells -o wide
```

The wide tables have a `DISPLAY NAME` column: the agent's name on its Config,
and the name an instance was given on its cell (`-` when it has none).

### More instances of the same agent

An agent is its Config; its cells are its instances. Start another one with:

```bash
sudo kuke run --from-config default-agent-claude-3f9a1c
```

### When creating or starting the cell fails

The Config is kept and the error names it, with the cell when one was created:

```
Config "default-agent-claude-3f9a1c" stored in default/default/default, from template default-agent-claude
Error: config default-agent-claude-3f9a1c stored, but creating its first cell failed: …
```

Finish with `kuke run --from-config <config>`, or `kuke start <cell>` when the
cell exists, once the cause is fixed. Delete the Config with
`kuke delete config <config>` if you no longer want the agent.

## From the web UI

1. Open the **Workbench** and choose **New agent** (in the page heading, and
   on the empty Workbench).
2. Pick **Claude** or **Codex**. Each harness shows whether its template is
   stored; when neither is, the page tells you to run `kuke team init`.
3. Optionally give the agent a name (up to 63 characters), then **Create**.

The web UI copies the template and creates and starts the first cell, as the
CLI does, then opens the cell's terminal. If the cell could not be created or
started, the page says **Agent stored, but not started**, links the stored
Config and says which step failed.

### Names in the web UI

Agents are listed under their display name, or under their Config's name when
they have none. Instances of an agent with a name are listed as
`<name> #1`, `<name> #2`, … unless an instance has a name of its own.

**Rename** changes either:

- the agent's name, on the cell's page next to its Config, or on the Config's
  page in the catalog;
- one instance's name, on the cell's page next to its name.

An empty name clears it. Renaming writes only the `kukeon.io/display-name`
annotation: it never restarts or recreates a cell and never marks one
OutOfSync.

Templates carry a **Template** badge in the Configs catalog and are not listed
as agents.

## How agents share state

Every copy keeps the template's spec, so every agent of a harness mounts the
same state volume (`default-agent-claude-state` or
`default-agent-codex-state`) at the harness's state directory. Log in once,
in any Claude agent, and every Claude agent is logged in. The home directory
outside that volume is the cell's own and does not survive the cell being
recreated. The agent has no project checkout and no access to the host; clone
what you need in its terminal.

## Related

- [Teams → The default team](../concepts/team.md#the-default-team)
- [`kuke create agent`](../cli/kuke-create.md#kuke-create-agent)
- [`kuke team init`](../cli/kuke-team-init.md)
