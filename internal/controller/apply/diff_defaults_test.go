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
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestDiff_ScopeDefaultsAreCompatible pins #1418: a spec.defaults edit on a
// Realm, Space or Stack (and a realm images edit) is a compatible change that
// `kuke apply` writes through, while nil and empty defaults compare equal.
func TestDiff_ScopeDefaultsAreCompatible(t *testing.T) {
	defaults := &intmodel.ScopeDefaults{Values: map[string]string{"REGION": "eu"}}

	realm := intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r"}}
	withDefaults := realm
	withDefaults.Spec.Defaults = defaults
	withDefaults.Spec.Images = []intmodel.RealmImage{{Name: "base", Ref: "alpine:3.20"}}
	diff := apply.DiffRealm(withDefaults, realm)
	if !diff.HasChanges || diff.ChangeType != apply.ChangeTypeCompatible ||
		len(diff.ChangedFields) != 2 {
		t.Errorf("realm diff = %+v, want compatible spec.defaults + spec.images", diff)
	}
	empty := realm
	empty.Spec.Defaults = &intmodel.ScopeDefaults{Values: map[string]string{}}
	if apply.DiffRealm(empty, realm).HasChanges {
		t.Error("empty and nil realm defaults must compare equal")
	}
	built := realm
	built.Spec.Images = []intmodel.RealmImage{{Name: "agent", Build: &intmodel.RealmImageBuild{Context: "."}}}
	builtEmptyArgs := realm
	builtEmptyArgs.Spec.Images = []intmodel.RealmImage{{Name: "agent", Build: &intmodel.RealmImageBuild{
		Context: ".", Args: map[string]string{},
	}}}
	if apply.DiffRealm(builtEmptyArgs, built).HasChanges {
		t.Error("empty and nil realm image build args must compare equal")
	}

	space := intmodel.Space{Metadata: intmodel.SpaceMetadata{Name: "s"}, Spec: intmodel.SpaceSpec{RealmName: "r"}}
	spaceWith := space
	spaceWith.Spec.Defaults = &intmodel.SpaceDefaults{Values: map[string]string{"REGION": "eu"}}
	if d := apply.DiffSpace(spaceWith, space); !d.HasChanges || d.ChangeType != apply.ChangeTypeCompatible {
		t.Errorf("space diff = %+v, want compatible", d)
	}

	stack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: "st"},
		Spec:     intmodel.StackSpec{RealmName: "r", SpaceName: "s"},
	}
	stackWith := stack
	stackWith.Spec.Defaults = defaults
	if d := apply.DiffStack(stackWith, stack); !d.HasChanges || d.ChangeType != apply.ChangeTypeCompatible {
		t.Errorf("stack diff = %+v, want compatible", d)
	}
}

// identityRefOnlyGit is the block scopedefaults.Set writes for a git
// connector default: nothing but the identity Secret ref (#1683).
func identityRefOnlyGit() *intmodel.ContainerGit {
	return &intmodel.ContainerGit{IdentityRef: diffGitRef("git-ssh")}
}

// TestDiff_ScopeDefaultsGitIdentityRefOnly pins #1683: a spec.defaults.git
// block holding only identityRef is a real default on a Realm, Space and
// Stack, so adding or removing it is a change and re-applying it is not,
// while an explicitly empty `git: {}` still equals none.
func TestDiff_ScopeDefaultsGitIdentityRefOnly(t *testing.T) {
	realm := intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "default"}}
	withGit := realm
	withGit.Spec.Defaults = &intmodel.ScopeDefaults{Git: identityRefOnlyGit()}

	if d := apply.DiffRealm(withGit, realm); !d.HasChanges || d.ChangeType != apply.ChangeTypeCompatible ||
		!hasChangedField(d, "spec.defaults") {
		t.Errorf("realm add identityRef-only git default: diff = %+v, want compatible spec.defaults", d)
	}
	if d := apply.DiffRealm(realm, withGit); !d.HasChanges || !hasChangedField(d, "spec.defaults") {
		t.Errorf("realm remove identityRef-only git default: diff = %+v, want spec.defaults", d)
	}
	again := realm
	again.Spec.Defaults = &intmodel.ScopeDefaults{Git: identityRefOnlyGit()}
	if d := apply.DiffRealm(again, withGit); d.HasChanges {
		t.Errorf("realm re-apply identityRef-only git default: diff = %+v, want none", d)
	}
	other := realm
	other.Spec.Defaults = &intmodel.ScopeDefaults{Git: &intmodel.ContainerGit{IdentityRef: diffGitRef("git-other")}}
	if d := apply.DiffRealm(other, withGit); !d.HasChanges {
		t.Errorf("realm switch git identity: diff = %+v, want spec.defaults", d)
	}
	emptyGit := realm
	emptyGit.Spec.Defaults = &intmodel.ScopeDefaults{Git: &intmodel.ContainerGit{}}
	if d := apply.DiffRealm(emptyGit, realm); d.HasChanges {
		t.Errorf("realm empty git block must equal none, got %+v", d)
	}

	space := intmodel.Space{Metadata: intmodel.SpaceMetadata{Name: "s"}, Spec: intmodel.SpaceSpec{RealmName: "default"}}
	spaceWith := space
	spaceWith.Spec.Defaults = &intmodel.SpaceDefaults{Git: identityRefOnlyGit()}
	if d := apply.DiffSpace(spaceWith, space); !d.HasChanges || !hasChangedField(d, "spec.defaults") {
		t.Errorf("space add identityRef-only git default: diff = %+v, want spec.defaults", d)
	}
	if d := apply.DiffSpace(space, spaceWith); !d.HasChanges || !hasChangedField(d, "spec.defaults") {
		t.Errorf("space remove identityRef-only git default: diff = %+v, want spec.defaults", d)
	}

	stack := intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: "st"},
		Spec:     intmodel.StackSpec{RealmName: "default", SpaceName: "s"},
	}
	stackWith := stack
	stackWith.Spec.Defaults = &intmodel.ScopeDefaults{Git: identityRefOnlyGit()}
	if d := apply.DiffStack(stackWith, stack); !d.HasChanges || !hasChangedField(d, "spec.defaults") {
		t.Errorf("stack add identityRef-only git default: diff = %+v, want spec.defaults", d)
	}
	if d := apply.DiffStack(stack, stackWith); !d.HasChanges || !hasChangedField(d, "spec.defaults") {
		t.Errorf("stack remove identityRef-only git default: diff = %+v, want spec.defaults", d)
	}
}
