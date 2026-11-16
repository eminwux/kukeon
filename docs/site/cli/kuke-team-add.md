# kuke team add

```
kuke team add DIRECTORY [--name NAME] [--source REPOSITORY] [--branch B | --tag T | --commit C] [--host ENDPOINT] [--configuration PATH]
kuke team add NAME --source REPOSITORY [--branch B | --tag T | --commit C] [--host ENDPOINT] [--configuration PATH]
```

Compose a project team, apply its blueprints and configs to kukeond, and
register it under `~/.kuke/kuketeam.d/<name>.yaml`. `add` creates no cells and
starts nothing. Run [`kuke team init`](kuke-team-init.md) first. For the shared
composition model, see [Teams](../concepts/team.md).

## Flags

| Flag              | Default | Description                                                                                  |
| ----------------- | ------- | -------------------------------------------------------------------------------------------- |
| `--source`        | —       | Project Git remote (SCP-style SSH, `ssh://`, or HTTPS). Required in `NAME` mode.             |
| `--branch`        | —       | Select a branch (default: local HEAD for directories, remote HEAD for named sources).        |
| `--tag`           | —       | Select a tag.                                                                                |
| `--commit`        | —       | Select a full commit object ID (40 or 64 hex characters).                                    |
| `--name`          | —       | Override the team name (default: local Team `metadata.name`, then the directory basename).   |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

kuke's global flags (`--verbose`, `--log-level`, `--run-path`, `--no-daemon`)
are accepted but have no effect on `kuke team` subcommands: they resolve the
operator home, the kukeond endpoint and the `ClientConfiguration` themselves.

`--branch`, `--tag`, and `--commit` are mutually exclusive. Explicit empty
`--name`, `--source`, or revision flags fail.

## Adding a named team from a Git source

Register a named team without a local project checkout or Team file:

```sh
kuke team add kukeon-dev --source git@github.com:eminwux/kukeon.git
kuke team add kukeon-dev --source ssh://git@work-alias:2222/eminwux/kukeon.git --branch develop
kuke team add kukeon-dev --source https://github.com/eminwux/kukeon.git --tag v1.0.0
kuke team add kukeon-dev --source github.com/eminwux/kukeon --commit <full-commit-object-id>
```

The positional argument names the team; `root` is reserved. Without a selector,
native `git ls-remote --symref` discovers the remote HEAD branch and saves that
branch name. A detached or missing remote HEAD requires an explicit selector.
Branches remain floating selectors; tags select `refs/tags/<name>` and commits
select the object ID. All selections are fetched and checked out before
application, including the project source when it supplies no role/harness
definitions. The explicit-source form does not read a local `kuketeam.yaml`.

## Adding from a directory

```sh
cd /path/to/my-project
kuke team add .
kuke team add . --name my-team
kuke team add ../another-project --source git@host:owner/repo.git --tag v1.0.0
```

An existing directory argument selects directory mode (including a bare
relative name); `./name` makes that intent explicit. Otherwise use
`NAME --source REPOSITORY`. Directory mode reads `kuketeam.yaml` when present,
as a strict `apiVersion: kuketeams.io/v1`, `kind: Team` declaration. Other
kinds, invalid YAML, unknown fields, multiple documents, and unreadable files
fail. A missing file starts with an inheriting declaration. The directory and
authored file are never modified; uncommitted local content is not uploaded or
used for definitions.

Naming precedence is `--name`, the local `metadata.name`, then the directory
basename. Names use lowercase letters, digits, dots or hyphens, start and end
with a letter or digit, and cannot be `root`. Choose a name distinct from the
operator's `kukeon` identity.

For example, kukeon's own `kuketeam.yaml` composes the `kukeon-dev` team: roles
from the agents repository, harnesses from this repository through the
`project` source:

```yaml
apiVersion: kuketeams.io/v1
kind: Team
metadata: { name: kukeon-dev }
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
  harnesses:
    - { name: claude, source: project, path: team/harnesses/claude }
    - { name: codex, source: project, path: team/harnesses/codex }
```

`--source` overrides only `projectSource.url`. One of `--branch`, `--tag`, or
`--commit` replaces the entire local revision selection. Without overrides,
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

## The project source and inherited defaults

Prerequisites are Git, an existing valid `~/.kuke/kuketeams.yaml`, kukeond, and
the parent `default/default/default` realm/space/stack metadata. `add` uses the
same endpoint and client configuration precedence as
[`kuke team init`](kuke-team-init.md#daemon-endpoint-and-client-configuration).

`init` leaves global defaults empty. Configure reusable sources, roles, and
harnesses first (see [reusable defaults](../concepts/team.md#reusable-defaults-after-init)),
or provide explicit selections in the directory's Team declaration. The
operator's own selections in `kukeon.yaml` do not become defaults for project
teams. Omitted `spec.sources`, `spec.roles`, and `spec.harnesses` inherit
global defaults; authored lists replace them, and an explicit `[]` selects none.

The additional project source is named `project`, with the clone destination
`/home/user/<team-name>`. It is combined with the resolved definition sources,
so inherited roles and harnesses keep their repositories, and roles or
harnesses may reference `source: project` to load definitions from the project
itself. Any existing source named `project` is a preflight error, and duplicate
clone destinations fail rather than replacing another checkout.

The accepted TeamEntry stores an additive top-level `projectSource`, the secret
bindings its selected pairs use (see [Secrets](#secrets)), and the original
omitted/explicit `spec` selections:

```yaml
apiVersion: kuketeams.io/v1
kind: TeamEntry
metadata:
  name: kukeon-dev
projectSource:
  name: project
  url: git@github.com:eminwux/kukeon.git
  branch: main
  clonePath: /home/user/kukeon-dev
secrets: # Generated when a selected role needs gh-token; edits are kept.
  - name: gh-token
    secretRef: { name: gh-token, realm: default }
    envName: GH_TOKEN
spec: {}
```

Omitted fields are not frozen into the entry, so later recomposition inherits
the current global defaults. Repeated add replaces the project source and
revision, retaining explicit selections in an existing valid registration. It
never resets global configuration.

`add` reads the global role/harness selections, Git identity, parameters, and
scoped secret bindings. It selects published images from each harness's image
catalog. It does not build images or create or start cells. Existing runtime
secrets are reused; missing ones use an explicit env/file creation source or
report a kuke provisioning command.

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
credentials: the `spec.git` block of `~/.kuke/kuketeams.yaml`, which `add`
writes as the git defaults of the team's stack (see
[`kuke team init`](kuke-team-init.md#git-identity-and-credentials) and the
[git credentials guide](../guides/git-credentials.md)). The transport follows
the credential, not the URL: a project added by its HTTPS URL is still cloned
and pushed over SSH when its host is bound to an SSH key, so an API token in the
cell can never take over a push. `add` prints a note for every runtime-cloned
SSH source no stored credential covers.

## Secrets

Roles declare only requirement names. `add` binds each required secret for the
selected role/harness pairs and records the bindings in the TeamEntry: a binding
already recorded there wins, then a global `spec.secrets` binding, then a
generated default (same-named secret in realm `default`, env var upper-snake
cased: `gh-token` → `GH_TOKEN`). No global configuration is needed. If a bound
runtime secret does not exist and has no `source`, `add` still applies and
registers the team, exits 0, and prints the `kuke create secret …` command for
each missing one; create them before launching cells. See
[secret declarations](../concepts/team.md#secret-declarations) for overrides and
[`kuke team init`](kuke-team-init.md) for creation sources.

## Registration and recovery

Before mutating the daemon, `add` validates source references, selected
definitions, images, and required secret bindings and scopes, and stages a complete
`0600` entry beside `~/.kuke/kuketeam.d/<name>.yaml`. It calls
`ApplyDocumentsForTeam` with the team's stable ownership identity and publishes
the registration by atomic rename only after full application success. Repeated
add converges the same resources and one registration; obsolete team-owned
configs are pruned, and a shared blueprint the team stops using is removed once
no other CellConfig references it.

Failure returns nonzero and preserves any previous accepted entry. A partial
apply or ambiguous RPC failure can leave some daemon resources changed. Fix the
prerequisite and rerun the same command, including any explicit revision flags.
`add` does not roll back created secrets or resources, launch cells, or retry
ambiguous writes.

## Output

```
Applied 6 resources; registered team kukeon-dev: /home/me/.kuke/kuketeam.d/kukeon-dev.yaml
No cells created or started.
```

The count is the team's shared blueprints (one per harness × security profile
it uses) plus one CellConfig per role/harness pair, plus one headless task CellConfig
per role/harness pair × selected role task, plus one when the stack's git
defaults were written. A pair's config is named `<team>-<role>-<harness>`, and
each of its task configs is named `<team>-<role>-<harness>-<task>`. See
[Tasks](../concepts/team.md#tasks). Missing runtime secrets and notes are listed after
it, as for [`kuke team init`](kuke-team-init.md#output).
