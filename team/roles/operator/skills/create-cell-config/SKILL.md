---
name: create-cell-config
description: Draft or apply a kukeon CellConfig from an existing CellBlueprint, including scalar parameters and repo/secret slot fills. Use when the user requests a reusable cell configuration; cell creation and startup require a separate request.
---

Create the requested configuration using the installed CLI's supported workflow.
Read [the CLI/schema reference](references/kukeon.md) first; it records inspected
upstream sources, their revision, supported syntax, and validation limits.

1. Establish whether the user requested a draft or a stored configuration. Use
   the configured `kuke` client/daemon endpoint and inspect `kuke create config --help`, `kuke get config --help`, `kuke get blueprint --help`, and `kuke apply --help`. Do not invent a dry-run or validation flag. If help differs from the
   reference, follow the installed version's verified contract.
2. Inspect relevant existing configs and blueprints with `kuke get configs -o yaml` and `kuke get blueprints -o yaml`, narrowing with supported scope flags
   where known. Select the requested blueprint by name **and scope** and inspect
   its full document, including parameters and all containers' repo/secret slots.
   Resolve the new config's name/scope independently of the blueprint reference;
   realm is required, space requires realm, and stack requires space. Reuse known
   inputs and gather only required missing values. If a config with that identity
   exists, compare it and report unchanged or apply only an authorized update.
   Failed reads are not evidence that a resource is absent.
3. Scaffold a local draft using the supported form below. Use a new file in an
   authorized location (avoid shell redirection over an existing user file),
   check the command's exit status, and discard partial output on failure:

   ```sh
   kuke create config "$config_name" --from-blueprint "$blueprint_name" \
     --realm "$blueprint_realm" --space "$blueprint_space" --stack "$blueprint_stack" > "$draft"
   ```

   These flags select the **blueprint lookup scope** and initially seed the
   config's scope. For a cross-scope config, edit `metadata.realm/space/stack` to
   the requested destination and preserve the scaffold's `spec.blueprint`
   identity. For realm/space-only resources, inspect the installed version's
   empty-coordinate behavior; inspect scoped list output if a named read falls
   back to defaults. If scaffolding cannot address the selected scope, author
   the draft from the inspected schema with its exact blueprint reference and
   explain that limitation. Never silently substitute a similarly named blueprint.

4. Complete the draft using the selected blueprint's actual schema:
   - `spec.values` is a string map of declared scalar parameters. Preserve useful
     defaults (including explicit empty defaults), supply required values, and
     remove unresolved TODOs. Do not rely on ambient environment for a reusable
     config's required inputs.
   - `spec.repos` maps structural slot names to `{url, branch?}` or `{url, ref?}`;
     branch and ref are mutually exclusive. Inline blueprint URLs are not slots.
   - `spec.secrets` maps structural slot names to `secretRef` identities with
     name/realm and applicable scope coordinates. Never put credential bytes in
     values, repo URLs, examples, or diagnostics. Use existing secret identities;
     missing secrets need a separately authorized provisioning operation.
   - The same slot name across containers shares one fill; it is required if any
     declaration requires it. Fill required slots and omit unused optional slots
     rather than leaving empty placeholder entries. Do not add undeclared keys.
5. Review YAML syntax and the config/blueprint name+scope, string value types,
   parameter requirements, repo/secret fills, and absence of credentials. Limit
   the file to the requested `kind: CellConfig` document: `kuke apply` accepts
   other kinds too. If only a draft was requested, stop here and report its path
   and checks; do not claim daemon validation or application.
6. When creation/application was requested, apply the completed file:

   ```sh
   kuke apply -f "$draft"
   kuke get config "$config_name" --realm "$config_realm" \
     --space "$config_space" --stack "$config_stack" -o yaml
   ```

   Check exit status, the per-resource created/updated/unchanged result, and
   stored-document readback against the intended config. For shallower scopes,
   verify exact identity via list output if needed. Report a failed apply/readback
   concretely; do not claim success based on scaffold output. Apply validates
   against the blueprint and its slot declarations, but is not a runtime test.

7. Report draft vs applied status, config identity (name and full scope), selected
   blueprint identity, supplied nonsecret inputs, validation evidence, and any
   remaining prerequisites. Explain that the user can later run the following
   **to create, start, and attach a fresh cell**; present it without executing it:

   ```sh
   kuke run --from-config "$config_name" --realm "$config_realm" \
     --space "$config_space" --stack "$config_stack"
   ```

   Configuration creation does not authorize this command or `kuke create cell`.
   Do not start workloads as a way of validating a configuration request.
