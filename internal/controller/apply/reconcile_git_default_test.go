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

package apply_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// scopeStoreRunner keeps one stored Realm, Space and Stack and records
// writes, so a test can check both the reconcile action and what was
// persisted. Methods not overridden nil-deref through the embedded interface.
type scopeStoreRunner struct {
	runner.Runner

	realm   intmodel.Realm
	space   intmodel.Space
	stack   intmodel.Stack
	updates int
}

func (f *scopeStoreRunner) GetRealm(intmodel.Realm) (intmodel.Realm, error) { return f.realm, nil }
func (f *scopeStoreRunner) GetSpace(intmodel.Space) (intmodel.Space, error) { return f.space, nil }
func (f *scopeStoreRunner) GetStack(intmodel.Stack) (intmodel.Stack, error) { return f.stack, nil }

func (f *scopeStoreRunner) UpdateRealm(r intmodel.Realm) (intmodel.Realm, error) {
	f.updates++
	f.realm = r
	return r, nil
}

func (f *scopeStoreRunner) UpdateSpace(s intmodel.Space) (intmodel.Space, error) {
	f.updates++
	f.space = s
	return s, nil
}

func (f *scopeStoreRunner) UpdateStack(s intmodel.Stack) (intmodel.Stack, error) {
	f.updates++
	f.stack = s
	return s, nil
}

func newScopeStoreRunner() *scopeStoreRunner {
	return &scopeStoreRunner{
		realm: intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "default"}},
		space: intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: "s"},
			Spec:     intmodel.SpaceSpec{RealmName: "default"},
		},
		stack: intmodel.Stack{
			Metadata: intmodel.StackMetadata{Name: "st"},
			Spec:     intmodel.StackSpec{RealmName: "default", SpaceName: "s"},
		},
	}
}

func wantAction(t *testing.T, step string, res apply.ReconcileResult, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	if res.Action != want {
		t.Errorf("%s: action = %q, want %q", step, res.Action, want)
	}
}

// TestReconcile_GitDefaultIdentityRefOnly pins #1683: the identityRef-only
// spec.defaults.git that scopedefaults.Set writes is persisted on add,
// cleared on remove, and a no-op on re-apply, for Realm, Space and Stack.
func TestReconcile_GitDefaultIdentityRefOnly(t *testing.T) {
	t.Run("realm", func(t *testing.T) {
		f := newScopeStoreRunner()
		desired := f.realm
		desired.Spec.Defaults = &intmodel.ScopeDefaults{Git: identityRefOnlyGit()}

		res, err := apply.ReconcileRealm(f, desired)
		wantAction(t, "add", res, err, "updated")
		if d := f.realm.Spec.Defaults; d == nil || d.Git == nil || d.Git.IdentityRef == nil ||
			d.Git.IdentityRef.Name != "git-ssh" {
			t.Fatalf("add: stored defaults = %+v, want git identityRef git-ssh", d)
		}

		res, err = apply.ReconcileRealm(f, desired)
		wantAction(t, "re-apply", res, err, "unchanged")
		if f.updates != 1 {
			t.Errorf("re-apply wrote again: updates = %d, want 1", f.updates)
		}

		cleared := desired
		cleared.Spec.Defaults = nil
		res, err = apply.ReconcileRealm(f, cleared)
		wantAction(t, "remove", res, err, "updated")
		if f.realm.Spec.Defaults != nil {
			t.Errorf("remove: stored defaults = %+v, want cleared", f.realm.Spec.Defaults)
		}
	})

	t.Run("space", func(t *testing.T) {
		f := newScopeStoreRunner()
		desired := f.space
		desired.Spec.Defaults = &intmodel.SpaceDefaults{Git: identityRefOnlyGit()}

		res, err := apply.ReconcileSpace(f, desired)
		wantAction(t, "add", res, err, "updated")
		if d := f.space.Spec.Defaults; d == nil || d.Git == nil || d.Git.IdentityRef == nil {
			t.Fatalf("add: stored defaults = %+v, want git identityRef", d)
		}
		res, err = apply.ReconcileSpace(f, desired)
		wantAction(t, "re-apply", res, err, "unchanged")

		cleared := desired
		cleared.Spec.Defaults = nil
		res, err = apply.ReconcileSpace(f, cleared)
		wantAction(t, "remove", res, err, "updated")
	})

	t.Run("stack", func(t *testing.T) {
		f := newScopeStoreRunner()
		desired := f.stack
		desired.Spec.Defaults = &intmodel.ScopeDefaults{Git: identityRefOnlyGit()}

		res, err := apply.ReconcileStack(f, desired)
		wantAction(t, "add", res, err, "updated")
		if d := f.stack.Spec.Defaults; d == nil || d.Git == nil || d.Git.IdentityRef == nil {
			t.Fatalf("add: stored defaults = %+v, want git identityRef", d)
		}
		res, err = apply.ReconcileStack(f, desired)
		wantAction(t, "re-apply", res, err, "unchanged")

		cleared := desired
		cleared.Spec.Defaults = nil
		res, err = apply.ReconcileStack(f, cleared)
		wantAction(t, "remove", res, err, "updated")
	})
}
