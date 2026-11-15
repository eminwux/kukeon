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
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// errInjectedPurge stands in for any underlying runner failure. It is injected
// directly rather than reproduced through the stack volumes/ residue (#1370) so
// these guards keep exercising the error contract after that trigger is fixed.
var errInjectedPurge = fmt.Errorf("%w: injected purge failure", errdefs.ErrDeleteStack)

func hasPurgeError(purged []string) bool {
	return slices.ContainsFunc(purged, func(p string) bool { return strings.HasPrefix(p, "purge-error:") })
}

// TestPurgeStack_FailurePropagatesError is the regression guard for #1370: a
// failed stack purge used to append "purge-error:..." to Purged and return a
// nil error, so `kuke purge stack` exited 0 and printed "Purged stack". The
// failure must now surface as a non-nil error that still wraps the underlying
// sentinel, with the partial result (and its purge-error entry) inspectable
// and no success markers recorded.
func TestPurgeStack_FailurePropagatesError(t *testing.T) {
	tests := []struct {
		name    string
		cascade bool
		setup   func(f *fakeRunner)
	}{
		{
			name: "comprehensive runner purge fails",
			setup: func(f *fakeRunner) {
				f.PurgeStackFn = func(_ intmodel.Stack) error { return errInjectedPurge }
			},
		},
		{
			name:    "standard delete fails during cascade",
			cascade: true,
			setup: func(f *fakeRunner) {
				f.DeleteStackFn = func(_ intmodel.Stack) error { return errInjectedPurge }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := buildTestStack("test-stack", "test-realm", "test-space")
			f := &fakeRunner{
				GetStackFn:    func(_ intmodel.Stack) (intmodel.Stack, error) { return existing, nil },
				ListCellsFn:   func(_, _, _ string) ([]intmodel.Cell, error) { return nil, nil },
				DeleteStackFn: func(_ intmodel.Stack) error { return nil },
				PurgeStackFn:  func(_ intmodel.Stack) error { return nil },
			}
			tt.setup(f)

			ctrl := setupTestController(t, f)
			result, err := ctrl.PurgeStack(existing, false, tt.cascade)
			if err == nil {
				t.Fatal("PurgeStack: want non-nil error when the purge fails, got nil")
			}
			if !errors.Is(err, errdefs.ErrDeleteStack) {
				t.Errorf("PurgeStack error must wrap the underlying sentinel; got %v", err)
			}
			if result.Stack.Metadata.Name != "test-stack" {
				t.Errorf("partial result must carry the stack; got %q", result.Stack.Metadata.Name)
			}
			if !hasPurgeError(result.Purged) {
				t.Errorf("partial result must record the purge-error entry; got %v", result.Purged)
			}
			if slices.Contains(result.Purged, "all-metadata") || slices.Contains(result.Deleted, "metadata") {
				t.Errorf("failed purge must not record success markers; Deleted=%v Purged=%v",
					result.Deleted, result.Purged)
			}
		})
	}
}

// TestPurgeSpace_FailurePropagatesError pins the same contract on the sibling
// space purge, both for its own runner purge failing and for a child stack
// failing mid-cascade.
func TestPurgeSpace_FailurePropagatesError(t *testing.T) {
	tests := []struct {
		name    string
		cascade bool
		setup   func(f *fakeRunner)
	}{
		{
			name: "comprehensive runner purge fails",
			setup: func(f *fakeRunner) {
				f.PurgeSpaceFn = func(_ intmodel.Space) error { return errInjectedPurge }
			},
		},
		{
			name:    "child stack purge fails during cascade",
			cascade: true,
			setup: func(f *fakeRunner) {
				stack := buildTestStack("child", "test-realm", "test-space")
				f.ListStacksFn = func(_, _ string) ([]intmodel.Stack, error) {
					return []intmodel.Stack{stack}, nil
				}
				f.PurgeStackFn = func(_ intmodel.Stack) error { return errInjectedPurge }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := buildTestSpace("test-space", "test-realm")
			deleteSpaceCalled := false
			f := &fakeRunner{
				GetSpaceFn:    func(_ intmodel.Space) (intmodel.Space, error) { return existing, nil },
				ListStacksFn:  func(_, _ string) ([]intmodel.Stack, error) { return nil, nil },
				ListCellsFn:   func(_, _, _ string) ([]intmodel.Cell, error) { return nil, nil },
				DeleteStackFn: func(_ intmodel.Stack) error { return nil },
				PurgeStackFn:  func(_ intmodel.Stack) error { return nil },
				DeleteSpaceFn: func(_ intmodel.Space) error {
					deleteSpaceCalled = true
					return nil
				},
				PurgeSpaceFn: func(_ intmodel.Space) error { return nil },
			}
			tt.setup(f)

			ctrl := setupTestController(t, f)
			result, err := ctrl.PurgeSpace(existing, false, tt.cascade)
			if err == nil {
				t.Fatal("PurgeSpace: want non-nil error when the purge fails, got nil")
			}
			if !errors.Is(err, errdefs.ErrDeleteStack) {
				t.Errorf("PurgeSpace error must wrap the underlying sentinel; got %v", err)
			}
			if result.PurgeSucceeded || result.MetadataDeleted {
				t.Errorf("failed purge must not report success; PurgeSucceeded=%v MetadataDeleted=%v",
					result.PurgeSucceeded, result.MetadataDeleted)
			}
			if !hasPurgeError(result.Purged) {
				t.Errorf("partial result must record the purge-error entry; got %v", result.Purged)
			}
			if tt.cascade && deleteSpaceCalled {
				t.Error("a failed child stack must stop the cascade before the space itself is deleted")
			}
		})
	}
}
