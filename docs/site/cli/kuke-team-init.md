# kuke team init

```
kuke team init [--force | --reapply | --upgrade] [--host ENDPOINT] [--configuration PATH]
```

Apply the two system teams to kukeond, which stores each `Team` with the
Configs composed from it:

- `kuke`, whose one role is `majordomo`, the agent that operates kukeon;
- `default`, whose one role is `agent`, composed as the agent templates
  `default-agent-claude` and `default-agent-codex` that
  [`kuke create agent`](kuke-create.md#kuke-create-agent) copies. See
  [The default team](#the-default-team).

`init` creates no cells and starts nothing; launch a composed cell explicitly
with kuke afterwards (for example `kuke run --from-config kuke-majordomo-codex`,
or `kuke create agent --harness claude`).

`kuke` is the operator's system team, named after the `kuke-` system prefix
(cf. the `kuke-system` realm). Both names are reserved: `kuke create team`
refuses them and `kuke delete team` refuses to delete either, so a project
team — including kukeon's own, which is named `kukeon` — can never take over
their configs.

To create a project team after `init`, use
[`kuke create team`](kuke-create.md#kuke-create-team).
For the other team verbs see [`kuke team`](kuke-team.md); for the composition
model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                                                                                                                                                                                                                       |
| ----------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--force`         | `false` | Replace the stored `kuke` and `default` teams with their initial declarations and apply them. Picks up a newer majordomo declaration (a new role need, a new harness); drops `spec.secrets`.                                                                                                      |
| `--reapply`       | `false` | Recompose and apply the `kuke` and `default` teams from their stored Teams; creates `default` when none is stored. Excludes `--force`.                                                                                                                                                            |
| `--upgrade`       | `false` | Re-pin the stored `kukeon` Source to this `kuke`'s revision, convert state stored before Sources were a kind, and recompose the `kuke` and `default` teams (creating `default` when none is stored). See [Upgrading the kuke team](#upgrading-the-kuke-team). Excludes `--force` and `--reapply`. |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.                                                                                                                                                                                                               |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`.                                                                                                                                                                                                      |

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

In the same apply, init stores the [Source](../manifests/source.md) `kukeon`
(`github.com/eminwux/kukeon` at the revision below) unless one of that name is
stored, and, when the `default` realm has no `TeamDefaults`, creates one with
the `codex` and `claude` harnesses of that Source and no default roles, so a
project team needs only roles. The `TeamDefaults` holds no sources: the sources
its harnesses name are stored Sources (`kuke get sources`). An existing
`TeamDefaults` is never changed, with or without `--force`, except by
`--upgrade`. See
[Sources and defaults](../concepts/team.md#sources-and-defaults).

The command fetches only the sources used by the selected role/harness
definitions into temporary Git checkouts, and composes the kuke team's
blueprints and configs. Git must be installed and able to read those sources
without a terminal prompt. `branch`, `tag`, and `commit` selectors are fetched
explicitly; no checkout in the current directory is used. Fetched definitions
are removed after the invocation.

The initial declaration contains the majordomo selections directly, so the
`kuke` team does not depend on the defaults. The majordomo role and both
harnesses come from the kukeon repository (`team/roles/` and `team/harnesses/`),
which the team names by its stored Source: the entry carries no address and
no ref, so it reads the Source's. `kuke get team kuke -o yaml` prints it:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: kuke
  realm: default
spec:
  sources:
    - name: kukeon # the stored Source kukeon, at its ref
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

`kuke get source kukeon -o yaml` prints the Source it reads:

```yaml
apiVersion: v1beta1
kind: Source
metadata:
  name: kukeon
  realm: default
spec:
  url: github.com/eminwux/kukeon
  tag: v0.10.0 # the revision this kuke was built from
```

The `kukeon` Source is pinned to the revision the running `kuke` was built
from, because `kuke` reads exactly one schema version of the documents it
fetches: a release build pins its tag, a development build pins the clean
commit it was built from, and only a build that knows neither (a dirty tree,
or no VCS stamp) falls back to `branch: main`. When a fetched document is
rejected anyway, the error names each fetched source with its URL and
revision. A source revision and the binary then disagree on schema version.
The stored Source keeps the revision it was stored with — for the `kuke` team
(`--reapply` and `--force`) and for every team that reads it — until
`kuke team init --upgrade`, which [`kuke update`](kuke-update.md) and the
installer run. When the rejected document came from the stored `kukeon`
Source at a revision other than the binary's, the error says so and names the
fix: `run kuke team init --upgrade to re-pin Source kukeon to this binary's
<revision>, then rerun kuke team init`.

This applies the shared blueprints `codex` and `claude` — one per harness ×
security profile, realm-wide, see [Teams](../concepts/team.md#shared-blueprints) —
and the CellConfigs `kuke-majordomo-codex` and `kuke-majordomo-claude`, labelled
`kukeon.io/team: kuke`, that reference them. Init does not start them.

## The default team

After the `kuke` team, init applies the `default` team in the same mode:

| Init                       | `default` stored                      | `default` not stored |
| -------------------------- | ------------------------------------- | -------------------- |
| `kuke team init` (first)   | —                                     | created              |
| `kuke team init --force`   | replaced with the initial declaration | created              |
| `kuke team init --reapply` | recomposed from the stored Team       | created              |
| `kuke team init --upgrade` | recomposed when the kuke team was     | created              |

A plain `kuke team init` on an initialized host stops before either team (see
above), so an install that predates the `default` team gets it from
`--upgrade` (which [`kuke update`](kuke-update.md) runs) or `--reapply`.

The team reads the `agent` role and both harnesses from the stored `kukeon`
Source, and its `spec.configs: template` makes composition label each Config
`kukeon.io/template: "true"`: `default-agent-claude` and `default-agent-codex`,
in `default/default/default`, using the shared `claude` and `codex` blueprints.
`kuke get team default -o yaml` prints the declaration; see
[Teams → The default team](../concepts/team.md#the-default-team) and
[Your first agent](../guides/first-agent.md).

Agents made from the templates have no `kukeon.io/team` label, so applying the
team (`--reapply`, `--force`, `--upgrade`) never prunes them or their cells.

**A team of your own named `default`.** A team you added under that name before
it became a system team has no `spec.configs`. Init leaves it untouched, still
applies `kuke`, and prints:

```
Notes:
  team default is a team you added before default became a system team; init left it untouched and did not apply the system default team (the agent templates). To rename yours: kuke team export default > team.yaml, set metadata.name in team.yaml, kuke apply -f team.yaml, kuke delete team default, then kuke team init --reapply
```

When the `kuke` team applies and the `default` team then fails, init prints
the `kuke` outcome and exits non-zero with
`team kuke applied; team default failed: …`; rerun with `--reapply` once the
cause is fixed.

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
A stored [Source](../manifests/source.md) may state its own identity
(`spec.git`: `identityRef`, author, committer, credential, signing), which is
rendered into that repo's fill of each CellConfig; a team entry carries no
`git` of its own.

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

A missing secret without a creation source does not fail init or `kuke create team`. The
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

## Upgrading the kuke team

`kuke team init --upgrade` moves the stored `kukeon` Source to the revision
the running `kuke` was built from — the release tag for a stable build, the
build commit for an edge build — and recomposes the `kuke` team.
[`kuke update`](kuke-update.md) runs it with the new binary after the daemon is
back, and the one-line installer runs it on a host that already has a daemon.

- It reads the stored Source `kukeon`, the stored `kuke` Team and the
  `TeamDefaults` of realm `default`. When the Source's address is
  `github.com/eminwux/kukeon` (over any transport), it gets the new `branch`,
  `tag` and `commit`; its `git` is kept. A `kukeon` Source with another address
  is the operator's: it is left alone and named in a note.
- It converts what was stored before Sources were a kind. A `TeamDefaults` with
  `spec.defaults.sources` has each listed source stored as a Source (unless one
  of the name is) and the list cleared, with the note
  `converted N registered sources to Source documents`; a source that was
  cloned anywhere but `/home/user/<name>` adds a note naming the path and
  `spec.sources[].clonePath`, since a Source has no clone path. The `kuke`
  team's `kukeon` entry, which carried kukeon's address and pin, becomes a
  reference to the stored Source (note
  `kuke team: its kukeon entry now reads the stored kukeon Source`), keeping
  its `clonePath`.
- When nothing changes, it prints `Source kukeon already at <revision>` and
  exits 0 without fetching or applying anything.
- Otherwise the Sources, the converted `TeamDefaults` and the `kuke` team land
  in one apply and the `kuke` team is recomposed. `spec.secrets` and every
  other field of the stored Team are kept.
- It prints one line per change (`Source kukeon: commit <old> -> commit <new>`)
  and every other stored team that inherits the defaults, with
  `kuke team apply NAME` to recompose it. Those teams are not recomposed, and
  any team that reads the `kukeon` Source sees the new revision only when it is.
- It never creates the `kuke` team. Without one, it re-pins and converts only
  the Sources and the `TeamDefaults` (if any) and prints
  `team kuke is not stored; run kuke team init to create it`.
- It does not merge a newer majordomo declaration (a new role need, a new
  harness) into the stored team; `--force` does that.

```bash
kuke team init --upgrade
kuke get sources                    # kukeon at the binary's revision
kuke team init --upgrade            # Source kukeon already at ...
```

### Kinds and compatibility

Since Sources became a kind, `kuke` and kukeond must be upgraded together: it
is a **flag day**. A `kuke` built before the change cannot write a `Team` or
`TeamDefaults` to a kukeond built after it — kukeond rejects a Team entry that
carries `url` or `git` and a `TeamDefaults` with `spec.defaults.sources` — and
a `kuke` built after it cannot use a kukeond built before it, which serves no
Source. Documents stored before the change keep reading, and the first command
that writes each converts it (`kuke team init --upgrade`, which `kuke update`
runs, converts the defaults and the `kuke` team).

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
Applied <n> resources; stored team default.
No cells created or started.
```

Each team's report is printed in turn, `kuke` first; a `default` team init
kept or left untouched is reported as a note instead. Pruned configs, missing runtime secrets and notes follow when there are any:

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
`Team` document, plus one when the Realm's secret defaults were written, one
for the `kukeon` Source and one for a `TeamDefaults` init created.

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
