# Checking the published Codex majordomo (`kuke team`)

This opt-in check exercises the real `kuke team init` command followed by an
explicit `kuke run --from-config kukeon-majordomo-codex`. Initialization applies
configuration and registers the kukeon team; it does not start cells. A passing
check requires a live work container, the published image's digest, and a
completed authenticated Codex turn with command-generated installation evidence.

## Prerequisites and isolation

- A local kukeond Unix endpoint with provisioned `default/default/default` scope
  and working containerd, networking, DNS, GitHub access, and GHCR image pulls,
  running the same kukeon revision as the driver checkout.
- `go`, `git`, `make`, and a compatible `kuke` CLI on the test driver's PATH
  (the driver itself runs `./kuke team init` from the checkout).
  The image provides Python 3, Git, Make, and Codex. The driver must be on the
  daemon host or have its log directory mounted at the same absolute path:
  `kuke log` resolves a host path through RPC and reads the file locally. With
  the default installation this requires readable `/opt/kukeon` files as well
  as access to `/run/kukeon/kukeond.sock`.
- The catalog-selected Codex release must be available from GHCR. The current
  reference is the one in `team/harnesses/codex/images.yaml`. The driver verifies an
  **anonymous public pull** using GHCR's pull-token/manifest protocol; it never
  reads registry login files or borrows GitHub credentials. A private or absent
  image is an unmet prerequisite for this check. Publish/make the intended
  release publicly pullable separately; neither this target nor `init` builds
  an image, publishes one, or creates a release tag.
- A reachable, full source commit SHA in `eminwux/kukeon`. Both initialization
  and the in-cell clone use this commit. By default it is the driver's `HEAD`;
  set `KUKETEAMS_REMOTE_REVISION` when testing unpushed driver changes against an
  already published source revision. Use matching majordomo/harness assets: their
  expected policy/hook hashes come from this checkout.
- A dedicated, pre-authenticated `kukeon-majordomo-codex` Volume at
  `default/default/default`. The check does not log in, obtain credentials, copy
  another team's credentials, rotate secrets, or delete this volume. The base
  majordomo's Codex role requires **no secret bindings**, including no `gh-token`
  or mandatory API-key secret. Account/model access must already be configured
  in its Codex state. An absent volume fails before launch; an empty/expired
  login cannot produce the required authenticated turn and fails the check.

Prefer a disposable daemon. A shared daemon is supported only when the `kukeon`
team identity is unused and exclusively reserved for this check. Before writing,
its guard lists blueprints/configs across **all realms**, rejecting any kukeon-team
label or conflicting majordomo resource name: team application can prune kukeon
resources across realms. Run only one kukeon check at a time and do not initialize
another kukeon team concurrently. Other team names are left alone. The driver uses
a temporary client HOME, explicit host/client configuration and runtime scope,
public HTTPS sources, and no ambient Git configuration or credential env vars.

## Prepare authentication explicitly

On the selected test daemon, authenticate a dedicated state volume before the
check. For example, author this one-cell manifest in a new local file:

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: majordomo-auth-setup
spec:
  realmId: default
  spaceId: default
  stackId: default
  tty:
    default: work
  containers:
    - id: work
      attachable: true
      image: ghcr.io/eminwux/kukeon/codex:v0.10.0 # from team/harnesses/codex/images.yaml
      command: /bin/bash
      workingDir: /home/user
      env:
        - CODEX_HOME=/home/user/.codex
      volumes:
        - kind: volume
          source: kukeon-majordomo-codex
          target: /home/user/.codex
          ensure: true
```

Use a fresh setup cell name and ensure the state volume belongs to this test.
Run `kuke --host unix:///path/to/kukeond.sock run -f /path/to/majordomo-auth.yaml`.
Inside that shell, perform your chosen supported `codex login` flow and check
`codex login status` privately. Do not include login output, tokens, `auth.json`,
or session transcripts in test evidence. Exit and delete only the setup cell
with `kuke --host unix:///path/to/kukeond.sock delete cell majordomo-auth-setup
--realm default --space default --stack default`, retaining the volume. The
check expects no existing role instruction files or conflicting
`AGENTS.override.md` in that dedicated state.

This login is an explicit operator setup action. See the official
[Codex authentication guide](https://learn.chatgpt.com/docs/auth) for the chosen
account/provider. Merely creating the Volume is not authentication evidence.

## Run and interpret the check

From a clean, published checkout whose `HEAD` is reachable on GitHub:

```sh
KUKETEAMS_REMOTE_HOST=unix:///run/kukeon/kukeond.sock \
KUKETEAMS_REMOTE_REPORT=/tmp/majordomo-check.json \
make test-remote-majordomo
```

The target builds the local `kuke` binary, then invokes the opt-in Go test
with a ten-minute outer timeout. The test has an eight-minute operation deadline
and bounded cleanup. Ordinary `make test` runs the evidence-reader unit tests
and skips the live test unless the explicit endpoint variable is set. A skipped
live test is not successful remote-image verification.

The driver:

1. Runs `kuke team init` in an isolated client HOME with an explicit Codex-only
   `kukeon` entry and its source commit pinned. Fresh init normally selects both
   Codex and Claude; this check intentionally verifies Codex alone.
   Reads back the majordomo blueprint/config and kukeon TeamEntry, checks repository
   pinning and absence of base-role secret bindings, and compares runtime cell
   identities/recipes and secret metadata before and after initialization.
2. Fetches and hashes the catalog image's published manifest. Once authentication
   volume metadata is present, pins **only its new test-owned blueprint** to
   that digest with a single `kuke apply -f`. This test-only pin prevents a stale
   or locally retagged image from satisfying the check. It does not change the
   production catalog or add image-pinning behavior to `init`.
3. Runs `kuke run --from-config kukeon-majordomo-codex --name majordomo-check-<unique>
--realm default --space default --stack default --env PROMPT=<probe> --detach`.
   The supported `PROMPT` override selects the existing `codex exec --json`
   startup path, with workspace-write sandbox and approval never for the batch
   turn. There is no invented shell-input protocol, PTY timing assumption, or
   automatic startup in `init`.
4. Requires a live root task and Ready `work` container, checks the work image's
   digest reference, and reads `kuke log` privately. A tool-executed Python probe
   verifies the installed policy/skill links, source revision, image-owned hook
   and requirements hashes, current working directory, and Codex home. It checks
   the current thread's recorded injected instructions/skill catalog, successful
   reads of the skill and its reference, and effective sandbox/approval context.
   It does not read authentication files. It fails closed if this Codex version
   does not expose the necessary session evidence.
5. Accepts a probe only from a successful `command_execution` item in the same
   thread followed by `turn.completed`. Echoed prompt text, a model's assertion,
   a failed command, or a shell/launch exit alone cannot pass.

The sanitized JSON report records timestamp, endpoint, driver/source revisions,
CLI versions, image reference/digest, test cell identity, command shapes, completed
checks, cleanup, and outcome. The `PROMPT` placeholder refers to the embedded
`internal/team/launchcheck/probe.py`, keeping evidence compact and reproducible.
Raw CLI stderr, capture files, session messages, auth files and credential values
are never copied to the report. Nonzero or missing stages remain unmet; refer to
the `failure` field instead of interpreting a partial report as a pass.

## Cleanup and recovery

On success or ordinary failure, cleanup kills only the unique test cell and
deletes only `kukeon-majordomo-codex` config/blueprint documents bearing its kukeon
team label. It removes its temporary HOME/registration. It never deletes or
rotates secrets, deletes state volumes, cascades a realm/space/stack, purges
images, or touches unrelated teams and persistent data. The pre-authenticated
volume and pulled image cache remain for reruns.

An externally killed process or outer test timeout can prevent cleanup. Use the
recorded cell identity and `kuke get` readback to identify those exact test-owned
resources. Kill/delete that cell, then delete its config and blueprint using
explicit `--realm default --space default --stack default`. Delete the dedicated
state volume separately only when intentionally retiring its credentials/state.
Do not purge the shared scope or clear other teams to make the check pass.
