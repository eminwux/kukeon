# kuke team init

```
kuke team init [--force] [--host ENDPOINT] [--configuration PATH]
```

Initialize the operator's team configuration and apply the kukeon operator
team to kukeond. `init` creates no cells and starts nothing; launch a composed
cell explicitly with kuke afterwards (for example
`kuke run --from-config kukeon-operator-codex`).

To register a project team after `init`, use [`kuke team add`](kuke-team-add.md).
For the shared composition model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                  |
| ----------------- | ------- | -------------------------------------------------------------------------------------------- |
| `--force`         | `false` | Replace an existing `~/.kuke/kuketeams.yaml` and reapply the initial operator declaration.   |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

kuke's global flags (`--verbose`, `--log-level`, `--run-path`, `--no-daemon`)
are accepted but have no effect on `kuke team` subcommands: they resolve the
operator home, the kukeond endpoint and the `ClientConfiguration` themselves.

## What it does

`kuke team init` creates `~/.kuke/kuketeams.yaml` only when it does not exist.
If the file already exists, it exits with guidance to use `--force` and does not
contact the daemon. `kuke team init --force` atomically replaces the global
configuration with the initial document and applies the initial operator team.
It replaces the accepted `kukeon` registration after successful application.

The command fetches only the sources used by the selected role/harness
definitions into temporary Git checkouts, and composes the kukeon team's
blueprints and configs. Git must be installed and able to read those sources
without a terminal prompt. `branch`, `tag`, and `commit` selectors are fetched
explicitly; no checkout in the current directory is used. Fetched definitions
are removed after the invocation. Runtime repository URLs and clone
destinations still come from the authored configuration.

Fresh global configuration is minimal (discovered Git settings are added under
`spec.git` when available):

```yaml
apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults: {}
```

The initial accepted declaration contains the operator selections directly. The
operator role and both harnesses come from the kukeon repository (`team/roles/` and
`team/harnesses/`):

```yaml
apiVersion: kuketeams.io/v1
kind: TeamEntry
metadata:
  name: kukeon
spec:
  sources:
    - name: kukeon
      url: github.com/eminwux/kukeon
      branch: main
      clonePath: /home/user/kukeon-src
  roles:
    - name: operator
      source: kukeon
      path: team/roles/operator
      initialPath: $HOME
      needs:
        image: [go]
  harnesses:
    - name: codex
      source: kukeon
      path: team/harnesses/codex
    - name: claude
      source: kukeon
      path: team/harnesses/claude
```

This applies matching blueprints and CellConfigs named `kukeon-operator-codex`
and `kukeon-operator-claude`. Init does not start them. The explicit entry keeps
recomposition independent of global operator defaults. Configure reusable
[global defaults](../concepts/team.md#reusable-defaults-after-init) separately.

## Daemon endpoint and client configuration

The command reaches kukeond with the public `kukeonv1` Unix JSON-RPC client.
Endpoint precedence:

1. `kuke team init --host unix:///absolute/path/to/kukeond.sock`
2. `KUKEON_HOST`
3. `ClientConfiguration.spec.host`
4. `unix:///run/kukeon/kukeond.sock`

The configuration path is `--configuration`, then `KUKE_CONFIGURATION`, then
`~/.kuke/kuke.yaml`. An absent default file is allowed; an explicitly selected
missing file, an unreadable file, invalid YAML, or a wrong kind/version is an
error. Only the Unix transport is accepted. Init does not start the daemon.

Blueprints and configs target the `default` realm, space, and stack. Preflight
checks that the parent metadata exists, checks secret prerequisites, and stages
a complete `0600` TeamEntry before mutating the daemon. Provision missing parent
scopes with kuke (`kuke init` creates `default/default/default`). Init never
creates, starts, restarts, or deletes cells. `ApplyDocumentsForTeam` receives
the stable team name `kukeon`, so reruns update that team's resources and prune
obsolete team-owned blueprints and configs. It does not prune credentials or
running cells.

## Authentication and secrets

Only selected role/harness requirements contribute secret lookups. The base
operator with Codex or Claude does not require `gh` or `gh-token`. Selecting
OpenCode requires its configured provider binding, as described in
[secret declarations](../concepts/team.md#secret-declarations).

Each runtime secret is identified by all explicit reference coordinates. A
successful metadata lookup with `MetadataExists: true` reuses the existing
credential, even when the local creation source is absent, unreadable, or holds
a different value. A failed lookup is an error; it never authorizes secret
creation.

For a missing secret, init checks the required parent scope and resolves only
an explicit `source.env` variable or `source.file` path. Missing or empty values
fail preflight. File sources follow kuke's trailing-newline removal. All
required sources are checked before the first create, and shared runtime
identities are created once. Creation happens before dependent blueprints and
configs are applied. Credentials and remote secret-error text are never printed
or saved in the TeamEntry or the blueprint/config request.

A missing secret without a creation source does not fail init or add. The
blueprints/configs are still applied and the team registered (kukeond resolves
secret references only when a cell launches), and the command exits 0 after
listing each missing runtime secret once:

```text
Missing secrets (cells will not start until they exist):
  provider-account  →  kuke create secret provider-account --realm auth --space shared --stack operators --from-file /path/to/token
```

Create them before launching cells; no rerun is needed. `kuke run` still fails
on a cell whose secret is absent. The actual command uses the binding's
resolved name and scope. A cell-scoped
reference requires a `Secret` manifest with its exact metadata coordinates,
applied with `kuke apply -f /path/to/secret.yaml`.

Init rechecks existence immediately before each create and never retries a
secret write, including when a transport failure leaves its outcome ambiguous.
On a rerun, a secret accepted by the previous attempt is discovered and reused.
Secret rotation is not an init action; rotate explicitly with kuke.

## Registration and recovery

The accepted authored declaration is saved atomically at
`~/.kuke/kuketeam.d/kukeon.yaml`. It retains explicit selections and
omitted/empty list semantics, with no resolved credentials or duplicated
effective defaults. `init --force` uses the initial operator declaration
regardless of an existing `kukeon.yaml`. Reserve the name `kukeon` for this
operator. Init does not discover or migrate other registrations.

Registration follows full blueprint/config success only. A secret failure, RPC
failure, total resource failure, partial apply, prune failure, or incomplete
response returns nonzero and leaves any prior TeamEntry unchanged. A first-time
failure creates no registration. Successful resources and created secrets are
not rolled back: rerun `kuke team init --force` after fixing the prerequisite to
converge the same `kukeon` identity. A registration write failure after
application is reported as such and is also recovered by rerunning
`init --force`.

## Output

```
Created configuration: /home/me/.kuke/kuketeams.yaml
Applied 4 resources; registered kukeon team: /home/me/.kuke/kuketeam.d/kukeon.yaml
No cells created or started.
```

With `--force`, the first line reads `Reset configuration: …`. Diagnostics (for
example, Git settings that could not be discovered) are written to stderr.

## Validation

`make test` runs the injectable-client success/failure/rerun tests and the real
Unix JSON-RPC transport tests. A separate check verifies actual daemon storage,
reads back blueprints and configs, repeats application, and confirms that cells
are unchanged:

```sh
KUKETEAMS_INTEGRATION_HOST=unix:///path/to/disposable/kukeond.sock make test-team-integration
```

Use a disposable kukeond with existing `default/default/default` scope metadata.
The check uses a unique `init-check-*` team and deletes only its generated
blueprints and configs on cleanup.
