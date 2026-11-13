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

package apischeme_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func sampleDefaults() *ext.ScopeDefaults {
	return &ext.ScopeDefaults{
		Values: map[string]string{"REGION": "eu"},
		Repos:  map[string]ext.CellConfigRepoFill{"src": {URL: "https://x/src.git", Branch: "main"}},
		Secrets: map[string]ext.CellConfigSecretFill{
			"token": {SecretRef: &ext.ContainerSecretRef{Name: "t", Realm: "r"}},
		},
	}
}

// TestScopeDefaults_RoundTrip pins that realm, space and stack spec.defaults
// and realm spec.images survive the external → internal → external trip the
// daemon's metadata writer takes (#1418).
func TestScopeDefaults_RoundTrip(t *testing.T) {
	realm := ext.RealmDoc{
		Metadata: ext.RealmMetadata{Name: "r"},
		Spec: ext.RealmSpec{
			Namespace: "r.kukeon.io",
			Defaults:  sampleDefaults(),
			Images: []ext.RealmImage{
				{Name: "base", Ref: "docker.io/library/alpine:3.20"},
				{Name: "agent", Build: &ext.RealmImageBuild{
					Context: "/src", Dockerfile: "Dockerfile.agent",
					Args: map[string]string{"V": "1"},
				}},
			},
		},
	}
	internal, err := apischeme.ConvertRealmDocToInternal(realm)
	if err != nil {
		t.Fatalf("ConvertRealmDocToInternal: %v", err)
	}
	back, err := apischeme.BuildRealmExternalFromInternal(internal, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(back.Spec.Defaults, realm.Spec.Defaults) ||
		!reflect.DeepEqual(back.Spec.Images, realm.Spec.Images) {
		t.Errorf("realm round trip lost data:\n got %+v %+v\nwant %+v %+v",
			back.Spec.Defaults, back.Spec.Images, realm.Spec.Defaults, realm.Spec.Images)
	}

	space := ext.SpaceDoc{
		Metadata: ext.SpaceMetadata{Name: "s"},
		Spec: ext.SpaceSpec{RealmID: "r", Defaults: &ext.SpaceDefaults{
			Values: map[string]string{"REGION": "us"},
		}},
	}
	spaceInt, err := apischeme.ConvertSpaceDocToInternal(space)
	if err != nil {
		t.Fatalf("ConvertSpaceDocToInternal: %v", err)
	}
	spaceBack, err := apischeme.BuildSpaceExternalFromInternal(spaceInt, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
	}
	if got := spaceBack.Spec.Defaults.Fill().Values["REGION"]; got != "us" {
		t.Errorf("space defaults REGION = %q, want us", got)
	}

	stack := ext.StackDoc{
		Metadata: ext.StackMetadata{Name: "st"},
		Spec:     ext.StackSpec{RealmID: "r", SpaceID: "s", Defaults: sampleDefaults()},
	}
	stackInt, err := apischeme.ConvertStackDocToInternal(stack)
	if err != nil {
		t.Fatalf("ConvertStackDocToInternal: %v", err)
	}
	stackBack, err := apischeme.BuildStackExternalFromInternal(stackInt, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildStackExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(stackBack.Spec.Defaults, stack.Spec.Defaults) {
		t.Errorf("stack defaults round trip = %+v, want %+v", stackBack.Spec.Defaults, stack.Spec.Defaults)
	}
}

func TestConvertRealmDocToInternal_ValidatesImages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		images []ext.RealmImage
		want   string
	}{
		{"missing name", []ext.RealmImage{{Ref: "a"}}, "name is required"},
		{"duplicate", []ext.RealmImage{{Name: "a", Ref: "x"}, {Name: "a", Ref: "y"}}, "duplicate"},
		{"neither", []ext.RealmImage{{Name: "a"}}, "exactly one of ref or build"},
		{"both", []ext.RealmImage{{Name: "a", Ref: "x", Build: &ext.RealmImageBuild{Context: "."}}}, "exactly one"},
		{"no context", []ext.RealmImage{{Name: "a", Build: &ext.RealmImageBuild{}}}, "build.context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := apischeme.ConvertRealmDocToInternal(ext.RealmDoc{
				Metadata: ext.RealmMetadata{Name: "r"},
				Spec:     ext.RealmSpec{Images: tc.images},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
