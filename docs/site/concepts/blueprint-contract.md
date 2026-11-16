# Blueprint contract: components and modes

**A blueprint is a form; a config is the answers.**

A [`CellBlueprint`](../manifests/blueprint.md) describes a kind of cell and everything that may differ between two cells of that kind. A [`CellConfig`](../manifests/config.md) answers it for one use. Because the blueprint is self-describing — typed parameters, named slots, components, modes, required image capabilities — anything can build a Config for it without knowing the template body: `kuke team` does, and so does the web UI's _New config_ form.

| The blueprint fixes                                                   | The config answers                                         |
| --------------------------------------------------------------------- | ---------------------------------------------------------- |
| The cell template: containers, commands, mounts, privileges           | `values` for the typed parameters                          |
| Which parameters exist, their type, default and form group            | `repos` — a URL and revision per repo slot                 |
| Which repo and secret slots exist and which are required              | `secrets` — a stored Secret per secret slot                |
| The components it is assembled from and the modes a harness starts in | `components` to enable, `modes` to allow, extra `requires` |
| The image capabilities it needs                                       | `git` — an override of the inherited git identity          |

Security posture stays in the blueprint. Running as root, privileged mode and host mounts are never a Config toggle: each posture is its own blueprint (`claude`, `claude-root`, …), so choosing one is a visible choice of blueprint.

Git identity and credentials are never in the blueprint either. A default identity is [inherited](../guides/git-credentials.md) from the Realm, Space or Stack (or set on the Config), and any repo may state its own.

## Typed parameters

Every `${KEY}` the template references can be declared with a `type` — `string`, `enum`, `path`, `image`, `bool` — an `enum` list, a `default`, a `description` and a `group`. The daemon rejects a value that does not fit its type when it renders a cell; a form uses the same declarations to draw the right input and to group fields.

## Components

A component bundles one concern:

- **`harness`** — the agent harness: its env, parameters, secret slots and the [modes](#modes) it can be started in.
- **`connector`** — access to an external service through that service's API credentials. The GitHub connector delivers a token as `GH_TOKEN` for the `gh` CLI.
- **`forge`** — the issue/change tracker the cell works against.

A component that is not `optional` is always part of the cell. An `optional` one is included only when the Config lists it in `spec.components`. When the daemon renders a cell it **flattens** the enabled components into the template: their parameters join the blueprint's, their env and secret slots land on one container, and their `requires` join the cell's. Two enabled components (or a component and the template) that claim the same env key or secret destination fail the render rather than silently overriding each other.

A component never contributes a git credential. That is deliberate: the GitHub connector's token is for the GitHub _API_; clone, fetch, push and signing use only the cell's git identity. An API token in the environment can therefore never take over a push.

## Modes

A harness usually starts in more than one way. Its component declares them as `modes`:

- **`interactive`** — an attachable session. No prompt; a login typically persists in a state volume.
- **`headless`** — a dispatched, unattended run. It takes what an unattended run needs: a `PROMPT` parameter, possibly an API-key secret, its own entrypoint.

Each mode carries its own `onInit`, `parameters`, `env` and `secrets`, and everything it declares applies only to cells created in that mode. A Config says which modes are allowed and which is the default; **the mode itself is picked per cell, at create time**:

```bash
kuke run dev-claude                                           # the config's default mode
kuke run dev-claude --mode headless --param PROMPT="fix the build" -d
```

So one Config serves both an attached cell and a dispatched run. `--param` on a Config-sourced cell is accepted only for the picked mode's parameters — a Config's own values are not overridable per cell. A cell that supplies a parameter only one mode declares is created in that mode without `--mode` (`--param PROMPT=…`, or `--env PROMPT=…` as dispatches were written before modes existed).

The mode is recorded on the cell (`kukeon.io/mode` label and the cell's provenance), so a restart or an `OutOfSync` re-render keeps it.

## Image capabilities

A cell needs an image that actually contains what it is going to run. The requirement is

```
blueprint requires  ∪  enabled components' requires  ∪  config requires
```

and images declare what they provide in the OCI image label `io.kukeon.capabilities` — a comma-separated list such as `claude,git,gh,go,node,make`. The harness images kukeon publishes are stamped with it at build, from `harness.yaml` `images[].capabilities`.

When a cell is created the daemon reads the label of each container image and refuses an image that declares capabilities but lacks a required one, naming the missing capability. An image with **no** label at all is allowed with a warning, so images built before the label existed — and images you build yourself — keep working. The web UI filters its image picker on the same label.

## Shared blueprints

A blueprint without role facts can be shared. `kuke team` renders **one blueprint per harness × security profile**, realm-wide (`claude`, `claude-root`, `codex`, …), labelled `kukeon.io/shared: "true"`, and every role's Config in the realm references it. See [Team](team.md).
