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

package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

var errPreflightCgroupReached = errors.New("test: cgroup provisioning reached")

// newPreflightTestExec wires a fake ctr.Client whose NewCgroup records the
// call and fails, so a test can tell "rejected by the parent preflight" (no
// cgroup call) from "passed the preflight and started provisioning".
func newPreflightTestExec(t *testing.T) (*Exec, *int) {
	t.Helper()
	calls := 0
	fake := &deleteCellFakeClient{
		newCgroupFn: func(ctr.CgroupSpec) (*cgroup2.Manager, error) {
			calls++
			return nil, errPreflightCgroupReached
		},
	}
	r := newDeleteCellTestExec(t, fake)
	// Space provisioning writes a CNI conflist and allocates a subnet
	// before the cgroup step; point both at the per-test run path.
	r.cniConf = &cni.Conf{
		CniConfigDir: filepath.Join(r.opts.RunPath, "cni", "config"),
		CniCacheDir:  filepath.Join(r.opts.RunPath, "cni", "cache"),
		CniBinDir:    filepath.Join(r.opts.RunPath, "cni", "bin"),
	}
	r.subnetAllocator = cni.NewDefaultSubnetAllocator(r.opts.RunPath)
	return r, &calls
}

func seedPreflightSpace(t *testing.T, r *Exec, realmName, spaceName string) {
	t.Helper()
	doc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: spaceName},
		Spec:       v1beta1.SpaceSpec{RealmID: realmName},
		Status:     v1beta1.SpaceStatus{State: v1beta1.SpaceStateReady},
	}
	path := fs.SpaceMetadataPath(r.opts.RunPath, realmName, spaceName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir space metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write space metadata: %v", err)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no residue at %s, stat err = %v", path, err)
	}
}

// TestCreateSpace_MissingRealm_NoResidue pins #1374: a space create under an
// absent realm fails before persisting the space document or touching the
// cgroup, on the first and on a repeated invocation, and the rejected
// attempt is not discoverable afterwards.
func TestCreateSpace_MissingRealm_NoResidue(t *testing.T) {
	r, cgroupCalls := newPreflightTestExec(t)
	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "no-parent"},
		Spec:     intmodel.SpaceSpec{RealmName: "missing"},
	}

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := r.CreateSpace(space)
		if !errors.Is(err, errdefs.ErrRealmNotFound) {
			t.Fatalf("attempt %d: CreateSpace error = %v, want ErrRealmNotFound", attempt, err)
		}
	}

	assertAbsent(t, fs.SpaceMetadataPath(r.opts.RunPath, "missing", "no-parent"))
	assertAbsent(t, filepath.Dir(fs.RealmMetadataPath(r.opts.RunPath, "missing")))
	if *cgroupCalls != 0 {
		t.Errorf("rejected space create reached cgroup provisioning %d times", *cgroupCalls)
	}
	if _, err := r.GetSpace(space); !errors.Is(err, errdefs.ErrSpaceNotFound) {
		t.Errorf("GetSpace after rejected create = %v, want ErrSpaceNotFound", err)
	}
	spaces, err := r.ListSpaces("missing")
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	if len(spaces) != 0 {
		t.Errorf("ListSpaces after rejected create returned %d spaces, want 0", len(spaces))
	}
}

// TestCreateSpace_ExistingRealm_PassesPreflight: with the realm present the
// preflight does not obstruct creation — provisioning persists the space
// document and proceeds through CNI config to the cgroup step.
func TestCreateSpace_ExistingRealm_PassesPreflight(t *testing.T) {
	r, cgroupCalls := newPreflightTestExec(t)
	seedDeleteCellRealm(t, r, "present")

	_, err := r.CreateSpace(intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "child"},
		Spec:     intmodel.SpaceSpec{RealmName: "present"},
	})
	if !errors.Is(err, errPreflightCgroupReached) {
		t.Fatalf("CreateSpace error = %v, want provisioning to reach the cgroup step", err)
	}
	if *cgroupCalls != 1 {
		t.Errorf("cgroup provisioning calls = %d, want 1", *cgroupCalls)
	}
	if _, statErr := os.Stat(fs.SpaceMetadataPath(r.opts.RunPath, "present", "child")); statErr != nil {
		t.Errorf("expected provisioning to persist space metadata, stat err = %v", statErr)
	}
}

// TestCreateStack_MissingParent_NoResidue pins #1373: a first-time stack
// create under an absent space (or realm) fails before persisting the stack
// document or creating its cgroup, and a repeated invocation obeys the same
// contract.
func TestCreateStack_MissingParent_NoResidue(t *testing.T) {
	tests := []struct {
		name    string
		seed    func(t *testing.T, r *Exec)
		wantErr error
	}{
		{"missing realm", func(*testing.T, *Exec) {}, errdefs.ErrRealmNotFound},
		{"missing space", func(t *testing.T, r *Exec) { seedDeleteCellRealm(t, r, "audit") }, errdefs.ErrSpaceNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, cgroupCalls := newPreflightTestExec(t)
			tt.seed(t, r)
			stack := intmodel.Stack{
				Metadata: intmodel.StackMetadata{Name: "no-parent"},
				Spec:     intmodel.StackSpec{RealmName: "audit", SpaceName: "missing"},
			}

			for attempt := 1; attempt <= 2; attempt++ {
				_, err := r.CreateStack(stack)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("attempt %d: CreateStack error = %v, want %v", attempt, err, tt.wantErr)
				}
			}

			assertAbsent(t, fs.StackMetadataPath(r.opts.RunPath, "audit", "missing", "no-parent"))
			assertAbsent(t, filepath.Dir(fs.SpaceMetadataPath(r.opts.RunPath, "audit", "missing")))
			if *cgroupCalls != 0 {
				t.Errorf("rejected stack create reached cgroup provisioning %d times", *cgroupCalls)
			}
			if _, err := r.GetStack(stack); !errors.Is(err, errdefs.ErrStackNotFound) {
				t.Errorf("GetStack after rejected create = %v, want ErrStackNotFound", err)
			}
		})
	}
}

// TestCreateStack_ExistingParents_PassesPreflight: with realm and space
// present the preflight does not obstruct creation — provisioning proceeds
// to the cgroup step.
func TestCreateStack_ExistingParents_PassesPreflight(t *testing.T) {
	r, cgroupCalls := newPreflightTestExec(t)
	seedDeleteCellRealm(t, r, "audit")
	seedPreflightSpace(t, r, "audit", "present")

	_, err := r.CreateStack(intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: "child"},
		Spec:     intmodel.StackSpec{RealmName: "audit", SpaceName: "present"},
	})
	if !errors.Is(err, errPreflightCgroupReached) {
		t.Fatalf("CreateStack error = %v, want provisioning to reach the cgroup step", err)
	}
	if *cgroupCalls != 1 {
		t.Errorf("cgroup provisioning calls = %d, want 1", *cgroupCalls)
	}
}
