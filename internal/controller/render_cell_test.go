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

// Tests for the daemon-side CellConfig render (issue #1418): the Realm →
// Space → Stack defaults merge, the preview/create digest contract, the
// provenance annotations, and the defaults / image-digest OutOfSync drift.

package controller_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// renderScope is the mutable Realm/Space/Stack state a render fixture serves.
type renderScope struct {
	realmDefaults *intmodel.ScopeDefaults
	spaceDefaults *intmodel.SpaceDefaults
	stackDefaults *intmodel.ScopeDefaults
	images        []intmodel.RealmImage
	digest        string
}

// renderBlueprint is a blueprint with a parameterised image, a repo slot and
// a secret slot so every fill kind can be merged from a level.
func renderBlueprint() ext.CellBlueprintDoc {
	def := "blueprint-region"
	return ext.CellBlueprintDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellBlueprint,
		Metadata:   ext.CellBlueprintMetadata{Name: "dev", Realm: "kuke-system"},
		Spec: ext.CellBlueprintSpec{
			Parameters: []ext.CellBlueprintParameter{{Name: "REGION", Default: &def}, {Name: "IMAGE"}},
			Cell: ext.BlueprintCellSpec{Containers: []ext.BlueprintContainer{{
				ID:    "main",
				Image: "${IMAGE}",
				Env:   []string{"REGION=${REGION}"},
				Repos: []ext.ContainerRepo{{Name: "project", Target: "/work", Required: true}},
				Secrets: []ext.BlueprintSecretSlot{
					{Name: "token", Mode: ext.BlueprintSecretModeEnv, EnvName: "TOKEN"},
				},
			}}},
		},
	}
}

func renderConfig() ext.CellConfigDoc {
	cfg := sampleConfig()
	cfg.Spec.Values = map[string]string{"IMAGE": "docker.io/library/busybox:latest"}
	return cfg
}

func newRenderRunner(t *testing.T, scope *renderScope, cfg ext.CellConfigDoc) *fakeRunner {
	t.Helper()
	return &fakeRunner{
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, cfg), nil
		},
		GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			return blueprintCarrier(t, renderBlueprint()), nil
		},
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) {
			r.Spec.Namespace = "kuke-system.kukeon.io"
			r.Spec.Defaults = scope.realmDefaults
			r.Spec.Images = scope.images
			r.Metadata.Generation = 3
			return r, nil
		},
		GetSpaceFn: func(s intmodel.Space) (intmodel.Space, error) {
			s.Spec.Defaults = scope.spaceDefaults
			return s, nil
		},
		GetStackFn: func(st intmodel.Stack) (intmodel.Stack, error) {
			st.Spec.Defaults = scope.stackDefaults
			return st, nil
		},
		GetImageFn: func(_, ref string) (ctr.ImageInfo, error) {
			if scope.digest == "" {
				return ctr.ImageInfo{}, errdefs.ErrImageNotFound
			}
			return ctr.ImageInfo{Name: ref, Digest: scope.digest}, nil
		},
		GetCellFn: func(intmodel.Cell) (intmodel.Cell, error) {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		},
		ResolveSpaceCNIConfigPathFn: func(string, string) (string, error) { return "", nil },
	}
}

func valueOrigin(values []ext.RenderedValue, kind, key string) (ext.RenderedValue, bool) {
	for _, v := range values {
		if v.Kind == kind && v.Key == key {
			return v, true
		}
	}
	return ext.RenderedValue{}, false
}

func previewRequest() controller.CellFromConfigRequest {
	return controller.CellFromConfigRequest{
		Config: intmodel.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Name:   "dev-1",
	}
}

// TestPreviewCellFromConfig_MergesScopeDefaults: realm, space and stack
// defaults merge under the Config (deeper wins per key) and every resolved
// value reports its origin; the cell records the origins and per-level stamps.
func TestPreviewCellFromConfig_MergesScopeDefaults(t *testing.T) {
	tokenRef := &intmodel.ContainerSecretRef{Name: "gh-token", Realm: "kuke-system"}
	scope := &renderScope{
		realmDefaults: &intmodel.ScopeDefaults{
			Values:  map[string]string{"REGION": "realm-region"},
			Secrets: map[string]intmodel.SecretFill{"token": {SecretRef: tokenRef}},
		},
		spaceDefaults: &intmodel.SpaceDefaults{Values: map[string]string{"REGION": "space-region"}},
	}
	ctrl := setupTestController(t, newRenderRunner(t, scope, renderConfig()))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	main := preview.Cell.Spec.Containers[0]
	if !containsString(main.Env, "REGION=space-region") {
		t.Errorf("env = %v, want REGION=space-region (space beats realm)", main.Env)
	}
	if len(main.Secrets) != 1 || main.Secrets[0].SecretRef.Name != "gh-token" {
		t.Errorf("secrets = %+v, want the realm-default token", main.Secrets)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindValue, "REGION"); !ok || v.Origin != "space" {
		t.Errorf("REGION origin = %+v, want space", v)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindValue, "IMAGE"); !ok || v.Origin != "config" {
		t.Errorf("IMAGE origin = %+v, want config", v)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindSecret, "token"); !ok || v.Origin != "realm" {
		t.Errorf("token origin = %+v, want realm", v)
	}

	ann := preview.Cell.Metadata.Annotations
	if got := ann[cellconfig.AnnotationRenderDigest]; got != preview.Digest || !strings.HasPrefix(got, "sha256:") {
		t.Errorf("digest annotation = %q, want the preview digest %q", got, preview.Digest)
	}
	if got := ann[cellconfig.AnnotationRenderRealmDefaults]; !strings.HasPrefix(got, "generation=3 hash=") ||
		strings.HasSuffix(got, "hash=none") {
		t.Errorf("realm defaults stamp = %q, want generation=3 with a content hash", got)
	}
	if got := ann[cellconfig.AnnotationRenderStackDefaults]; !strings.HasSuffix(got, "hash=none") {
		t.Errorf("stack defaults stamp = %q, want hash=none (no stack defaults)", got)
	}
	if got := ann[cellconfig.AnnotationRenderOrigins]; !strings.Contains(got, "value:REGION=space") {
		t.Errorf("origins annotation = %q, want value:REGION=space", got)
	}
	for _, key := range []string{
		cellconfig.AnnotationRenderConfigRevision, cellconfig.AnnotationRenderValuesHash,
		cellconfig.AnnotationRenderKukeonVersion,
	} {
		if _, ok := ann[key]; !ok {
			t.Errorf("annotation %s missing", key)
		}
	}
}

// TestPreviewCellFromConfig_DigestStableAndInputSensitive: the same inputs
// render the same digest, and a defaults change that alters the cell moves it.
func TestPreviewCellFromConfig_DigestStableAndInputSensitive(t *testing.T) {
	scope := &renderScope{}
	ctrl := setupTestController(t, newRenderRunner(t, scope, renderConfig()))

	first, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("first preview: %v", err)
	}
	second, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("second preview: %v", err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("digest not stable: %s vs %s", first.Digest, second.Digest)
	}

	scope.stackDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"REGION": "stack-region"}}
	third, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("third preview: %v", err)
	}
	if third.Digest == first.Digest {
		t.Fatal("digest did not change after a stack default changed the rendered env")
	}
}

// TestCreateCellFromConfig_RefusesChangedDigest: a create pinned to an older
// preview's digest is refused before anything is written.
func TestCreateCellFromConfig_RefusesChangedDigest(t *testing.T) {
	scope := &renderScope{}
	mock := newRenderRunner(t, scope, renderConfig())
	ctrl := setupTestController(t, mock)

	req := previewRequest()
	req.ExpectedDigest = "sha256:stale"
	_, err := ctrl.CreateCellFromConfig(req)
	if !errors.Is(err, errdefs.ErrRenderDigestMismatch) {
		t.Fatalf("CreateCellFromConfig error = %v, want ErrRenderDigestMismatch", err)
	}
}

// TestCreateCellFromConfig_RefusesExistingName: an explicit name already in
// scope is refused rather than silently attached to.
func TestCreateCellFromConfig_RefusesExistingName(t *testing.T) {
	mock := newRenderRunner(t, &renderScope{}, renderConfig())
	mock.GetCellFn = func(c intmodel.Cell) (intmodel.Cell, error) { return c, nil }
	ctrl := setupTestController(t, mock)

	_, err := ctrl.CreateCellFromConfig(previewRequest())
	if !errors.Is(err, errdefs.ErrCellAlreadyExists) {
		t.Fatalf("CreateCellFromConfig error = %v, want ErrCellAlreadyExists", err)
	}
}

// TestPreviewCellFromConfig_ResolvesRealmImage: a container whose image names
// a realm image renders with the entry's reference and records its digest.
func TestPreviewCellFromConfig_ResolvesRealmImage(t *testing.T) {
	scope := &renderScope{
		images: []intmodel.RealmImage{{Name: "agent", Build: &intmodel.RealmImageBuild{Context: "."}}},
		digest: "sha256:aaa",
	}
	cfg := renderConfig()
	cfg.Spec.Values["IMAGE"] = "agent"
	ctrl := setupTestController(t, newRenderRunner(t, scope, cfg))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := preview.Cell.Spec.Containers[0].Image; got != "kukeon.internal/kuke-system/agent:latest" {
		t.Errorf("image = %q, want the realm image's built tag", got)
	}
	if got := preview.Cell.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests]; got != "agent=sha256:aaa" {
		t.Errorf("image digests annotation = %q, want agent=sha256:aaa", got)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindImage, "agent"); !ok ||
		v.Value != "kukeon.internal/kuke-system/agent:latest@sha256:aaa" || v.Origin != "realm" {
		t.Errorf("image explain = %+v", v)
	}
}

// renderLiveCell renders the cell the way CreateCellFromConfig persists it.
func renderLiveCell(t *testing.T, ctrl *controller.Exec) intmodel.Cell {
	t.Helper()
	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("render live cell: %v", err)
	}
	live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		t.Fatalf("convert live cell: %v", err)
	}
	return live
}

// TestReconcileCells_OutOfSync_DefaultsChangeNamesLevel: editing a level's
// spec.defaults marks the dependent cell OutOfSync with a reason naming the
// level, even when the edit leaves the rendered spec unchanged.
func TestReconcileCells_OutOfSync_DefaultsChangeNamesLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*renderScope)
		reason string
	}{
		{
			name: "realm",
			mutate: func(s *renderScope) {
				s.realmDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"UNUSED": "x"}}
			},
			reason: "realm defaults changed",
		},
		{
			name: "space",
			mutate: func(s *renderScope) {
				s.spaceDefaults = &intmodel.SpaceDefaults{Values: map[string]string{"REGION": "eu"}}
			},
			reason: "space defaults changed",
		},
		{
			name: "stack",
			mutate: func(s *renderScope) {
				s.stackDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"REGION": "us"}}
			},
			reason: "stack defaults changed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := &renderScope{}
			mock := newRenderRunner(t, scope, renderConfig())
			live := renderLiveCell(t, setupTestController(t, mock))

			tc.mutate(scope)
			mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
				return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
			}
			mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
				return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
			}
			mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
				return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
			}
			mock.ReconcileCellFn = passthroughReconcile
			_, captured := runOutOfSyncHarness(t, live, mock)
			if captured == nil {
				t.Fatal("no OutOfSync write after a defaults change")
			}
			if !captured.Status.OutOfSync || !strings.Contains(captured.Status.OutOfSyncReason, tc.reason) {
				t.Errorf("OutOfSync=%v reason=%q, want reason containing %q",
					captured.Status.OutOfSync, captured.Status.OutOfSyncReason, tc.reason)
			}
		})
	}
}

// TestReconcileCells_OutOfSync_UnchangedDefaultsStaySynced: a re-render with
// the same defaults leaves the cell Synced with no metadata write.
func TestReconcileCells_OutOfSync_UnchangedDefaultsStaySynced(t *testing.T) {
	scope := &renderScope{realmDefaults: &intmodel.ScopeDefaults{Values: map[string]string{"REGION": "eu"}}}
	mock := newRenderRunner(t, scope, renderConfig())
	live := renderLiveCell(t, setupTestController(t, mock))
	mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
	}
	mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
		return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
	}
	mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
		return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
	}
	mock.ReconcileCellFn = passthroughReconcile
	_, captured := runOutOfSyncHarness(t, live, mock)
	if captured != nil {
		t.Errorf("unexpected OutOfSync write: %+v", captured.Status)
	}
}

// TestReconcileCells_OutOfSync_ImageRebuildNamesImage: a realm image rebuilt
// under the same tag (new digest) marks the cell OutOfSync, while an image
// whose digest was unknown at create does not.
func TestReconcileCells_OutOfSync_ImageRebuildNamesImage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		before    string
		after     string
		wantDrift bool
	}{
		{name: "rebuilt", before: "sha256:aaa", after: "sha256:bbb", wantDrift: true},
		{name: "first pull", before: "", after: "sha256:bbb", wantDrift: false},
		{name: "unchanged", before: "sha256:aaa", after: "sha256:aaa", wantDrift: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := &renderScope{
				images: []intmodel.RealmImage{{Name: "agent", Ref: "docker.io/library/agent:1"}},
				digest: tc.before,
			}
			cfg := renderConfig()
			cfg.Spec.Values["IMAGE"] = "agent"
			mock := newRenderRunner(t, scope, cfg)
			live := renderLiveCell(t, setupTestController(t, mock))

			scope.digest = tc.after
			mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
				return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
			}
			mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
				return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
			}
			mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
				return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
			}
			mock.ReconcileCellFn = passthroughReconcile
			_, captured := runOutOfSyncHarness(t, live, mock)
			drifted := captured != nil && captured.Status.OutOfSync &&
				strings.Contains(captured.Status.OutOfSyncReason, "image agent digest changed")
			if drifted != tc.wantDrift {
				status := intmodel.CellStatus{}
				if captured != nil {
					status = captured.Status
				}
				t.Errorf("drift = %v (status %+v), want %v", drifted, status, tc.wantDrift)
			}
		})
	}
}

// TestReconcileCells_OutOfSync_BackfillsUnpulledImageDigest: a registry realm
// image not pulled at render time records an empty digest; the reconciler
// back-fills it once the namespace holds the image, so a later re-pull under
// the same reference is detected as drift.
func TestReconcileCells_OutOfSync_BackfillsUnpulledImageDigest(t *testing.T) {
	scope := &renderScope{
		images: []intmodel.RealmImage{{Name: "agent", Ref: "docker.io/library/agent:1"}},
	}
	cfg := renderConfig()
	cfg.Spec.Values["IMAGE"] = "agent"
	mock := newRenderRunner(t, scope, cfg)
	live := renderLiveCell(t, setupTestController(t, mock))
	if got := live.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests]; got != "agent=" {
		t.Fatalf("unpulled image digests annotation = %q, want agent=", got)
	}

	mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
	}
	mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
		return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
	}
	mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
		return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
	}
	mock.ReconcileCellFn = passthroughReconcile

	// The create pulled the image: the first tick back-fills its digest and
	// leaves the cell Synced.
	scope.digest = "sha256:aaa"
	_, captured := runOutOfSyncHarness(t, live, mock)
	if captured == nil {
		t.Fatal("reconcile did not persist the back-filled digest")
	}
	if got := captured.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests]; got != "agent=sha256:aaa" {
		t.Errorf("back-filled image digests annotation = %q, want agent=sha256:aaa", got)
	}
	if captured.Status.OutOfSync {
		t.Errorf("back-fill marked the cell OutOfSync: %q", captured.Status.OutOfSyncReason)
	}

	// A re-pull under the same reference now drifts.
	backfilled := *captured
	scope.digest = "sha256:bbb"
	mock.UpdateCellMetadataFn = nil
	mock.PatchCellMetadataFn = nil
	_, captured = runOutOfSyncHarness(t, backfilled, mock)
	if captured == nil || !captured.Status.OutOfSync ||
		!strings.Contains(captured.Status.OutOfSyncReason, "image agent digest changed") {
		t.Errorf("re-pull after back-fill not detected as drift: %+v", captured)
	}
}

// TestCreateCellFromConfig_RefusesRealmImageRebuild: a realm image rebuilt
// between preview and create moves the render digest even though the rendered
// spec is unchanged, so ExpectedDigest refuses the create.
func TestCreateCellFromConfig_RefusesRealmImageRebuild(t *testing.T) {
	scope := &renderScope{
		images: []intmodel.RealmImage{{Name: "agent", Build: &intmodel.RealmImageBuild{Context: "."}}},
		digest: "sha256:aaa",
	}
	cfg := renderConfig()
	cfg.Spec.Values["IMAGE"] = "agent"
	ctrl := setupTestController(t, newRenderRunner(t, scope, cfg))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	scope.digest = "sha256:bbb"
	req := previewRequest()
	req.ExpectedDigest = preview.Digest
	if _, err = ctrl.CreateCellFromConfig(req); !errors.Is(err, errdefs.ErrRenderDigestMismatch) {
		t.Fatalf("CreateCellFromConfig error = %v, want ErrRenderDigestMismatch", err)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// passthroughReconcile is a no-op lifecycle reconcile so the OutOfSync pass
// runs against the cell the harness fed in.
func passthroughReconcile(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
	return c, runner.ReconcileOutcome{}, nil
}

// reapplyRenderRunner extends the render fixture with the runner hooks
// StartCell's OutOfSync re-apply drives.
func reapplyRenderRunner(t *testing.T, mock *fakeRunner, live intmodel.Cell) {
	t.Helper()
	mock.GetCellFn = func(intmodel.Cell) (intmodel.Cell, error) { return live, nil }
	mock.ExistsCgroupFn = func(any) (bool, error) { return true, nil }
	mock.ExistsCellRootContainerFn = func(intmodel.Cell) (bool, error) { return true, nil }
	mock.UpdateCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		return intmodel.Cell{}, errors.New("runner.UpdateCell must not run: the rendered spec is unchanged")
	}
}

// TestStartCell_OutOfSyncReapply_DefaultsOnlyRecordsProvenance: a defaults
// edit that leaves the rendered spec unchanged starts the cell on its on-disk
// spec and records the new provenance, so the next reconcile clears OutOfSync.
func TestStartCell_OutOfSyncReapply_DefaultsOnlyRecordsProvenance(t *testing.T) {
	scope := &renderScope{}
	mock := newRenderRunner(t, scope, renderConfig())
	live := renderLiveCell(t, setupTestController(t, mock))
	live.Status = intmodel.CellStatus{
		State: intmodel.CellStateStopped, OutOfSync: true, OutOfSyncReason: "realm defaults changed",
	}
	scope.realmDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"UNUSED": "x"}}

	reapplyRenderRunner(t, mock, live)
	var recreated, started bool
	var stamped map[string]string
	mock.RecreateCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		recreated = true
		return intmodel.Cell{}, errors.New("RecreateCell must not run for a defaults-only change")
	}
	mock.StartCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
		started = true
		c.Status.State = intmodel.CellStateReady
		return c, nil
	}
	// The reapply records the fresh render through the locked patch
	// (issue #1569).
	mock.PatchCellMetadataFn = patchAgainstSnapshot(t, live, func(c intmodel.Cell) error {
		if stamped == nil {
			stamped = c.Metadata.Annotations
		}
		return nil
	})

	ctrl := setupTestController(t, mock)
	res, err := ctrl.StartCell(buildTestCell(live.Metadata.Name, live.Spec.RealmName, live.Spec.SpaceName,
		live.Spec.StackName))
	if err != nil {
		t.Fatalf("StartCell: %v", err)
	}
	if recreated || !started || !res.Started {
		t.Fatalf("recreated=%v started=%v res.Started=%v, want an in-place start", recreated, started, res.Started)
	}
	key := cellconfig.AnnotationRenderRealmDefaults
	if stamped[key] == "" || stamped[key] == live.Metadata.Annotations[key] {
		t.Errorf("realm defaults stamp = %q (was %q), want the new defaults recorded", stamped[key],
			live.Metadata.Annotations[key])
	}
}

// TestStartCell_OutOfSyncReapply_ImageRebuildRecreates: a realm image rebuilt
// under the same tag recreates the cell so its containers run the new image.
func TestStartCell_OutOfSyncReapply_ImageRebuildRecreates(t *testing.T) {
	scope := &renderScope{
		images: []intmodel.RealmImage{{Name: "agent", Ref: "docker.io/library/agent:1"}},
		digest: "sha256:aaa",
	}
	cfg := renderConfig()
	cfg.Spec.Values["IMAGE"] = "agent"
	mock := newRenderRunner(t, scope, cfg)
	live := renderLiveCell(t, setupTestController(t, mock))
	live.Status = intmodel.CellStatus{
		State: intmodel.CellStateStopped, OutOfSync: true, OutOfSyncReason: "image agent digest changed",
	}
	scope.digest = "sha256:bbb"

	reapplyRenderRunner(t, mock, live)
	var recreated bool
	var stamped map[string]string
	mock.RecreateCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
		recreated = true
		c.Status.State = intmodel.CellStateReady
		return c, nil
	}
	mock.StartCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		return intmodel.Cell{}, errors.New("runner.StartCell must not run: RecreateCell owns the start")
	}
	mock.PatchCellMetadataFn = patchAgainstSnapshot(t, live, func(c intmodel.Cell) error {
		stamped = c.Metadata.Annotations
		return nil
	})

	ctrl := setupTestController(t, mock)
	res, err := ctrl.StartCell(buildTestCell(live.Metadata.Name, live.Spec.RealmName, live.Spec.SpaceName,
		live.Spec.StackName))
	if err != nil {
		t.Fatalf("StartCell: %v", err)
	}
	if !recreated || !res.Started {
		t.Fatalf("recreated=%v started=%v, want RecreateCell for a rebuilt image", recreated, res.Started)
	}
	if got := stamped[cellconfig.AnnotationRenderImageDigests]; got != "agent=sha256:bbb" {
		t.Errorf("recorded image digests = %q, want agent=sha256:bbb", got)
	}
}

// runRenderTick runs one reconcile tick over live against the render fixture
// and returns the counters and the cell the OutOfSync patch persisted (nil
// when nothing was written).
func runRenderTick(t *testing.T, mock *fakeRunner, live intmodel.Cell) (controllerResult, *intmodel.Cell) {
	t.Helper()
	mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
	}
	mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
		return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
	}
	mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
		return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
	}
	mock.ReconcileCellFn = passthroughReconcile
	mock.UpdateCellMetadataFn = nil
	mock.PatchCellMetadataFn = nil
	return runOutOfSyncHarness(t, live, mock)
}

func serveConfig(t *testing.T, mock *fakeRunner, cfg ext.CellConfigDoc) {
	t.Helper()
	mock.GetConfigFn = func(intmodel.CellConfig) (intmodel.CellConfig, error) {
		return configCarrier(t, cfg), nil
	}
}

func serveBlueprint(t *testing.T, mock *fakeRunner, bp ext.CellBlueprintDoc) {
	t.Helper()
	mock.GetBlueprintFn = func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		return blueprintCarrier(t, bp), nil
	}
}

// TestRenderCellFromConfig_StampsBlueprintRevision pins AC10: a newly
// rendered cell carries kukeon.io/render-blueprint-revision as
// "<blueprint name>@<hash>".
func TestRenderCellFromConfig_StampsBlueprintRevision(t *testing.T) {
	mock := newRenderRunner(t, &renderScope{}, renderConfig())
	live := renderLiveCell(t, setupTestController(t, mock))
	rev := live.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
	name, hash, ok := strings.Cut(rev, "@")
	if !ok || name != "dev" || len(hash) != 16 {
		t.Errorf("blueprint revision = %q, want dev@<16 hex>", rev)
	}
}

// TestReconcileCells_OutOfSync_NamesTheInputThatMoved covers cases 13 and 14
// (AC11): a blueprint content change under the same name or a Config
// repointed at another blueprint names the blueprint; a Config document
// change says "config changed"; a fill-value change through a level default
// says "values changed". Reasons come in the fixed order. Each edit changes
// the rendered cell: one that does not leaves the cell Synced
// (TestReconcileCells_OutOfSync_RevisionOnlyDriftStaysSynced, issue #1772).
func TestReconcileCells_OutOfSync_NamesTheInputThatMoved(t *testing.T) {
	nextRegion := "blueprint-region-next"
	for _, tc := range []struct {
		name       string
		change     func(t *testing.T, mock *fakeRunner, scope *renderScope)
		wantPrefix string
		wantExact  string
	}{
		{
			name: "blueprint content changed, same name",
			change: func(t *testing.T, mock *fakeRunner, _ *renderScope) {
				bp := renderBlueprint()
				bp.Spec.Parameters[0].Default = &nextRegion
				serveBlueprint(t, mock, bp)
			},
			wantExact: "blueprint dev changed; spec differs at containers[main].env",
		},
		{
			name: "config repointed at another blueprint",
			change: func(t *testing.T, mock *fakeRunner, _ *renderScope) {
				cfg := renderConfig()
				cfg.Spec.Blueprint.Name = "dev-next"
				serveConfig(t, mock, cfg)
				bp := renderBlueprint()
				bp.Metadata.Name = "dev-next"
				bp.Spec.Parameters[0].Default = &nextRegion
				serveBlueprint(t, mock, bp)
			},
			wantPrefix: "blueprint dev-next changed; config changed; ",
		},
		{
			name: "config document changed",
			change: func(t *testing.T, mock *fakeRunner, _ *renderScope) {
				cfg := renderConfig()
				cfg.Spec.Values["REGION"] = "eu"
				serveConfig(t, mock, cfg)
			},
			wantPrefix: "config changed; ",
		},
		{
			name: "fill values changed through a level default",
			change: func(_ *testing.T, _ *fakeRunner, scope *renderScope) {
				scope.spaceDefaults = &intmodel.SpaceDefaults{Values: map[string]string{"REGION": "eu"}}
			},
			wantPrefix: "values changed; space defaults changed; spec differs at ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := &renderScope{}
			mock := newRenderRunner(t, scope, renderConfig())
			live := renderLiveCell(t, setupTestController(t, mock))

			tc.change(t, mock, scope)
			_, captured := runRenderTick(t, mock, live)
			if captured == nil || !captured.Status.OutOfSync {
				t.Fatalf("no OutOfSync verdict persisted: %+v", captured)
			}
			reason := captured.Status.OutOfSyncReason
			if tc.wantExact != "" && reason != tc.wantExact {
				t.Errorf("reason = %q, want %q", reason, tc.wantExact)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(reason, tc.wantPrefix) {
				t.Errorf("reason = %q, want prefix %q", reason, tc.wantPrefix)
			}
		})
	}
}

// TestReconcileCells_OutOfSync_CellWithoutBlueprintStampDoesNotFlap is case
// 15 (AC12): a cell rendered before the blueprint revision existed gets no
// blueprint reason and no spurious OutOfSync, and a Synced one has the stamp
// back-filled, so a later blueprint change is named for it.
func TestReconcileCells_OutOfSync_CellWithoutBlueprintStampDoesNotFlap(t *testing.T) {
	scope := &renderScope{}
	mock := newRenderRunner(t, scope, renderConfig())
	live := renderLiveCell(t, setupTestController(t, mock))
	fresh := live.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
	delete(live.Metadata.Annotations, cellconfig.AnnotationRenderBlueprintRevision)

	// The blueprint changed since, without changing the rendered spec.
	bp := renderBlueprint()
	bp.Spec.Prefix = "renamed"
	serveBlueprint(t, mock, bp)

	res, captured := runRenderTick(t, mock, live)
	if captured == nil {
		t.Fatal("Synced cell without the blueprint stamp was not back-filled")
	}
	if captured.Status.OutOfSync || captured.Status.OutOfSyncReason != "" {
		t.Errorf("unstamped cell flapped: %+v", captured.Status)
	}
	got := captured.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
	if got == "" || got == fresh {
		t.Errorf("back-filled blueprint revision = %q, want the current blueprint's (not %q)", got, fresh)
	}
	if res.CellsUpdated != 1 {
		t.Errorf("CellsUpdated = %d, want 1 for the back-fill", res.CellsUpdated)
	}

	// Once stamped, a blueprint change is named.
	backfilled := *captured
	nextRegion := "blueprint-region-next"
	bp.Spec.Parameters[0].Default = &nextRegion
	serveBlueprint(t, mock, bp)
	_, captured = runRenderTick(t, mock, backfilled)
	want := "blueprint dev changed; spec differs at containers[main].env"
	if captured == nil || captured.Status.OutOfSyncReason != want {
		t.Errorf("after back-fill, blueprint change verdict = %+v, want %q", captured, want)
	}
}

// TestReconcileCells_OutOfSync_RevisionOnlyDriftStaysSynced pins issue #1772:
// a blueprint or Config edit that leaves the rendered cell as it was (the spec
// diff is empty and the render digest matches) keeps the running cell Synced.
// The tick records the new revisions on the cell with a metadata patch and
// never stops, starts or recreates it; the next tick then writes nothing.
func TestReconcileCells_OutOfSync_RevisionOnlyDriftStaysSynced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, mock *fakeRunner)
		// moved are the provenance annotations the edit changes.
		moved []string
	}{
		{
			name: "blueprint content changed, same name",
			change: func(t *testing.T, mock *fakeRunner) {
				bp := renderBlueprint()
				bp.Spec.Prefix = "renamed" // content only; the rendered cell is unchanged
				serveBlueprint(t, mock, bp)
			},
			moved: []string{cellconfig.AnnotationRenderBlueprintRevision},
		},
		{
			name: "config repointed at an identical blueprint",
			change: func(t *testing.T, mock *fakeRunner) {
				cfg := renderConfig()
				cfg.Spec.Blueprint.Name = "dev-next"
				serveConfig(t, mock, cfg)
				bp := renderBlueprint()
				bp.Metadata.Name = "dev-next"
				serveBlueprint(t, mock, bp)
			},
			moved: []string{
				cellconfig.AnnotationRenderBlueprintRevision, cellconfig.AnnotationRenderConfigRevision,
			},
		},
		{
			name: "config document changed only",
			change: func(t *testing.T, mock *fakeRunner) {
				cfg := renderConfig()
				cfg.Metadata.Annotations = map[string]string{"owner": "ops"} // not copied onto the cell
				serveConfig(t, mock, cfg)
			},
			moved: []string{cellconfig.AnnotationRenderConfigRevision},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRenderRunner(t, &renderScope{}, renderConfig())
			ctrl := setupTestController(t, mock)
			live := renderLiveCell(t, ctrl)
			live.Status.State = intmodel.CellStateReady

			tc.change(t, mock)
			fresh := renderLiveCell(t, ctrl)
			digest := cellconfig.AnnotationRenderDigest
			if fresh.Metadata.Annotations[digest] != live.Metadata.Annotations[digest] {
				t.Fatalf("fixture: the edit changed the render digest")
			}
			for _, key := range tc.moved {
				if fresh.Metadata.Annotations[key] == live.Metadata.Annotations[key] {
					t.Fatalf("fixture: the edit left %s unchanged", key)
				}
			}

			restarted := func(verb string) {
				t.Errorf("revision-only drift %s the cell", verb)
			}
			mock.StopCellFn = func(c intmodel.Cell) (intmodel.Cell, error) { restarted("stopped"); return c, nil }
			mock.StartCellFn = func(c intmodel.Cell) (intmodel.Cell, error) { restarted("started"); return c, nil }
			mock.RecreateCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
				restarted("recreated")
				return c, nil
			}

			res, captured := runRenderTick(t, mock, live)
			if captured == nil {
				t.Fatal("new revisions were not recorded on the cell")
			}
			if st := captured.Status; st.OutOfSync || st.OutOfSyncReason != "" || st.OutOfSyncError != "" {
				t.Errorf("revision-only drift marked the cell out of sync: %+v", st)
			}
			if captured.Status.State != intmodel.CellStateReady {
				t.Errorf("state = %v, want Ready kept", captured.Status.State)
			}
			for _, key := range tc.moved {
				if got, want := captured.Metadata.Annotations[key], fresh.Metadata.Annotations[key]; got != want {
					t.Errorf("%s = %q, want the new revision %q", key, got, want)
				}
			}
			if !reflect.DeepEqual(captured.Spec, live.Spec) {
				t.Errorf("metadata-only sync changed the spec:\n got %+v\nwant %+v", captured.Spec, live.Spec)
			}
			if res.CellsUpdated != 1 {
				t.Errorf("CellsUpdated = %d, want 1 for the revision back-fill", res.CellsUpdated)
			}

			// The stamps now match: the next tick has nothing to write.
			res, again := runRenderTick(t, mock, *captured)
			if again != nil || res.CellsUpdated != 0 {
				t.Errorf("second tick wrote %+v (CellsUpdated %d), want no write", again, res.CellsUpdated)
			}
		})
	}
}

// TestReconcileCells_OutOfSync_LegacyConfigRevisionDoesNotFlap: a cell
// rendered before issue #1569 carries the config revision as the hash of the
// stored document bytes. While that document is unchanged the cell stays
// Synced, and the stamp is rewritten in the current form.
func TestReconcileCells_OutOfSync_LegacyConfigRevisionDoesNotFlap(t *testing.T) {
	cfg := renderConfig()
	mock := newRenderRunner(t, &renderScope{}, cfg)
	live := renderLiveCell(t, setupTestController(t, mock))
	current := live.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision]
	sum := sha256.Sum256(configCarrier(t, cfg).Document)
	legacy := hex.EncodeToString(sum[:])[:16]
	if legacy == current {
		t.Fatal("fixture: legacy and current config revisions coincide")
	}
	live.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision] = legacy

	_, captured := runRenderTick(t, mock, live)
	if captured == nil {
		t.Fatal("legacy config revision was not back-filled")
	}
	if captured.Status.OutOfSync {
		t.Errorf("legacy config revision flapped the cell: %q", captured.Status.OutOfSyncReason)
	}
	if got := captured.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision]; got != current {
		t.Errorf("back-filled config revision = %q, want %q", got, current)
	}
}

// TestReconcileCells_OutOfSync_StableAcrossTicks is case 16 (AC13): with
// several reasons present and no input change between two ticks, the reason
// string is identical and the second tick writes nothing.
func TestReconcileCells_OutOfSync_StableAcrossTicks(t *testing.T) {
	scope := &renderScope{}
	mock := newRenderRunner(t, scope, renderConfig())
	live := renderLiveCell(t, setupTestController(t, mock))

	cfg := renderConfig()
	cfg.Metadata.Annotations = map[string]string{"owner": "ops"}
	serveConfig(t, mock, cfg)
	bp := renderBlueprint()
	bp.Spec.Prefix = "renamed"
	serveBlueprint(t, mock, bp)
	scope.realmDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"REGION": "eu"}}
	scope.stackDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"UNUSED": "x"}}

	_, first := runRenderTick(t, mock, live)
	if first == nil || !first.Status.OutOfSync {
		t.Fatalf("first tick: no OutOfSync verdict: %+v", first)
	}
	want := "blueprint dev changed; config changed; values changed; realm defaults changed; " +
		"stack defaults changed; spec differs at "
	if !strings.HasPrefix(first.Status.OutOfSyncReason, want) {
		t.Errorf("reason = %q, want prefix %q", first.Status.OutOfSyncReason, want)
	}

	for i := range 2 {
		res, again := runRenderTick(t, mock, *first)
		if again != nil {
			t.Errorf("tick %d with no input change wrote %+v", i+2, again.Status)
		}
		if res.CellsUpdated != 0 {
			t.Errorf("tick %d CellsUpdated = %d, want 0", i+2, res.CellsUpdated)
		}
	}
}

// TestReconcileCells_OutOfSync_SkipsWhenAnotherWriterGotIn covers D10 / AC7:
// the tick's verdict is patched onto the fresh document only when that
// document still has the generation and annotations the verdict was computed
// from; otherwise the write is skipped with no error and no CellsUpdated.
func TestReconcileCells_OutOfSync_SkipsWhenAnotherWriterGotIn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		moved func(*intmodel.Cell)
	}{
		{name: "generation moved", moved: func(c *intmodel.Cell) { c.Metadata.Generation++ }},
		{name: "annotations moved", moved: func(c *intmodel.Cell) {
			c.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision] = "recorded-by-a-start"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := &renderScope{}
			mock := newRenderRunner(t, scope, renderConfig())
			live := renderLiveCell(t, setupTestController(t, mock))
			live.Metadata.Generation = 4
			scope.realmDefaults = &intmodel.ScopeDefaults{Values: map[string]string{"UNUSED": "x"}}

			runRenderTick(t, mock, live) // wire list fns
			var wrote bool
			mock.UpdateCellMetadataFn = func(intmodel.Cell) error { wrote = true; return nil }
			mock.PatchCellMetadataFn = func(_ intmodel.Cell, mutate func(*intmodel.Cell) bool) error {
				fresh := cloneCellForTest(t, live)
				tc.moved(&fresh)
				if mutate(&fresh) {
					wrote = true
				}
				return nil
			}
			res, err := setupTestController(t, mock).ReconcileCells()
			if err != nil {
				t.Fatalf("ReconcileCells: %v", err)
			}
			if wrote {
				t.Error("the tick wrote its verdict over another writer's update")
			}
			if res.CellsUpdated != 0 || res.CellsErrored != 0 {
				t.Errorf("CellsUpdated=%d CellsErrored=%d, want 0/0", res.CellsUpdated, res.CellsErrored)
			}
		})
	}
}

// targetScopeRunner is a render runner whose Space and Stack defaults depend
// on the level's name, so a test can tell which levels a render merged.
func targetScopeRunner(t *testing.T, cfg ext.CellConfigDoc) *fakeRunner {
	t.Helper()
	mock := newRenderRunner(t, &renderScope{}, cfg)
	mock.GetSpaceFn = func(s intmodel.Space) (intmodel.Space, error) {
		s.Spec.Defaults = &intmodel.SpaceDefaults{Values: map[string]string{"REGION": "space-" + s.Metadata.Name}}
		return s, nil
	}
	mock.GetStackFn = func(st intmodel.Stack) (intmodel.Stack, error) {
		ref := &intmodel.ContainerSecretRef{Name: "token-" + st.Spec.SpaceName + "-" + st.Metadata.Name}
		st.Spec.Defaults = &intmodel.ScopeDefaults{Secrets: map[string]intmodel.SecretFill{"token": {SecretRef: ref}}}
		return st, nil
	}
	return mock
}

// TestPreviewCellFromConfig_TargetScope (#1603): a realm-scoped Config
// renders into the requested space and stack, merges that space's and stack's
// defaults, and keeps the binding ref on the Config's own scope.
func TestPreviewCellFromConfig_TargetScope(t *testing.T) {
	ctrl := setupTestController(t, targetScopeRunner(t, renderConfig()))

	req := previewRequest()
	req.TargetSpace, req.TargetStack = "a", "b"
	preview, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := preview.Cell.Spec; got.RealmID != "kuke-system" || got.SpaceID != "a" || got.StackID != "b" {
		t.Fatalf("scope = %s/%s/%s, want kuke-system/a/b", got.RealmID, got.SpaceID, got.StackID)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindValue, "REGION"); !ok ||
		v.Origin != "space" || v.Value != "space-a" {
		t.Errorf("REGION = %+v, want space-a from the space level", v)
	}
	if v, ok := valueOrigin(preview.Values, ext.RenderKindSecret, "token"); !ok || v.Origin != "stack" {
		t.Errorf("token = %+v, want the stack level", v)
	}
	if secrets := preview.Cell.Spec.Containers[0].Secrets; len(secrets) != 1 ||
		secrets[0].SecretRef.Name != "token-a-b" {
		t.Errorf("secrets = %+v, want the a/b stack default token-a-b", secrets)
	}
	prov := preview.Cell.Spec.Provenance
	if prov == nil || prov.BindingRef.Realm != "kuke-system" || prov.BindingRef.Space != "" ||
		prov.BindingRef.Stack != "" {
		t.Errorf("binding ref = %+v, want the realm-scoped Config", prov)
	}
}

// TestPreviewCellFromConfig_NoTargetKeepsDigest (#1603): no target and an
// explicit default/default target render the same cell as before.
func TestPreviewCellFromConfig_NoTargetKeepsDigest(t *testing.T) {
	ctrl := setupTestController(t, targetScopeRunner(t, renderConfig()))

	plain, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("preview without target: %v", err)
	}
	if got := plain.Cell.Spec; got.SpaceID != "default" || got.StackID != "default" {
		t.Fatalf("scope = %s/%s, want default/default", got.SpaceID, got.StackID)
	}
	req := previewRequest()
	req.TargetSpace, req.TargetStack = "default", "default"
	explicit, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("preview with default target: %v", err)
	}
	if explicit.Digest != plain.Digest {
		t.Errorf("digest with default target = %s, want %s", explicit.Digest, plain.Digest)
	}
	req.TargetSpace, req.TargetStack = "a", "b"
	moved, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("preview with a/b target: %v", err)
	}
	if moved.Digest == plain.Digest {
		t.Error("digest did not change with the target scope")
	}
}

// TestPreviewCellFromConfig_PinnedScopeRefusesOtherTarget (#1603): a
// coordinate the Config sets cannot be moved; naming the same one is fine.
func TestPreviewCellFromConfig_PinnedScopeRefusesOtherTarget(t *testing.T) {
	cfg := renderConfig()
	cfg.Metadata.Space = "team"
	ctrl := setupTestController(t, targetScopeRunner(t, cfg))

	req := previewRequest()
	req.Config.Space = "team"
	req.TargetSpace = "other"
	if _, err := ctrl.PreviewCellFromConfig(req); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("other space: error = %v, want ErrConfig", err)
	}
	req.TargetSpace, req.TargetStack = "team", "b"
	preview, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("pinned space with a free stack: %v", err)
	}
	if got := preview.Cell.Spec; got.SpaceID != "team" || got.StackID != "b" {
		t.Errorf("scope = %s/%s, want team/b", got.SpaceID, got.StackID)
	}

	cfg.Metadata.Stack = "pinned"
	ctrl = setupTestController(t, targetScopeRunner(t, cfg))
	req.TargetStack = "b"
	if _, err = ctrl.PreviewCellFromConfig(req); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("other stack: error = %v, want ErrConfig", err)
	}
	req.TargetSpace = "bad/space"
	req.TargetStack = ""
	cfg.Metadata.Space, cfg.Metadata.Stack = "", ""
	ctrl = setupTestController(t, targetScopeRunner(t, cfg))
	if _, err = ctrl.PreviewCellFromConfig(req); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("invalid space name: error = %v, want ErrConfig", err)
	}
}

// TestPreviewCellFromConfig_AllocatesNameInTargetScope (#1603): an omitted
// name is allocated against the cells of the target scope.
func TestPreviewCellFromConfig_AllocatesNameInTargetScope(t *testing.T) {
	mock := targetScopeRunner(t, renderConfig())
	var probed []string
	mock.GetCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
		probed = append(probed, c.Spec.SpaceName+"/"+c.Spec.StackName)
		if c.Spec.SpaceName == "default" {
			// Every name is taken in the default scope.
			return c, nil
		}
		return intmodel.Cell{}, errdefs.ErrCellNotFound
	}
	ctrl := setupTestController(t, mock)

	req := previewRequest()
	req.Name = ""
	req.TargetSpace, req.TargetStack = "a", "b"
	preview, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if preview.Cell.Metadata.Name == "" {
		t.Fatal("no name allocated")
	}
	for _, scope := range probed {
		if scope != "a/b" {
			t.Errorf("name probed in %s, want only a/b (probes %v)", scope, probed)
		}
	}
}
