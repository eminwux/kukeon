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
	"strings"
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
		ListCellsFn: func(_, _, _ string) ([]intmodel.Cell, error) { return nil, nil },
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
		ListCellsFn: func(_, _, _ string) ([]intmodel.Cell, error) { return nil, nil },
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

// cellWithSecretRef builds a one-container cell whose single secret references
// the given scoped secret, for exercising the live-reference delete guard.
func cellWithSecretRef(cellName string, ref intmodel.ContainerSecretRef) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			RealmName: ref.Realm, SpaceName: "team-a", StackName: "web",
			Containers: []intmodel.ContainerSpec{{
				Secrets: []intmodel.ContainerSecret{{Name: "TOKEN", SecretRef: &ref}},
			}},
		},
	}
}

// TestDeleteSecret_RefusesWhenReferenced pins AC5 (issue #623): delete is
// refused while a cell's container references the secret via secretRef, the
// error names the referencing cell, and the runner delete never fires.
func TestDeleteSecret_RefusesWhenReferenced(t *testing.T) {
	var deleteCalled bool
	ctrl := setupTestController(t, &fakeRunner{
		ListCellsFn: func(_, _, _ string) ([]intmodel.Cell, error) {
			return []intmodel.Cell{
				cellWithSecretRef("api", intmodel.ContainerSecretRef{Name: "tok", Realm: "default"}),
			}, nil
		},
		DeleteSecretFn: func(_ intmodel.Secret) error {
			deleteCalled = true
			return nil
		},
	})

	res, err := ctrl.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrSecretInUse) {
		t.Fatalf("DeleteSecret() error = %v, want ErrSecretInUse", err)
	}
	if res.Deleted {
		t.Errorf("Deleted = true, want false when referenced")
	}
	if deleteCalled {
		t.Errorf("runner.DeleteSecret called, want skipped when referenced")
	}
	if !strings.Contains(err.Error(), "api") {
		t.Errorf("error %q does not name the referencing cell", err)
	}
}

// TestDeleteSecret_IgnoresNonMatchingRef confirms a secretRef to a different
// secret (different scope) does not block deletion — the match is by resolved
// storage path, not by name alone.
func TestDeleteSecret_IgnoresNonMatchingRef(t *testing.T) {
	var deleteCalled bool
	ctrl := setupTestController(t, &fakeRunner{
		ListCellsFn: func(_, _, _ string) ([]intmodel.Cell, error) {
			return []intmodel.Cell{
				// Same name, different realm scope → different on-disk path.
				cellWithSecretRef("api", intmodel.ContainerSecretRef{Name: "tok", Realm: "other"}),
			}, nil
		},
		DeleteSecretFn: func(_ intmodel.Secret) error {
			deleteCalled = true
			return nil
		},
	})

	res, err := ctrl.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	})
	if err != nil {
		t.Fatalf("DeleteSecret() error = %v, want nil", err)
	}
	if !res.Deleted || !deleteCalled {
		t.Errorf("Deleted=%v deleteCalled=%v, want both true for non-matching ref", res.Deleted, deleteCalled)
	}
}

// reachableSecretRunner is a fakeRunner whose realm lookups succeed and whose
// WriteSecret records what it was handed.
func reachableSecretRunner(wrote *[]intmodel.Secret) *fakeRunner {
	return &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
		WriteSecretFn: func(secret intmodel.Secret) (bool, error) {
			*wrote = append(*wrote, secret)
			return true, nil
		},
	}
}

// TestCreateSecret_TypedSecretsReachRunner confirms a valid typed secret is
// written with its type and material intact (#1472, step 1).
func TestCreateSecret_TypedSecretsReachRunner(t *testing.T) {
	specs := []intmodel.SecretSpec{
		{Data: "opaque-value"},
		{
			Type:      intmodel.SecretTypeBasicAuth,
			BasicAuth: &intmodel.SecretBasicAuth{Username: "u", Token: "t"},
		},
		{
			Type: intmodel.SecretTypeSSHKey,
			SSHKey: &intmodel.SecretSSHKey{
				PrivateKey: "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nYm9keQ==\n-----END OPENSSH PRIVATE" + " KEY-----\n",
			},
		},
	}
	for _, spec := range specs {
		t.Run(spec.EffectiveType(), func(t *testing.T) {
			var wrote []intmodel.Secret
			ctrl := setupTestController(t, reachableSecretRunner(&wrote))
			res, err := ctrl.CreateSecret(intmodel.Secret{
				Metadata: intmodel.SecretMetadata{Name: "cred", Realm: "default"},
				Spec:     spec,
			})
			if err != nil {
				t.Fatalf("CreateSecret() error = %v", err)
			}
			if !res.Created {
				t.Errorf("Created = false, want true")
			}
			if len(wrote) != 1 || wrote[0].Spec.EffectiveType() != spec.EffectiveType() ||
				wrote[0].Spec.BasicAuth != spec.BasicAuth || wrote[0].Spec.SSHKey != spec.SSHKey {
				t.Errorf("runner received %+v, want the %s spec unchanged", wrote, spec.EffectiveType())
			}
		})
	}
}

// TestCreateSecret_RejectsInvalidSpec confirms the per-type shape is enforced
// before anything is written, and that the type sidecar directory's name is
// reserved.
func TestCreateSecret_RejectsInvalidSpec(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		spec    intmodel.SecretSpec
		wantErr error
	}{
		{
			name:   "basic-auth without credential",
			secret: "cred",
			spec: intmodel.SecretSpec{
				Type:      intmodel.SecretTypeBasicAuth,
				BasicAuth: &intmodel.SecretBasicAuth{Username: "u"},
			},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "ssh-key with opaque data",
			secret:  "cred",
			spec:    intmodel.SecretSpec{Type: intmodel.SecretTypeSSHKey, Data: "x"},
			wantErr: errdefs.ErrSecretTypeFields,
		},
		{
			name:    "unknown type",
			secret:  "cred",
			spec:    intmodel.SecretSpec{Type: "oauth", Data: "x"},
			wantErr: errdefs.ErrSecretTypeUnknown,
		},
		{
			name:    "empty opaque data",
			secret:  "cred",
			spec:    intmodel.SecretSpec{},
			wantErr: errdefs.ErrSecretDataRequired,
		},
		{
			name:    "reserved name",
			secret:  ".types",
			spec:    intmodel.SecretSpec{Data: "x"},
			wantErr: errdefs.ErrSecretReservedName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wrote []intmodel.Secret
			ctrl := setupTestController(t, reachableSecretRunner(&wrote))
			_, err := ctrl.CreateSecret(intmodel.Secret{
				Metadata: intmodel.SecretMetadata{Name: tt.secret, Realm: "default"},
				Spec:     tt.spec,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateSecret() error = %v, want %v", err, tt.wantErr)
			}
			if len(wrote) != 0 {
				t.Errorf("runner.WriteSecret called for an invalid spec")
			}
		})
	}
}

// TestDeleteSecret_RefusesWhenReferencedByGit extends the live-reference guard
// to the secrets a container's git block names (#1472): default credentials,
// the signing key, the allowed-signers secret, and each repo's own credential
// and signing key. Deleting any of them would break the cell's next clone,
// push or signed commit.
func TestDeleteSecret_RefusesWhenReferencedByGit(t *testing.T) {
	ref := func() *intmodel.ContainerSecretRef {
		return &intmodel.ContainerSecretRef{Name: "tok", Realm: "default"}
	}
	other := &intmodel.ContainerSecretRef{Name: "other", Realm: "default"}
	tests := []struct {
		name      string
		container intmodel.ContainerSpec
	}{
		{
			name: "default credential",
			container: intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
				Credentials: []intmodel.GitCredential{
					{Host: "gitlab.com", SecretRef: other},
					{Host: "github.com", SecretRef: ref()},
				},
			}},
		},
		{
			name: "signing key",
			container: intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
				Signing: &intmodel.GitSigning{SecretRef: ref()},
			}},
		},
		{
			name: "allowed signers",
			container: intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
				Signing: &intmodel.GitSigning{SecretRef: other, AllowedSignersRef: ref()},
			}},
		},
		{
			name: "repo credential",
			container: intmodel.ContainerSpec{Repos: []intmodel.ContainerRepo{{
				Name: "src", Git: &intmodel.RepoGit{Credential: &intmodel.GitCredential{SecretRef: ref()}},
			}}},
		},
		{
			name: "repo signing key",
			container: intmodel.ContainerSpec{Repos: []intmodel.ContainerRepo{
				{Name: "plain"},
				{Name: "src", Git: &intmodel.RepoGit{Signing: &intmodel.GitSigning{SecretRef: ref()}}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deleteCalled bool
			ctrl := setupTestController(t, &fakeRunner{
				ListCellsFn: func(_, _, _ string) ([]intmodel.Cell, error) {
					return []intmodel.Cell{{
						Metadata: intmodel.CellMetadata{Name: "api"},
						Spec: intmodel.CellSpec{
							RealmName: "default", SpaceName: "team-a", StackName: "web",
							Containers: []intmodel.ContainerSpec{tt.container},
						},
					}}, nil
				},
				DeleteSecretFn: func(_ intmodel.Secret) error {
					deleteCalled = true
					return nil
				},
			})

			_, err := ctrl.DeleteSecret(intmodel.Secret{
				Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
			})
			if !errors.Is(err, errdefs.ErrSecretInUse) {
				t.Fatalf("DeleteSecret() error = %v, want ErrSecretInUse", err)
			}
			if deleteCalled {
				t.Errorf("runner.DeleteSecret called, want skipped when referenced")
			}
			if !strings.Contains(err.Error(), "default/team-a/web/api") {
				t.Errorf("error %q does not name the referencing cell", err)
			}

			// A secret none of the git blocks name is still deletable.
			res, err := ctrl.DeleteSecret(intmodel.Secret{
				Metadata: intmodel.SecretMetadata{Name: "unreferenced", Realm: "default"},
			})
			if err != nil || !res.Deleted {
				t.Errorf("DeleteSecret(unreferenced) = %+v, %v; want deleted", res, err)
			}
		})
	}
}
