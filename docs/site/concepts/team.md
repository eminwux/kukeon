# Teams

A **team** is a set of agent cells composed from **roles** (what an agent is
told to do) and **harnesses** (which agent CLI runs it, in which image). For
every selected role × harness pair, `kuke team` renders one `CellConfig` — the
pair's answers — against a **shared** `CellBlueprint`, applies them to kukeond,
and records the accepted declaration on the host. Composing a team never creates or
starts cells: launch them explicitly, for example with
`kuke run --from-config <team>-<role>-<harness>`.

Two commands drive it:

- [`kuke team init`](../cli/kuke-team-init.md) creates the global configuration
  and applies the `kuke` team, whose one role is `majordomo`. `kuke` is the
  operator's reserved system team; `add` refuses that name.
- [`kuke team add`](../cli/kuke-team-add.md) composes a project team from a
  directory's `kuketeam.yaml` or from a named Git source.

## Where the files live

| File                             | Where        | Holds                                                                                                               |
| -------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------- |
| `~/.kuke/kuketeams.yaml`         | host         | `TeamsConfig`, optional and hand-written: global defaults (sources, roles, harnesses), parameters, secret bindings. |
| `~/.kuke/kuketeam.d/<team>.yaml` | host         | `TeamEntry`: the accepted declaration of one registered team. Written only after a fully successful apply.          |
| `kuketeam.yaml`                  | project repo | `Team`: the project's selections, read by `kuke team add DIRECTORY`.                                                |
| `roles/<role>/`                  | a source     | Role definition (`role.yaml`) and the instruction/skill assets materialized into the harness home.                  |
| `harnesses/<harness>/`           | a source     | Harness definition (`harness.yaml`), image catalog (`images.yaml`), and image build context.                        |

All documents use `apiVersion: kuketeams.io/v1` and are decoded strictly
(single document, no unknown fields). This repository ships the `majordomo`
role under `team/roles/` and the `claude`, `codex`, `opencode`, and `script`
harnesses under `team/harnesses/` (`script` runs a program role, see
[Program roles](#program-roles-the-script-harness)); other roles (dev, pm, pr-reviewer, …) can come from any Git
source, such as the agents repository. The kukeon renderer that turns a pair
into kukeon documents is embedded in `kuke` (`pkg/team/compose/renderers/kukeon/v1beta1`); see
[`team/harnesses/templates.md`](https://github.com/eminwux/kukeon/blob/main/team/harnesses/templates.md)
for the harness and renderer contract.

## Selections and persistence

`TeamsConfig.spec.defaults` and `Team.spec` share `sources`, `roles`, and
`harnesses`. An omitted list inherits the corresponding global default. An
explicit list replaces it; `[]` selects none. References are checked against
the effective lists. Each source has exactly one of `branch`, `tag`, or
`commit`. A role with an explicit `harness` selects only that harness, and
fails if the role does not declare it; otherwise it selects every harness in the
effective list that the role declares in `spec.harnesses`, and fails only if it
declares none of them. A team can therefore list `[claude, script]` and keep
agent roles that declare only `claude`. Harness `default` is
not supported; use `roles[].harness`. Unselected definitions are neither
fetched, loaded, nor rendered.

```yaml
apiVersion: kuketeams.io/v1
kind: Team
metadata:
  name: example
# All selections omitted: inherit the operator's defaults.
```

A `TeamEntry` has the same `metadata` and `spec` as the accepted `Team`, plus
the `projectSource` that `kuke team add` resolves. Effective defaults are not
persisted, and omitted versus explicit-empty lists round-trip unchanged, so a
later recomposition picks up the current global defaults.

Authored `roles[].needs.image` contains **capability names**, never image
URLs. These additions cannot erase requirements in `Role.spec.needs.image`. The
selected harness is also a mandatory image capability. One catalog image must
satisfy the complete set; catalog order breaks ties, and a root-capable image
is not selected unless `root` is requested. The team name `root` grants no
privilege. Published image references are read from the `images.yaml` beside
the selected `harness.yaml`; no registry lookup is made. An update re-resolves
the image from the current definitions and catalog.

## Reusable defaults after init

`~/.kuke/kuketeams.yaml` is optional and hand-written: kuke reads it and never
writes it, and a missing file means empty global defaults. `kuke team init` and
`kuke team add` both work without it. Create it to declare reusable sources,
roles, and harnesses. Both harnesses are listed here; dev narrows to Claude and pm to
Codex:

```yaml
apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults:
    sources:
      - name: kukeon
        url: github.com/eminwux/kukeon
        branch: main
      - name: agents
        url: github.com/eminwux/agents
        branch: main
    roles:
      - name: dev
        source: agents
        path: dev
        initialPath: /home/user/work
        harness: claude
      - name: pm
        source: agents
        path: pm
        initialPath: $HOME
        harness: codex
    harnesses:
      - name: codex
        source: kukeon
        path: team/harnesses/codex
      - name: claude
        source: kukeon
        path: team/harnesses/claude
```

Check external role paths, runtime `clonePath` requirements, image
capabilities, and secret prerequisites against the selected source revision
before use.

## Starting directory

`initialPath` is accepted on role selections in global defaults, Team, and
TeamEntry. An explicit roles list replaces the entire global list, so omitting
`initialPath` on a replacement role uses the home fallback rather than
borrowing a path from the same-named global role.

Supported values are clean absolute Unix paths (including `/`), `$HOME`, and
`$HOME/subdirectory`, with path components of letters, digits, `_`, `.`, and
`-`. Relative paths, `~`, `${HOME}`, other variables, shell expressions,
whitespace, empty strings, and `.`/`..` components are rejected. Omit the field
to start in the in-cell harness user's `$HOME`. `$HOME` resolves to the
renderer profile's HOME (`/home/user`), never the host HOME. Directories must
already exist; a failed `cd` stops startup before the harness runs.

`initialPath` controls startup even when a role declares the `project`
repository slot. To start in the project, set `initialPath` to the project's
absolute in-cell clone destination. Role `path` still locates definition
assets: materialization runs against the role source before launch and
installs instructions and skills in the harness's native home directories.

## Shared blueprints

A blueprint rendered by `kuke team` is a form with no role facts in it, so it
is shared: **one blueprint per harness × security profile**, realm-wide, used by
every role and every team in the realm — and by the web UI's _New config_ form.
See [Blueprint contract](blueprint-contract.md).

| Blueprint             | For roles that need                                                            |
| --------------------- | ------------------------------------------------------------------------------ |
| `<harness>`           | the default posture (`claude`, `codex`, `opencode`)                            |
| `<harness>-root`      | the `root` image capability: a privileged cell with a nested container runtime |
| `<harness>-host`      | the `kukeond-socket` and `kuke-binary` host mounts                             |
| `<harness>-root-host` | both                                                                           |

A role that selects any other set of host mounts — only one of the two, or
`kukeon-install` as well — gets a profile named after them, such as
`<harness>-kuke-binary-kukeon-install-kukeond-socket`. The security posture is fixed in the blueprint and is never a Config
toggle.

The daemon socket and the `kuke` binary are mounted through directories, so a
daemon restart or a rebuild reaches running cells without recreating them:

| Selector         | Mounted                                                                                                                                                                              |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `kukeond-socket` | `/run/kukeon/kukeond/`, where kukeond hard-links its socket on every start; the cell's `KUKEON_HOST` points at `/run/kukeon/kukeond/kukeond.sock`                                    |
| `kuke-binary`    | `/opt/kukeon/bin/client/` at `/.kukeon/client/`, where `kuke init` stages its own binary, and a fixed shim at `/usr/local/bin/kuke` that runs it; re-run `kuke init` after a rebuild |
| `kukeon-install` | `/opt/kukeon`, read-only                                                                                                                                                             |

Each blueprint is assembled from components: the **harness** (its parameters,
env, secret slots and start modes, copied from `harness.yaml`), and the optional
**connector** and **forge** components of the library kuke ships under
`team/components/` (`github`, `github-issues`). Everything that differs between
two roles lives in the pair's CellConfig:

| Role fact                                                                             | In the CellConfig                                                        |
| ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Image picked for the role's `needs.image`                                             | `values.IMAGE`, plus `requires` for the capabilities                     |
| Role source checkout, role path, start dir                                            | `values.ROLE_SOURCE_DIR`, `ROLE_PATH`, `INITIAL_PATH`                    |
| Harness settings (claude settings file, codex sandbox/approval, opencode permissions) | `values.HARNESS_SETTINGS`, `SANDBOX`, `APPROVAL`, `PERMISSIONS`          |
| Repositories                                                                          | `repos.role-source`, `repos.project`, one slot per further cloned source |
| Secrets                                                                               | `secrets.<slot>` → a stored Secret                                       |
| Connectors and forges                                                                 | `components`                                                             |
| Harness state volume                                                                  | `values.STATE_VOLUME`                                                    |

Blueprints carry `kukeon.io/shared: "true"` and no team label. One realm has
one copy of each shared blueprint: when a team renders different content under
a shared name, its apply replaces the blueprint in place, whichever teams'
Configs reference it, and says so in a note (old → new content digest, and the
other Configs using it). Their cells turn `OutOfSync` and pick up the new
content on `kuke restart`. The last apply wins, so keep teams on the same
harness revision by pinning it in each team's sources. Only a blueprint kuke
team did not render — without the shared label — is never overwritten; this
team's is then applied as `<name>-<content digest>`. A team's apply removes a
shared blueprint it stopped using only when no other CellConfig references it.

### Modes

A harness declares the ways it starts under `spec.modes` in `harness.yaml` —
the shipped ones declare `interactive` and `headless`. The mode is picked per
cell, so one role Config serves both:

```bash
kuke run acme-dev-claude                                               # interactive
kuke run acme-dev-claude --mode headless --param PROMPT="triage #42" -d
```

### Tasks

A role can declare named **tasks** in `role.yaml`: unattended runs that need no
runtime arguments. Each task names one of the role's skills by base name, and
can add a fixed argument string:

```yaml
# role.yaml
spec:
  skills: [dev/skills/pick-issue, dev/skills/pick-changes]
  tasks:
    - { name: pick-issue, skill: pick-issue }
    - { name: pick-bug, skill: pick-issue, args: "--label bug" }
    - { name: pick-changes, skill: pick-changes }
```

A team's role entry picks which of them it uses. If `tasks` is omitted, the team
gets all of the role's tasks. `tasks: []` gives none, and a name the role does
not declare fails `kuke team add`. A task whose `skill` is not one of the role's
`spec.skills` also fails, with an error that names the role, the task and the
skill.

```yaml
# kuketeam.yaml
spec:
  roles:
    - { name: dev, source: agents, path: dev, tasks: [pick-issue, pick-bug] }
```

`kuke team add` renders each selected task as its own CellConfig per selected
harness (the role entry's `harness` pin applies), named
`<team>-<role>-<harness>-<task>` and labelled `kukeon.io/task: <task>` next to
the team and role labels. It has the same blueprint, values, repos, secrets, components
and requires as the pair's config, plus:

```yaml
spec:
  modes: { enabled: [headless], default: headless }
  values:
    { PROMPT: "Use the pick-issue skill with arguments: --label bug", ... }
  autoDelete: true
```

The prompt is the same for every harness: `Use the <skill> skill`, followed by
` with arguments: <args>` when `args` is set. Launch a task with no `--rm` and
no `--param`. The cell deletes itself when the run ends:

```bash
kuke run -d acme-dev-claude-pick-bug
```

A harness without a `headless` mode gets no task configs, and the output says
why. The pair's interactive config does not change. Tasks that need a value at
run time (`issue <N>`) still go through it:
`kuke run --rm -d acme-dev-claude --mode headless --param PROMPT="work on issue 22"`.
When a task is deselected, or the role stops declaring it, the next
`kuke team add` prunes its config along with the team's other obsolete configs.

#### Scheduling a task

A task config can run on a schedule instead of by hand. How often a task runs
is a deployment decision, so the team sets it. An entry in a role's `tasks` is
either a task name or an object that also gives the task's cadence:

```yaml
# kuketeam.yaml
spec:
  roles:
    - name: dev
      source: agents
      path: dev
      tasks:
        - pick-issue # runs only when started by hand
        - { name: pick-bug, schedule: "*/15 * * * *" }
        - { name: pick-nits, schedule: "@daily", suspend: true }
```

`schedule` is a five-field cron expression or one of `@hourly`, `@daily`,
`@weekly`, `@monthly`, read in UTC. `suspend: true` keeps the schedule but
switches it off.

A role can ship defaults for both, so a team that just names the task gets
them:

```yaml
# role.yaml
spec:
  tasks:
    - {
        name: pick-bug,
        skill: pick-issue,
        args: "--label bug",
        schedule: "*/30 * * * *",
      }
    - {
        name: health-sweep,
        skill: health-sweep,
        schedule: "@hourly",
        suspend: true,
      }
```

Each value a team's entry sets overrides the role's default for that value, and
the other one is kept. The `pick-bug` entry above replaces the role's
`*/30 * * * *` with `*/15 * * * *`. A role can ship a schedule that is off
until a team opts in, as `health-sweep` does: the team turns it on with
`{ name: health-sweep, suspend: false }`. A `suspend` on a task that ends up
with no schedule fails `kuke team add`, because there is nothing for it to
switch.

`kuke team add` renders the resolved values into the task config's
[`spec.schedule`](../manifests/config.md#specschedule-object-optional), and
kukeond then fires the config on that schedule: each fire starts a
`<config>-<unix-fire-time>` cell, and a fire is skipped while the previous one
is still running. A task with no schedule renders as before. See what is
scheduled, and when it next fires, with `kuke get configs`.

Team configs are re-rendered: `kuke team add` rewrites every one of them whole.
A `schedule` or `suspend` edited by hand on a task config is therefore lost on
the next `kuke team add`. Change it in `kuketeam.yaml` (or the role's defaults)
instead.

A team's entry can also replace a task's argument text with `args`, for example
`{ name: pick-bug, args: "--label regression" }`. Like `schedule` and
`suspend`, it overrides only that value.

### Program roles: the script harness

A role that is a plain program, such as a reactor or a poller, runs on the
`script` harness. Its cell runs a command the role declares, with no model and
no login, and gets the same connectors, repos, mounts and secrets as any other
role:

```yaml
# role.yaml
apiVersion: kuketeams.io/v1
kind: Role
metadata: { name: ticker }
spec:
  harnesses:
    script:
      command: python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/tick.py"
  connectors: [github]
  needs:
    mounts: [kuke-binary, kukeond-socket]
  tasks:
    - { name: tick, args: "--once", schedule: "*/5 * * * *" }
```

- **`harnesses.script.command`** is required, and only the `script` harness
  accepts it. The cell runs it as `/bin/bash -c "$COMMAND $ARGS"` from the
  role's starting directory, and the cell exits with its status. Reference
  environment variables as `$VAR`, never `${VAR}`: kukeon substitutes the braced
  form in blueprints, so `kuke team add` rejects it. The command is composed
  from the role; `spec.parameters` cannot override `COMMAND`.
- **Tasks** take `args` and name no `skill`: a task config runs the command
  with the task's `args` appended (`values.ARGS`) instead of a `PROMPT`.
  `schedule`, `suspend` and the team's overrides work as for any task. A
  script task that sets `skill` fails `kuke team add`.
- **State.** `$SCRIPT_STATE_DIR` (`/home/user/.script-state`) is on the pair's
  state volume, `<team>-<role>-script-state`, which the pair config and its
  task configs share, so the program keeps a small state directory across
  runs.
- **Assets.** A program role's directory may hold only `role.yaml`: the script
  harness needs no `AGENTS.md` and loads no skills.
- **Image.** The harness runs on the published
  `ghcr.io/eminwux/kukeon/base-user` image (bash, git, gh, jq, yq, python3);
  no harness image is built for it. `base-user` carries no
  `io.kukeon.capabilities` label yet, so kukeond logs
  `image declares no capabilities; required capabilities not verified` when it
  creates a script cell, and creates it.
- **Modes.** There is only a `headless` mode; a script cell is not an
  attachable session.

kukeon's own `kuketeam.yaml` runs the agents repository's `pipeline` role this
way: pinned to `script`, with `tasks: [{ name: tick, suspend: false }]`
switching on the five-minute tick the role ships suspended. To stop the
reactor:

- **Durably:** set `tasks: [{ name: tick, suspend: true }]` on the role entry
  in `kuketeam.yaml` (or remove the role entry) and run `kuke team add`.
- **Immediately:** `kuke delete config kukeon-pipeline-script-tick`. No new
  tick fires, and worker cells that are already running are left alone. The
  next `kuke team add` renders the config again.

### The majordomo health sweep

The `kuke` team's `majordomo` role declares one task, `health-sweep`, with
`schedule: "@hourly"` and `suspend: true`. `kuke team init` therefore renders
`kuke-majordomo-<harness>-health-sweep` **suspended**: no install spends
model credits on sweeps until its user turns them on.

A sweep is read-only. It reads kukeond's health digest
([`kuke health`](../cli/kuke-health.md)), stops if the digest is clean, and
otherwise triages each finding and stores one report in kukeond. It never
restarts, deletes or changes anything; fixes appear in the report as suggested
commands.

To turn sweeps on, edit the kuke team's stored entry and reapply it. The
entry is the team's source: `kuke team init` writes it and
`kuke team init --reapply` recomposes the team from it.

```yaml
# ~/.kuke/kuketeam.d/kuke.yaml
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      initialPath: $HOME
      needs:
        image: [go]
      tasks:
        - { name: health-sweep, suspend: false }
```

```bash
kuke team init --reapply
kuke get config kuke-majordomo-claude-health-sweep -o yaml   # spec.schedule, status.schedule
```

Change the cadence the same way, with `schedule: "*/30 * * * *"` in the task
entry. Run one sweep by hand at any time, suspended or not:

```bash
kuke run -d kuke-majordomo-claude-health-sweep
kuke health reports
kuke health report latest
```

The sweep cell deletes itself when it ends. It uses the same state volume as
the interactive `kuke-majordomo-<harness>` config, so it runs with the
harness login you made there.

### Filing kukeon defects as issues

By default majordomo has no GitHub access: a sweep only stores its report, and
the interactive cell cannot search or file issues. GitHub access is opt-in per
install, and a default `kuke team init` needs no GitHub token. One secret turns
it on for both the interactive cell and the sweep:

1. Create the token as a secret named `gh-token`:

   ```bash
   kuke create secret gh-token --realm default --from-file /path/to/token
   ```

2. In the kuke entry, give the role that secret, and tell the sweep where to
   file if it should:

   ```yaml
   # ~/.kuke/kuketeam.d/kuke.yaml
   spec:
     roles:
       - name: majordomo
         # ...
         needs:
           image: [go]
           secrets: [gh-token] # enables the github connector: GH_TOKEN + gh
         tasks:
           - name: health-sweep
             suspend: false
             args: "--file-issues <owner>/<repo>"
   ```

3. Run `kuke team init --reapply`.

`needs.secrets` in a team entry adds runtime secrets to a role for that team
only; the role itself still requires none. `gh-token` enables the
[`github` connector](#connectors), so the interactive `kuke-majordomo-<harness>`
cell and its `health-sweep` task cell both get the token as `GH_TOKEN` and an
image with `gh`. The token is never a git credential: clone and push keep using
the git identity.

With the token, the sweep submits its report with
`kuke health report submit --file-issues <owner>/<repo>`, and `kuke` does the
filing: one issue per new `kukeon` finding, a comment on the open issue of a
recurring one, and never anything for `workload` or `unclear` findings. See
[`kuke health report submit`](../cli/kuke-health.md#filing-issues-opt-in) for
the issue format and what is redacted.

In the interactive cell, ask majordomo to file a defect it diagnosed. Its
`file-kukeon-bug` skill looks for an existing issue first, sweep-filed ones
included, then comments on it or opens one, and reports the URL. It renders the
issue with [`kuke health issue`](../cli/kuke-health.md#rendering-an-issue-for-a-diagnosed-defect),
which uses the sweep's redaction and signature marker, so the two paths dedupe
against each other. Without the opt-in it says filing is unavailable and how to
turn it on.

Do step 1 before step 3, so the secret exists when the cells first start.

### Connectors

A [connector](connectors.md) is a kind of access a cell needs. A role enables
one by name:

```yaml
# role.yaml
spec:
  connectors: [github] # GH_TOKEN for the gh CLI, plus the `gh` image capability
  forges: [github-issues] # optional; implies the github connector
```

The `github` connector's token is used only for the GitHub API. It is never a
git credential: clone, push and signing use the git identity below. The older
spelling — `harnesses.<h>.secrets: [gh-token]` with `needs.image: [gh]` — still
composes and enables the same connector, filled from the same `gh-token`
secret.

Two more connectors are enabled without the role asking: the one that
authenticates the pair's harness (`claude` or `codex` — exactly one of its
methods per Config, the login by default) and `git`, for a role with
`needs.repos`.

A team decides **how** each connector is delivered and **which identity** fills
it under `connectors`, in its `kind: Team` file or TeamEntry; a role entry
overrides the team for that role and can give the role a connector it does not
ask for itself:

```yaml
# kuketeam.yaml (kind: Team) or ~/.kuke/kuketeam.d/<team>.yaml
spec:
  connectors:
    claude: { method: subscription, identity: claude-team }
    github: { identity: work-github }
    git: { identity: bot-key }
  roles:
    - name: reviewer
      # ...
      connectors:
        github: { identity: review-github }
```

`identity` names a stored Secret, `name[@realm[/space[/stack]]]`. A connector
left out — or an entry with no identity — uses the default method and inherits
its identity from the Stack, Space and Realm defaults, so replacing a default
reaches every team that did not choose, with no re-render. Only identities a
team chose are written as fills on its Configs. See
[Connectors → Teams](connectors.md#teams) for the full rules.

## Git identity and credentials

Team cells use [stored git credentials](../guides/git-credentials.md); the host
`~/.ssh` is never mounted. A team cell's git identity is the git connector
identity a Realm, Space or Stack default names (`spec.defaults.git.identityRef`),
or the one the team names under [`connectors.git`](#connectors). `kuke team`
reads no git identity from the host and writes no git defaults. Store yours with
[`kuke connector import git --default`](../cli/kuke-connector.md#kuke-connector-import-git).

`TeamsConfig` has no `spec.git`: a `kuketeams.yaml` that still has one fails to
decode and names `kuke connector import git --default`. A source may state its
own identity with `sources[].git` (author, committer, credential, signing); it
is rendered into that repo's fill of each CellConfig. The remote transport
follows the credential, so a project added by its HTTPS URL is still cloned and
pushed over SSH when its host is bound to an SSH key.

When a team clones a source over SSH that states no credential of its own and
nothing gives its cells a git identity, `kuke team init` and `kuke team add`
print `no git identity is set; run kuke connector import git --default`. A
role's `needs.mounts: [ssh]` is still accepted but mounts nothing.

## Runtime repository and parameter bindings

A source is the single declaration of its URL and branch/tag/commit. Optional
`sources[].clonePath` requests a runtime clone at an absolute in-cell
destination; omitting it requests no runtime clone. The initial `kuke`
TeamEntry clones its role source to `/home/user/kukeon-src`.

The selected role's source and every `Role.spec.needs.repos` slot must resolve
to an effective source with `clonePath`. Other effective sources with
`clonePath` also request clones. Clone destinations must be unique. The
`github.com/owner/repo` URL shorthand becomes HTTPS in runtime config fills;
authored declarations keep their spelling.

The shared blueprint declares the repository slots `role-source` (the selected
role's source) and `project`, plus one slot per further cloned source;
destinations, URLs and revisions are bound through `CellConfig` values and
`spec.repos`. Branches map to `branch`; tags and
commits map to `ref`. The harness launchers preserve this checkout: they do not
switch branches or pull newer revisions during launch.

`TeamsConfig.spec.parameters` is a string map of declared scalar parameters.
Each composition binds only keys its blueprint declares. Required parameters
without defaults must be supplied, and unknown keys fail. The parameters
composition derives from the role and source selections (`IMAGE`, `ROLE_PATH`,
`STATE_VOLUME`, …) cannot be set here. `AGENT_NAME` defaults
to the full resource identity. Parameters must not hold credentials: use secret
bindings instead.

A role declares the further parameters it reads in `Role.spec.needs.params`.
Each entry is a bare name, which the operator must supply in
`TeamsConfig.spec.parameters`, or a `{ name, default }` mapping that carries
the role's own value:

```yaml
needs:
  params:
    - REGION
    - { name: AGENTS_REPO, default: "${ROLE_SOURCE_REPO}" }
```

A default may reference any composed fact as `${FACT}`. Besides the values
composition derives (`ROLE_PATH`, `ROLE_SOURCE_DIR`, …) there is
`ROLE_SOURCE_REPO`: the `<owner>/<repo>` of the role source's URL, reduced from
scp-like (`git@host:owner/repo`), `ssh://` and `https://` URLs with or without
a trailing `.git`. A fork's roles therefore point at the fork without an edit.
`ROLE_SOURCE_REPO` is a fact only: it is not a blueprint parameter and
`spec.parameters` cannot set it.

The resolved value is written to the `CellConfig` and exported into the cell
environment under the parameter's name. Precedence, highest first:
`TeamsConfig.spec.parameters.<NAME>`, then the role's default, then the
blueprint's empty default. A role cannot set a default for a parameter the
blueprint or composition already declares (`PROMPT`, `AGENT_NAME`, `IMAGE`, …).
When a URL does not reduce to `<owner>/<repo>` (a nested group path, a local
path), `ROLE_SOURCE_REPO` is empty, and `kuke team init` / `kuke team add` print
a note naming the role and each parameter whose default resolved empty.

Harness-owned `onInit` scripts (`spec.onInit`, or each mode's) call the image-installed
`/opt/kuketeams/harness/Makefile.start setup`, which runs
`make -C "$ROLE_SOURCE_DIR" <harness> ROLE="$ROLE_PATH"` against the role's
checkout. A repository that serves roles therefore exposes one Makefile target
per harness — this repository's `make claude`, `make codex`, and
`make opencode-openrouter` (all backed by `team/scripts/materialize-role.sh`).
Startup then uses `INITIAL_PATH`; the harness source need not be checked out in
the cell.

The base majordomo clones over HTTPS and needs no git credential.

## Secret declarations

Only selected role/harness requirements contribute bindings. The shipped
majordomo has no `gh` or `gh-token` requirement. Claude and Codex authenticate
through their [connector](connectors.md), by default with the login kept in the
state volume, and declare no mandatory token. OpenCode's selected provider
requires `openrouter-api-key`.

Each required secret is bound by name, in this order of precedence:

1. the identity the team chose for the secret's connector (`connectors`, above);
2. the binding recorded in the team's TeamEntry (`~/.kuke/kuketeam.d/<team>.yaml`
   `secrets:`), including any operator edit to it;
3. a global `TeamsConfig.spec.secrets` binding;
4. the default: the slot is left to the Realm → Space → Stack defaults.

A binding from 1–3 is written as a fill on the Config. The default is not: the
Config carries no fill, and `kuke team` writes a Realm `spec.defaults.secrets`
entry for the slot — pointing at the runtime secret of the same name in the team
realm (`gh-token`, `openrouter-api-key`) — only when no Realm, Space or Stack
default exists for it yet. It never overwrites one, so the default the operator
sets later is the one every such Config uses.

A shared blueprint fixes where a secret lands: a harness slot
(`openrouter-api-key` → `OPENROUTER_API_KEY`) and a connector slot
(`github-token` → `GH_TOKEN`, `claude-code-oauth-token` →
`CLAUDE_CODE_OAUTH_TOKEN`) keep their declared destination, and a binding whose
`envName` differs is rejected.
A role secret no harness or component declares becomes an optional slot on the
shared blueprint, with the binding's `envName`.

Composition records every binding the selected pairs use in the TeamEntry, so a
repeated add or a recomposition from the entry alone keeps them. Bindings for
requirements no selected pair uses are dropped. Because the recorded binding
wins, change a binding after its first add by editing the TeamEntry (or delete
it there to fall back to the global binding or default). A requirement whose
name cannot yield an environment identifier (e.g. it starts with a digit) must
be bound explicitly. The project's `kind: Team` file never declares bindings.

An explicit global binding looks like this:

```yaml
# Optional, under TeamsConfig.spec (or the same shape under TeamEntry secrets).
secrets:
  - name: openrouter-api-key # identifier in Role.spec.harnesses.opencode.secrets
    secretRef:
      name: provider-account
      realm: auth
      space: shared
      stack: operators
    envName: OPENROUTER_API_KEY
    # Optional fallback if application finds no runtime secret:
    source:
      env: OPERATOR_OPENROUTER_KEY
      # Alternatively: file: /absolute/path/to/private/provider-key
```

`name`, the scoped `secretRef`, and `envName` are required. `source` is
optional; when present it names exactly one environment **variable** or
absolute file **path** — there is no inline value field. Composition emits
secret **references** in configs and consumption slots in blueprints; it never
reads values or emits `Secret` documents. Application checks runtime existence,
reuses existing secrets, and reads a creation source only when a secret is
missing.

## Resource names and state volumes

Pairs follow role order, then harness order. Each pair's config
is named `<team>-<role>-<harness>`, with dots and punctuation runs collapsed
to single hyphens: team `esp32`, role `pr-reviewer`, and harness `codex`
produce `esp32-pr-reviewer-codex`. A task config appends the task name:
`esp32-pr-reviewer-codex-pick-review-pr`. Different selections can collide on these
readable names (`root-a`/`majordomo` and `root`/`a-majordomo`); choose distinct
names in the same scope. Within one team, composition fails when two role ×
harness pairs produce the same config name (role `a-b` and role `a.b`), and
the error names both pairs. `kukeon.io/team` labels keep the authored team name,
and `kukeon.io/role` labels the role name from the team's `roles` list, on pair
and task configs alike.
Resources default to realm/space/stack `default`.

A harness keeps its state (logins, sessions) on a Volume named after the
pair's config plus `-state`: `<team>-<role>-<harness>-state`, for example
`kuke-majordomo-claude-state` or `esp32-pr-reviewer-codex-state`. The Volume
is mounted only at the harness's `statePath` (`$HOME/.claude` for claude,
`$HOME/.codex` for codex). It is not the cell's home or root filesystem, and a
harness without a `statePath` gets no state volume. The pair config, its task
configs and the sweep cell all mount the same one, so one harness login covers
every cell of that role × harness.

The Config passes the name to the shared blueprint as `STATE_VOLUME`, and the
Volume's scope as `STATE_VOLUME_REALM`, `STATE_VOLUME_SPACE` and
`STATE_VOLUME_STACK` (the Config's own realm, space and stack). The blueprint
mounts it through a fully-qualified `volumeRef` with `ensure: true`, so the
first cell creates the Volume at those coordinates and the web UI links it.

Installs composed before this name existed used
`<team>-<role>-<harness>[-<project>-<role>-<harness>]-<digest>`. Composition
does not migrate those Volumes: after re-rendering, each role × harness starts
with a new, empty state volume, so log in to each harness again. Delete the
old Volumes with `kuke delete volume` once they are no longer needed.

## What preflight covers

Preflight covers strict document shape, selection/reference resolution, role
assets, template and partial execution, image capabilities, parameter
completeness, repository bindings, and structural secret requirements. It does
not prove published image availability, runtime scope or secret existence,
source credentials, daemon acceptance, or live authentication; those surface
when the team is applied or a cell is launched.
