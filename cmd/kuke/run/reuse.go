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

package run

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// errReusePoolEmpty signals to the caller of pickAndStartReusableClone that
// no clone of the source was claimable (no clone has a Stopped cell at the
// healthy filter, or every Stopped candidate lost the StartCell race to a
// concurrent --reuse). The caller's contract is to fall back to the --clone
// code path so the operator never sees a "pool empty" error on the first
// tick or after a host reboot (issue #835).
//
// Internal-only — the CLI never surfaces this sentinel directly.
var errReusePoolEmpty = errors.New("reuse: no claimable clone in pool")

// pickAndStartReusableClone walks clones of source (lowest counter N first)
// and atomically claims a healthy-Stopped one via StartCell — preserving the
// containerd overlay filesystem across the stop/start transition (issue
// #835). Returns the started clone CellConfig and the StartCell result on
// success.
//
// Pick algorithm (matches the AC):
//
//  1. List CellConfigs in source's scope.
//  2. Filter to clones of source via the kukeon.io/source-config annotation
//     (set by --clone in #839). The annotation read verifies the lineage so
//     a manually-named `<src>-<N>` Config doesn't masquerade as a clone.
//  3. Sort by counter suffix N ascending (-0 before -1 before -2 …).
//  4. For each candidate, GetCell at the clone's stable name and skip cells
//     in Pending / Failed / Unknown sub-states (error-state cells are
//     excluded from the pick set). Cells in Ready state are skipped too —
//     they represent already-Running pool members.
//  5. On a Stopped candidate, call StartCell. The daemon enforces the
//     atomic claim: a concurrent --reuse that won the slot leaves the cell
//     in Ready, so this StartCell returns a "must first be stopped" error
//     and we advance to the next candidate. A successful StartCell means
//     we won the claim; return.
//
// Returns errReusePoolEmpty when no candidate could be claimed and the
// caller should fall back to cloneCellConfig (the --clone path).
//
// runtimeEnv carries the CLI-injected `--env KEY=VALUE` entries (issue
// #834). They ride on the StartCell lookup doc's Spec.RuntimeEnv field so
// the daemon's start path forwards them to the runner's OCI build for the
// claimed cell's attachable container. Per-invocation knob: each --reuse
// tick supplies its own --env set; nothing persists into the cell's stored
// spec.
func pickAndStartReusableClone(
	ctx context.Context,
	client kukeonv1.Client,
	source v1beta1.CellConfigDoc,
	runtimeEnv []string,
) (v1beta1.CellConfigDoc, kukeonv1.StartCellResult, error) {
	pool, err := listReuseCandidates(ctx, client, source)
	if err != nil {
		return v1beta1.CellConfigDoc{}, kukeonv1.StartCellResult{}, err
	}

	for _, cand := range pool {
		cellLookup := cloneCellLookup(cand.cfg)
		// Thread --env onto the StartCell lookup doc only (never onto the
		// GetCell probe: the daemon's GetCell path doesn't consume
		// RuntimeEnv and we want the probe to look identical to a no-env
		// invocation). The StartCell daemon-side then forwards it to the
		// runner; see internal/controller/start_cell.go.
		cellRes, getErr := client.GetCell(ctx, cellLookup)
		if getErr != nil {
			if errors.Is(getErr, errdefs.ErrCellNotFound) {
				// Clone Config exists but the cell is gone (operator
				// manually deleted it, or a prior --rm wiped it). Skip;
				// don't fork into a phantom slot.
				continue
			}
			return v1beta1.CellConfigDoc{}, kukeonv1.StartCellResult{}, getErr
		}
		if !cellRes.MetadataExists {
			continue
		}
		if cellRes.Cell.Status.State != v1beta1.CellStateStopped {
			// Skip Ready (already running — invisible to the pool), and
			// Pending / Failed / Unknown (error sub-states excluded per
			// the AC's "Error-state cells skipped" invariant).
			continue
		}
		// Attach --env to the StartCell doc just before the wire call. The
		// runtimeEnv slice is the operator's per-invocation injection; the
		// daemon ferries it onto the runner's OCI build for the attachable
		// container without persisting it back to the cell's stored spec.
		startLookup := cellLookup
		startLookup.Spec.RuntimeEnv = runtimeEnv
		startRes, startErr := client.StartCell(ctx, startLookup)
		if startErr != nil {
			if isStartCellRace(startErr) {
				// A concurrent --reuse won this slot between our state-read
				// and the StartCell call. Advance to the next candidate
				// rather than surface the race to the operator.
				continue
			}
			return v1beta1.CellConfigDoc{}, kukeonv1.StartCellResult{}, startErr
		}
		return cand.cfg, startRes, nil
	}
	return v1beta1.CellConfigDoc{}, kukeonv1.StartCellResult{}, errReusePoolEmpty
}

// reuseCandidate is one clone of the source CellConfig considered for
// reuse. The N field is the parsed counter suffix; pickAndStartReusableClone
// sorts ascending on N so the lowest-N clone is tried first.
type reuseCandidate struct {
	n   int
	cfg v1beta1.CellConfigDoc
}

// listReuseCandidates returns the source-config-annotated clones of source
// in scope, sorted by counter suffix ascending. Shares the listing
// machinery with --clone (counterSuffixPattern) so the two flags agree on
// what counts as a clone.
func listReuseCandidates(
	ctx context.Context,
	client kukeonv1.Client,
	source v1beta1.CellConfigDoc,
) ([]reuseCandidate, error) {
	configs, err := client.ListConfigs(
		ctx, source.Metadata.Realm, source.Metadata.Space, source.Metadata.Stack,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list cellconfigs in scope: %w", err)
	}

	var pool []reuseCandidate
	for _, cfg := range configs {
		// Scope-filter: ListConfigs returns a subtree; restrict to the exact
		// target scope so a clone of source@realm doesn't pick up a same-named
		// Config nested in a child space (parity with cloneCellConfig).
		if cfg.Metadata.Realm != source.Metadata.Realm ||
			cfg.Metadata.Space != source.Metadata.Space ||
			cfg.Metadata.Stack != source.Metadata.Stack {
			continue
		}
		match := counterSuffixPattern.FindStringSubmatch(cfg.Metadata.Name)
		if match == nil || match[1] != source.Metadata.Name {
			continue
		}
		n, parseErr := strconv.Atoi(match[2])
		if parseErr != nil || n < 0 {
			continue
		}
		body, getErr := client.GetConfig(ctx, v1beta1.CellConfigDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindCellConfig,
			Metadata: v1beta1.CellConfigMetadata{
				Name:  cfg.Metadata.Name,
				Realm: cfg.Metadata.Realm,
				Space: cfg.Metadata.Space,
				Stack: cfg.Metadata.Stack,
			},
		})
		if getErr != nil {
			return nil, fmt.Errorf(
				"failed to read cellconfig %q while scanning clones of %q: %w",
				cfg.Metadata.Name, source.Metadata.Name, getErr,
			)
		}
		if !body.MetadataExists {
			continue
		}
		if body.Config.Metadata.Annotations[cellconfig.AnnotationSourceConfig] !=
			source.Metadata.Name {
			continue
		}
		pool = append(pool, reuseCandidate{n: n, cfg: body.Config})
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].n < pool[j].n })
	return pool, nil
}

// cloneCellLookup builds the lookup CellDoc for the clone Config's cell.
// The cell's stable name is the clone Config's metadata.name and its scope
// inherits from the Config (parity with the cellconfig.Materialize contract
// downstream).
func cloneCellLookup(clone v1beta1.CellConfigDoc) v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name: clone.Metadata.Name,
		},
		Spec: v1beta1.CellSpec{
			RealmID: clone.Metadata.Realm,
			SpaceID: clone.Metadata.Space,
			StackID: clone.Metadata.Stack,
		},
	}
}

// isStartCellRace recognises the StartCell error pattern that means another
// --reuse won this slot. The daemon's controller.StartCell rejects a Start
// against a cell that already has running containers ("has running
// containers and must first be stopped") or whose persisted state is Ready
// without live container statuses ("is already in Ready state and must
// first be stopped"). Both messages end with "must first be stopped"; we
// key on that substring so a future error-message refinement keeps the
// contract.
//
// On a true race, the caller advances to the next pool candidate rather
// than surface the message to the operator — the pool query saw Stopped a
// moment ago but the daemon transitioned the slot between our read and
// our claim.
func isStartCellRace(err error) bool {
	return err != nil && strings.Contains(err.Error(), "must first be stopped")
}
