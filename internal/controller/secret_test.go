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

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestGetSecret_ReportsNotFoundWithoutError pins the GetRealm-shaped contract:
// an absent secret yields MetadataExists=false and a nil error, never an error.
func TestGetSecret_ReportsNotFoundWithoutError(t *testing.T) {
	mockRunner := &fakeRunner{
		GetSecretFn: func(_ intmodel.Secret) (intmodel.Secret, error) {
			return intmodel.Secret{}, errdefs.ErrSecretNotFound
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("GetSecret() error = %v, want nil", err)
	}
	if res.MetadataExists {
		t.Errorf("MetadataExists = true, want false for absent secret")
	}
}

// TestGetSecret_MetadataOnly confirms a present secret surfaces its metadata
// and never carries the bytes back through the controller.
func TestGetSecret_MetadataOnly(t *testing.T) {
	mockRunner := &fakeRunner{
		GetSecretFn: func(secret intmodel.Secret) (intmodel.Secret, error) {
			return intmodel.Secret{Metadata: secret.Metadata}, nil
		},
	}
	ctrl := setupTestController(t, mockRunner)

	res, err := ctrl.GetSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if !res.MetadataExists {
		t.Fatal("MetadataExists = false, want true")
	}
	if res.Secret.Metadata.Name != "tok" || res.Secret.Metadata.Space != "team-a" {
		t.Errorf("metadata = %+v, want name=tok space=team-a", res.Secret.Metadata)
	}
	if res.Secret.Spec.Data != "" {
		t.Errorf("Spec.Data = %q, want empty", res.Secret.Spec.Data)
	}
}

func TestGetSecret_ValidatesScope(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{})

	tests := []struct {
		name string
		md   intmodel.SecretMetadata
		want error
	}{
		{"missing_name", intmodel.SecretMetadata{Realm: "default"}, errdefs.ErrSecretNameRequired},
		{"missing_realm", intmodel.SecretMetadata{Name: "tok"}, errdefs.ErrSecretRealmRequired},
		{
			"cell_without_stack",
			intmodel.SecretMetadata{Name: "tok", Realm: "default", Space: "s", Cell: "c"},
			errdefs.ErrSecretScopeIncomplete,
		},
		{
			"stack_without_space",
			intmodel.SecretMetadata{Name: "tok", Realm: "default", Stack: "st"},
			errdefs.ErrSecretScopeIncomplete,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ctrl.GetSecret(intmodel.Secret{Metadata: tt.md}); !errors.Is(err, tt.want) {
				t.Errorf("GetSecret() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListSecrets_ValidatesFilter(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{
		ListSecretsFn: func(_, _, _, _ string) ([]intmodel.Secret, error) {
			return nil, nil
		},
	})

	// A set space with an empty realm is an incomplete filter.
	if _, err := ctrl.ListSecrets("", "team-a", "", ""); !errors.Is(err, errdefs.ErrSecretScopeIncomplete) {
		t.Errorf("ListSecrets(empty realm, set space) error = %v, want ErrSecretScopeIncomplete", err)
	}

	// An empty filter is valid (lists across all realms).
	if _, err := ctrl.ListSecrets("", "", "", ""); err != nil {
		t.Errorf("ListSecrets(empty filter) error = %v, want nil", err)
	}
}

func TestListSecrets_PassesScopeThrough(t *testing.T) {
	var gotR, gotS, gotSt, gotC string
	ctrl := setupTestController(t, &fakeRunner{
		ListSecretsFn: func(r, s, st, c string) ([]intmodel.Secret, error) {
			gotR, gotS, gotSt, gotC = r, s, st, c
			return []intmodel.Secret{{Metadata: intmodel.SecretMetadata{Name: "tok", Realm: r}}}, nil
		},
	})

	out, err := ctrl.ListSecrets("default", "team-a", "web", "api")
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}
	if gotR != "default" || gotS != "team-a" || gotSt != "web" || gotC != "api" {
		t.Errorf("scope passed = %q/%q/%q/%q, want default/team-a/web/api", gotR, gotS, gotSt, gotC)
	}
	if len(out) != 1 {
		t.Errorf("len(out) = %d, want 1", len(out))
	}
}

func TestDeleteSecret_ReportsNotFound(t *testing.T) {
	ctrl := setupTestController(t, &fakeRunner{
		DeleteSecretFn: func(_ intmodel.Secret) error {
			return errdefs.ErrSecretNotFound
		},
	})

	res, err := ctrl.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "ghost", Realm: "default"},
	})
	if err == nil {
		t.Fatal("DeleteSecret() error = nil, want not-found error")
	}
	if res.Deleted {
		t.Errorf("Deleted = true, want false on not-found")
	}
}

func TestDeleteSecret_Succeeds(t *testing.T) {
	var deleted intmodel.Secret
	ctrl := setupTestController(t, &fakeRunner{
		DeleteSecretFn: func(s intmodel.Secret) error {
			deleted = s
			return nil
		},
	})

	res, err := ctrl.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}
	if !res.Deleted {
		t.Errorf("Deleted = false, want true")
	}
	if deleted.Metadata.Name != "tok" {
		t.Errorf("runner received name %q, want tok", deleted.Metadata.Name)
	}
}
