# Getting Started

This guide takes a Linux host from nothing to a running agent: install Kukeon, open the web UI, and follow its **Get started** checklist. Every step has a CLI equivalent, listed [below](#5-cli-equivalents). The last section runs a plain cell from a manifest, without the web UI or a team.

## 1. Check prerequisites

Kukeon needs:

- Linux with cgroups v2
- [containerd](https://containerd.io/) running at `/run/containerd/containerd.sock`
- `git`, for the step that stores the `kuke` team (the installer goes on without it and tells you how to finish later)

See [Installation → Prerequisites](install/prerequisites.md) for the full check (including `kuke doctor cgroups` to verify controller delegation) and quick install commands for each dependency.

## 2. Install

```bash
curl -fsSL https://kukeon.io/install.sh | bash
```

The installer checks the prerequisites, downloads and verifies the binaries, and installs them to `/usr/local/bin`. It then:

1. runs `sudo kuke init`, which provisions the two default realms and starts the `kukeond` daemon;
2. runs `sudo kuke team init`, which stores the `kuke` team (majordomo) and the `default` team (the agent templates);
3. installs `kukeond.service`, so the daemon comes back after a reboot;
4. installs `kuke-serve.service`, which serves the web UI on `http://localhost:5129`.

It ends with the web UI's address and login:

```text
✓ kukeon v0.10.0-77-gabc1234 (edge) installed and initialized

Open the web UI:
  http://localhost:5129
  user:     root
  password: <a 32-character token>
  read it again: kuke get secret web-ui-admin --realm kuke-system -o yaml
Then follow Get started on its first page: initialize, log in to Claude or
Codex, set up git and launch your first agent.
```

Pass `--check` to run the prerequisite checks only. [Install on Linux](install/install-linux.md) lists every step, the environment variables the installer honors, its exit codes, and the manual install path.

!!! note "When a step is skipped"

    - **No `git`, or `kuke team init` failed**: the summary has a `✗ kuke team init failed: …` line and the installer exits 3. Everything else is installed; finish with **Initialize** in the web UI (step 4) or `sudo kuke team init`.
    - **No systemd, or `KUKE_SKIP_WEB_UI=1`**: no `kuke-serve.service`. Run `kuke serve` in a terminal and leave it running.

## 3. Open the web UI

Open `http://localhost:5129` on the host and sign in as `root` with the password the installer printed. The web UI listens on localhost only; from another machine, forward the port first:

```bash
ssh -L 5129:localhost:5129 <host>
```

See [Local web server](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md) for the listen address and the other `kuke serve` options.

## 4. Follow Get started

The first page, the **Workbench**, shows a **Get started** card until its required steps are done. Each step has a button that does it or takes you to where it is done, and turns to **Done** by itself.

| Step                          | What it does                                                                                                                                                                                                                                                  | Done when                                                            |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| **Initialize**                | Runs `kuke team init` from the browser: the server fetches the kukeon source and stores the `kuke` and `default` teams. The installer has normally done this already.                                                                                         | The `kuke` and `default` teams are stored.                           |
| **Log in to Claude or Codex** | **Log in** opens a short-lived cell that runs the harness's own login in a terminal; follow its prompts, and the cell is removed when the login ends. **Use a token** opens the connector's page, where you store a subscription token or an API key instead. | A Claude or Codex login identity is logged in, or a token is stored. |
| **Set up Git and GitHub**     | Recommended. Opens the Git connector's **New identity** page: import the SSH key and git author of the account running the web UI, use a GitHub token for both `gh` and git, or enter an identity by hand.                                                    | A git identity or a GitHub token is stored.                          |
| **Enable majordomo**          | Optional. Opens the majordomo panel, where **Enable majordomo** creates and starts kukeon's own assistant. The panel says whether its harness is logged in; majordomo keeps a login of its own.                                                               | A majordomo cell exists.                                             |
| **Launch your first agent**   | Opens **New agent**: pick Claude or Codex, name the agent, **Create**. Its terminal opens when the cell has started.                                                                                                                                          | A cell of an agent exists.                                           |

The recommended and optional steps never keep the card open. **Dismiss** hides it; **Get started** in the user menu (top right) shows it again.

An agent has no project checkout: clone one in its terminal. See [Your first agent](guides/first-agent.md) for what an agent is and how to work with it, and [Git credentials](guides/git-credentials.md) for git identities.

## 5. CLI equivalents

Everything the checklist does is a `kuke` command. Add yourself to the `kukeon` group first (step 6), or prefix each command with `sudo`.

| Step                 | Command                                                                                                                                                                  |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Initialize           | `sudo kuke team init`                                                                                                                                                    |
| Log in               | `kuke connector login claude default-agent-claude-state --space default --stack default` (or `codex` and `default-agent-codex-state`): the agent template's state volume |
| Git from this host   | `kuke connector import git --default`                                                                                                                                    |
| GitHub token and git | `kuke connector import github --token-stdin --git --default < token.txt`                                                                                                 |
| Majordomo            | `kuke run --from-config kuke-majordomo-claude` (or `kuke-majordomo-codex`)                                                                                               |
| First agent          | `kuke create agent --harness claude --name "My agent" -a` (or `--harness codex`)                                                                                         |
| Web UI by hand       | `kuke serve`                                                                                                                                                             |

`kuke connector list` shows every connector, its identities and its login identities with their state.

## 6. Daily use without sudo

`kuke init` provisions a system `kukeon` group and sets the daemon socket at `/run/kukeon/kukeond.sock` to mode `0660 root:kukeon`. When the installer runs under `sudo`, it adds the invoking user to the group; otherwise add yourself:

```bash
sudo usermod -aG kukeon $USER
# Log out and back in (or run `newgrp kukeon`) so the group takes effect.
```

The rest of this guide assumes you have done that. If you skip this step, prepend `sudo` to every `kuke` command below. Operations that bypass the daemon still need root: `kuke init`, `kuke daemon reset`, `kuke image load` (in-process by design — image commands run in-process regardless of flags), `kuke doctor cgroups --probe`, and any command that runs in-process (`kuke get <kind> --no-daemon`, `kuke purge ... --no-daemon`, or any command with `KUKEON_NO_DAEMON=true` / an explicit `--run-path`).

## Advanced: a cell from a manifest

Agents are cells that a team's Configs describe. A cell can also be written by hand; this section runs one without the web UI or a team.

<a id="3-bootstrap-the-runtime"></a>

### What `kuke init` created

`kuke init` provisions the two default realms (`default` for user workloads, `kuke-system` for the `kukeond` daemon cell), sets up CNI dirs, pulls the `kukeond` image into the `kuke-system` realm's containerd namespace, and starts the daemon. If you installed [by hand](install/install-linux.md#manual-install-fallback), run it yourself with `sudo kuke init`. A fresh bootstrap ends with:

```text
  System hierarchy:
    - realm "kuke-system": created
    - cell "kukeond": created (image ghcr.io/eminwux/kukeon:<version>)
    - cell cgroup: created
    - cell root container: created
    - cell containers: started
kukeond is ready (unix:///run/kukeon/kukeond.sock)
```

`init` is idempotent: items that already exist are reported as `already existed`, and on a healthy host its header reads `Kukeon runtime already initialized`. [`kuke status`](cli/kuke-status.md) confirms the daemon is healthy and both realms are Ready.

```bash
$ kuke get realms
NAME         STATE  AGE
default      Ready  <age>
kuke-system  Ready  <age>
```

`default` is your user-workload realm — `kuke create`, `kuke apply`, etc. land here when no `--realm` flag is given. `kuke-system` is reserved for Kukeon itself; the `kukeond` daemon runs as a cell inside `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell).

```bash
$ kuke get spaces --realm kuke-system
NAME    REALM        STATE  AGE
kukeon  kuke-system  Ready  <age>

$ kuke get stacks --realm kuke-system --space kukeon
NAME    REALM        SPACE   STATE  AGE
kukeon  kuke-system  kukeon  Ready  <age>
```

Add `-o wide` for per-kind extra columns (e.g. realm gains `NAMESPACE`), or `-o yaml` / `-o json` for full resource details (including `cgroupPath`).

### Run a hello-world cell

A minimal example that brings up a single container serving a static HTML page with busybox httpd ships in the repo at [`docs/examples/hello-world.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/hello-world.yaml):

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: hello-world
spec:
  id: hello-world
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: web
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - |
          mkdir -p /www && \
          cat > /www/index.html <<'HTML'
          <!doctype html>
          <html>
            <head><meta charset="utf-8"><title>kukeon hello-world</title></head>
            <body style="font-family: sans-serif">
              <h1>Hello, world from kukeon!</h1>
            </body>
          </html>
          HTML
          exec busybox httpd -f -v -p 8080 -h /www
```

Apply it:

```bash
sudo kuke apply -f docs/examples/hello-world.yaml
```

Confirm the cell is ready, find its IP on the default-default bridge, and curl it:

```bash
kuke get cells --realm default --space default --stack default

ROOT_PID=$(sudo ctr -n default.kukeon.io task ls | awk '/hello-world_root/ {print $2}')
CELL_IP=$(sudo nsenter -t "${ROOT_PID}" -n ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
curl http://${CELL_IP}:8080/
```

Tear it down with:

```bash
sudo kuke delete cell hello-world \
    --realm default --space default --stack default --cascade
```

The cell also shows in the web UI's Workbench while it runs.

## Shell autocomplete

```bash
cat >> ~/.bashrc <<EOF
source <(kuke autocomplete bash)
EOF
```

`kuke autocomplete zsh` and `kuke autocomplete fish` are also supported.

## Where to go next

- Work with agents: [Your first agent](guides/first-agent.md)
- Understand what you just created: [Concepts → Overview](concepts/overview.md)
- Teams, roles and harnesses: [Concepts → Team](concepts/team.md)
- Dig into the resource model: [Manifest Reference](manifests/overview.md)
- Learn the CLI: [CLI Reference → Commands](cli/commands.md)
- Iterate on Kukeon from source: [Guides → Local development](guides/local-dev.md)
