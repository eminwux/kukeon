# Codex cells

Role installation commands in this source contract run in the external
[agents checkout](https://github.com/eminwux/agents/tree/a5dbb59c3942621c5d52ba321b1d81e5987a5f3a),
not the kuketeams image-build Makefile. Hook parity tests here use recorded
Claude hook outputs from that revision. See [packaging and builds](../README.md).

`make codex ROLE=<role>` uses the same Makefile recipe as the other harnesses:
copy `common/skills/`, overlay `<role>/skills/`, and copy the role's `CLAUDE.md`
into the declared skill directory. Directory contents determine the installed
skills; `role.yaml` remains the team-distribution declaration.

Codex additionally receives the role instructions at `$CODEX_HOME/AGENTS.md`
(default `$HOME/.codex/AGENTS.md`). The Makefile checks ownership and overrides
before changing skills. It refuses symlinks, operator-owned instruction files,
and nonempty `AGENTS.override.md` files. Resolve those conflicts explicitly;
operator instructions are never silently replaced. A marker identifies generated
instructions, and a temporary file prevents partial policy writes. Auth,
history, and configuration are preserved. `make clean` removes skills only.

Materialization is intended for cell startup, with one writer per destination.
It is not a transaction across skills and policy: a filesystem failure during
installation requires rerunning make. Startup stops on any failure, before
launching Codex. Do not rematerialize during an active session.

The Harness owns its complete onInit shell script and calls the image-installed
`Makefile.start setup` before launching Codex. See the
[renderer/startup contract](../templates.md). Assigned tasks use
`codex --ask-for-approval never exec`; interactive launches use the role's
approval policy. Both use the selected sandbox. Current role defaults select
`danger-full-access` and `never`. A role can explicitly choose a narrower
sandbox in `role.yaml`; its approval setting applies to interactive sessions,
while assigned tasks always use `never`. These CLI settings govern Codex inside
the cell; the cell is its outer boundary, and a separate session tool runner
may apply its own sandbox.
Cells start in the selected `initialPath`, defaulting to `/home/user`. Role
instructions and skills are installed before changing to that directory.

Project `CLAUDE.md` is configured as a fallback after native instruction files,
and the instruction allowance is 128 KiB. Shared references stay in the agents
checkout and are read on demand. See the official
[instruction discovery documentation](https://learn.chatgpt.com/docs/agent-configuration/agents-md).

The images pin Codex 0.163.0 and install a managed reflection `Stop` hook in
`/etc/codex/requirements.toml`, with its Python handler under
`/opt/agents/codex/hooks/`. Python is already provided by the base image.
The Harness environment supplies `AGENTS_ROLE` for both assigned-task and interactive
sessions. Supported roles are `dev`, `pm`, `pr-reviewer`, `meta-dev`, `meta-pm`,
and `meta-reviewer`; other roles and launches without `AGENTS_ROLE` are no-ops.

On the first stop, the hook requests the same reflection check as the role's
Claude `Stop` hook (including dev's verified-project-bug check). The agent uses
the installed `/reflect` skill only when its criteria apply. When
`stop_hook_active` is true, the handler allows completion without another
reminder. This is a per-turn check, not an unconditional issue-filing command.
It does not register `SubagentStop` or `SessionEnd` handlers.

The hook requires a nonempty `transcript_path`. Codex 0.163.0 also emits `Stop`
for its internal title generator, which has no persisted transcript; this guard
keeps reflection out of that helper. Explicit `codex exec --ephemeral` runs also
skip the hook. Both blueprint launch modes use normal persisted task sessions.

The image-owned requirements enable hooks and trust this managed handler without
an interactive `/hooks` review. Other hook sources keep their normal trust rules;
no global hook-trust bypass is used. User auth, config, and history are untouched.
See [Codex hooks](https://learn.chatgpt.com/docs/hooks) for the lifecycle and
managed-policy contract. `make codex` installs skills and instructions only;
the managed hook is delivered by rebuilding the images.

Run the hook contract and Claude-parity checks with:

```sh
python3 -m unittest discover -s harnesses/codex/tests -v
```

For each built image, run the CLI smoke test with no external network or
credentials (substitute its tag for `<image>`):

```sh
docker run --rm --network none \
  --mount type=bind,src="$(pwd)/harnesses/codex/tests",dst=/tests,readonly \
  <image> python3 /tests/smoke_hooks.py
```

It checks managed-hook trust, all six roles through `codex exec`, and an
interactive CLI turn using a local Responses fixture. This verifies lifecycle
continuation, not a real model's judgment about whether to file a reflection.

After updating source, rematerialize and start a new session. Regenerate installed
blueprints for startup changes, and rebuild images when changing the pinned CLI,
hook handler, or managed requirements. For the reflection-hook rollout, rebuild
both Codex images, regenerate blueprints to supply `AGENTS_ROLE`, and recreate
cells with the updated images. Verify a first-stop continuation followed by normal
completion in both interactive and assigned-task modes before deployment.
Also verify native role/project instruction and skill discovery,
volume ownership, and both assigned-task and interactive launches in actual cells.
Local rendering and CLI argument checks do not certify daemon or model behavior.
