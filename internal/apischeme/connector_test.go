// Copyright 2026 Emiliano Spinella (eminwux)
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
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// A Secret's readable identity facts (issue #1548) reach the internal model
// trimmed and come back on the get/list view — the only part of a Secret that
// does, next to its type.
func TestSecretConversion_ReadableIdentity(t *testing.T) {
	identity := map[string]string{"account": "work", "hosts": "a.example"}
	internal, err := apischeme.ConvertSecretDocToInternal(ext.SecretDoc{
		Metadata: ext.SecretMetadata{Name: "tok", Realm: "r"},
		Spec:     ext.SecretSpec{Data: "do-not-echo", Connector: " forge ", Identity: identity},
	})
	if err != nil {
		t.Fatalf("ConvertSecretDocToInternal: %v", err)
	}
	if internal.Spec.Connector != "forge" || !reflect.DeepEqual(internal.Spec.Identity, identity) {
		t.Fatalf("internal identity = %q %v", internal.Spec.Connector, internal.Spec.Identity)
	}
	identity["account"] = "changed"
	if internal.Spec.Identity["account"] != "work" {
		t.Fatal("the internal identity aliases the document's map")
	}

	out := apischeme.ConvertSecretToExternal(internal)
	if out.Spec.Connector != "forge" || out.Spec.Identity["account"] != "work" || len(out.Spec.Identity) != 2 {
		t.Fatalf("external identity = %q %v", out.Spec.Connector, out.Spec.Identity)
	}
	if out.Spec.Data != "" || out.Spec.BasicAuth != nil || out.Spec.SSHKey != nil {
		t.Fatalf("external view carries secret material: %+v", out.Spec)
	}
	out.Spec.Identity["account"] = "changed"
	if internal.Spec.Identity["account"] != "work" {
		t.Fatal("the external identity aliases the internal map")
	}
	// An untagged secret reports no identity block at all.
	plain := apischeme.ConvertSecretToExternal(intmodel.Secret{Spec: intmodel.SecretSpec{Data: "x"}})
	if plain.Spec.Connector != "" || plain.Spec.Identity != nil {
		t.Fatalf("untagged secret identity = %q %v", plain.Spec.Connector, plain.Spec.Identity)
	}
}

func identityRef() *ext.ContainerSecretRef {
	return &ext.ContainerSecretRef{Name: "bot-key", Realm: "r", Space: "s"}
}

// git.identityRef names a stored identity at the levels the daemon expands it
// from — a scope default, a CellConfig, one repo — and never on the block a
// container actually runs with.
func TestGitIdentityRef(t *testing.T) {
	layer := &ext.ContainerGit{IdentityRef: identityRef(), Sign: []string{ext.GitSignCommits}}
	internal := apischeme.GitToInternal(layer)
	if internal.IdentityRef == nil || internal.IdentityRef.Name != "bot-key" || internal.IdentityRef.Space != "s" {
		t.Fatalf("internal identityRef = %+v", internal.IdentityRef)
	}
	if got := apischeme.GitToExternal(internal); !reflect.DeepEqual(got, layer) {
		t.Fatalf("round trip = %+v, want %+v", got, layer)
	}
	repo := &ext.RepoGit{IdentityRef: identityRef()}
	if got := apischeme.RepoGitToExternal(apischeme.RepoGitToInternal(repo)); !reflect.DeepEqual(got, repo) {
		t.Fatalf("repo round trip = %+v, want %+v", got, repo)
	}

	if err := apischeme.ValidateGitLayer(layer); err != nil {
		t.Fatalf("ValidateGitLayer: %v", err)
	}
	if err := apischeme.ValidateRepoGit(repo); err != nil {
		t.Fatalf("ValidateRepoGit: %v", err)
	}
	// A rendered container states everything directly.
	if err := apischeme.ValidateGit(&ext.ContainerGit{IdentityRef: identityRef()}); err == nil ||
		!strings.Contains(err.Error(), "git.identityRef is resolved from a CellConfig") {
		t.Fatalf("ValidateGit accepted an identityRef: %v", err)
	}
	for _, bad := range []*ext.ContainerSecretRef{{Realm: "r"}, {Name: "bot-key"}} {
		if err := apischeme.ValidateGitLayer(&ext.ContainerGit{IdentityRef: bad}); err == nil ||
			!strings.Contains(err.Error(), "git.identityRef") {
			t.Fatalf("ValidateGitLayer(%+v) = %v", bad, err)
		}
		if err := apischeme.ValidateRepoGit(&ext.RepoGit{IdentityRef: bad}); err == nil ||
			!strings.Contains(err.Error(), "git.identityRef") {
			t.Fatalf("ValidateRepoGit(%+v) = %v", bad, err)
		}
	}

	// A cell document cannot carry one on a container or on a repo.
	cell := func(spec ext.ContainerSpec) ext.CellDoc {
		spec.ID, spec.Image = "work", "registry.example.com/img:1"
		return ext.CellDoc{
			APIVersion: ext.APIVersionV1Beta1, Kind: ext.KindCell,
			Metadata: ext.CellMetadata{Name: "c"},
			Spec: ext.CellSpec{
				ID:         "c",
				RealmID:    "r",
				SpaceID:    "s",
				StackID:    "k",
				Containers: []ext.ContainerSpec{spec},
			},
		}
	}
	for name, spec := range map[string]ext.ContainerSpec{
		"container": {Git: &ext.ContainerGit{IdentityRef: identityRef()}},
		"repo": {Repos: []ext.ContainerRepo{{
			Name: "src", URL: "https://example.test/src.git", Target: "/src",
			Git: &ext.RepoGit{IdentityRef: identityRef()},
		}}},
	} {
		if _, err := apischeme.ConvertCellDocToInternal(cell(spec)); err == nil ||
			!strings.Contains(err.Error(), "git.identityRef") {
			t.Fatalf("%s: a cell document with an identityRef was accepted: %v", name, err)
		}
	}
}

// A Realm's registry credential may name a Secret of the Realm instead of
// carrying a username and password.
func TestRealmRegistryCredentialSecretRef(t *testing.T) {
	realm := func(creds ...ext.RegistryCredentials) ext.RealmDoc {
		return ext.RealmDoc{
			APIVersion: ext.APIVersionV1Beta1, Kind: ext.KindRealm,
			Metadata: ext.RealmMetadata{Name: "ops"},
			Spec:     ext.RealmSpec{Namespace: "ops.kukeon.io", RegistryCredentials: creds},
		}
	}
	internal, err := apischeme.ConvertRealmDocToInternal(realm(
		ext.RegistryCredentials{
			ServerAddress: "ghcr.io",
			SecretRef:     &ext.ContainerSecretRef{Name: " registry-ghcr-io "},
		},
		ext.RegistryCredentials{ServerAddress: "docker.io", Username: "hub", Password: "inline"},
	))
	if err != nil {
		t.Fatalf("ConvertRealmDocToInternal: %v", err)
	}
	creds := internal.Spec.RegistryCredentials
	// The Secret is the Realm's own: the realm is filled in, whatever was written.
	if len(creds) != 2 || creds[0].SecretRef == nil || creds[0].SecretRef.Name != "registry-ghcr-io" ||
		creds[0].SecretRef.Realm != "ops" || creds[0].Username != "" || creds[0].Password != "" {
		t.Fatalf("secret-backed entry = %+v", creds)
	}
	if creds[1].SecretRef != nil || creds[1].Username != "hub" || creds[1].Password != "inline" {
		t.Fatalf("inline entry = %+v", creds[1])
	}
	out, err := apischeme.BuildRealmExternalFromInternal(internal, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal: %v", err)
	}
	got := out.Spec.RegistryCredentials
	if len(got) != 2 || got[0].SecretRef == nil || got[0].SecretRef.Name != "registry-ghcr-io" ||
		got[0].SecretRef.Realm != "ops" || got[1].Username != "hub" {
		t.Fatalf("external credentials = %+v", got)
	}

	for _, tc := range []struct {
		name string
		cred ext.RegistryCredentials
		want string
	}{
		{"unnamed", ext.RegistryCredentials{SecretRef: &ext.ContainerSecretRef{}}, "secretRef.name is required"},
		{
			"another realm",
			ext.RegistryCredentials{SecretRef: &ext.ContainerSecretRef{Name: "x", Realm: "other"}},
			"a registry credential is a Secret of the Realm itself",
		},
		{
			"deeper scope",
			ext.RegistryCredentials{SecretRef: &ext.ContainerSecretRef{Name: "x", Realm: "ops", Space: "s"}},
			"a registry credential is realm-scoped",
		},
		{
			"both forms",
			ext.RegistryCredentials{SecretRef: &ext.ContainerSecretRef{Name: "x"}, Username: "u", Password: "p"},
			"keep only secretRef",
		},
	} {
		if _, err = apischeme.ConvertRealmDocToInternal(realm(tc.cred)); err == nil ||
			!strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "\"p\"") {
			t.Fatalf("%s: error echoes the password: %v", tc.name, err)
		}
	}
}

// The identities a cell was created with (`--connector`) are part of its
// provenance, so a re-render uses them again.
func TestCellProvenance_ConnectorOverrides(t *testing.T) {
	provenance := provenanceFixture()
	provenance.ConnectorOverrides = map[string]ext.ContainerSecretRef{
		"forge": {Name: "work-token", Realm: "default"},
		"scm":   {Name: "bot-key", Realm: "ops", Space: "shared"},
	}
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1, Kind: ext.KindCell,
		Metadata: ext.CellMetadata{Name: "web"},
		Spec: ext.CellSpec{
			ID: "web", RealmID: "default", Containers: []ext.ContainerSpec{}, Provenance: provenance,
		},
	}
	internal, err := apischeme.ConvertCellDocToInternal(input)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	if got := internal.Spec.Provenance.ConnectorOverrides["scm"]; got.Name != "bot-key" || got.Space != "shared" {
		t.Fatalf("internal overrides = %+v", internal.Spec.Provenance.ConnectorOverrides)
	}
	output, err := apischeme.BuildCellExternalFromInternal(internal, apischeme.VersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(output.Spec.Provenance, provenance) {
		t.Fatalf("provenance = %+v, want %+v", output.Spec.Provenance, provenance)
	}
	clone := ext.CloneCellProvenance(provenance)
	clone.ConnectorOverrides["forge"] = ext.ContainerSecretRef{Name: "changed"}
	if provenance.ConnectorOverrides["forge"].Name != "work-token" {
		t.Fatal("CloneCellProvenance aliases the overrides")
	}
}

// A Config's status reports how each connector it requires resolves, on get
// and on list, next to (and independent of) its schedule state.
func TestCellConfigStatus_Connectors(t *testing.T) {
	doc := ext.CellConfigDoc{
		Metadata: ext.CellConfigMetadata{Name: "cfg", Realm: "r", Space: "s", Stack: "k"},
		Spec:     ext.CellConfigSpec{Blueprint: ext.CellConfigBlueprintRef{Name: "bp", Realm: "r"}},
		// A status on the way in is the daemon's, not the author's.
		Status: &ext.CellConfigStatus{Connectors: []ext.ConnectorStatus{{Connector: "stale"}}},
	}
	carrier, err := apischeme.ConvertCellConfigDocToInternal(doc)
	if err != nil {
		t.Fatalf("ConvertCellConfigDocToInternal: %v", err)
	}
	if len(carrier.Connectors) != 0 || strings.Contains(string(carrier.Document), "stale") {
		t.Fatalf("an authored status was stored: %s", carrier.Document)
	}
	plain, err := apischeme.ConvertCellConfigToExternal(carrier)
	if err != nil || plain.Status != nil {
		t.Fatalf("a Config with nothing to report renders a status: %+v, %v", plain.Status, err)
	}

	carrier.Connectors = []ext.ConnectorStatus{
		{
			Connector: "forge", Component: "forge", Delivery: ext.ConnectorDeliveryEnv, Slot: "forge-token",
			State: ext.ConnectorResolved, Required: true, Origin: "realm",
			Identity: &ext.ContainerSecretRef{Name: "work-token", Realm: "r"},
		},
		{Connector: "cloud", Component: "cloud", Delivery: ext.ConnectorDeliveryEnv, State: ext.ConnectorUnresolved},
	}
	want := append([]ext.ConnectorStatus(nil), carrier.Connectors...)
	full, err := apischeme.ConvertCellConfigToExternal(carrier)
	if err != nil {
		t.Fatalf("ConvertCellConfigToExternal: %v", err)
	}
	listed := apischeme.ConvertCellConfigListToExternal([]intmodel.CellConfig{carrier})[0]
	for name, got := range map[string]*ext.CellConfigStatus{"get": full.Status, "list": listed.Status} {
		if got == nil || got.Schedule != nil || !reflect.DeepEqual(got.Connectors, want) {
			t.Fatalf("%s status = %+v", name, got)
		}
		// The view is a copy: editing it never reaches the carrier.
		got.Connectors[0].Identity.Name = "changed"
		if carrier.Connectors[0].Identity.Name != "work-token" {
			t.Fatalf("%s status aliases the carrier", name)
		}
	}

	now := time.Unix(1700000000, 0).UTC()
	carrier.ScheduleStatus = &intmodel.CellConfigScheduleStatus{NextScheduleTime: &now}
	if both := apischeme.ConvertCellConfigMetadataToExternal(carrier).Status; both == nil || both.Schedule == nil ||
		len(both.Connectors) != 2 {
		t.Fatalf("schedule and connectors together = %+v", both)
	}
}
