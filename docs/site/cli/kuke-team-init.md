# kuke team init

```
kuke team init [--force | --reapply] [--host ENDPOINT] [--configuration PATH]
```

Apply the `kuke` team, whose one role is `majordomo`, to kukeond, which stores
the `Team` with the Configs composed from it. `init` creates no cells and
starts nothing; launch a composed cell explicitly with kuke afterwards (for
example `kuke run --from-config kuke-majordomo-codex`).

`kuke` is the operator's system team, named after the `kuke-` system prefix
(cf. the `kuke-system` realm). The name is reserved: `kuke team add` refuses it
and `kuke delete team` refuses to delete it, so a project team — including
kukeon's own, which is named `kukeon` — can never take over its configs.

To add a project team after `init`, use [`kuke team add`](kuke-team-add.md).
For the other team verbs see [`kuke team`](kuke-team.md); for the composition
model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                  |
| ----------------- | ------- | -------------------------------------------------------------------------------------------- |
| `--force`         | `false` | Replace the stored `kuke` team with the initial majordomo declaration and apply it.          |
| `--reapply`       | `false` | Recompose and apply the `kuke` team from its stored Team. Excludes `--force`.                |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

Init does not read or store a git identity; use
[`kuke connector import git --default`](kuke-connector.md#kuke-connector-import-git)
(see [Git identity and credentials](#git-identity-and-credentials)).

kuke's global flags (`--verbose`, `--log-level`, `--run-path`, `--no-daemon`)
are accepted but have no effect on `kuke team` subcommands: they resolve the
kukeond endpoint and the `ClientConfiguration` themselves.

## What it does

"Already initialized" means kukeond stores a Team named `kuke`. Without
`--force` (or `--reapply`) init then stops before fetching anything:

```
init: already initialized (team kuke is stored); use --force to reset or --reapply to recompose
```

`kuke team init --force` applies the initial `kuke` team, replacing the stored
declaration. Recorded secret bindings are not kept by `--force`: it starts
again from the initial declaration.

When the `default` realm has no `TeamDefaults`, init creates one in the same
apply, with the `kukeon` source (at the same revision as the `kuke` team, below)
and its `codex` and `claude` harnesses and no default roles, so a project team needs only roles. An existing `TeamDefaults`
is never changed, with or without `--force`. See
[Sources and defaults](../concepts/team.md#sources-and-defaults).

The command fetches only the sources used by the selected role/harness
definitions into temporary Git checkouts, and composes the kuke team's
blueprints and configs. Git must be installed and able to read those sources
without a terminal prompt. `branch`, `tag`, and `commit` selectors are fetched
explicitly; no checkout in the current directory is used. Fetched definitions
are removed after the invocation.

The initial declaration contains the majordomo selections directly, so the
`kuke` team does not depend on the defaults. The majordomo role and both
harnesses come from the kukeon repository (`team/roles/` and `team/harnesses/`).
`kuke get team kuke -o yaml` prints it:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: kuke
  realm: default
spec:
  sources:
    - name: kukeon
      url: github.com/eminwux/kukeon
      tag: v0.10.0 # the revision this kuke was built from
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

The `kukeon` source is pinned to the revision the running `kuke` was built
from, because `kuke` reads exactly one schema version of the documents it
fetches: a release build pins its tag, a development build pins the clean
commit it was built from, and only a build that knows neither (a dirty tree,
or no VCS stamp) falls back to `branch: main`. When a fetched document is
rejected anyway, the error names each fetched source with its URL and
revision. A source revision and the binary then disagree on schema version.
A stored `kuke` team (`--reapply`) and an existing `TeamDefaults` keep the
revision they were stored with.

This applies the shared blueprints `codex` and `claude` — one per harness ×
security profile, realm-wide, see [Teams](../concepts/team.md#shared-blueprints) —
and the CellConfigs `kuke-majordomo-codex` and `kuke-majordomo-claude`, labelled
`kukeon.io/team: kuke`, that reference them. Init does not start them.

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
checks that the parent metadata exists and checks secret prerequisites before
mutating the daemon. Provision missing parent scopes with kuke (`kuke init`
creates `default/default/default`). Init never creates, starts, restarts, or
deletes cells. `ApplyDocumentsForTeam` receives the stable team name `kuke`, so
reruns update that team's resources and prune obsolete team-owned configs; the
`Team` document is stored last, and only when every other document applied
(see [A Team is stored only with its Configs](../concepts/team.md#a-team-is-stored-only-with-its-configs)).
A shared blueprint the team stops using is removed only when no other
CellConfig references it. A shared blueprint that already exists with different
content is replaced in place, whichever team's configs use it, so every team
sees the same content; a note names the old and new content digest and the
other configs using it, whose cells turn `OutOfSync` until `kuke restart`. Only
a blueprint kuke team did not render (no `kukeon.io/shared: "true"` label) is
never overwritten: this team's is applied as `<name>-<content digest>` instead.
It does not prune credentials or running cells.

## Git identity and credentials

Team cells clone, push and sign with [stored git credentials](../guides/git-credentials.md).
The host `~/.ssh` is never mounted, and init reads nothing from it or from your
git configuration. It writes no `spec.defaults.git` on the team's stack.

A team cell uses the git connector identity a Realm, Space or Stack default
names (`spec.defaults.git.identityRef`), unless the team names one for its
Configs (`connectors.git.identity`). Store yours once and make it the Realm
default:

```bash
kuke connector import git --default
```

See [`kuke connector import git`](kuke-connector.md#kuke-connector-import-git).
A source may still state its own identity (`sources[].git`: author, committer,
credential, signing), which is rendered into that repo's fill of each
CellConfig.

When the team clones a source over SSH that states no credential of its own,
no Realm, Space or Stack default names a git identity, and no Config names its
own, init prints one note:

```
no git identity is set; run kuke connector import git --default
```

A role's `needs.mounts: [ssh]` mounts nothing; init prints a note for it.

A `TeamDefaults` has no `spec.git`; a document that still has one fails to
decode and names `kuke connector import git --default`.

Blueprints carry the connector definitions of the `kuke` build that applied the
team. After upgrading `kuke`, run `kuke team init --reapply` (or `--force`)
before the team's cells accept an author-only git identity.

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
fail. File sources follow kuke's trailing-newline removal. All required sources
are checked before the first create, and shared runtime identities are created
once. Creation happens before dependent blueprints and configs are applied.
Credentials and remote secret-error text are never printed or stored in the
Team or the blueprint/config request.

A missing secret without a creation source does not fail init or add. The
blueprints/configs are still applied and the team stored (kukeond resolves
secret references only when a cell launches), and the command exits 0 after
listing each missing runtime secret once:

```text
Missing secrets (cells will not start until they exist):
  provider-account  →  kuke create secret provider-account --realm auth --space shared --stack operators --from-file /path/to/token
```

Create them before launching cells; no rerun is needed. `kuke run` still fails
on a cell whose secret is absent. The actual command uses the binding's
resolved name and scope. A cell-scoped reference requires a `Secret` manifest
with its exact metadata coordinates, applied with
`kuke apply -f /path/to/secret.yaml`.

Init rechecks existence immediately before each create and never retries a
secret write, including when a transport failure leaves its outcome ambiguous.
On a rerun, a secret accepted by the previous attempt is discovered and reused.
Secret rotation is not an init action; rotate explicitly with kuke.

## Editing and reapplying the kuke team

The stored `kuke` Team is the team's editable source. Edit it and apply it,
for example to [turn on the health sweep](../concepts/team.md#the-majordomo-health-sweep):

```bash
kuke get team kuke -o yaml > kuke.yaml
$EDITOR kuke.yaml
kuke team apply -f kuke.yaml --dry-run   # preview
kuke team apply -f kuke.yaml
```

The web UI's Teams page does the same: edit the declaration as YAML, preview,
apply. `kuke team init --reapply` (equal to `kuke team apply kuke`) recomposes
the team from its stored Team without changing it, applies the result, and has
kukeond prune the team's configs the new composition no longer renders — after
the role or harness definitions changed upstream, or after a `kuke` upgrade.

- The stored Team changes only with a fully successful apply. A failed apply
  leaves the previous declaration stored.
- A Team document is decoded strictly: an unknown or misspelled field, or a
  task the role does not declare, fails before anything is applied.
- Without a stored `kuke` Team, `--reapply` fails and tells you to run
  `kuke team init` first; it never creates one.
- `--reapply --force` is rejected. `--force` discards the stored declaration
  and starts again from the initial one; `--reapply` keeps it.
- `--reapply` does not import a git identity; nothing in `kuke team` does. Use
  [`kuke connector import git --default`](kuke-connector.md#kuke-connector-import-git).

A failure — a secret failure, RPC failure, total resource failure, partial
apply, prune failure, or incomplete response — returns nonzero and leaves the
previously stored Team unchanged; a first-time failure stores no Team.
Successful resources and created secrets are not rolled back: rerun
`kuke team init --force` (or `--reapply`) after fixing the prerequisite to
converge the same `kuke` identity.

## Upgrading from host files

Earlier releases kept the kuke team in a file under `~/.kuke` and read global
defaults from another. Those files are no longer read: when either exists,
init prints one note saying so and nothing is converted. Run
`kuke team init` (or `--force`) and re-make any edits you had made — an enabled
health sweep, for one — in the stored Team. See
[Upgrading from host files](../concepts/team.md#upgrading-from-host-files).

## Output

```
Applied 6 resources; stored team kuke.
No cells created or started.
```

Pruned configs, missing runtime secrets and notes follow when there are any:

```
Pruned configs: kuke-majordomo-opencode
Missing secrets (cells will not start until they exist):
  git-ssh  →  kuke create secret git-ssh --realm default --type ssh-key --from-file /path/to/private-key  (git credential; or --type basic-auth)
  bot-key  →  kuke create secret bot-key --realm default --connector git --type ssh-key --from-file /path/to/private-key --hosts github.com --author 'Name <email>'  (git identity; or --type basic-auth)
Notes:
  role dev: needs.mounts ssh no longer mounts the host ~/.ssh; cells clone, push and sign with stored git credentials
```

A Secret named as a git identity (`spec.connectors.git.identity`) is listed
with the `--connector git` form, since a cell renders only with a Secret tagged
for the git connector; replace the example host and author with your own. A
credential named directly keeps the plain `--type ssh-key` form.

The resource count is the shared blueprints plus the CellConfigs, plus the
`Team` document, plus one when the Realm's secret defaults were written and one
for a `TeamDefaults` init created.

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
