// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package status

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// checkParity is the regression-guard the CLAUDE.md daemon-parity ritual
// has been doing by hand: every `kuke get <kind>` must return the same
// set of names when run against the daemon and in-process. The walk
// covers each kind in turn — realm, then space (per realm), then stack
// (per realm/space), then cell (per realm/space/stack), then container
// (per cell), then the scope-flat kinds (secret, blueprint, config).
//
// Both clients must be constructible. When either is nil (the daemon-down
// case at status startup, or an in-process construction failure injected
// in tests), the parity row degrades to a single WARN row naming the
// missing branch — there is nothing to compare against, but the section
// stays present so JSON shape is stable.
func checkParity(ctx context.Context, rc *runCtx) []Result {
	if rc.daemonClient == nil {
		return []Result{{
			Section:     sectionParity,
			Name:        "realms",
			Status:      StatusWARN,
			Detail:      "skipped: daemon not reachable",
			Remediation: "the daemon row above carries the underlying cause",
		}}
	}
	if rc.localClient == nil {
		return []Result{{
			Section:     sectionParity,
			Name:        "realms",
			Status:      StatusWARN,
			Detail:      "skipped: in-process client unavailable",
			Remediation: "internal: status invoked without a local client",
		}}
	}

	var results []Result

	// Realm parity: top of the walk. If the realm lists disagree the
	// nested kinds may not even be enumerable (a realm present in one
	// branch but missing in the other can't have its spaces compared),
	// so the parity row carries that information and the walk
	// recurses only into the names that appear in both branches —
	// divergent realms surface as the realm row above.
	// Distinct error-var names per nesting level — Go's lexical scoping
	// would shadow a reused `dErr`/`lErr` inside the nested loops, which
	// govet flags. The naming keeps the linter quiet without inflating
	// the loop bodies with intermediate result structs.
	dRealms, dRealmErr := listRealmNames(ctx, rc.daemonClient)
	lRealms, lRealmErr := listRealmNames(ctx, rc.localClient)
	realmResult, realmsBoth := parityFromLists("realms", dRealms, dRealmErr, lRealms, lRealmErr)
	results = append(results, realmResult)

	for _, realm := range realmsBoth {
		dSpaces, dSpaceErr := listSpaceNames(ctx, rc.daemonClient, realm)
		lSpaces, lSpaceErr := listSpaceNames(ctx, rc.localClient, realm)
		spaceResult, spacesBoth := parityFromLists(
			fmt.Sprintf("spaces/%s", realm),
			dSpaces, dSpaceErr, lSpaces, lSpaceErr,
		)
		results = append(results, spaceResult)

		for _, space := range spacesBoth {
			dStacks, dStackErr := listStackNames(ctx, rc.daemonClient, realm, space)
			lStacks, lStackErr := listStackNames(ctx, rc.localClient, realm, space)
			stackResult, stacksBoth := parityFromLists(
				fmt.Sprintf("stacks/%s/%s", realm, space),
				dStacks, dStackErr, lStacks, lStackErr,
			)
			results = append(results, stackResult)

			for _, stack := range stacksBoth {
				dCells, dCellErr := listCellNames(ctx, rc.daemonClient, realm, space, stack)
				lCells, lCellErr := listCellNames(ctx, rc.localClient, realm, space, stack)
				cellResult, cellsBoth := parityFromLists(
					fmt.Sprintf("cells/%s/%s/%s", realm, space, stack),
					dCells, dCellErr, lCells, lCellErr,
				)
				results = append(results, cellResult)

				for _, cell := range cellsBoth {
					dContainers, dContainerErr := listContainerNames(ctx, rc.daemonClient, realm, space, stack, cell)
					lContainers, lContainerErr := listContainerNames(ctx, rc.localClient, realm, space, stack, cell)
					containerResult, _ := parityFromLists(
						fmt.Sprintf("containers/%s/%s/%s/%s", realm, space, stack, cell),
						dContainers, dContainerErr, lContainers, lContainerErr,
					)
					results = append(results, containerResult)
				}
			}
		}
	}

	// Scope-flat kinds: secret / blueprint / config support a
	// cross-scope listing via empty filter arguments. One row per kind,
	// not one per scope — the AC's "every `kuke get <kind>`" wording
	// reads against the user-facing verbs, and these three have no
	// per-scope sub-verb the operator would run separately.
	dSecrets, dSecretErr := listSecretNames(ctx, rc.daemonClient)
	lSecrets, lSecretErr := listSecretNames(ctx, rc.localClient)
	results = append(results, parityFromCall("secrets", dSecrets, dSecretErr, lSecrets, lSecretErr))

	dBlueprints, dBlueprintErr := listBlueprintNames(ctx, rc.daemonClient)
	lBlueprints, lBlueprintErr := listBlueprintNames(ctx, rc.localClient)
	results = append(results, parityFromCall("blueprints", dBlueprints, dBlueprintErr, lBlueprints, lBlueprintErr))

	dConfigs, dConfigErr := listConfigNames(ctx, rc.daemonClient)
	lConfigs, lConfigErr := listConfigNames(ctx, rc.localClient)
	results = append(results, parityFromCall("configs", dConfigs, dConfigErr, lConfigs, lConfigErr))

	return results
}

// parityFromLists builds a single parity Result and the set of names that
// appear in both branches, which the recursive walk uses to descend into
// nested kinds. Errors on either branch demote the row to WARN (we can't
// compare what we can't enumerate) — only the FAIL path is a true
// divergence between equally-served branches.
func parityFromLists(name string, daemon []string, daemonErr error, local []string, localErr error) (Result, []string) {
	r := Result{
		Section: sectionParity,
		Name:    name,
	}

	if daemonErr != nil && localErr != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("both branches errored: daemon=%v local=%v", daemonErr, localErr)
		return r, nil
	}
	if daemonErr != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("daemon errored: %v", daemonErr)
		return r, nil
	}
	if localErr != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("in-process errored: %v", localErr)
		return r, nil
	}

	dSet := toSet(daemon)
	lSet := toSet(local)

	onlyDaemon := diff(dSet, lSet)
	onlyLocal := diff(lSet, dSet)
	both := intersect(dSet, lSet)

	if len(onlyDaemon) == 0 && len(onlyLocal) == 0 {
		r.Status = StatusOK
		if len(both) == 0 {
			r.Detail = "0 items (both branches empty)"
		} else {
			r.Detail = fmt.Sprintf("%d items match (%s)", len(both), strings.Join(both, ", "))
		}
		return r, both
	}

	r.Status = StatusFAIL
	var parts []string
	if len(onlyDaemon) > 0 {
		parts = append(parts, "daemon-only: "+strings.Join(onlyDaemon, ", "))
	}
	if len(onlyLocal) > 0 {
		parts = append(parts, "in-process-only: "+strings.Join(onlyLocal, ", "))
	}
	r.Detail = strings.Join(parts, "; ")
	r.Remediation = "the daemon's view of /opt/kukeon diverges from the in-process controller's; " +
		"investigate bind-mount or run-path mismatch"
	return r, both
}

// parityFromCall is the no-recursion variant — secret / blueprint /
// config are scope-flat kinds whose intersection is not used for a
// nested walk. Same Result shape as parityFromLists.
func parityFromCall(name string, daemon []string, daemonErr error, local []string, localErr error) Result {
	r, _ := parityFromLists(name, daemon, daemonErr, local, localErr)
	return r
}

// ---- Listers ----
//
// Each lister wraps one List* method on kukeonv1.Client and pulls just
// the Name (or qualified-name, for scope-flat kinds) field so
// parityFromLists can compare strings. Errors are propagated untouched
// so the parity row can name which branch errored.

func listRealmNames(ctx context.Context, c kukeonv1.Client) ([]string, error) {
	in, err := c.ListRealms(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Metadata.Name)
	}
	return out, nil
}

func listSpaceNames(ctx context.Context, c kukeonv1.Client, realm string) ([]string, error) {
	in, err := c.ListSpaces(ctx, realm)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Metadata.Name)
	}
	return out, nil
}

func listStackNames(ctx context.Context, c kukeonv1.Client, realm, space string) ([]string, error) {
	in, err := c.ListStacks(ctx, realm, space)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Metadata.Name)
	}
	return out, nil
}

func listCellNames(ctx context.Context, c kukeonv1.Client, realm, space, stack string) ([]string, error) {
	in, err := c.ListCells(ctx, realm, space, stack)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Metadata.Name)
	}
	return out, nil
}

func listContainerNames(
	ctx context.Context, c kukeonv1.Client, realm, space, stack, cell string,
) ([]string, error) {
	in, err := c.ListContainers(ctx, realm, space, stack, cell)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		// ContainerSpec carries its identity as ID, not Name (the
		// scope-flat parent is implicit in the per-cell query). The
		// daemon and in-process branches both populate this from the
		// same on-disk container.json, so it is the right comparable.
		out = append(out, in[i].ID)
	}
	return out, nil
}

func listSecretNames(ctx context.Context, c kukeonv1.Client) ([]string, error) {
	in, err := c.ListSecrets(ctx, "", "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, secretQualifiedName(in[i]))
	}
	return out, nil
}

func listBlueprintNames(ctx context.Context, c kukeonv1.Client) ([]string, error) {
	in, err := c.ListBlueprints(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, blueprintQualifiedName(in[i]))
	}
	return out, nil
}

func listConfigNames(ctx context.Context, c kukeonv1.Client) ([]string, error) {
	in, err := c.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, configQualifiedName(in[i]))
	}
	return out, nil
}

// secretQualifiedName joins the scope coordinates and the secret name.
// Scope-flat lists span all realms, so the name alone isn't unique — a
// Secret "foo" in realm A is distinct from a Secret "foo" in realm B.
// Joining with "/" matches the storage layout's path shape. Scope
// coordinates live in Metadata, not Spec, for Secret / Blueprint /
// Config (consistent with the parent realm/space/stack Doc shape).
func secretQualifiedName(doc v1beta1.SecretDoc) string {
	return qualifiedName(
		doc.Metadata.Realm,
		doc.Metadata.Space,
		doc.Metadata.Stack,
		doc.Metadata.Cell,
		doc.Metadata.Name,
	)
}

// blueprintQualifiedName is the analogous helper for blueprints. A
// blueprint is never cell-scoped (#643), so the cell coordinate is
// always empty.
func blueprintQualifiedName(doc v1beta1.CellBlueprintDoc) string {
	return qualifiedName(doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack, "", doc.Metadata.Name)
}

// configQualifiedName is the analogous helper for configs. A config is
// never cell-scoped (#644), so the cell coordinate is always empty.
func configQualifiedName(doc v1beta1.CellConfigDoc) string {
	return qualifiedName(doc.Metadata.Realm, doc.Metadata.Space, doc.Metadata.Stack, "", doc.Metadata.Name)
}

func qualifiedName(realm, space, stack, cell, name string) string {
	parts := make([]string, 0)
	if realm != "" {
		parts = append(parts, realm)
	}
	if space != "" {
		parts = append(parts, space)
	}
	if stack != "" {
		parts = append(parts, stack)
	}
	if cell != "" {
		parts = append(parts, cell)
	}
	parts = append(parts, name)
	return strings.Join(parts, "/")
}

// ---- Set helpers ----

func toSet(in []string) map[string]struct{} {
	s := make(map[string]struct{}, len(in))
	for _, x := range in {
		s[x] = struct{}{}
	}
	return s
}

// diff returns the names in a that are not in b, sorted.
func diff(a, b map[string]struct{}) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// intersect returns the names common to a and b, sorted.
func intersect(a, b map[string]struct{}) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
