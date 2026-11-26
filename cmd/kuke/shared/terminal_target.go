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

package shared

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TerminalTarget is the `kuke attach` target shape: a positional cell,
// --realm/--space/--stack, and an optional --container. `kuke attach`,
// `kuke screenshot` and `kuke write` all resolve it through
// ResolveTerminalTarget so the three refuse the same targets with the same
// errors.
type TerminalTarget struct {
	Realm     string
	Space     string
	Stack     string
	Cell      string
	Container string
}

// Trimmed returns t with every coordinate whitespace-trimmed.
func (t TerminalTarget) Trimmed() TerminalTarget {
	return TerminalTarget{
		Realm:     strings.TrimSpace(t.Realm),
		Space:     strings.TrimSpace(t.Space),
		Stack:     strings.TrimSpace(t.Stack),
		Cell:      strings.TrimSpace(t.Cell),
		Container: strings.TrimSpace(t.Container),
	}
}

// Validate reports the first missing required coordinate. Container may be
// empty (ResolveTerminalTarget auto-picks it).
func (t TerminalTarget) Validate() error {
	switch {
	case t.Cell == "":
		return fmt.Errorf("%w (positional cell)", errdefs.ErrCellNameRequired)
	case t.Realm == "":
		return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
	case t.Space == "":
		return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
	case t.Stack == "":
		return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
	}
	return nil
}

// ResolveTerminalTarget turns t into the ContainerDoc of the terminal to
// connect to:
//
//  1. The cell-level liveness guard (GuardCellTaskLiveness) refuses a
//     missing, stopped or diverged cell before the container picker runs,
//     so a missing cell surfaces a "create it first" pointer instead of
//     ErrAttachNoCandidate.
//  2. With no --container, the only non-root Attachable container is
//     picked (ErrAttachNoCandidate / ErrAttachAmbiguous otherwise).
//
// The container-level gates (Attachable, task Running) are enforced by the
// daemon on ConnectContainerTerminal; WrapTerminalConnectError gives those
// refusals their operator-facing wording.
func ResolveTerminalTarget(
	ctx context.Context,
	client kukeonv1.Client,
	t TerminalTarget,
) (v1beta1.ContainerDoc, error) {
	cellGet, err := client.GetCell(ctx, terminalCellDoc(t))
	if err != nil {
		return v1beta1.ContainerDoc{}, err
	}
	if err = GuardCellTaskLiveness(cellGet, t.Cell, t.Realm, t.Space, t.Stack); err != nil {
		return v1beta1.ContainerDoc{}, err
	}

	container := t.Container
	if container == "" {
		container, err = PickContainer(ctx, client, t.Realm, t.Space, t.Stack, t.Cell,
			func(spec v1beta1.ContainerSpec) bool {
				return !spec.Root && spec.Attachable
			})
		if err != nil {
			return v1beta1.ContainerDoc{}, err
		}
	}
	return terminalContainerDoc(container, t), nil
}

// ResolveExecTarget turns t into the ContainerDoc `kuke exec` runs a
// process in (issue #1432). The cell-level liveness guard is the one
// ResolveTerminalTarget applies, so a missing or stopped cell fails with the
// same message as `kuke attach`. With no --container the cell's only
// non-root container is picked, Attachable or not (ErrExecNoCandidate /
// ErrExecAmbiguous otherwise); a named container may be any container of
// the cell, the root one included. Whether the container exists and its
// task is running is decided by the daemon on ExecContainer;
// WrapExecError gives those refusals their operator-facing wording.
func ResolveExecTarget(
	ctx context.Context,
	client kukeonv1.Client,
	t TerminalTarget,
) (v1beta1.ContainerDoc, error) {
	cellGet, err := client.GetCell(ctx, terminalCellDoc(t))
	if err != nil {
		return v1beta1.ContainerDoc{}, err
	}
	if err = GuardCellTaskLiveness(cellGet, t.Cell, t.Realm, t.Space, t.Stack); err != nil {
		return v1beta1.ContainerDoc{}, err
	}

	container := t.Container
	if container == "" {
		container, err = PickContainerWith(ctx, client, t.Realm, t.Space, t.Stack, t.Cell,
			func(spec v1beta1.ContainerSpec) bool { return !spec.Root },
			PickErrors{NoCandidate: errdefs.ErrExecNoCandidate, Ambiguous: errdefs.ErrExecAmbiguous})
		if err != nil {
			return v1beta1.ContainerDoc{}, err
		}
	}
	return terminalContainerDoc(container, t), nil
}

// WrapExecError adds the container and cell to the daemon's not-found
// refusal of ExecContainer, keeping the sentinel in the chain. A refusal
// that already names the container is left alone, so the two are not said
// twice. Other errors pass through unchanged (ErrExecTaskNotRunning already
// names both).
func WrapExecError(err error, doc v1beta1.ContainerDoc) error {
	if !errors.Is(err, errdefs.ErrContainerNotFound) ||
		strings.Contains(err.Error(), strconv.Quote(doc.Metadata.Name)) {
		return err
	}
	return fmt.Errorf("container %q not found in cell %q: %w", doc.Metadata.Name, doc.Spec.CellID, err)
}

// WrapTerminalConnectError adds the container and cell to the daemon's
// Attachable / not-found refusals, keeping the sentinel in the chain.
// Other errors pass through unchanged.
func WrapTerminalConnectError(err error, doc v1beta1.ContainerDoc) error {
	container, cell := doc.Metadata.Name, doc.Spec.CellID
	switch {
	case errors.Is(err, errdefs.ErrAttachNotSupported):
		return fmt.Errorf("container %q in cell %q is not attachable: %w", container, cell, err)
	case errors.Is(err, errdefs.ErrContainerNotFound):
		return fmt.Errorf("container %q not found in cell %q: %w", container, cell, err)
	default:
		return err
	}
}

// terminalCellDoc assembles the lookup CellDoc the liveness guard queries
// via GetCell. Only the identity coordinates are needed — GetCell resolves
// the persisted spec and the live root-task status from them.
func terminalCellDoc(t TerminalTarget) v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   t.Cell,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.CellSpec{
			RealmID: t.Realm,
			SpaceID: t.Space,
			StackID: t.Stack,
		},
	}
}

func terminalContainerDoc(name string, t TerminalTarget) v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      name,
			RealmID: t.Realm,
			SpaceID: t.Space,
			StackID: t.Stack,
			CellID:  t.Cell,
		},
	}
}
