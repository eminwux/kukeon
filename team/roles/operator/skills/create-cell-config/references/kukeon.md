# Inspected kukeon contract

Grounded in `eminwux/kukeon` revision
`58250581d283592cd59bda8c0d083f83940b6ae4`. Check installed help and actual resource
schemas on each use; this reference does not pin or install the runtime.

- [Scaffold command and emitter](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/cmd/kuke/create/config/config.go):
  `kuke create config NAME --from-blueprint BP --realm R --space S --stack T`
  reads the daemon blueprint and emits `apiVersion: v1beta1`, `kind: CellConfig`
  YAML to stdout. It does not write a config to the daemon. Defaults are copied;
  required scalars without defaults and structural slot sources get TODOs.
- [CLI documentation](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/docs/site/cli/kuke-create.md#kuke-create-config)
  confirms the scaffold → edit → apply workflow. Scope flags seed both the
  config scope and blueprint lookup; `spec.blueprint` preserves the resolved
  blueprint scope. Cross-scope destination changes belong in config metadata.
- [CellConfig schema](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/pkg/api/model/v1beta1/cellconfig.go)
  and [manifest documentation](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/docs/site/manifests/config.md):
  required name/realm; optional hierarchical space/stack; `spec.blueprint` has
  its own required name/realm and optional scope. `spec.values` maps strings to
  strings. `spec.repos` maps slot names to url plus optional branch OR ref.
  `spec.secrets` maps slot names to secretRef (name/realm and applicable scope),
  never credential bytes. Config `spec.prefix` is an optional cell-name prefix;
  the config name is not a running cell identity.
- [CellBlueprint schema](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/pkg/api/model/v1beta1/cellblueprint.go):
  inspect `spec.parameters` (name, description, default, required) and all
  `spec.cell.containers` repo/secret declarations. A nil default differs from
  an explicit empty default. Repos with inline URLs are not fillable slots.
- [Slot validation](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/internal/cellconfig/cellconfig.go):
  keys match across containers by name; required is the OR of all same-name
  declarations. Required unfilled and unknown slots fail validation. Optional
  unfilled slots can be omitted.
- [Apply implementation](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/cmd/kuke/apply/apply.go):
  `kuke apply -f FILE` sends documents to the daemon and reports each result;
  failed resources produce a nonzero exit. There is no dry-run/validate flag
  here. Applying a completed config is a write, not a read-only validation step.
  Syntax/shape and blueprint-slot acceptance do not certify runtime parameter
  resolution, repository access, available credentials, images, or startup.
- [Config reads](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/cmd/kuke/get/config/config.go)
  and [blueprint reads](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/cmd/kuke/get/blueprint/blueprint.go):
  `get configs` / `get blueprints` list with scope filters; the singular forms
  plus a name read one exact identity. Both support `-o yaml` and `-o json`.
  Named reads and scaffolding default unset coordinates; verify shallow-scope
  behavior rather than interpreting a default-scope miss as absence everywhere.
- [Run workflow](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/docs/site/cli/kuke-run.md):
  `kuke run --from-config NAME` creates, starts, and attaches a fresh cell each
  time. It is a separate lifecycle operation, not config validation. The
  reusable config is a 1:N binding. Do not use `kuke team` for this workflow.
