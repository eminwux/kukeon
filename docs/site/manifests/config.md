# CellConfig manifest

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod # the binding's name; stamped cells carry it as the kukeon.io/config lineage label
  realm: kuke-system # scope coordinates; deepest non-empty wins
  # space: team-a           # optional — space-scoped
  # stack: agents           # optional — stack-scoped (requires space)
spec:
  blueprint:
    name: claude-code # cross-scope reference; deeper coordinates optional
    realm: kuke-system
  values: # scalar ${KEY} fills for the blueprint's parameters
    MODEL: claude-opus-4-7
  repos: # structural repo slot fills, keyed by slot name
    workspace:
      url: https://github.com/example/repo
  secrets: # structural secret slot fills, keyed by slot name
    anthropic-token:
      secretRef:
        name: anthropic-token
        realm: kuke-system
```

A `CellConfig` is a reusable **binding**: a [`CellBlueprint`](blueprint.md) reference plus the concrete scalar `values` filled into the blueprint's parameters and the structural repo/secret slot fills keyed by the blueprint's slot names. Where a Blueprint answers _"what does this cell look like?"_, a Config answers _"with which values and slot sources?"_. A Config is a **1:N** binding — each `kuke run --from-config <cfg>` (or `kuke create cell --from-config <cfg>`) stamps a **fresh** cell, not a singleton. The cell's identity is its own [`CellDoc`](../concepts/cell.md): it owns its name, spec, and status. The Config name is demoted to the `kukeon.io/config` lineage label every stamped cell carries (epic:cell-identity).

See [`kuke run --from-config`](../cli/kuke-run.md) for the fused create + start + attach verb, and [`kuke create cell --from-config`](../cli/kuke-create.md) for the un-fused create-only primitive. Stamping always produces a fresh cell — a `--name X` collision against a live cell is refused, never attached. To pull a stamped cell back in line with an edited Config, re-resolve it explicitly with [`kuke restart <cell>`](../cli/kuke-restart.md) (or `kuke restart -l <selector>` to roll a fleet); drift surfaces as an informational `OutOfSync` until you do.

## metadata

A Config is scopable at realm, space, or stack — never cell. A Config materializes a cell; scoping it to a single cell is nonsensical. The scope is the deepest non-empty coordinate, and a deeper coordinate may only be set when every shallower one is.

| Field         | Type              | Required | Description                                                                                                                                                                             |
| ------------- | ----------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | string            | yes      | The config's name, unique within its scope. Stamped cells carry it as the `kukeon.io/config` lineage label, **not** as their name — see [Lineage, not identity](#lineage-not-identity). |
| `realm`       | string            | yes      | The always-required top-level scope coordinate.                                                                                                                                         |
| `space`       | string            | no       | When set, scopes the config to a space within `realm`.                                                                                                                                  |
| `stack`       | string            | no       | When set, scopes the config to a stack within `space` (requires space).                                                                                                                 |
| `labels`      | map[string]string | no       | Copied onto every stamped cell, in addition to the `kukeon.io/config` lineage label.                                                                                                    |
| `annotations` | map[string]string | no       | Non-identifying metadata. Never copied onto cells, never compared for OutOfSync. Listed with the Config (`kuke get configs`, `/api/catalog/configs`).                                   |

Well-known keys of a Config's metadata (issue #1692):

| Key                        | Kind       | Set by                         | Meaning                                                                                                                                                                              |
| -------------------------- | ---------- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `kukeon.io/template`       | label      | composition of a template team | `"true"` on an agent template, a Config the system `default` team composes; New agent copies it. A template is not run itself.                                                       |
| `kukeon.io/agent-template` | annotation | New agent                      | The name of the agent template a Config was copied from.                                                                                                                             |
| `kukeon.io/agent-team`     | annotation | New agent                      | The team of that template. A copy has no `kukeon.io/team` label, so no team prunes it; this names the role it runs (`<team>/<role>`), so it may share the template's login identity. |
| `kukeon.io/display-name`   | annotation | New agent, rename              | The name a person gave the agent, apart from the generated Config name. Left out of the config revision, so a rename never marks a cell OutOfSync.                                   |

A cell may carry `kukeon.io/display-name` too, set by renaming that instance; like every cell annotation it is never compared.

## spec

### `spec.blueprint` (object, required)

Cross-scope reference to the [`CellBlueprint`](blueprint.md) this Config instantiates.

| Field   | Type   | Required | Description                                                 |
| ------- | ------ | -------- | ----------------------------------------------------------- |
| `name`  | string | yes      | The referenced blueprint's name within its scope.           |
| `realm` | string | yes      | The blueprint's always-required top-level scope coordinate. |
| `space` | string | no       | When set, scopes the reference to a space within `realm`.   |
| `stack` | string | no       | When set, scopes the reference to a stack within `space`.   |

The reference may cross scopes: a Config in one realm may instantiate a Blueprint owned by another (for example, a `default`-realm Config referencing a `kuke-system`-scoped template), the same cross-scope freedom a `secretRef` has.

### `spec.values` (map[string]string, optional)

Scalar fills for `${KEY}` references in the blueprint's cell body. Stored verbatim; resolution happens when the Config materializes a cell. Keys need not be declared in the blueprint's `spec.parameters[]`: undeclared keys are accepted and substituted wherever the body references them. For example, `values: {ROLE: dev}` resolves `${ROLE}` to `dev` even without a `ROLE` parameter declaration. Unreferenced keys are tolerated, and declared required parameters must still resolve to a value.

Interactive `kuke run --from-blueprint --param KEY=VALUE` remains strict: an undeclared `--param` key errors at call time. See the Blueprint's [`spec.parameters[]`](blueprint.md#specparameters-list-optional) reference.

### `spec.repos` (map[string][RepoFill](#cellconfigrepofill), optional)

Structural repo slot fills, keyed by the blueprint's repo slot `name`. Each entry supplies the clone URL the blueprint deliberately left open.

#### CellConfigRepoFill

| Field    | Type   | Required | Description                                                                                                                                                                                                                                      |
| -------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `url`    | string | yes      | The clone URL filling the slot.                                                                                                                                                                                                                  |
| `branch` | string | no       | The branch to check out (moving target). Empty clones the remote's default. Mutually exclusive with `ref`.                                                                                                                                       |
| `ref`    | string | no       | Immutable pin — tag name or full commit SHA. Survives in-place restarts (see [ContainerRepo `ref`](container.md#containerrepo)). Mutually exclusive with `branch`.                                                                               |
| `git`    | object | no       | This source's own git identity — `author`, `committer`, `credential`, `signing`, `sign`, or an `identityRef` naming a stored git identity that supplies them. Unset fields use the cell's default identity. See [RepoGit](container.md#repogit). |

### `spec.secrets` (map[string][SecretFill](#cellconfigsecretfill), optional)

Structural secret slot fills, keyed by the blueprint's secret slot `name`. The blueprint owns the consumption side (env var or file mount) declared on the slot; this map supplies the source side — which `kind: Secret` provides the bytes.

#### CellConfigSecretFill

| Field       | Type   | Required | Description                                                                                                                                                                                                    |
| ----------- | ------ | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `secretRef` | object | yes      | Points at the `kind: Secret` that provides the slot's bytes. Same shape as [Container `secretRef`](container.md) — `name` + `realm` (required), `space` / `stack` / `cell` (optional, deepest-non-empty wins). |

### `spec.requires` (array of string, optional)

Image capabilities this Config's cells need on top of the blueprint's and its enabled components' — for example `[go]`. See [Image capabilities](../concepts/blueprint-contract.md#image-capabilities).

### `spec.components` (array of string, optional)

The blueprint's **optional** components to enable, by name. A component that is not optional is always on and need not be listed; naming a component the blueprint does not declare is an apply-time error. Enabling a component adds its parameters, env, secret slots and required capabilities to the cell. Components that share a `group` are alternatives: enabling two of one group — `claude-subscription` and `claude-api-key`, say — is an apply-time error.

A `connector` component makes its [connector](../concepts/connectors.md) a requirement of the Config's cells. Which identity fills it is resolved at render time: a per-cell `--connector`, else this Config's `spec.secrets` fill (or `spec.git.identityRef`), else the Stack, Space and Realm defaults.

### `spec.modes` (object, optional)

Which of the blueprint's harness modes this Config's cells may be created in. Unset allows every mode the blueprint declares.

| Field     | Type            | Required | Description                                                                                            |
| --------- | --------------- | -------- | ------------------------------------------------------------------------------------------------------ |
| `enabled` | array of string | no       | Modes a cell may be created in. Empty allows all.                                                      |
| `default` | string          | no       | Mode used when a create names none. Must be enabled. Unset: the only enabled mode, else `interactive`. |

The mode itself is picked **per cell**, at create time — `kuke run <config> --mode headless --param PROMPT=…` — so one Config serves both an attached session and a dispatched run. A Config may fill the secret slots of every mode it enables; only the picked mode's required slots and parameters are checked for a given cell.

### `spec.git` (object, optional)

Overrides the git identity, credentials and signing config the cell inherits from its Realm → Space → Stack defaults. Same shape as a container's [`git`](container.md#containergit): `author`, `committer` and `signing` replace the inherited value; `credentials[]` merge by `host`. See the [git credentials guide](../guides/git-credentials.md).

`identityRef` names a stored [git identity](../concepts/connectors.md#git) — an `ssh-key`, `basic-auth` or `opaque` (author-only) Secret tagged `git` — instead of spelling the block out. At render time the daemon expands it at the level it is written on into author, committer, a credential for each of the identity's hosts and, for an SSH key, signing with the same key; an [author-only](../concepts/connectors.md#author-only-identities) identity expands into author and committer only. The result then merges like any other level. Fields written next to the reference win over the identity's. `identityRef` is valid here, on a repo fill's `git`, and on a Realm, Space or Stack `spec.defaults.git`; a rendered cell never carries it. A reference to a Secret that is missing, is tagged for another connector, or lacks a required field (`hosts` on an `ssh-key` or `basic-auth` identity) fails the render. [`kuke connector import git`](../cli/kuke-connector.md#kuke-connector-import-git) stores your own identity.

```yaml
spec:
  git:
    identityRef: { name: bot-key, realm: default }
    author: { name: Release Bot, email: release@example.com } # overrides the identity's author
```

### `spec.autoDelete` (bool, optional)

When `true`, every cell materialized from this Config gets `Spec.AutoDelete`, so it is deleted once its run ends. This is the same deletion `kuke run --rm` asks for, without the flag. The value is OR'd with the blueprint's own `spec.cell.autoDelete`. It is set on the Config, not the blueprint, because a blueprint is shared across modes and roles. A re-render of an existing cell (OutOfSync, `kuke restart`) keeps it. `kuke create team` sets it on every [task config](../concepts/team.md#tasks).

### `spec.schedule` (object, optional)

Makes the Config recurring: kukeond creates and starts a fresh cell from it on a cron schedule, doing on each fire what `kuke run -d --from-config <config>` does. Without it the Config is an ordinary one. A Config has one schedule; two cadences are two Configs.

```yaml
spec:
  blueprint: { name: disk-watch, realm: default }
  schedule:
    cron: "*/10 * * * *"
    timezone: Europe/Madrid
    concurrencyPolicy: Forbid
    startingDeadlineSeconds: 300
    suspend: false
```

| Field                     | Type    | Required | Description                                                                                                                                          |
| ------------------------- | ------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cron`                    | string  | yes      | Five fields — `minute hour day-of-month month day-of-week` — or one of `@hourly`, `@daily`, `@weekly`, `@monthly`. No seconds field and no `@every`. |
| `timezone`                | string  | no       | The IANA time zone the expression is read in, for example `Europe/Madrid`. Default `UTC`.                                                            |
| `concurrencyPolicy`       | string  | no       | What a fire does while a cell from an earlier fire is still running. Only `Forbid` (skip the fire) is supported, and it is the default.              |
| `startingDeadlineSeconds` | integer | no       | How late a fire that was missed while kukeond was down may still start. Unset, missed fires are dropped. See [Missed fires](#missed-fires).          |
| `suspend`                 | bool    | no       | `true` stops new fires and leaves running cells alone. Setting it back to `false` resumes from the next scheduled time; nothing is caught up.        |

A `cron` field is a comma-separated list of `*`, a value, a range `a-b`, and any of those with a step `/n`. Months and days of week also take three-letter names (`jan`, `mon`); day of week runs `0`–`6` from Sunday, and `7` is also Sunday. As in Vixie cron, a day field that starts with `*` — a bare `*` or a stepped `*/n` — is unrestricted, and the two day fields then both have to match: `0 0 */2 * mon` fires on Mondays that fall on an odd day of the month. When neither day field starts with `*`, a day matches if either one does.

#### What a fire does

Each fire creates and starts one cell from the Config, in the Config's default mode and with no per-fire overrides: the Config is the whole description. The cell is:

- named `<config>-<unix-fire-time>`, for example `disk-watch-1794904200`;
- labelled `kukeon.io/schedule=<config>`, next to the usual `kukeon.io/config` lineage label, so `kuke get cells -l kukeon.io/schedule=<config>` lists a schedule's cells;
- always self-deleting, as if `spec.autoDelete` were set.

The fire-time name makes a fire idempotent: a fire retried after a daemon restart finds the cell it already made and counts as fired.

A Config's scheduled cells are the cells carrying its schedule label whose provenance points at that Config's own scope. Configs of the same name at realm, space and stack scope can fire into the same stack, and each counts only its own cells: another Config's cells never hold its fires back. When two such Configs fire at the same time they derive the same cell name; the one that comes second does not create a cell, records the collision in `status.schedule.lastError`, and fires again at its next fire time. Give same-named Configs that share a stack distinct names or fire times.

kukeond runs the scheduler on its own timer, once a minute on the minute, so a schedule has one-minute precision. The timer does not depend on the reconcile loop: schedules still fire when the operator disables it with `--reconcile-interval 0`. A new or edited schedule is picked up on the next pass.

The schedule is not part of the rendered cell. Adding, changing or removing `spec.schedule` never marks the Config's existing cells `OutOfSync`.

#### Concurrency

Under `Forbid`, a fire is skipped — and `status.schedule.skippedFires` goes up by one — while any of the Config's scheduled cells (see above) is `Pending`, `Ready`, `Degraded` or `Unknown`. Cells in `Stopped`, `Exited`, `Failed` or `Error` do not hold a fire back. A cell started by hand from the same Config (`kuke run --from-config`) has no schedule label, so it neither blocks a fire nor is blocked by one.

#### Missed fires

Fires that came due while kukeond was down are not replayed. When it comes back, at most one runs — the latest — and only if the schedule sets `startingDeadlineSeconds` and that fire is no older than the deadline. Every other missed fire is dropped and counted in `skippedFires`. Without `startingDeadlineSeconds`, all of them are dropped.

#### Failures and edits

- A fire that cannot create its cell — a missing blueprint or secret, a value that does not validate — records the reason in `status.schedule.lastError`. It is not retried before the next scheduled time, and later fires still run.
- A changed `cron` or `timezone` takes effect from the new expression's next fire time.
- Removing `spec.schedule`, or deleting the Config, stops future fires. Cells that are already running are left alone.

#### Daylight saving time

Fire times are wall-clock times in `timezone`. On the day clocks go forward, a time inside the skipped hour does not fire. On the day they go back, a time inside the repeated hour fires on both occurrences. An interval such as `*/10 * * * *` keeps its real-time spacing across both.

#### Validation

Applying a Config is refused when `cron` does not parse, `timezone` is unknown, `concurrencyPolicy` is anything other than `Forbid`, or `startingDeadlineSeconds` is negative. It is also refused when the Config's default mode is `interactive`, because nothing attaches to a cell the daemon starts: set [`spec.modes.default`](#specmodes-object-optional) to a mode that runs unattended, such as `headless`. A Config whose blueprint declares no modes is accepted.

## status

`status` is written by kukeond and is read-only: `kuke get config <name> -o yaml` shows it, and a `status` block in an applied document is ignored. A Config has one when it is scheduled or requires a connector.

```yaml
status:
  schedule:
    lastScheduleTime: 2026-11-17T08:30:00Z
    nextScheduleTime: 2026-11-17T08:40:00Z
    lastCell: disk-watch-1794904200
    activeCells: [disk-watch-1794904200]
    skippedFires: 0
    lastError: ""
```

| Field              | Type            | Description                                                                                                               |
| ------------------ | --------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `lastScheduleTime` | timestamp       | The fire time of the last fire that created a cell.                                                                       |
| `nextScheduleTime` | timestamp       | The next fire time. Unset while the schedule is suspended, and until kukeond's first pass after the schedule was applied. |
| `lastCell`         | string          | The name of the cell the last fire created.                                                                               |
| `activeCells`      | array of string | The schedule's cells that are still running — the ones a `Forbid` schedule waits for.                                     |
| `skippedFires`     | integer         | How many fires did not run: skipped by the concurrency policy, or missed and past their starting deadline.                |
| `lastError`        | string          | Why the last fire failed. Empty once a fire succeeds.                                                                     |

All times are UTC. `kuke get configs` shows the same state in two columns: `SCHEDULE` (the cron expression, followed by the time zone when one is set) and `NEXT` (the next fire time, or `suspended`). Both are empty for an unscheduled Config.

### `status.connectors`

How each [connector](../concepts/connectors.md) the Config requires resolves right now, computed on every get and list — so it follows a change of a Realm, Space or Stack default with no re-apply.

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
      origin: realm
    - connector: claude
      component: claude-login
      method: login
      delivery: stateVolume
      state: resolved
      volume: team-dev-claude-state
      volumeRef:
        name: team-dev-claude-state
        realm: default
        space: default
        stack: default
```

| Field       | Type   | Description                                                                                            |
| ----------- | ------ | ------------------------------------------------------------------------------------------------------ |
| `connector` | string | The connector's name; `title` is its display name.                                                     |
| `component` | string | The blueprint component that requires it; `method` is the connector method that component is.          |
| `delivery`  | string | How the identity reaches the cell: `env`, `file`, `stateVolume` or `git`.                              |
| `slot`      | string | The secret slot the identity fills (`env` and `file`).                                                 |
| `state`     | string | `resolved` or `unresolved`.                                                                            |
| `required`  | bool   | A cell cannot be created while the connector is unresolved.                                            |
| `identity`  | object | The Secret that fills the connector. Absent when unresolved, and for a login kept in a state volume.   |
| `origin`    | string | The level the identity comes from: `config`, `stack`, `space` or `realm` (`cell` in a create preview). |
| `volume`    | string | The state volume holding the login of a `stateVolume` connector.                                       |
| `volumeRef` | object | That volume with its scope, when the Config sets `STATE_VOLUME_REALM` (and `_SPACE` / `_STACK`).       |
| `message`   | string | Why the connector is unresolved, or what is wrong with the identity it names.                          |

## Slot fills

A `CellConfig` fills the structural slots a [`CellBlueprint`](blueprint.md) declares. Slot matching is by **name**, across all of the blueprint's containers — the same slot name on two containers shares a single Config-side entry. The validation gates are:

- A Config that fills a slot the blueprint does not declare is an apply-time error (unknown repo slot / unknown secret slot).
- A _required_ slot the blueprint declares that the Config leaves unfilled is an apply-time error. A slot is treated as required if **any** declaration of that name across the blueprint's containers is required.
- Optional unfilled slots are dropped silently from the materialized container.

The two channels are independent: scalar `values` are blueprint-side parameters resolved at run time (see the blueprint's [`spec.parameters[]`](blueprint.md)), while `repos:` and `secrets:` are structural slot fills validated at apply time. A `${KEY}` parameter is not a slot, and a slot is not a `${KEY}` parameter.

## Defaults merge

kukeond is the only place a CellConfig becomes a Cell: `kuke create cell --from-config`, `kuke run --from-config`, `kuke create cell --clone`, the web UI, the `OutOfSync` reconciler and the `kuke restart` re-apply all ask the daemon to render. The render merges the `spec.defaults` of the cell's [Realm](realm.md#specdefaults-object-optional), [Space](space.md) and [Stack](stack.md) under the Config's own fill:

```
Realm defaults → Space defaults → Stack defaults → CellConfig → (per-cell --env, --connector)
```

Maps merge per key, and the deeper level wins. The `git` block merges the same way: identity and signing are replaced by the deepest level that sets them, and `credentials[]` merge by `host`. A blueprint parameter no level sets keeps its blueprint default. Repo and secret defaults apply only to slots the referenced blueprint declares — a realm-wide `github-token` secret default is simply ignored by a blueprint without that slot — while the Config's own fills stay strictly validated. A level default can satisfy a required slot the Config leaves open. Container `image` values that name a realm [`spec.images`](realm.md#specimages-array-optional) entry resolve to that image.

A cell lands in the Config's scope by default, with an open space or stack coordinate taken as `default`. A create may instead name a space and stack below the Config's scope — today from the web UI's **New Cell** form: a realm-scoped Config may place its cell in any space and stack of its realm, a space-scoped one in any stack of its space, and a coordinate the Config sets cannot be changed. The Space and Stack defaults merged are those of that target, and the cell keeps its scope on every re-render (`OutOfSync`, `kuke restart`, `kuke create cell --clone`). Its lineage still points at the Config's own scope.

`kuke create cell <name> --from-config <cfg> --dry-run --explain` prints every resolved value with the level that set it (`realm`, `space`, `stack`, `config`, or `blueprint`) and the rendered cell's digest; the web UI's create preview shows the same values, grouped by the source that set them (scalar values hidden).

Every rendered cell records its render provenance as annotations: `kukeon.io/render-digest`, `kukeon.io/render-config-revision` (a content hash of the Config document, `spec.schedule` and the `kukeon.io/display-name` annotation left out), `kukeon.io/render-blueprint-revision` (`<blueprint name>@<content hash>`), `kukeon.io/render-{realm,space,stack}-defaults` (`generation=<n> hash=<h>`), `kukeon.io/render-image-digests`, `kukeon.io/render-values-hash`, `kukeon.io/render-origins` and `kukeon.io/render-kukeon-version`. Preview and create of the same inputs (including the cell name) produce the same digest.

Editing a level's `spec.defaults` marks every dependent cell `OutOfSync` with `outOfSyncReason` naming the level (`realm defaults changed`, …), and a rebuilt realm image does the same (`image <name> digest changed`). The reason also names a changed blueprint (`blueprint <name> changed` — new content under the same name, or the Config pointed at another blueprint), a changed Config document (`config changed`) and changed fill values (`values changed`), when the change alters the rendered cell. A blueprint, Config or values change that leaves the rendered cell as it was (no spec diff and the same `kukeon.io/render-digest`) keeps the cell Synced: the next tick records the new revision on the cell without restarting it. Reasons come in a fixed order — blueprint, config, values, realm/space/stack defaults, images, then `spec differs at …` — so the status is stable between ticks. A cell is only compared on the inputs it carries a stamp for; a Synced cell rendered before the blueprint revision existed has it back-filled. `kuke restart <cell>` applies the change: an input change that leaves the rendered spec untouched only records the new provenance; a rebuilt image recreates the cell.

## Lineage, not identity

A `CellConfig` is a **1:N** binding: it stamps as many cells as you run it, each its own [`CellDoc`](../concepts/cell.md) identity. The stamped cell's name is the unified `<prefix>-<6hex>` generated per invocation (prefix = the referenced blueprint's `spec.prefix`, defaulting to its `metadata.name`), or the verbatim `--name X` when you pin one. The Config name is **not** the cell name — it survives only as lineage metadata.

The lineage contract:

- Every cell stamped from a Config carries the `kukeon.io/config=<name>` **lineage label** (a back-reference, not an identity constraint). List a Config's stamped cells with `kuke get cells -l kukeon.io/config=<name>`.
- Every stamped cell also persists a `Spec.Provenance` block recording the binding it came from (kind, scoped ref, resolved params, and any `--env`, `--connector` and `--connector-method` per-cell overrides), so a later re-resolve rebuilds the cell from the same Config without re-supplying values.
- Editing the Config does **not** push to live cells. A stamped cell whose spec has drifted from the current Config surfaces as informational `OutOfSync` in the `SYNC` column of `kuke get cell -o wide`; an operator pulls it back in line explicitly with [`kuke restart <cell>`](../cli/kuke-restart.md) (or `kuke restart -l <selector>` to roll a fleet). `kuke run` / `kuke create cell` never mutate a live cell.

This is the structural parallel with a [`CellBlueprint`](blueprint.md): both are always-fresh templates (`<prefix>-<6hex>` per invocation). A Blueprint binds only scalar `${KEY}` parameters; a Config additionally binds structural repo/secret slot sources, so a blueprint with required slots can only run through a Config.

## Storage layout

The daemon writes the config document to a root-owned, world-readable file under the scope's metadata tree:

```
<runPath>/data/<realm>/configs/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/configs/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/configs/<name>         # stack-scoped
```

The `configs/` directory is `0755` and each config document is `0644`, both owned by root. A config carries only references (a blueprint name, repo URLs, secretRefs) — no credential bytes — so the directory is world-readable, unlike `secrets/`. Because `configs/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its configs.

The [`status.schedule`](#status) of a scheduled Config is stored beside the document, not in it, at `<scope>/config-status/<name>` with the same ownership and modes. Keeping it out of the document means an apply never overwrites it and the scheduler's writes never change the document. `kuke delete config` removes it with the Config.

## Invariants

- **One Config → N cells (1:N binding).** Each `kuke run --from-config` / `kuke create cell --from-config` stamps a fresh `<prefix>-<6hex>` cell (or a `--name`-pinned one); the cell's identity is its [`CellDoc`](../concepts/cell.md), and the Config name lives on only as the `kukeon.io/config` lineage label.
- **Lineage label.** Every stamped cell carries the `kukeon.io/config=<name>` label, so an operator can list all of a Config's cells with `kuke get cells -l kukeon.io/config=<name>`.
- **Persisted provenance.** Every stamped cell records its binding in `Spec.Provenance` (kind, scoped ref, resolved params, `--env` and `--connector` overrides), so `kuke restart <cell>` can re-resolve it from the Config without re-supplying values.
- **Apply-time slot validation.** A Config that fills an undeclared slot, or that leaves a required slot unfilled, errors at apply time against the referenced blueprint's current shape — not at run time.
- **Cross-scope references.** A Config may reference a Blueprint in a different scope; the same is true of the `secretRef` inside each secret slot fill.
- **A schedule is not part of the cell.** `spec.schedule` decides when kukeond creates cells from the Config, never what they contain: editing it leaves existing cells in sync, and a scheduled cell differs from a hand-started one only by its name, its `kukeon.io/schedule` label and being self-deleting.

## Minimal

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod
  realm: kuke-system
spec:
  blueprint:
    name: claude-code
    realm: kuke-system
  values:
    MODEL: claude-opus-4-7
  repos:
    workspace:
      url: https://github.com/example/repo
  secrets:
    anthropic-token:
      secretRef:
        name: anthropic-token
        realm: kuke-system
```

A realm-scoped Config named `prod` that instantiates the `claude-code` blueprint (also realm-scoped), overrides the `MODEL` scalar parameter, fills the `workspace` repo slot, and fills the `anthropic-token` secret slot from the existing `kind: Secret` of the same name. Stamp a cell with `sudo kuke run --from-config prod --realm kuke-system`; each invocation stamps a fresh `<prefix>-<6hex>` cell (pin one with `--name`).
