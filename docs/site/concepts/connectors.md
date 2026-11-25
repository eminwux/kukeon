# Connectors

A **connector** is a kind of access a cell needs: the login of its harness, a
token for an API, the identity it clones, pushes and signs with, the credential
a Realm pulls private images with. An **identity** is one stored
[Secret](../manifests/secret.md) that fills a connector — a subscription token,
a work GitHub token, a bot's SSH key.

Connectors separate three decisions that used to be one:

| Decision                                 | Made by                                  | Where it lives                              |
| ---------------------------------------- | ---------------------------------------- | ------------------------------------------- |
| Which connectors a cell requires         | the role and its harness                 | components of the blueprint and the Config  |
| Which identity fills each one by default | the operator, once                       | Realm, Space or Stack `spec.defaults`       |
| Which identity one team or one cell uses | the team (`connectors`) or `--connector` | the Config's fill, or the cell's provenance |

So a token is stored once, picked by name, and replaced in one place.

!!! warning "Trusted network only"

    `kuke serve` — the web UI and its `/api/connectors` endpoints — asks for
    the `root` login ([Sign in](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#sign-in)) but serves plain
    HTTP, and it can be bound to any address. Anyone signed in can create
    identities, change defaults and start cells that use them. Run it on a
    trusted network or loopback, or behind a TLS proxy. No API response or
    error prints secret material; only a named
    `kuke get secret <name> -o yaml|json` does.

## The connectors kuke ships

Each connector is one file under
[`team/connectors/<name>/connector.yaml`](https://github.com/eminwux/kukeon/tree/main/team/connectors).

### `claude`

How a Claude Code cell authenticates. The three methods exclude each other;
`kuke team` enables exactly one on every claude Config.

| Method         | Component             | Delivery                  | Identity                                              |
| -------------- | --------------------- | ------------------------- | ----------------------------------------------------- |
| `login`        | `claude-login`        | the harness state volume  | a login identity: a Volume holding the login          |
| `subscription` | `claude-subscription` | `CLAUDE_CODE_OAUTH_TOKEN` | opaque Secret: a token from `claude setup-token`      |
| `api-key`      | `claude-api-key`      | `ANTHROPIC_API_KEY`       | opaque Secret: an Anthropic API key, billed per token |

`login` is the default: it is what a cell did before connectors existed. Its
identity is a [login identity](#login-identities), created and logged in to
with `kuke connector login claude`. A `subscription` token is valid for a year
and serves every role, so it is the method to choose when cells of several
roles — or a headless task with an empty state volume — must start
authenticated.

### `codex`

How a Codex cell authenticates. Same shape: one method per codex Config.

| Method         | Component            | Delivery                 | Identity                                                            |
| -------------- | -------------------- | ------------------------ | ------------------------------------------------------------------- |
| `login`        | `codex-login`        | the harness state volume | a login identity: a Volume holding the `codex login`                |
| `api-key`      | `codex-api-key`      | `OPENAI_API_KEY`         | opaque Secret: an OpenAI API key                                    |
| `access-token` | `codex-access-token` | `CODEX_ACCESS_TOKEN`     | opaque Secret: a Codex access token (Business and Enterprise plans) |

### `github`

GitHub API access for the `gh` CLI: one method, `token`, delivered as
`GH_TOKEN` through the `github-token` slot, with the `gh` image capability. The
token is never a git credential — clone, push and signing use the `git`
connector.

### `git`

The identity a cell clones, pushes and signs with. A git identity is one
`ssh-key`, `basic-auth` or `opaque` Secret that also carries readable facts:

| Field               | Type              | Meaning                                                                                                |
| ------------------- | ----------------- | ------------------------------------------------------------------------------------------------------ |
| `hosts`             | list              | Hosts or URL prefixes the Secret authenticates (`github.com`); required for `ssh-key` and `basic-auth` |
| `author`            | `Name <email>`    | Commit author                                                                                          |
| `committer`         | `Name <email>`    | Committer; git falls back to the author                                                                |
| `sign`              | `commits`, `tags` | What to sign with the key (`ssh-key` only)                                                             |
| `allowedSignersRef` | secret reference  | Allowed-signers file used to verify (`ssh-key` only)                                                   |

`git.identityRef` names such a Secret — on a Realm, Space or Stack default, on a
Config's `git` block, or on one repo's `git`. When a cell is rendered the daemon
expands it into author, committer, one credential per host and, for an SSH key,
signing with the same key. A field written next to the reference wins over the
identity's, so a Config can keep the identity's key and credentials and state
its own author. The rendered cell never carries the reference, only the result.

A role with `needs.repos` requires the connector. It is optional for the cell
to start: a public repository clones without an identity.

#### Author-only identities

An `opaque` git identity is **author-only**: it carries `author` (required) and
`committer`, and no key. `hosts`, `sign` and `allowedSignersRef` are rejected
on it. Its stored value is the fixed placeholder `kukeon:git-author-only`, which
nothing reads. It is for a user without an SSH key who still wants commits made
under their name.

- A cell whose default names it gets the author and committer, no credential
  and no signing.
- Named on one repo's `git.identityRef`, it sets that repo's author and no
  credential.

```bash
kuke create secret me --connector git --type opaque --author 'Me <me@example.com>'
```

`--type opaque` may be left out: `--connector git` without `--type` and without
`--from-file` creates an author-only identity. With `--from-file`, `--type` is
required. An author-only identity takes no value: `--from-literal` and
`--from-file` fail with
`an author-only git identity stores no value; use --type ssh-key for a key`.

#### `kuke connector`

[`kuke connector`](../cli/kuke-connector.md) manages git identities and Realm
defaults from the command line:

```bash
kuke connector list                      # identities and Realm defaults
kuke connector import git --default      # store your SSH key and gitconfig author, make it the default
kuke connector default git git-ssh       # make an existing identity the Realm default
kuke connector default git --clear       # remove it
```

`import git` reads the SSH key and the global git configuration of the user
running it. With a key it stores an `ssh-key` identity (`git-ssh`); without one,
an author-only identity (`git-author`). It turns signing on only when
`user.signingkey` names the imported key. See the
[git credentials guide](../guides/git-credentials.md).

#### Import from host in the web UI

On the Connectors page, the `git` connector's Identities panel has an
**Import from host** action. It takes a realm, the hosts, an optional identity
name, an optional key and passphrase file, and a _Make it the Realm default_
checkbox (on by default). It calls `POST /api/connectors/git/import` with
`{realm, hosts, name, key, passphraseFile, makeDefault}` and runs the same
import as `kuke connector import git`.

The import runs **as the user running `kuke serve`**: it reads that account's
`~/.ssh` and global git configuration. Under a service manager that may not be
your shell account; the result shows the home directory and the key path that
were read. The key and passphrase file are paths inside that home directory
(`.ssh/id_ed25519` or `~/.ssh/id_ed25519`); absolute paths and `..` are refused.
Key material never appears in a response or an error. `kuke serve` has no
authentication: see the _Trusted network only_ warning at the top of this page.

The identity create form also offers `opaque` for `git`, with no value field: an
author-only identity.

### `registry`

The credential a Realm pulls private images with: a `basic-auth` Secret in the
Realm, bound to a registry server by a `spec.registryCredentials[]` entry's
`secretRef`. It is a Realm concern and never appears on a Config. See
[Private registry](../guides/private-registry.md).

## Identities

An identity is a Secret tagged with its connector:

```bash
# A token identity
sudo kuke create secret work-github --connector github --from-file ./token

# A git identity: the key plus the readable facts
sudo kuke create secret bot-key --connector git --type ssh-key \
    --from-file ~/.ssh/id_ed25519 \
    --author 'Dev Bot <bot@example.com>' --hosts github.com --sign commits,tags
```

`--connector` validates the Secret against the connector: its type must be one
the connector accepts, required fields must be set, and each value must read as
its declared type. Every field of the connector is a flag of its own
(`--author`, `--hosts`, …) and can also be written `--identity FIELD=VALUE`.

```console
$ sudo kuke get secrets -o wide
NAME         TYPE     CONNECTOR  REALM    SPACE  STACK  CELL  IDENTITY
bot-key      ssh-key  git        default  -      -      -     author=Dev Bot <bot@example.com>; hosts=github.com; sign=commits,tags
work-github  opaque   github     default  -      -      -     -
```

The tag and the fields are readable; the material never is. A Secret without a
tag still works wherever it is named by hand — one created before connectors
existed needs no migration — but a Secret tagged for one connector is refused
for another.

### Login identities

A method that keeps its login in the harness state volume (`login`) has
identities too. A **login identity** is a Volume tagged with its connector —
the counterpart of `Secret.spec.connector`: the harness keeps its login in its
state directory, and that directory is the Volume.

```yaml
apiVersion: v1beta1
kind: Volume
metadata:
  name: personal-max
  realm: default
spec:
  reclaimPolicy: Retain
  connector: claude
  method: login
  loginMarker: .credentials.json
```

`spec.connector` and `spec.method` are the tag. `spec.loginMarker` is the path,
relative to the Volume, whose presence means the Volume holds a login; it is
copied from the connector's definition when the Volume is tagged, because the
daemon keeps no connector catalog. A Volume tagged for one connector is refused
as the state volume of another.

```console
$ sudo kuke connector list
IDENTITIES
CONNECTOR  IDENTITY                 SCOPE                    TYPE    STATUS     USED BY
claude     kukeon-dev-claude-state  default/default/default  login   logged in  kukeon-dev-claude (+4)
claude     personal-max             default                  login   empty      -
codex      codex-token              default                  opaque  -          -
```

`STATUS` is `logged in` when the marker exists and `empty` when it does not. The
daemon only checks that the marker exists: it never opens it, and no login
content appears in any output or error. `USED BY` lists the Configs whose
status names the Volume. The same facts are on the Volume itself
(`kuke get volume <name> -o yaml` shows `spec.connector`, `spec.method` and
`status.login`; `kuke get volumes -o wide` adds `CONNECTOR`, `METHOD` and
`LOGIN` columns) and in a Config's `status.connectors[].login`.

**Existing state volumes need no migration.** The state volume of a Config
that uses a `login` method is tagged the next time one of its cells starts,
whether that start creates the Volume or it already existed. Before that, a
state volume a Config's status names is still listed — untagged — so nothing is
hidden.

#### Logging in: the identity cell

```bash
sudo kuke connector login claude personal-max
```

`kuke connector login CONNECTOR [NAME]` creates the tagged Volume when it does
not exist (`reclaimPolicy: Retain`), starts an **identity cell**, attaches your
terminal to it, and deletes the cell when the login ends. Run it again on an
existing identity to log in again. Without `--space` and `--stack` the identity
is scoped to the realm; `NAME` defaults to the connector's name.

An identity cell is a session, not a stored object — the Volume is the
identity. It is one container on the harness image with the Volume mounted at
the harness state directory, running the login command the connector declares
(`login.command`), and `spec.autoDelete: true`. It has no role, no repos, no
secrets and no other connector, so it needs no team: a login identity can be
created on a realm with nothing else in it. The cell carries the label
`kukeon.io/identity-cell: <volume>`; a second `kuke connector login` — or a
second click in the web UI — while a session runs attaches to it instead of
starting another.

A team's own state volumes (`<team>-<role>-<harness>-state`) are login
identities too and take the same command; their names do not change. The login
then runs on the image of a Config that uses the Volume, so a locally built
harness image is used as is. `--image` picks another.

`kuke create volume <name> --connector <connector>` creates a login identity
without logging in, and tags an existing untagged Volume.

In the web UI the connector's page lists its login identities with their status
and the Configs using them. **New login identity** creates one; **Log in** (or
**Log in again**) starts the identity cell and opens its terminal in the
browser, and the row's status follows when the session ends.

#### One role per login identity

A login identity serves one role. The state directory also holds role content
— Claude Code skills under `$HOME/.claude/skills`, Codex's
`$CODEX_HOME/AGENTS.md` — which the role rewrites on every start, so two roles
on one Volume would overwrite each other. kukeon enforces it where the
identity is bound: a Config that names, as its state volume, a login identity
Configs of another `kukeon.io/role` already use is refused when it is applied —
so the role already using it is not affected — and so is a cell that picks one
with `--connector`. The error names both roles. Configs of the same role (a
role's task Configs) share its identity, and Configs without a role label may
share with each other. For one subscription across roles use a
token method — see
[Why login volumes are not shared across roles](#why-login-volumes-are-not-shared-across-roles).

### Harness state: persistent or ephemeral

A harness cell keeps a state directory whatever its auth method. With a `login`
method that directory holds the login, so it is always a Volume. With a token
method (`subscription`, `api-key`, `access-token`) the credential is the
Secret, and where the harness keeps the rest of its state is a choice:

| Method                                    | Identity (credential) | Harness state                            |
| ----------------------------------------- | --------------------- | ---------------------------------------- |
| `login`                                   | tagged Volume         | the same Volume, always persistent       |
| `subscription`, `api-key`, `access-token` | tagged Secret         | persistent Volume (default) or ephemeral |

`persistent` — the default, and what every existing Config does — mounts the
state volume the Config names (`STATE_VOLUME`). `ephemeral` mounts no state
volume: the state directory is part of the container and goes away with the
cell, which starts authenticated from its Secret.

```yaml
kind: CellConfig
spec:
  state: ephemeral
```

Set it on the Config (`spec.state`), in a team declaration
(`connectors.<name>.state`), or for one cell with `--state`. `ephemeral` with a
`login` method is rejected with an error saying why: the login would not
survive the cell.

```console
$ sudo kuke get configs -o wide
NAME                 ...  CONNECTORS
team-dev-claude      ...  claude=login [team-dev-claude-state, logged in]
team-pm-claude       ...  claude=claude-sub (realm) [state team-pm-claude-state]
team-triage-claude   ...  claude=claude-sub (realm) [state ephemeral]
```

A Config's `status.connectors[]` carries the same: `volume` / `volumeRef` for
any method that mounts a state volume, `login` for a login method, and
`harnessState` (`persistent` or `ephemeral`).

## Defaults and precedence

The identity of a connector is resolved when a cell is rendered, most specific
first:

1. the cell's own choice (`--connector`),
2. the Config's fill,
3. the Stack default,
4. the Space default,
5. the Realm default.

A default is a `spec.defaults.secrets` entry keyed by the connector's slot (for
`git`, `spec.defaults.git.identityRef`):

```yaml
apiVersion: v1beta1
kind: Realm
metadata:
  name: default
spec:
  namespace: default.kukeon.io
  defaults:
    secrets:
      github-token:
        secretRef: { name: work-github, realm: default }
    git:
      identityRef: { name: bot-key, realm: default }
```

Defaults are **live**: a Config that chose no identity has no fill, so changing
the default changes the identity of every such Config's next cell, with nothing
to re-render. `kuke team` relies on this — it writes a Realm default for a slot
only when no Realm, Space or Stack default exists yet, and never overwrites one.

```console
$ sudo kuke get configs -o wide
NAME                  ...  CONNECTORS
kuke-majordomo-claude ...  claude=login, github=work-github (realm)
team-dev-codex        ...  codex=login, git=bot-key (config), github=MISSING
```

A required connector with no identity at any level is **unresolved**. Previewing
or creating a cell from that Config fails with an error naming the connector;
the web UI shows a _Needs connector_ badge on the Config, counts the affected
Configs on the Connectors page, and disables _Create_ until a default is set or
an identity is picked. A named identity that is missing, tagged for another
connector or of the wrong type is an error even for an optional connector.

## Teams

A team picks a method and an identity per connector under `spec.connectors` of
its Team declaration — a repository's `kuketeam.yaml`, or the declaration
kukeond stores (`kuke get team NAME -o yaml`, the web UI's Teams page). A team
that omits `connectors` inherits the realm `TeamDefaults`' map. A role entry
overrides the team for that role.

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: backend
spec:
  connectors:
    claude: { method: subscription, identity: claude-team }
    github: { identity: work-github }
    git: { identity: bot-key }
  roles:
    - name: reviewer
      source: agents
      path: roles/reviewer
      connectors:
        github: { identity: review-github } # this role only
```

- `method` picks one of the connector's methods; empty selects its default.
- `identity` names the Secret, `name[@realm[/space[/stack]]]`; without a scope it
  is looked up in the team's realm. Empty leaves the identity to the defaults.
  With `method: login` it names a [login identity](#login-identities) — the
  Volume the role keeps its harness state in — in the same notation; empty
  keeps the team's own `<team>-<role>-<harness>-state`.
- `state` is `persistent` (the default) or `ephemeral`, for the connector that
  authenticates a harness: see
  [Harness state](#harness-state-persistent-or-ephemeral). `ephemeral` with a
  `login` method is rejected.
- A role entry that names no method keeps the team's method, and the team's
  identity unless it names its own. One that names a method states the whole
  choice.
- A role entry also gives the role's cells a connector the role does not ask
  for itself.

A stored team's role entry is switched with
[`kuke team connector`](../cli/kuke-team.md#kuke-team-connector), which edits
the stored Team and applies it in one command, or with the per-role switches
of the Edit team form (Teams page → Edit → Credentials → Per-role access):

```bash
kuke team connector enable backend github --role reviewer --identity review-github
kuke team connector list backend
kuke team connector disable backend github --role reviewer
```

The older spelling of the same opt-in — a secret name the connector's methods
answer to, in the role entry's `needs.secrets` (`needs: { secrets: [gh-token] }`
for `github`) — is still read and still enables the connector; `enable` never
writes it, and `disable` removes it along with the `connectors` entry.

Every rendered Config lists all its connector components under
`spec.components` — the harness's auth method, `git` when the role works on
repositories, and each connector the role or the team asks for. Only an identity
the team chose is written as a fill on the Config; everything else stays with
the defaults. A team's git identity is written on the Config
(`spec.git.identityRef`), never on the Stack, because other teams share the
Stack.

A stored [Source](../manifests/source.md) may name a git identity of its own
(`spec.git.identityRef`, `kuke create source --identity`). It is that
repository's identity in every team cell that clones it, and the credential
`kuke` fetches the Source with while composing; without one, the fetch uses the
realm's default git identity when it covers the address's host, else the
caller's git configuration (see
[Fetching a Source on the host](../guides/git-credentials.md#fetching-a-source-on-the-host)).
A team's own entries carry no `git`.

Roles written before connectors existed keep rendering: a role that lists the
old secret names (`claude-code-oauth-token`, `anthropic-api-key`,
`openai-api-key`, `gh-token`) selects the method that delivers that secret. Two
such names of one connector are alternatives — the first method wins and
`kuke team` says so; set `connectors.<name>.method` to choose.

### Enable a connector on a Config

A Config no team owns enables a connector itself: list the method's component
in `spec.components` (the blueprint must declare it; a blueprint `kuke team`
renders declares every connector component), optionally fill its secret slot,
apply, and restart the cells that should get it:

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: dev
  realm: default
spec:
  blueprint:
    name: dev-claude
    realm: default
  components: [github] # the github connector's token method
  secrets: # optional: without it the Stack, Space and Realm defaults fill the slot
    github-token:
      secretRef: { name: work-github, realm: default }
```

```bash
kuke apply -f dev.yaml
kuke get config dev -o yaml   # status.connectors
kuke restart <cell>           # a running cell keeps what it started with
```

`status.connectors` says how the connector resolves right now — resolved, with
the identity and the level it came from, or unresolved while no fill or
default names an identity:

```yaml
status:
  connectors:
    - connector: github
      component: github
      method: token
      delivery: env
      slot: github-token
      state: resolved
      required: true
      identity: { name: work-github, realm: default }
      origin: config
---
status:
  connectors:
    - connector: github
      component: github
      method: token
      delivery: env
      slot: github-token
      state: unresolved
      required: true
```

A Config a team renders (label `kukeon.io/team`) is changed through its team,
with `kuke team connector` or the Teams page: the team's next apply renders the
Config whole again, so a hand edit of it does not last.
[`kuke apply -f`](../cli/kuke-apply.md) of such a Config still applies it, and
prints a warning naming the team.

## Per-cell overrides

```bash
sudo kuke create cell --from-config team-dev-claude --connector github=other-github
sudo kuke run -c team-dev-claude --connector git=release-key@ops
sudo kuke create cell --from-config team-dev-codex --connector-method codex=api-key --connector codex=my-openai-key
```

`--connector <connector>=<secret>[@realm[/space[/stack]]]` uses another identity
for this one cell. The override is recorded in the cell's provenance
(`spec.provenance.connectorOverrides`), so `kuke restart` and a
reconcile-driven recreate render the cell with the same identity, and
`kuke create cell --clone` carries it over. The flag is valid with
`--from-config` only. An unknown connector, a missing Secret, or a Secret
tagged for another connector is rejected before anything is created.

When the cell uses the connector through a method that keeps a login, the value
names a [login identity](#login-identities) instead:

```bash
sudo kuke create cell --from-config team-dev-claude --connector claude=personal-max
sudo kuke run -c team-dev-claude --connector claude=shared-login@default/teams/core
```

`--connector <connector>=<volume>[@realm[/space[/stack]]]` makes that Volume the
cell's state volume. A name without a scope is a realm-scoped Volume of the
Config's realm. It is recorded in `spec.provenance.connectorVolumes` — a
separate map from `connectorOverrides`, so the entry says it names a Volume —
and kept by `kuke restart`, a reconcile-driven recreate and `--clone`. A Volume
that does not exist, one tagged for another connector, and one Configs of
another role already use are rejected before anything is created.

`--state persistent|ephemeral` picks the
[harness state](#harness-state-persistent-or-ephemeral) of this one cell. It is
recorded in `spec.provenance.state` and kept the same way. Valid with
`--from-config` only.

`--connector-method <connector>=<method>` uses another method of a connector
for this one cell — for example a codex Config on `login` started once with
`api-key`. The blueprint already carries every method of the connector as one
exclusive group, so the cell enables the picked method's component in place of
the one the Config enables; the Config itself is not changed. The new method's
identity resolves as usual: a `--connector` override, the Config's fill, then
the Stack, Space and Realm defaults — a Config's fill for its own method's slot
does not carry over. The choice is recorded in
`spec.provenance.connectorMethods` and kept by `kuke restart`, a
reconcile-driven recreate and `--clone`. A connector the blueprint declares no
methods for, a method it does not declare, or a connector the Config does not
require (a method pick swaps a method, it never adds a connector) is rejected.
Valid with `--from-config` only.

In the web UI's New Cell flow every required connector is pre-selected with its
effective method and identity. A connector with several methods offers a method
picker; choosing another identity or method sends the same overrides, and the
preview shows the methods and identities the cell will use. A login method
offers a login identity picker, and a token method of a harness connector a
harness state choice; New Config offers both too, writing the Config's
`values.STATE_VOLUME*` and `spec.state`. A per-cell pick is
allowed on a team-owned Config too: only the Config's own page shows its
identities read-only, because applying the team re-renders it; that page links
the team, whose declaration is edited on the Teams page.

## Why login volumes are not shared across roles

The `login` methods keep the session in a [login identity](#login-identities),
and a login identity serves one role — `kuke-majordomo-claude` and
`team-dev-claude` each have their own and each log in once
(`kuke connector login`). Sharing one across roles is not supported, and kukeon
refuses it: binding a login identity that Configs of another role already use
— applying a Config that names it, or picking it for a cell — fails with an
error naming both roles.

- The state directory also holds the role itself — settings, skills and
  instructions the role's launch installs there — so a shared volume would let
  one role's content overwrite another's. Lifting the rule needs that content
  moved out of the state directory first.
- A Codex login's refresh token is single-use. Two cells refreshing one copied
  login invalidate each other.

A team may still point a role at a login identity of its choosing —
`connectors.<name>.identity` with `method: login` names the Volume, and leaving
it empty keeps `<team>-<role>-<harness>-state` — as long as no other role uses
it.

When several roles must start authenticated without an interactive login each,
use a token method instead: a Realm-default `claude-subscription` identity
authenticates every claude cell, interactive or headless. Such a cell can keep
its harness state in the role's state volume or, with `state: ephemeral`, in no
volume at all.

## Adding a connector

A connector is data. Adding one is one file, `team/connectors/<name>/connector.yaml`;
no Go and no TypeScript names a connector, and a test
(`pkg/team/connector/guard_test.go`) enforces it.

```yaml
apiVersion: v1beta1
kind: Connector
metadata:
  name: example-token # must match the directory
spec:
  title: Example
  description: An API token for the example service.
  scopes: [realm, space, stack]
  identity:
    secretTypes: [opaque]
    fields:
      - name: account
        required: true
        help: Account the token belongs to.
  methods:
    - name: token
      default: true
      delivery: env
      description: A token delivered as EXAMPLE_TOKEN.
      requires: [example-cli]
      secret:
        name: example-token
        envName: EXAMPLE_TOKEN
```

With that file the connector appears in `kuke create secret --connector`, team
`connectors`, `kuke create cell --connector`, and the web UI's Connectors page
and pickers.

| Field                     | Meaning                                                                                                                                                  |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `title`, `description`    | Display name and what the access is for                                                                                                                  |
| `identity.secretTypes`    | Secret types accepted: `opaque`, `basic-auth`, `ssh-key`                                                                                                 |
| `identity.fields[]`       | Readable identity fields: `name`, `type` (`string`, `list`, `person`, `secret`), `required`, `enum`, `secretTypes`, `help`                               |
| `scopes`                  | Where a default may be set: `realm`, `space`, `stack` (empty allows all)                                                                                 |
| `harness`                 | The harness this connector authenticates; `kuke team` enables exactly one method on each of its Configs                                                  |
| `group`                   | Exclusion group of the method components (defaults to the connector's name)                                                                              |
| `optional`                | A cell may start without an identity                                                                                                                     |
| `methods[]`               | The ways to deliver the connector; exactly one is `default: true`                                                                                        |
| `methods[].component`     | Blueprint component name; `<connector>-<method>` when empty                                                                                              |
| `methods[].delivery`      | `env`, `file` or `stateVolume`                                                                                                                           |
| `methods[].requires`      | Image capabilities the method needs                                                                                                                      |
| `methods[].secret`        | The slot: `name`, and `envName` (`env`) or an absolute `mountPath` (`file`); `secretName` and `aliases` for older roles                                  |
| `methods[].login`         | `stateVolume` only. `marker`: path relative to the state directory whose presence means "logged in"; `command`: the argv an identity cell runs to log in |
| `delivery` (no `methods`) | `git` or `registry`: a connector delivered by a handler                                                                                                  |

Component names and slot names are unique across the catalog.

### Delivery handlers

The delivery says how an identity reaches a cell:

| Delivery      | What it does                                                                  |
| ------------- | ----------------------------------------------------------------------------- |
| `env`         | The Secret's value is set as the environment variable of the method's slot    |
| `file`        | The Secret's value is mounted read-only at the slot's path                    |
| `stateVolume` | The login lives in the harness state volume: the Volume is the identity       |
| `git`         | `git.identityRef` is expanded into author, committer, credentials and signing |
| `registry`    | The Secret is the pull credential of a Realm's `registryCredentials[]` entry  |

`env`, `file` and `stateVolume` need no code. `git` and `registry` are the two
handlers implemented in the daemon (`internal/connector/handlers.go` names their
fields); a new connector with one of the first three deliveries is only a file.

The daemon keeps no catalog. `kuke team` copies each connector's definition into
the blueprint component that requires it, so the daemon validates and resolves
identities from the blueprint alone — including for a connector added after the
daemon was built.

## Related

- [Secret manifest](../manifests/secret.md) — `spec.connector` and `spec.identity`
- [Blueprint contract](blueprint-contract.md) — components and groups
- [Teams](team.md) — roles, harnesses and bindings
- [Git credentials](../guides/git-credentials.md) — what a git identity expands into
- [Private registry](../guides/private-registry.md) — the registry connector
