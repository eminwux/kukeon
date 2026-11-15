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
