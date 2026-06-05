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
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestStatusCmd_Structure pins the user-facing surface — section names,
// flags, and SilenceErrors so the structured report (text or JSON) on
// stdout is what the operator reads, not cobra's auto-printed sentinel.
func TestStatusCmd_Structure(t *testing.T) {
	cmd := NewStatusCmd()
	if cmd.Use != "status" {
		t.Errorf("Use=%q want status", cmd.Use)
	}
	for _, flag := range []string{"json", "verbose"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected %q flag", flag)
		}
	}
	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage=true")
	}
	if !cmd.SilenceErrors {
		t.Error("expected SilenceErrors=true so the structured report carries the verdict")
	}
}

// TestRunChecks_AllHealthy is the smoke happy-path: every check returns
// OK, the report's ok flag is true, and the human renderer's bottom line
// reads "Status: OK". Tests the full pipeline (runChecks → render) so a
// regression in either side surfaces here.
func TestRunChecks_AllHealthy(t *testing.T) {
	dir := setupHealthyHost(t)
	rc := &runCtx{
		daemonHost:       "unix:///run/test/kukeond.sock",
		runPath:          dir.runPath,
		containerdSocket: "/run/test/containerd.sock",
		cgroupRoot:       dir.cgroupRoot,
		cniBinDir:        dir.cniBinDir,
		logger:           testLogger(),
		daemonClient:     newFakeClient().withDefaultRealms(),
		localClient:      newFakeClient().withDefaultRealms(),
		ctrClient:        &fakeCtrClient{namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io"}},
	}

	report := runChecks(context.Background(), rc)
	if !report.OK {
		t.Errorf("expected OK report; got FAIL\n%s", renderToString(report, true))
	}

	seenSections := map[string]bool{}
	for _, c := range report.Checks {
		seenSections[c.Section] = true
		if c.Status == StatusFAIL {
			t.Errorf("unexpected FAIL row: %+v", c)
		}
	}
	for _, want := range []string{sectionDaemon, sectionHost, sectionState, sectionStorage, sectionParity} {
		if !seenSections[want] {
			t.Errorf("section %q not represented in report", want)
		}
	}
}

// TestCheckParity_DivergenceIsFAIL pins the regression-guard contract —
// when the daemon and in-process branches disagree on a realm's
// existence, the parity row is FAIL and Detail names the divergent side
// and resource. This is the exact replacement for AGENTS.md's
// dev-init.sh manual `kuke get realms` vs `--no-daemon` diff.
func TestCheckParity_DivergenceIsFAIL(t *testing.T) {
	daemon := newFakeClient().withRealms("default", "kuke-system", "extra")
	local := newFakeClient().withRealms("default", "kuke-system")

	rc := &runCtx{
		daemonClient: daemon,
		localClient:  local,
	}

	results := checkParity(context.Background(), rc)
	if len(results) == 0 {
		t.Fatal("expected at least one parity row")
	}
	realmRow := results[0]
	if realmRow.Name != "realms" {
		t.Fatalf("first parity row should be realms; got %q", realmRow.Name)
	}
	if realmRow.Status != StatusFAIL {
		t.Errorf("expected FAIL on divergent realms; got %s", realmRow.Status)
	}
	if !strings.Contains(realmRow.Detail, "extra") {
		t.Errorf("Detail should name the divergent realm; got %q", realmRow.Detail)
	}
	if !strings.Contains(realmRow.Detail, "daemon-only") {
		t.Errorf("Detail should label the divergent side; got %q", realmRow.Detail)
	}
}

// TestCheckParity_DaemonDownIsWARN documents the degraded path: when the
// daemon couldn't be dialed, the parity walk surfaces a single WARN row
// rather than FAIL (the underlying cause already shows up in the daemon
// section), and the JSON shape stays stable.
func TestCheckParity_DaemonDownIsWARN(t *testing.T) {
	rc := &runCtx{
		daemonClient: nil,
		localClient:  newFakeClient().withDefaultRealms(),
	}
	results := checkParity(context.Background(), rc)
	if len(results) != 1 {
		t.Fatalf("expected exactly one degraded parity row; got %d", len(results))
	}
	if results[0].Status != StatusWARN {
		t.Errorf("expected WARN on daemon-down parity; got %s", results[0].Status)
	}
	if !strings.Contains(results[0].Detail, "daemon not reachable") {
		t.Errorf("Detail should name daemon-down cause; got %q", results[0].Detail)
	}
}

// TestCheckParity_NestedDescent confirms the walk recurses into the
// realm-set intersection (spaces / stacks / cells / containers) — a
// regression in the descent loop would silently stop reporting nested
// kinds.
func TestCheckParity_NestedDescent(t *testing.T) {
	daemon := newFakeClient().
		withRealms("default").
		withSpaces("default", "ns-a").
		withStacks("default", "ns-a", "stack-a").
		withCells("default", "ns-a", "stack-a", "cell-a").
		withContainers("default", "ns-a", "stack-a", "cell-a", "main")
	local := newFakeClient().
		withRealms("default").
		withSpaces("default", "ns-a").
		withStacks("default", "ns-a", "stack-a").
		withCells("default", "ns-a", "stack-a", "cell-a").
		withContainers("default", "ns-a", "stack-a", "cell-a", "main")

	rc := &runCtx{daemonClient: daemon, localClient: local}
	results := checkParity(context.Background(), rc)

	var names []string
	for _, r := range results {
		names = append(names, r.Name)
	}
	wantNames := []string{
		"realms",
		"spaces/default",
		"stacks/default/ns-a",
		"cells/default/ns-a/stack-a",
		"containers/default/ns-a/stack-a/cell-a",
		"secrets",
		"blueprints",
		"configs",
	}
	for _, want := range wantNames {
		if !containsStr(names, want) {
			t.Errorf("parity walk missing %q; got %v", want, names)
		}
	}
}

// TestCheckHostCgroupV2 exercises the three branches of the cgroup-v2
// probe — present-with-controllers, present-but-empty (WARN), and absent
// (FAIL) — against a temp dir so the test doesn't depend on the test
// host's actual /sys/fs/cgroup.
func TestCheckHostCgroupV2(t *testing.T) {
	t.Run("controllers present", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "cgroup.controllers"), []byte("cpu memory io pids\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rc := &runCtx{cgroupRoot: dir}
		r := checkHostCgroupV2(rc)
		if r.Status != StatusOK {
			t.Errorf("expected OK; got %s (%s)", r.Status, r.Detail)
		}
		if !strings.Contains(r.Detail, "4 controllers") {
			t.Errorf("Detail should name controller count; got %q", r.Detail)
		}
	})
	t.Run("empty controllers", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "cgroup.controllers"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		rc := &runCtx{cgroupRoot: dir}
		r := checkHostCgroupV2(rc)
		if r.Status != StatusWARN {
			t.Errorf("expected WARN on empty controllers; got %s", r.Status)
		}
	})
	t.Run("not mounted", func(t *testing.T) {
		dir := t.TempDir()
		rc := &runCtx{cgroupRoot: filepath.Join(dir, "missing")}
		r := checkHostCgroupV2(rc)
		if r.Status != StatusFAIL {
			t.Errorf("expected FAIL when cgroup root absent; got %s", r.Status)
		}
	})
}

// TestCheckHostCNIPlugins covers the binary-present / binary-missing
// split. The status check stats each name in requiredCNIPlugins; a
// missing one demotes the row to FAIL with the missing name surfaced.
func TestCheckHostCNIPlugins(t *testing.T) {
	plugins := requiredCNIPlugins()
	t.Run("all present", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range plugins {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rc := &runCtx{cniBinDir: dir}
		r := checkHostCNIPlugins(rc)
		if r.Status != StatusOK {
			t.Errorf("expected OK; got %s (%s)", r.Status, r.Detail)
		}
	})
	t.Run("missing one", func(t *testing.T) {
		dir := t.TempDir()
		// Lay only the first plugin so the second is missing.
		if err := os.WriteFile(filepath.Join(dir, plugins[0]), []byte(""), 0o755); err != nil {
			t.Fatal(err)
		}
		rc := &runCtx{cniBinDir: dir}
		r := checkHostCNIPlugins(rc)
		if r.Status != StatusFAIL {
			t.Errorf("expected FAIL when a plugin is missing; got %s", r.Status)
		}
		if !strings.Contains(r.Detail, plugins[1]) {
			t.Errorf("Detail should name the missing plugin; got %q", r.Detail)
		}
	})
}

// TestCheckStateRunDir_Orphans confirms a non-canonical entry under the
// run-socket dir surfaces as a WARN row naming the orphan.
func TestCheckStateRunDir_Orphans(t *testing.T) {
	dir := t.TempDir()
	for _, expected := range []string{"kukeond.sock", "kukeond.pid", "s", "tty"} {
		if err := os.WriteFile(filepath.Join(dir, expected), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.sock"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Trigger the non-default-runPath branch of canonicalRunSocketDir
	// by setting runPath to the temp dir.
	rc := &runCtx{runPath: dir}
	r := checkStateRunDir(rc)
	if r.Status != StatusWARN {
		t.Errorf("expected WARN with stale.sock orphan; got %s (%s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "stale.sock") {
		t.Errorf("Detail should name the orphan; got %q", r.Detail)
	}
}

// TestCheckStateRunDir_AbsentDirIsOK covers the bare-host case: a host
// that never ran `kuke init` has no /run/kukeon, and we don't want
// false-positive WARN/FAIL rows for an environment that legitimately
// hasn't been initialized.
func TestCheckStateRunDir_AbsentDirIsOK(t *testing.T) {
	rc := &runCtx{runPath: filepath.Join(t.TempDir(), "never-existed")}
	r := checkStateRunDir(rc)
	if r.Status != StatusOK {
		t.Errorf("expected OK on absent run dir; got %s (%s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "not present") {
		t.Errorf("Detail should say 'not present'; got %q", r.Detail)
	}
}

// TestRunChecks_JSONShape confirms the JSON form is stable enough for
// CI integration — `ok` is a boolean, every check has the four required
// string fields, and Status renders as the human label.
func TestRunChecks_JSONShape(t *testing.T) {
	rc := &runCtx{
		daemonClient: nil, // daemon down → FAIL
		localClient:  newFakeClient().withDefaultRealms(),
		ctrClient:    &fakeCtrClient{connectErr: nil, namespaces: nil},
		cgroupRoot:   filepath.Join(t.TempDir(), "missing"),
		cniBinDir:    filepath.Join(t.TempDir(), "missing"),
		runPath:      t.TempDir(),
		logger:       testLogger(),
	}
	report := runChecks(context.Background(), rc)

	var buf bytes.Buffer
	if err := renderJSON(&buf, report); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON output is malformed: %v\n%s", err, buf.String())
	}
	if okField, _ := parsed["ok"].(bool); okField {
		t.Errorf("expected ok=false with daemon down; got true")
	}
	checks, ok := parsed["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("expected non-empty checks array; got %v", parsed["checks"])
	}
	first, ok := checks[0].(map[string]any)
	if !ok {
		t.Fatalf("first check is not an object: %v", checks[0])
	}
	for _, field := range []string{"section", "name", "status"} {
		if _, present := first[field]; !present {
			t.Errorf("check missing required field %q", field)
		}
	}
	if s, _ := first["status"].(string); s != "FAIL" {
		t.Errorf("expected daemon row status=FAIL; got %v", first["status"])
	}
}

// TestRenderText_RemediationVisibility pins the verbose / non-verbose
// branching: WARN/FAIL rows always show the remediation hint; OK rows
// show it only with --verbose.
func TestRenderText_RemediationVisibility(t *testing.T) {
	report := Report{
		OK: false,
		Checks: []Result{
			{
				Section: "x", Name: "ok-row", Status: StatusOK, Detail: "all fine",
				Remediation: "would-suggest",
			},
			{
				Section: "x", Name: "fail-row", Status: StatusFAIL, Detail: "broken",
				Remediation: "fix-now",
			},
		},
	}
	t.Run("non-verbose hides OK remediation", func(t *testing.T) {
		var buf bytes.Buffer
		renderText(&buf, report, false)
		out := buf.String()
		if strings.Contains(out, "would-suggest") {
			t.Error("non-verbose mode should hide OK-row remediation")
		}
		if !strings.Contains(out, "fix-now") {
			t.Error("FAIL-row remediation must always be visible")
		}
	})
	t.Run("verbose surfaces OK remediation", func(t *testing.T) {
		var buf bytes.Buffer
		renderText(&buf, report, true)
		if !strings.Contains(buf.String(), "would-suggest") {
			t.Error("verbose mode should surface OK-row remediation")
		}
	})
}

// TestCommandExitCodeFAIL drives the cobra command end-to-end with a
// mock runCtx injected via MockRunCtxKey, then asserts the command
// returns a non-nil error so cobra propagates a non-zero exit.
func TestCommandExitCodeFAIL(t *testing.T) {
	rc := &runCtx{
		daemonClient: nil, // → daemon FAIL row → overall FAIL
		localClient:  newFakeClient().withDefaultRealms(),
		ctrClient:    &fakeCtrClient{},
		cgroupRoot:   t.TempDir(), // no cgroup.controllers → FAIL
		cniBinDir:    t.TempDir(),
		runPath:      t.TempDir(),
		logger:       testLogger(),
	}

	cmd := NewStatusCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	ctx := context.WithValue(context.Background(), types.CtxLogger, testLogger())
	ctx = context.WithValue(ctx, MockRunCtxKey{}, rc)
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error so cobra exits non-zero")
	}
	if !strings.Contains(buf.String(), "Status: FAIL") {
		t.Errorf("expected report tail 'Status: FAIL'; got:\n%s", buf.String())
	}
}

// TestCommandExitCodeOK is the success-side companion to the above —
// when every row is OK, the command returns nil and the report tail
// reads "Status: OK".
func TestCommandExitCodeOK(t *testing.T) {
	dir := setupHealthyHost(t)
	rc := &runCtx{
		daemonClient:     newFakeClient().withDefaultRealms(),
		localClient:      newFakeClient().withDefaultRealms(),
		ctrClient:        &fakeCtrClient{namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io"}},
		cgroupRoot:       dir.cgroupRoot,
		cniBinDir:        dir.cniBinDir,
		runPath:          dir.runPath,
		daemonHost:       "unix:///run/test/kukeond.sock",
		containerdSocket: "/run/test/containerd.sock",
		logger:           testLogger(),
	}

	cmd := NewStatusCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	ctx := context.WithValue(context.Background(), types.CtxLogger, testLogger())
	ctx = context.WithValue(ctx, MockRunCtxKey{}, rc)
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected nil error on healthy host; got %v\nReport:\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Status: OK") {
		t.Errorf("expected report tail 'Status: OK'; got:\n%s", buf.String())
	}
}

// ---- Test helpers ----

// healthyHost wires the three temp paths a happy-path run reads.
type healthyHost struct {
	runPath    string
	cgroupRoot string
	cniBinDir  string
}

func setupHealthyHost(t *testing.T) healthyHost {
	t.Helper()

	cgroupRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.controllers"),
		[]byte("cpu memory io pids cpuset"), 0o644); err != nil {
		t.Fatal(err)
	}

	cniBinDir := t.TempDir()
	for _, name := range requiredCNIPlugins() {
		if err := os.WriteFile(filepath.Join(cniBinDir, name), []byte{}, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runPath := t.TempDir()
	for _, expected := range []string{"kukeond.sock", "kukeond.pid", "s", "tty"} {
		if err := os.WriteFile(filepath.Join(runPath, expected), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return healthyHost{runPath: runPath, cgroupRoot: cgroupRoot, cniBinDir: cniBinDir}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

func renderToString(report Report, verbose bool) string {
	var buf bytes.Buffer
	renderText(&buf, report, verbose)
	return buf.String()
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// ---- fakeClient: kukeonv1.Client mock for the status tests ----

type fakeClient struct {
	kukeonv1.FakeClient

	realms     []v1beta1.RealmDoc
	spaces     map[string][]v1beta1.SpaceDoc                      // realm
	stacks     map[string]map[string][]v1beta1.StackDoc           // realm/space
	cells      map[string]map[string]map[string][]v1beta1.CellDoc // realm/space/stack
	containers map[string][]v1beta1.ContainerSpec                 // realm/space/stack/cell key

	secrets    []v1beta1.SecretDoc
	blueprints []v1beta1.CellBlueprintDoc
	configs    []v1beta1.CellConfigDoc

	version    string
	pingErr    error
	versionErr error
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		spaces:     map[string][]v1beta1.SpaceDoc{},
		stacks:     map[string]map[string][]v1beta1.StackDoc{},
		cells:      map[string]map[string]map[string][]v1beta1.CellDoc{},
		containers: map[string][]v1beta1.ContainerSpec{},
		version:    "test-version",
	}
}

// withDefaultRealms seeds the two realms `kuke init` provisions. Used by
// every happy-path test that just needs a non-empty realm list.
func (f *fakeClient) withDefaultRealms() *fakeClient {
	return f.withRealms("default", "kuke-system")
}

func (f *fakeClient) withRealms(names ...string) *fakeClient {
	for _, name := range names {
		f.realms = append(f.realms, v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{Name: name},
		})
	}
	return f
}

func (f *fakeClient) withSpaces(realm string, names ...string) *fakeClient {
	for _, name := range names {
		f.spaces[realm] = append(f.spaces[realm], v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{Name: name},
		})
	}
	return f
}

func (f *fakeClient) withStacks(realm, space string, names ...string) *fakeClient {
	if f.stacks[realm] == nil {
		f.stacks[realm] = map[string][]v1beta1.StackDoc{}
	}
	for _, name := range names {
		f.stacks[realm][space] = append(f.stacks[realm][space], v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: name},
		})
	}
	return f
}

func (f *fakeClient) withCells(realm, space, stack string, names ...string) *fakeClient {
	if f.cells[realm] == nil {
		f.cells[realm] = map[string]map[string][]v1beta1.CellDoc{}
	}
	if f.cells[realm][space] == nil {
		f.cells[realm][space] = map[string][]v1beta1.CellDoc{}
	}
	for _, name := range names {
		f.cells[realm][space][stack] = append(f.cells[realm][space][stack], v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: name},
		})
	}
	return f
}

func (f *fakeClient) withContainers(realm, space, stack, cell string, names ...string) *fakeClient {
	key := strings.Join([]string{realm, space, stack, cell}, "/")
	for _, name := range names {
		f.containers[key] = append(f.containers[key], v1beta1.ContainerSpec{
			ID: name,
		})
	}
	return f
}

func (f *fakeClient) Close() error { return nil }

func (f *fakeClient) Ping(_ context.Context) error { return f.pingErr }

func (f *fakeClient) PingVersion(_ context.Context) (string, error) {
	if f.versionErr != nil {
		return "", f.versionErr
	}
	return f.version, nil
}

func (f *fakeClient) ListRealms(_ context.Context) ([]v1beta1.RealmDoc, error) {
	return f.realms, nil
}

func (f *fakeClient) ListSpaces(_ context.Context, realm string) ([]v1beta1.SpaceDoc, error) {
	return f.spaces[realm], nil
}

func (f *fakeClient) ListStacks(_ context.Context, realm, space string) ([]v1beta1.StackDoc, error) {
	if f.stacks[realm] == nil {
		return nil, nil
	}
	return f.stacks[realm][space], nil
}

func (f *fakeClient) ListCells(_ context.Context, realm, space, stack string) ([]v1beta1.CellDoc, error) {
	if f.cells[realm] == nil || f.cells[realm][space] == nil {
		return nil, nil
	}
	return f.cells[realm][space][stack], nil
}

func (f *fakeClient) ListContainers(
	_ context.Context, realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	return f.containers[strings.Join([]string{realm, space, stack, cell}, "/")], nil
}

func (f *fakeClient) ListSecrets(_ context.Context, _, _, _, _ string) ([]v1beta1.SecretDoc, error) {
	return f.secrets, nil
}

func (f *fakeClient) ListBlueprints(_ context.Context, _, _, _ string) ([]v1beta1.CellBlueprintDoc, error) {
	return f.blueprints, nil
}

func (f *fakeClient) ListConfigs(_ context.Context, _, _, _ string) ([]v1beta1.CellConfigDoc, error) {
	return f.configs, nil
}

// ---- fakeCtrClient: ctrConn mock for the host/state tests ----
//
// Status narrowed the ctr surface to a three-method ctrConn interface
// (Connect, Close, ListNamespaces) precisely so this test fake stays
// trivial — no embedded stub, no method explosion.

type fakeCtrClient struct {
	connectErr error
	namespaces []string
	// storage seeds NamespaceStorage's response keyed by containerd
	// namespace (e.g. "default.kukeon.io"). A missing key means
	// NamespaceStorage returns a zero StorageStats for that namespace
	// — the no-leak case the storage section reads as OK.
	storage map[string]ctr.StorageStats
	// storageErr seeds NamespaceStorage's error path; a non-nil value
	// fails every call, mirroring the metadata-store-unreachable
	// branch the storage check demotes to WARN.
	storageErr error
}

func (f *fakeCtrClient) Connect() error { return f.connectErr }
func (f *fakeCtrClient) Close() error   { return nil }
func (f *fakeCtrClient) ListNamespaces() ([]string, error) {
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	return f.namespaces, nil
}
func (f *fakeCtrClient) NamespaceStorage(ns string) (ctr.StorageStats, error) {
	if f.storageErr != nil {
		return ctr.StorageStats{}, f.storageErr
	}
	return f.storage[ns], nil
}
