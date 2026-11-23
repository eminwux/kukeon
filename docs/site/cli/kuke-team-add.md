# kuke team add

```
kuke team add NAME --source REPOSITORY [--branch B | --tag T | --commit C] [OVERRIDES] [--force] [--dry-run]
kuke team add DIRECTORY [--name NAME] [--source REPOSITORY] [--branch B | --tag T | --commit C] [OVERRIDES] [--force] [--dry-run]

OVERRIDES: [--role R]... [--harness H]... [-f FILE | --no-repo-file]
```

Create a project team: compose it and apply its blueprints and configs to
kukeond, which stores the `Team` with them. `add` creates no cells and starts
nothing. Run [`kuke team init`](kuke-team-init.md) first. To recompose a team
that is already stored, use [`kuke team apply NAME`](kuke-team.md#kuke-team-apply).
For the composition model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                           |
| ----------------- | ------- | ----------------------------------------------------------------------------------------------------- |
| `--source`        | —       | Project Git remote (SCP-style SSH, `ssh://`, or HTTPS). Required in `NAME` mode.                      |
| `--branch`        | —       | Select a branch (default: local HEAD for directories, remote HEAD for named sources).                 |
| `--tag`           | —       | Select a tag.                                                                                         |
| `--commit`        | —       | Select a full commit object ID (40 or 64 hex characters).                                             |
| `--name`          | —       | Override the team name (default: `NAME`; in directory mode the local `metadata.name`, then basename). |
| `--role`          | —       | Replace the roles: a default role's `NAME` or `[NAME=]SOURCE:PATH`. Repeatable.                       |
| `--harness`       | —       | Replace the harnesses: a default harness's `NAME` or `[NAME=]SOURCE:PATH`. Repeatable.                |
| `-f`, `--file`    | —       | Use the Team document in FILE as the whole declaration.                                               |
| `--no-repo-file`  | `false` | Ignore the repository's `kuketeam.yaml`.                                                              |
| `--force`         | `false` | Replace a stored team of the same name.                                                               |
| `--dry-run`       | `false` | Print what would be applied; write nothing.                                                           |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.                   |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`.          |

kuke's global flags (`--verbose`, `--log-level`, `--run-path`, `--no-daemon`)
are accepted but have no effect on `kuke team` subcommands: they resolve the
kukeond endpoint and the `ClientConfiguration` themselves.

`--branch`, `--tag`, and `--commit` are mutually exclusive, and so are `-f` and
`--no-repo-file`. Explicit empty `--name`, `--source`, or revision flags fail.

## Adding a named team from a Git repository

```sh
kuke team add kukeon --source git@github.com:eminwux/kukeon.git
kuke team add kukeon --source ssh://git@work-alias:2222/eminwux/kukeon.git --branch develop
kuke team add kukeon --source https://github.com/eminwux/kukeon.git --tag v1.0.0
kuke team add kukeon --source github.com/eminwux/kukeon --commit <full-commit-object-id>
```

The positional argument names the team; `root` and `kuke` are
[reserved](#reserved-names). Without a selector, native
`git ls-remote --symref` discovers the remote HEAD branch and records that
branch name. A detached or missing remote HEAD requires an explicit selector.
Branches remain floating selectors; tags select `refs/tags/<name>` and commits
select the object ID.

The repository is fetched once. When it commits a `kuketeam.yaml` at the root,
that file — read from the fetched revision, never from a local checkout — is
the team's declaration; `NAME` wins over its `metadata.name`. Without one, the
team inherits the [defaults](../concepts/team.md#sources-and-defaults): it
needs at least default roles there, or `--role`.

## Adding from a directory

```sh
cd /path/to/my-project
kuke team add .
kuke team add . --name my-team
kuke team add ../another-project --source git@host:owner/repo.git --tag v1.0.0
```

An existing directory argument selects directory mode (including a bare
relative name); `./name` makes that intent explicit. Otherwise use
`NAME --source REPOSITORY`. Directory mode reads the local `kuketeam.yaml` when
present, as a strict `apiVersion: v1beta1`, `kind: Team` document. Other
kinds, invalid YAML, unknown fields, multiple documents, and unreadable files
fail. A missing file starts with an inheriting declaration. The directory and
authored file are never modified; uncommitted local content is not uploaded or
used for definitions.

Naming precedence is `--name`, the local `metadata.name`, then the directory
basename. Names use lowercase letters, digits, dots or hyphens, start and end
with a letter or digit, and cannot be a [reserved name](#reserved-names).

For example, kukeon's own `kuketeam.yaml` composes the `kukeon` team: roles
from the agents repository, harnesses from this repository through the
`project` source:

```yaml
apiVersion: v1beta1
kind: Team
metadata: { name: kukeon }
spec:
  sources:
    - name: agents
      url: git@github.com:eminwux/agents
      branch: main
      clonePath: /home/user/agents
  roles:
    - { name: dev, source: agents, path: dev, needs: { image: [go, root] } }
    - { name: pm, source: agents, path: pm }
    - { name: pr-reviewer, source: agents, path: pr-reviewer }
    - {
        name: pipeline,
        source: agents,
        path: pipeline,
        harness: script,
        tasks: [{ name: tick, suspend: false }],
      }
  harnesses:
    - { name: claude, source: project, path: team/harnesses/claude }
    - { name: codex, source: project, path: team/harnesses/codex }
    - { name: script, source: project, path: team/harnesses/script }
```

`--source` overrides only `spec.projectSource.url`. One of `--branch`, `--tag`,
or `--commit` replaces the entire local revision selection. Without overrides,
local project-source fields win over discovery. A missing source name becomes
`project`; an omitted clone destination becomes
`/home/user/<accepted-team-name>`. Authored clone destinations are retained.

For an unspecified URL, native Git reads `remote.origin.url` from the local Git
metadata. For an unspecified revision, it records the current local branch, or
the full current commit ID for a detached HEAD. Git worktrees are supported. No
origin or revision is guessed: missing Git metadata or origin, an unborn HEAD,
or an unusable source/revision reports an error with override guidance. The
selected revision must be available from the remote; push local-only commits
and branches, or override them, before adding.

## Where the declaration comes from

The first match wins:

1. `-f FILE`: the Team document in FILE replaces the whole declaration.
2. The repository's `kuketeam.yaml` (or the directory's), unless
   `--no-repo-file`.
3. An empty declaration that inherits every list from the defaults.

`--role` and `--harness` then replace the corresponding list of that
declaration: a bare `NAME` picks the default role or harness of that name, and
`[NAME=]SOURCE:PATH` names any role or harness in a registered source, named
after the path's last element without `NAME=` (see
[`kuke team source show`](kuke-team.md#kuke-team-source)). The stored Team is
what was applied, overrides included.

```sh
kuke team add web --source git@github.com:acme/web --no-repo-file --role dev --role reviewer=agents:pr-reviewer --harness claude
```

When the team ends up with no role × harness pair, the error names the empty
input and suggests `--role` and `kuke team source add`.

## `add` creates; replacing needs `--force`

When a team of that name is stored, `add` fails in both modes before anything
is fetched and writes nothing:

```
add: team already exists: web; run kuke team apply web to recompose it from its stored declaration, or add it again with --force to replace the declaration
```

With `--force` the stored declaration is not consulted and is replaced; the
secret bindings it recorded are kept (those the new composition uses). The web
UI asks for the same confirmation.

## Reserved names

`add` refuses two team names, whether given positionally, with `--name`, or as
the directory's `metadata.name`, before anything is fetched or applied:

- `root` — the legacy root identity.
- `kuke` — the operator's system team that [`kuke team init`](kuke-team-init.md)
  applies. A project team under that name would replace its declaration and
  prune its `kuke-majordomo-*` configs, which carry the same
  `kukeon.io/team: kuke` label.

Any other name, `kukeon` included, is a project team. Team names are unique
across realms.

## The project source and inherited defaults

Prerequisites are Git, kukeond, and the parent `default/default/default`
realm/space/stack metadata. `add` uses the same endpoint and client
configuration precedence as
[`kuke team init`](kuke-team-init.md#daemon-endpoint-and-client-configuration).

Omitted `spec.sources`, `spec.roles`, `spec.harnesses` and `spec.connectors`
inherit the realm's `TeamDefaults`; authored lists replace them, and an
explicit `[]` selects none. The `kuke` team's own selections do not become
defaults for project teams.

The additional project source is named `project`, with the clone destination
`/home/user/<team-name>`. It is combined with the resolved definition sources,
so inherited roles and harnesses keep their repositories, and roles or
harnesses may reference `source: project` to load definitions from the project
itself. Any other source named `project` is an error, and duplicate clone
destinations fail rather than replacing another checkout.

The stored Team keeps the project source in `spec.projectSource`, the secret
bindings its selected pairs use in `spec.secrets` (see [Secrets](#secrets)),
and the original omitted/explicit selections. `kuke get team kukeon -o yaml`:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: kukeon
  realm: default
spec:
  projectSource:
    name: project
    url: git@github.com:eminwux/kukeon.git
    branch: main
    clonePath: /home/user/kukeon
  secrets: # recorded when a selected role needs gh-token; edits are kept
    - name: gh-token
      secretRef: { name: gh-token, realm: default }
      envName: GH_TOKEN
```

Omitted fields are not frozen into the Team, so a later
`kuke team apply NAME` inherits the current defaults.

`add` selects published images from each harness's image catalog. It does not
build images or create or start cells. Existing runtime secrets are reused;
missing ones use an explicit env/file creation source or report a kuke
provisioning command.

## Git transports and host credentials

Supported remote forms are SCP-style `[user@]host:path`, explicit
`ssh://[user@]host[:port]/path`, HTTPS, and the `github.com/owner/repo` HTTPS
shorthand. SSH URLs stay SSH. Host aliases and custom ports use native Git/SSH
settings. `git@github.com/owner/repo` is malformed: use
`git@github.com:owner/repo` or `ssh://git@github.com/owner/repo`. Local paths,
`file://`, HTTP, and other schemes are rejected.

Do not put passwords, tokens, query parameters, or fragments in source URLs.
HTTPS userinfo is rejected; SSH usernames such as `git@` are allowed. Git output
and credential-helper output are never included in diagnostics or generated
documents.

Discovery and fetch use native Git with the caller's environment and
configuration. HTTPS uses configured Git credential helpers; public repositories
need none. For SSH, configure hosts, identities, ports, and trusted host keys in
your OpenSSH configuration, and load the identity into your agent
(`ssh-add ~/.ssh/id_ed25519`) before running `add`. Git runs non-interactively
(`BatchMode=yes`, no terminal, prompts disabled) with a one-minute deadline per
subprocess.

Host-side fetch validates the source for composition only; it does not
provision clone credentials for future cells. A cell still needs access to every
requested repository, the published image, and its harness/provider
authentication. `add` does not forward the host SSH agent or copy keys.

Inside a cell, repositories are cloned and pushed with **stored** git
credentials: the git connector identity a Realm, Space or Stack default names,
or the one the team names under `connectors.git`. `add` writes no git defaults.
Store yours with
[`kuke connector import git --default`](kuke-connector.md#kuke-connector-import-git)
(see the [git credentials guide](../guides/git-credentials.md)). The transport
follows the credential, not the URL: a project added by its HTTPS URL is still
cloned and pushed over SSH when its host is bound to an SSH key, so an API token
in the cell can never take over a push.

When the team clones a source over SSH that states no credential of its own,
and neither a scope default nor a Config names a git identity, `add` prints the
note `no git identity is set; run kuke connector import git --default`.

## Secrets

Roles declare only requirement names. `add` binds each required secret for the
selected role/harness pairs and records the bindings in the stored Team: a
binding in the declaration wins, then one recorded by a previous apply, then a
`TeamDefaults` `spec.secrets` binding, then a generated default (same-named
secret in realm `default`, env var upper-snake cased: `gh-token` →
`GH_TOKEN`). If a bound runtime secret does not exist and has no `source`, `add`
still applies and stores the team, exits 0, and prints the
`kuke create secret …` command for each missing one; create them before
launching cells. See
[secret declarations](../concepts/team.md#secret-declarations) for overrides and
[`kuke team init`](kuke-team-init.md) for creation sources.

## Preview, application and recovery

`--dry-run` prints the plan and writes nothing: where the declaration came from
(the repository file at its commit, the directory file, a file, the defaults),
the declaration, each source with its resolved commit, each role × harness with
its Config name, image, tasks with schedules, and connectors and components,
the Configs that would be created, re-rendered and pruned, missing secrets, and
notes. It creates no Secret and reads no secret value. See
[Previewing a change](../concepts/team.md#previewing-a-change).

Before mutating the daemon, `add` validates source references, selected
definitions, images, and required secret bindings and scopes. It then calls
`ApplyDocumentsForTeam` with the team's stable ownership identity; kukeond
stores the `Team` last, and only when every other document applied. Obsolete
team-owned configs are pruned, and a shared blueprint the team stops using is
removed once no other CellConfig references it.

Failure returns nonzero and leaves any previously stored Team unchanged. A
partial apply or ambiguous RPC failure can leave some daemon resources changed.
Fix the prerequisite and rerun — `kuke team apply NAME` if the team was stored,
or the same `add` otherwise. `add` does not roll back created secrets or
resources, launch cells, or retry ambiguous writes.

## Output

```
Applied 7 resources; stored team kukeon.
No cells created or started.
```

The count is the team's shared blueprints (one per harness × security profile
it uses) plus one CellConfig per role/harness pair, plus one headless task
CellConfig per role/harness pair × selected role task, plus the `Team`
document, plus one when the Realm's secret defaults were written. A pair's
config is named `<team>-<role>-<harness>`, and each of its task configs is
named `<team>-<role>-<harness>-<task>`. See [Tasks](../concepts/team.md#tasks).
Pruned configs, missing runtime secrets and notes are listed after it, as for
[`kuke team init`](kuke-team-init.md#output).
