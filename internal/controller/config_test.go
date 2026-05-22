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

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestGetConfig_ReportsNotFoundWithoutError pins the GetBlueprint-shaped
// contract: an absent config yields MetadataExists=false and a nil error.
func TestGetConfig_ReportsNotFoundWithoutError(t *testing.T) {
	mockRunner := &fakeRunner{
		GetConfigFn: func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}
	if res.MetadataExists {
		t.Errorf("MetadataExists = true, want false for absent config")
	}
}

// TestGetConfig_ReturnsFullDocument confirms a present config surfaces its full
// document (a Config carries no credential bytes, so the body is safe to read).
func TestGetConfig_ReturnsFullDocument(t *testing.T) {
	mockRunner := &fakeRunner{
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			return intmodel.CellConfig{Metadata: c.Metadata, Document: []byte("body")}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if !res.MetadataExists {
		t.Fatal("MetadataExists = false, want true")
	}
	if string(res.Config.Document) != "body" {
		t.Errorf("Document = %q, want body", res.Config.Document)
	}
}

func TestGetConfig_ValidatesScope(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	tests := []struct {
		name string
		md   intmodel.CellConfigMetadata
		want error
	}{
		{"missing_name", intmodel.CellConfigMetadata{Realm: "default"}, errdefs.ErrConfigNameRequired},
		{"missing_realm", intmodel.CellConfigMetadata{Name: "web"}, errdefs.ErrConfigRealmRequired},
		{
			"stack_without_space",
			intmodel.CellConfigMetadata{Name: "web", Realm: "default", Stack: "st"},
			errdefs.ErrConfigScopeIncomplete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ctrl.GetConfig(intmodel.CellConfig{Metadata: tt.md}); !errors.Is(err, tt.want) {
				t.Errorf("GetConfig() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestListConfigs_DelegatesAfterFilterValidation confirms the controller
// validates the filter's scope contiguity and forwards the trimmed coordinates
// to the runner (issue #644).
func TestListConfigs_DelegatesAfterFilterValidation(t *testing.T) {
	var gotRealm, gotSpace, gotStack string
	mockRunner := &fakeRunner{
		ListConfigsFn: func(realm, space, stack string) ([]intmodel.CellConfig, error) {
			gotRealm, gotSpace, gotStack = realm, space, stack
			return []intmodel.CellConfig{
				{Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: realm, Space: space}},
			}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	got, err := ctrl.ListConfigs("  default ", "team-a", "")
	if err != nil {
		t.Fatalf("ListConfigs() error = %v", err)
	}
	if gotRealm != "default" || gotSpace != "team-a" || gotStack != "" {
		t.Errorf("runner got (%q,%q,%q), want (default,team-a,)", gotRealm, gotSpace, gotStack)
	}
	if len(got) != 1 || got[0].Metadata.Name != "web" {
		t.Errorf("ListConfigs() = %+v, want one config named web", got)
	}
}

func TestListConfigs_RejectsIncompleteFilter(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	tests := []struct {
		name                string
		realm, space, stack string
	}{
		{"scope_without_realm", "", "team-a", ""},
		{"stack_without_space", "default", "", "web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ctrl.ListConfigs(tt.realm, tt.space, tt.stack); !errors.Is(
				err, errdefs.ErrConfigScopeIncomplete,
			) {
				t.Errorf("ListConfigs() error = %v, want ErrConfigScopeIncomplete", err)
			}
		})
	}
}

func TestDeleteConfig_NotFoundFriendlyError(t *testing.T) {
	mockRunner := &fakeRunner{
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) { return nil, nil },
		DeleteConfigFn: func(intmodel.CellConfig) error {
			return errdefs.ErrConfigNotFound
		},
	}
	ctrl := setupTestController(t, mockRunner)

	_, err := ctrl.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "ghost", Realm: "default"},
	})
	if err == nil || err.Error() != `config "ghost" not found` {
		t.Errorf("DeleteConfig() error = %v, want `config \"ghost\" not found`", err)
	}
}

func TestDeleteConfig_SuccessReportsDeleted(t *testing.T) {
	var gotMeta intmodel.CellConfigMetadata
	mockRunner := &fakeRunner{
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) { return nil, nil },
		DeleteConfigFn: func(c intmodel.CellConfig) error {
			gotMeta = c.Metadata
			return nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("DeleteConfig() error = %v", err)
	}
	if !res.Deleted {
		t.Error("Deleted = false, want true")
	}
	if len(res.BackRefCells) != 0 {
		t.Errorf("BackRefCells = %v, want empty", res.BackRefCells)
	}
	if gotMeta.Name != "web" || gotMeta.Realm != "default" || gotMeta.Space != "team-a" {
		t.Errorf("runner got metadata %+v, want web/default/team-a", gotMeta)
	}
}

// TestDeleteConfig_ReportsBackRefCells pins the AC's back-reference notice: a
// live cell carrying the kukeon.io/config label to this config (within scope)
// is surfaced in BackRefCells — informational, never a refusal, so the delete
// still succeeds.
func TestDeleteConfig_ReportsBackRefCells(t *testing.T) {
	mockRunner := &fakeRunner{
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) {
			return []intmodel.Cell{
				{
					Metadata: intmodel.CellMetadata{
						Name:   "web",
						Labels: map[string]string{cellconfig.LabelConfig: "web"},
					},
					Spec: intmodel.CellSpec{
						RealmName: "default", SpaceName: "team-a", StackName: "api",
					},
				},
				{ // different config name — must not match
					Metadata: intmodel.CellMetadata{
						Name:   "other",
						Labels: map[string]string{cellconfig.LabelConfig: "elsewhere"},
					},
					Spec: intmodel.CellSpec{RealmName: "default", SpaceName: "team-a", StackName: "api"},
				},
				{ // right label value, wrong realm — out of scope, must not match
					Metadata: intmodel.CellMetadata{
						Name:   "web",
						Labels: map[string]string{cellconfig.LabelConfig: "web"},
					},
					Spec: intmodel.CellSpec{RealmName: "other-realm", SpaceName: "team-a", StackName: "api"},
				},
			}, nil
		},
		DeleteConfigFn: func(intmodel.CellConfig) error { return nil },
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Name: "web", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("DeleteConfig() error = %v", err)
	}
	if !res.Deleted {
		t.Error("Deleted = false, want true (notice is informational, never a refusal)")
	}
	if len(res.BackRefCells) != 1 || res.BackRefCells[0] != "default/team-a/api/web" {
		t.Errorf("BackRefCells = %v, want [default/team-a/api/web]", res.BackRefCells)
	}
}

func TestDeleteConfig_ValidatesScope(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	if _, err := ctrl.DeleteConfig(intmodel.CellConfig{
		Metadata: intmodel.CellConfigMetadata{Realm: "default"},
	}); !errors.Is(err, errdefs.ErrConfigNameRequired) {
		t.Errorf("DeleteConfig() error = %v, want ErrConfigNameRequired", err)
	}
}
