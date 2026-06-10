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
	"errors"
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

// persistedKukeondCellWithState builds the same persisted cell record as
// persistedKukeondCell but with the root container's live status populated to
// the given state — simulating what fakeRunner.GetCell would observe off
// containerd on the pre-StartCell lookup. Used to drive the issue #1186
// container-start-phase wording: a Ready root means the daemon was already up
// (StartCell no-ops → "already running"); a Stopped root means it must be
// (re)started → "started".
func persistedKukeondCellWithState(image string, state intmodel.ContainerState) intmodel.Cell {
	cell := persistedKukeondCell(image)
	cell.Status.Containers = []intmodel.ContainerStatus{
		{
			ID:    consts.KukeSystemContainerName,
			Name:  consts.KukeSystemContainerName,
			State: state,
		},
	}
	return cell
}

// TestBootstrapCell_IdempotentRerunReportsAlreadyRunning covers issue #1186:
// a healthy re-run where the persisted cell's containers are already running
// must record CellStartedPre=true so the container-start phase renders
// "already running" rather than a bare "started" (which would misstate the
// no-op re-run as a (re)start of the daemon's containers).
func TestBootstrapCell_IdempotentRerunReportsAlreadyRunning(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return persistedKukeondCellWithState(image, intmodel.ContainerStateReady), nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		EnsureCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if !section.CellStartedPre {
		t.Errorf("CellSection.CellStartedPre = false; want true (containers already running on a healthy re-run)")
	}
	if section.CellStarted {
		t.Errorf("CellSection.CellStarted = true; want false (no-op re-run must render \"already running\", not \"started\")")
	}
}

// TestBootstrapCell_RerunWithStoppedContainersReportsStarted is the contrast
// to the already-running case (issue #1186): when the persisted cell's
// containers are not running (a re-run after a crash/stop), StartCell genuinely
// transitions them, so CellStartedPre stays false and the phase renders
// "started".
func TestBootstrapCell_RerunWithStoppedContainersReportsStarted(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return persistedKukeondCellWithState(image, intmodel.ContainerStateStopped), nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return true, nil
		},
		EnsureCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if section.CellStartedPre {
		t.Errorf("CellSection.CellStartedPre = true; want false (containers were not running pre-StartCell)")
	}
	if !section.CellStarted {
		t.Errorf("CellSection.CellStarted = false; want true (StartCell transitioned stopped containers → \"started\")")
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
			// Root is Ready pre — the daemon was up before this drift re-run.
			// The recreate path must still render "started" (issue #1186): the
			// running container is torn down and a fresh one started.
			return persistedKukeondCellWithState(priorImage, intmodel.ContainerStateReady), nil
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
	if section.CellStartedPre {
		t.Errorf("CellSection.CellStartedPre = true; want false (recreate genuinely (re)starts → \"started\")")
	}
	if !section.CellStarted {
		t.Errorf("CellSection.CellStarted = false; want true (drift recreate started a fresh container)")
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

// TestBootstrapCell_DigestDriftRecreatesCell covers issue #915 defect 2:
// a persisted kukeond cell whose root container's anchored snapshot chain
// has diverged from what the same image ref would unpack to today (because
// `kuke build` re-pointed the tag at fresh layers between init runs) must
// be torn down and rebuilt — the ref-string match alone is the trap the
// pre-issue path fell into, and EnsureCell+StartCell would restart the
// stale anchored snapshot.
func TestBootstrapCell_DigestDriftRecreatesCell(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"
	// containerd chainID for the kukeond container's existing snapshot.
	const priorChain = "sha256:700a21b4fa4c2000000000000000000000000000000000000000000000000000"
	// containerd chainID the freshly built image would unpack to today.
	const freshChain = "sha256:23bbde593a4b0000000000000000000000000000000000000000000000000000"
	// The exact containerd ID the runner would derive for the kukeond
	// container (BuildContainerdID(space, stack, cell, container)). Asserted
	// against the runner-call args so a future rename in
	// internal/util/naming surfaces here instead of silently breaking the
	// digest-drift probe.
	const wantContainerdID = "kukeon_kukeon_kukeond_kukeond"
	const wantNamespace = "kuke-system.kukeon.io"

	var (
		recreateCalled        bool
		recreateImage         string
		ensureCalled          bool
		gotContainerNamespace string
		gotContainerdID       string
		gotImageNamespace     string
		gotImageRef           string
		startCallCount        int
		containerChainCalled  int
		imageChainCalled      int
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
		ContainerRootChainIDFn: func(ns, id string) (string, error) {
			containerChainCalled++
			gotContainerNamespace = ns
			gotContainerdID = id
			return priorChain, nil
		},
		ImageChainIDFn: func(ns, ref string) (string, error) {
			imageChainCalled++
			gotImageNamespace = ns
			gotImageRef = ref
			return freshChain, nil
		},
		EnsureCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			ensureCalled = true
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
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if containerChainCalled != 1 {
		t.Errorf("ContainerRootChainID call count = %d; want 1", containerChainCalled)
	}
	if imageChainCalled != 1 {
		t.Errorf("ImageChainID call count = %d; want 1", imageChainCalled)
	}
	if gotContainerNamespace != wantNamespace {
		t.Errorf("ContainerRootChainID namespace = %q; want %q", gotContainerNamespace, wantNamespace)
	}
	if gotContainerdID != wantContainerdID {
		t.Errorf("ContainerRootChainID containerd id = %q; want %q", gotContainerdID, wantContainerdID)
	}
	if gotImageNamespace != wantNamespace {
		t.Errorf("ImageChainID namespace = %q; want %q", gotImageNamespace, wantNamespace)
	}
	if gotImageRef != image {
		t.Errorf("ImageChainID ref = %q; want %q", gotImageRef, image)
	}
	if !recreateCalled {
		t.Fatalf("RecreateCell must be called on digest drift; got Ensure=%v", ensureCalled)
	}
	if ensureCalled {
		t.Errorf("EnsureCell must not be called on the digest-drift branch (would restart the stale snapshot)")
	}
	if recreateImage != image {
		t.Errorf("RecreateCell received image %q; want %q (the desired --kukeond-image)", recreateImage, image)
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
	if section.CellPriorImage != image {
		t.Errorf(
			"CellSection.CellPriorImage = %q; want %q (refs match on the digest-drift path)",
			section.CellPriorImage,
			image,
		)
	}
	if !section.CellRootContainerCreated {
		t.Errorf("CellSection.CellRootContainerCreated = false; want true (drift recreate is a rebuild)")
	}
}

// TestBootstrapCell_MatchingDigestEnsuresInPlace confirms that when the
// chainIDs match (the only-cell-stale path is genuinely not in play), the
// idempotent EnsureCell+StartCell branch still runs. This pins the
// digest-drift probe down so a false-positive recreate never replaces a
// healthy daemon container.
func TestBootstrapCell_MatchingDigestEnsuresInPlace(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"
	const chain = "sha256:aaaaaaaa00000000000000000000000000000000000000000000000000000000"

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
		ContainerRootChainIDFn: func(string, string) (string, error) {
			return chain, nil
		},
		ImageChainIDFn: func(string, string) (string, error) {
			return chain, nil
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
		t.Errorf("RecreateCell must not be called when chainIDs match")
	}
	if !ensureCalled {
		t.Errorf("EnsureCell must be called on the idempotent path")
	}
	if !startCalled {
		t.Errorf("StartCell must be called on the idempotent path")
	}
	if section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = true; want false (chainIDs match)")
	}
}

// TestBootstrapCell_DigestProbeFailureFallsThrough confirms that a probe
// error (containerd hiccup, image missing, snapshotter unreachable) does
// not abort the init — the fallback is the existing-cell EnsureCell path
// so the daemon can still come up. The probe error is a soft signal, not
// a hard fail; the daemon's reconciler will re-derive on the next tick.
func TestBootstrapCell_DigestProbeFailureFallsThrough(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	var (
		recreateCalled bool
		ensureCalled   bool
		startCalled    bool
	)
	probeErr := errors.New("snapshotter unreachable")
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
		ContainerRootChainIDFn: func(string, string) (string, error) {
			return "", probeErr
		},
		// ImageChainIDFn is not set: the ContainerRootChainID error path
		// short-circuits and never reaches the image probe. A second stub
		// would be unreachable — leave it unset so the default "no stub"
		// path on fakeRunner.ImageChainID returns "" + nil. Either way the
		// drift branch never fires.
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
		t.Errorf("RecreateCell must not be called when the probe errored")
	}
	if !ensureCalled {
		t.Errorf("EnsureCell must be called on probe-failure fallback")
	}
	if !startCalled {
		t.Errorf("StartCell must be called on probe-failure fallback")
	}
	if section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = true; want false (probe failed, drift undecided)")
	}
}

// TestBootstrapCell_DigestProbeSkippedWhenRootAbsent confirms the digest
// probe never runs when the root container record is missing — the
// CellMetadataExistsPre branch in bootstrapCell already routes through
// EnsureCell+StartCell (which provisions a fresh container against the
// desired image), so a chainID comparison would be against a non-existent
// snapshot. The guard also keeps the runner calls off the hot path when
// they cannot return a meaningful answer.
func TestBootstrapCell_DigestProbeSkippedWhenRootAbsent(t *testing.T) {
	const image = "docker.io/library/kukeon-local:dev"

	var (
		containerChainCalled int
		imageChainCalled     int
		ensureCalled         bool
	)
	mockRunner := &fakeRunner{
		GetCellFn: func(_ intmodel.Cell) (intmodel.Cell, error) {
			return persistedKukeondCell(image), nil
		},
		ExistsCgroupFn: func(_ any) (bool, error) {
			return true, nil
		},
		ExistsCellRootContainerFn: func(_ intmodel.Cell) (bool, error) {
			return false, nil
		},
		ContainerRootChainIDFn: func(string, string) (string, error) {
			containerChainCalled++
			return "", nil
		},
		ImageChainIDFn: func(string, string) (string, error) {
			imageChainCalled++
			return "", nil
		},
		EnsureCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			ensureCalled = true
			return c, nil
		},
		StartCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			return c, nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	var section controller.CellSection
	if err := ctrl.BootstrapCellForTest(&section, kukeondCellDocForTest(image)); err != nil {
		t.Fatalf("BootstrapCellForTest: %v", err)
	}

	if containerChainCalled != 0 {
		t.Errorf(
			"ContainerRootChainID must not be called when the root container is absent; got %d",
			containerChainCalled,
		)
	}
	if imageChainCalled != 0 {
		t.Errorf("ImageChainID must not be called when the root container is absent; got %d", imageChainCalled)
	}
	if !ensureCalled {
		t.Errorf("EnsureCell must run when the metadata exists but the root container does not")
	}
	if section.CellRecreatedDueToImageDrift {
		t.Errorf("CellSection.CellRecreatedDueToImageDrift = true; want false (probe skipped)")
	}
}
