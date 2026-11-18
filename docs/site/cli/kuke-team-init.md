# kuke team init

```
kuke team init [--force | --reapply] [--import-git-identity [--git-key PATH]
               [--git-key-passphrase-file FILE] [--git-known-hosts FILE]]
               [--host ENDPOINT] [--configuration PATH]
```

Initialize the operator's team configuration and apply the `kuke` team,
whose one role is `majordomo`, to kukeond. `init` creates no cells and starts nothing; launch a composed
cell explicitly with kuke afterwards (for example
`kuke run --from-config kuke-majordomo-codex`).

`kuke` is the operator's system team, named after the `kuke-` system prefix
(cf. the `kuke-system` realm). The name is reserved: `kuke team add` refuses it,
so a project team — including kukeon's own, which is named `kukeon` — can never
take over its registration or its configs.

To register a project team after `init`, use [`kuke team add`](kuke-team-add.md).
For the shared composition model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                  |
| ----------------- | ------- | -------------------------------------------------------------------------------------------- |
| `--force`         | `false` | Replace an existing `~/.kuke/kuketeams.yaml` and reapply the initial majordomo declaration.  |
| `--reapply`       | `false` | Recompose and apply the `kuke` team from its stored entry. Excludes `--force`.               |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

Git identity import (see [Git identity and credentials](#git-identity-and-credentials)):

| Flag                        | Default | Description                                                                                                                                               |
| --------------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--import-git-identity`     | `false` | Your consent to copy an SSH private key into the `ssh-key` secret `git-ssh` and use it for the team's git credentials (and signing, if you already sign). |
| `--git-key`                 | —       | Private key to import. Default: the key git signs with, else `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`.                                                   |
| `--git-key-passphrase-file` | —       | File holding the key's passphrase. Required for a passphrase-protected key.                                                                               |
| `--git-known-hosts`         | —       | `known_hosts` file to pin for the imported key. Default: the built-in `github.com`, `gitlab.com` and `bitbucket.org` keys.                                |

The last three require `--import-git-identity`.

kuke's global flags (`--verbose`, `--log-level`, `--run-path`, `--no-daemon`)
are accepted but have no effect on `kuke team` subcommands: they resolve the
operator home, the kukeond endpoint and the `ClientConfiguration` themselves.

## What it does

`kuke team init` creates `~/.kuke/kuketeams.yaml` only when it does not exist.
If the file already exists, it exits with guidance to use `--force` and does not
contact the daemon. `kuke team init --force` atomically replaces the global
configuration with the initial document and applies the initial `kuke` team.
It replaces the accepted `kuke` registration after successful application, and
[migrates](#migrating-from-the-kukeon-team-name) a host initialized while the
team was still named `kukeon`.

The command fetches only the sources used by the selected role/harness
definitions into temporary Git checkouts, and composes the kuke team's
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

The initial accepted declaration contains the majordomo selections directly. The
majordomo role and both harnesses come from the kukeon repository (`team/roles/` and
`team/harnesses/`):

```yaml
apiVersion: kuketeams.io/v1
kind: TeamEntry
metadata:
  name: kuke
spec:
  sources:
    - name: kukeon
      url: github.com/eminwux/kukeon
      branch: main
      clonePath: /home/user/kukeon-src
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
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

This applies the shared blueprints `codex` and `claude` — one per harness ×
security profile, realm-wide, see [Teams](../concepts/team.md#shared-blueprints) —
and the CellConfigs `kuke-majordomo-codex` and `kuke-majordomo-claude`, labelled
`kukeon.io/team: kuke`, that reference them. Init does not start them. The explicit entry keeps
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
the stable team name `kuke`, so reruns update that team's resources and prune
obsolete team-owned configs. A shared blueprint the team stops using is removed
only when no other CellConfig references it; if the realm already holds a
different blueprint under a shared name that another team uses, this team's is
applied as `<name>-<content digest>` instead of overwriting it. It does not
prune credentials or running cells.

## Git identity and credentials

Team cells clone, push and sign with [stored git credentials](../guides/git-credentials.md).
The host `~/.ssh` is never mounted, and init reads nothing from it unless you
pass `--import-git-identity`.

`spec.git` in `~/.kuke/kuketeams.yaml` is the operator's default identity. It
names secrets, not host paths:

```yaml
spec:
  git:
    author: { name: Ada Lovelace, email: ada@example.com }
    committer: { name: Ada Lovelace, email: ada@example.com }
    credentials:
      - host: github.com
        secretRef: { name: git-ssh, realm: default }
    signing:
      secretRef: { name: git-ssh, realm: default }
    sign: [commits, tags]
```

Init writes it as `spec.defaults.git` of the team's stack, so every team cell
inherits it. Only that `git` block is touched: the stack's other defaults are
kept, and credentials for other hosts are merged by host. A source may state its
own identity (`sources[].git`), which is rendered into that repo's fill of each
CellConfig.

Init discovers `user.name` / `user.email` from your global git configuration.
With `--import-git-identity` it also stores your SSH key as the secret
`git-ssh`, binds it to every host the team's sources use, and — when your git
configuration already signs commits or tags **with that key** — makes it the
signing key and stores your allowed-signers file as `git-allowed-signers`. If
`user.signingkey` names something else (a GPG key, or a different SSH key), the
key is imported as a credential only, signing is left off and a note says so:
cells signing with a key your forge does not know as your signing key would
produce unverified commits. An import always writes the secret: an existing
`git-ssh` is replaced with the key you named, and a note reports the
replacement. Without the flag, init reports each SSH
source no stored credential covers and each setting that no longer has an
effect (the pre-stored-credential `signingKey` / `allowedSigners` host paths, a
role's `needs.mounts: [ssh]`) as a note.

Migrating an existing installation: see
[Migrating to stored git credentials](../guides/migrate-to-stored-git-credentials.md).

## Authentication and secrets

Only selected role/harness requirements contribute secret lookups. The base
majordomo with Codex or Claude does not require `gh` or `gh-token`. Selecting
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
`~/.kuke/kuketeam.d/kuke.yaml`. It retains explicit selections and
omitted/empty list semantics, with no resolved credentials or duplicated
effective defaults. `init --force` uses the initial majordomo declaration
regardless of an existing `kuke.yaml`. The name `kuke` is reserved for this
team. Init does not discover or migrate other registrations, apart from the
legacy `kukeon.yaml` [below](#migrating-from-the-kukeon-team-name).

### Reapplying an edited entry

`~/.kuke/kuketeam.d/kuke.yaml` is the kuke team's editable source.
`kuke team init --reapply` recomposes the team from it, applies the result, and
has kukeond prune the team's configs the new composition no longer renders. Use
it after editing the entry, for example to
[turn on the health sweep](../concepts/team.md#the-majordomo-health-sweep).

```bash
$EDITOR ~/.kuke/kuketeam.d/kuke.yaml
kuke team init --reapply
```

- The entry is rewritten only after a fully successful apply, like every
  registration. A failed apply leaves your edit in place.
- `~/.kuke/kuketeams.yaml` is read and never written.
- The entry is decoded strictly: an unknown or misspelled field, or a task the
  role does not declare, fails before anything is fetched or applied.
- Without a stored entry the command fails and tells you to run
  `kuke team init` first; it never creates one.
- `--reapply --force` is rejected. `--force` discards the entry and starts
  again from the initial declaration; `--reapply` keeps it.
- `--reapply` does not import a git identity. Use
  `--force --import-git-identity` for that.

### Migrating from the `kukeon` team name

The init team was named `kukeon` before it became `kuke`. That collided with
kukeon's own project team (`metadata.name: kukeon` in the repository's
`kuketeam.yaml`): `kuke team add .` in a kukeon checkout overwrote the init
team's entry and pruned its configs, since both carried
`kukeon.io/team: kukeon`. A host initialized under the old name holds
`~/.kuke/kuketeam.d/kukeon.yaml` and the configs `kukeon-majordomo-codex`,
`kukeon-majordomo-claude` and `kukeon-majordomo-<harness>-health-sweep`.

Run `kuke team init --force`. When `kukeon.yaml` is the legacy init declaration
— a `kukeon` entry with no `projectSource` whose roles are only `majordomo`
(or `operator`, see below) — init:

1. deletes every config labelled `kukeon.io/team: kukeon`,
2. applies `kuke-majordomo-codex` and `kuke-majordomo-claude` (and their
   health-sweep configs) labelled `kukeon.io/team: kuke`, and registers
   `~/.kuke/kuketeam.d/kuke.yaml`,
3. removes `kukeon.yaml` and prints a note saying so.

The legacy entry is removed only after the `kuke` team is fully applied and
registered, so a failed run leaves it in place and rerunning `--force` finishes
the migration. A `kukeon.yaml` holding any other declaration — a project team
registered with `kuke team add`, or an init entry you added roles to — is left
untouched, together with its configs. Like any `--force`, the migration starts
from the initial declaration: re-make edits you had made to `kukeon.yaml` (an
enabled health sweep, for one) in `kuke.yaml` and run `kuke team init --reapply`.
Cells already created from the old configs are not touched.

Log in to each harness once more afterwards. The state volume that holds the
harness login is named after the config, so a `kuke-majordomo-*` cell starts
with a new, empty volume. The old `kukeon-majordomo-*` volumes are left in
place; delete them with `kuke delete volume` when you no longer need them.

### Migrating from the `operator` role

The team's role was named `operator` before it became `majordomo`, and its
configs were `kukeon-operator-codex` and `kukeon-operator-claude`. On a host
initialized then, `kuke team init --force` migrates as above: the legacy entry
counts as the init declaration, so the `kukeon-operator-*` configs (labelled
`kukeon.io/team: kukeon`) are deleted and the `kuke-majordomo-*` configs
applied. The same re-login applies; the old `kukeon-operator-*` volumes are left
in place.

Registration follows full blueprint/config success only. A secret failure, RPC
failure, total resource failure, partial apply, prune failure, or incomplete
response returns nonzero and leaves any prior TeamEntry unchanged. A first-time
failure creates no registration. Successful resources and created secrets are
not rolled back: rerun `kuke team init --force` after fixing the prerequisite to
converge the same `kuke` identity. A registration write failure after
application is reported as such and is also recovered by rerunning
`init --force`.

## Output

```
Created configuration: /home/me/.kuke/kuketeams.yaml
Applied 4 resources; registered kuke team: /home/me/.kuke/kuketeam.d/kuke.yaml
No cells created or started.
```

Missing runtime secrets and notes follow when there are any:

```
Missing secrets (cells will not start until they exist):
  git-ssh  →  kuke create secret git-ssh --realm default --type ssh-key --from-file /path/to/private-key  (git credential; or --type basic-auth)
Notes:
  source agents is cloned over SSH but no stored git credential covers github.com; add one to spec.git.credentials (kuke team init --force --import-git-identity imports your key)
```

The resource count is the shared blueprints plus the CellConfigs, plus one when
the stack's git defaults were written. With `--force`, the first line reads
`Reset configuration: …`. Diagnostics (for
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
