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
	"errors"
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
	mock.UpdateCellMetadataFn = func(c intmodel.Cell) error {
		if stamped == nil {
			stamped = c.Metadata.Annotations
		}
		return nil
	}

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
	mock.UpdateCellMetadataFn = func(c intmodel.Cell) error {
		stamped = c.Metadata.Annotations
		return nil
	}

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
