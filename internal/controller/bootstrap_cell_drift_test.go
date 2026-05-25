//go:build !integration

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

package controller_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// kukeondCellDocForTest builds a minimal CellDoc carrying a single root
// container with the supplied image, matching the spec keys (realm/space/
// stack/cell + KukeSystemContainerName) that bootstrapCell looks up. Mirrors
// the shape of the production kukeondCellDoc without dragging in the bind-
// mount plumbing the drift test does not exercise.
func kukeondCellDocForTest(image string) *v1beta1.CellDoc {
	return &v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{
			Name: consts.KukeSystemCellName,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: consts.KukeSystemRealmName,
				consts.KukeonSpaceLabelKey: consts.KukeSystemSpaceName,
				consts.KukeonStackLabelKey: consts.KukeSystemStackName,
				consts.KukeonCellLabelKey:  consts.KukeSystemCellName,
			},
		},
		Spec: v1beta1.CellSpec{
			ID:      consts.KukeSystemCellName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
			Containers: []v1beta1.ContainerSpec{
				{
					ID:      consts.KukeSystemContainerName,
					RealmID: consts.KukeSystemRealmName,
					SpaceID: consts.KukeSystemSpaceName,
					StackID: consts.KukeSystemStackName,
					CellID:  consts.KukeSystemCellName,
					Image:   image,
					Command: "/bin/kukeond",
				},
			},
		},
	}
}

// persistedKukeondCell builds the intmodel.Cell that fakeRunner.GetCell
// returns on the pre-lookup, simulating a cell record left behind by a prior
// `kuke init` (or a `kuke daemon reset` that didn't actually purge it). The
// image is parameterized so individual tests can stage drift against a
// freshly built `kuke build` output.
func persistedKukeondCell(image string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: consts.KukeSystemCellName,
		},
		Spec: intmodel.CellSpec{
			ID:        consts.KukeSystemCellName,
			RealmName: consts.KukeSystemRealmName,
			SpaceName: consts.KukeSystemSpaceName,
			StackName: consts.KukeSystemStackName,
			Containers: []intmodel.ContainerSpec{
				{
					ID:        consts.KukeSystemContainerName,
					RealmName: consts.KukeSystemRealmName,
					SpaceName: consts.KukeSystemSpaceName,
					StackName: consts.KukeSystemStackName,
					CellName:  consts.KukeSystemCellName,
					Root:      true,
					Image:     image,
				},
			},
		},
	}
}

// TestBootstrapCell_ImageDriftRecreatesCell covers issue #868: a persisted
// kukeond cell whose root container image diverges from the desired
// `--kukeond-image` must be torn down and rebuilt rather than re-used in
// place via EnsureCell+StartCell.
func TestBootstrapCell_ImageDriftRecreatesCell(t *testing.T) {
	const priorImage = "docker.io/library/kukeon-local:dev@stale"
	const desiredImage = "docker.io/library/kukeon-local:dev@fresh"

	var (
		recreateCalled bool
		recreateImage  string
		ensureCalled   bool
		createCalled   bool
		startCallCount int
	)
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return persistedKukeondCell(priorImage), nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		EnsureCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			ensureCalled = true
			return intmodel.Cell{}, nil
		},
		CreateCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			createCalled = true
			return intmodel.Cell{}, nil
		},
		RecreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			if len(c.Spec.Containers) > 0 {
				recreateImage = c.Spec.Containers[0].Image
			}
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCallCount++
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(desiredImage)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if !recreateCalled {
		t.Fatalf("RecreateCell must be called on image drift; got Ensure=%v Create=%v",
			ensureCalled, createCalled)
	}
	if ensureCalled {
		t.Errorf("EnsureCell must not be called on the drift branch (would restart the stale image)")
	}
	if createCalled {
		t.Errorf("CreateCell must not be called when metadata already exists")
	}
	if recreateImage != desiredImage {
		t.Errorf("RecreateCell received image %q; want %q (the desired --kukeond-image)",
			recreateImage, desiredImage)
	}
	if startCallCount != 0 {
		t.Errorf(
			"StartCell must not be called on the drift branch (RecreateCell runs it internally); got %d calls",
			startCallCount,
		)
	}
	if !section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = false; want true")
	}
	if section.CellPriorImage != priorImage {
		t.Errorf("CellSection.CellPriorImage = %q; want %q", section.CellPriorImage, priorImage)
	}
	if !section.CellRootContainerCreated {
		t.Errorf("CellSection.CellRootContainerCreated = false; want true (drift recreate is a rebuild)")
	}
}

// TestBootstrapCell_SameImageEnsuresInPlace confirms the non-drift idempotent
// path is preserved: matching persisted and desired images route through
// EnsureCell+StartCell, no RecreateCell call.
func TestBootstrapCell_SameImageEnsuresInPlace(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	var (
		recreateCalled bool
		ensureCalled   bool
		startCalled    bool
	)
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return persistedKukeondCell(image), nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		EnsureCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			ensureCalled = true
			return c, nil
		},
		RecreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCalled = true
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if recreateCalled {
		t.Errorf("RecreateCell must not be called when images match")
	}
	if !ensureCalled {
		t.Errorf("EnsureCell must be called on the idempotent path")
	}
	if !startCalled {
		t.Errorf("StartCell must be called on the idempotent path")
	}
	if section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = true; want false (images match)")
	}
}

// TestBootstrapCell_NoPriorMetadataCreatesFresh confirms the fresh-create
// path is untouched: an absent persisted cell record routes through
// CreateCell+StartCell, no drift comparison.
func TestBootstrapCell_NoPriorMetadataCreatesFresh(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	var (
		recreateCalled bool
		ensureCalled   bool
		createCalled   bool
		startCalled    bool
	)
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return false, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return false, nil
		},
		CreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			createCalled = true
			return c, nil
		},
		EnsureCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			ensureCalled = true
			return c, nil
		},
		RecreateCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			recreateCalled = true
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			startCalled = true
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if !createCalled {
		t.Errorf("CreateCell must be called when no prior metadata exists")
	}
	if ensureCalled {
		t.Errorf("EnsureCell must not be called when no prior metadata exists")
	}
	if recreateCalled {
		t.Errorf("RecreateCell must not be called when no prior metadata exists")
	}
	if !startCalled {
		t.Errorf("StartCell must be called on the fresh-create path")
	}
	if section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = true; want false (no prior metadata)")
	}
}
