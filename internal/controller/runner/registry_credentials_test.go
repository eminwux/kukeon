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
	"reflect"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestRegistryCredentials_ResolvesSecretBackedEntries: a Secret-backed entry
// pulls with the username and password (or token) of its basic-auth Secret,
// read from the Realm's own scope; an inline entry is used as written; an
// entry whose Secret cannot be a registry credential is left out.
func TestRegistryCredentials_ResolvesSecretBackedEntries(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	write := func(name string, spec intmodel.SecretSpec) {
		t.Helper()
		if _, err := r.WriteSecret(intmodel.Secret{
			Metadata: intmodel.SecretMetadata{Name: name, Realm: "default"},
			Spec:     spec,
		}); err != nil {
			t.Fatalf("WriteSecret(%s): %v", name, err)
		}
	}
	basicAuth := func(user, password, token, tag string) intmodel.SecretSpec {
		return intmodel.SecretSpec{
			Type:      intmodel.SecretTypeBasicAuth,
			Connector: tag,
			BasicAuth: &intmodel.SecretBasicAuth{Username: user, Password: password, Token: token},
		}
	}
	write("registry-ghcr", basicAuth("octo", "ghcr-pass", "", "registry"))
	write("registry-token", basicAuth("bot", "", "tok-123", ""))
	write("forge", basicAuth("me", "pw", "", "github"))
	write("opaque", intmodel.SecretSpec{Data: "raw"})

	ref := func(name string) *intmodel.ContainerSecretRef {
		// A reference to another realm is ignored: the lookup is the Realm's.
		return &intmodel.ContainerSecretRef{Name: name, Realm: "elsewhere"}
	}
	realm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "default"},
		Spec: intmodel.RealmSpec{RegistryCredentials: []intmodel.RegistryCredentials{
			{ServerAddress: "ghcr.io", SecretRef: ref("registry-ghcr")},
			{ServerAddress: "legacy.example.com", Username: "old", Password: "inline"},
			{ServerAddress: "registry.example.com", SecretRef: ref("registry-token")},
			{ServerAddress: "wrong-tag.example.com", SecretRef: ref("forge")},
			{ServerAddress: "wrong-type.example.com", SecretRef: ref("opaque")},
			{ServerAddress: "missing.example.com", SecretRef: ref("nope")},
		}},
	}

	want := []ctr.RegistryCredentials{
		{ServerAddress: "ghcr.io", Username: "octo", Password: "ghcr-pass"},
		{ServerAddress: "legacy.example.com", Username: "old", Password: "inline"},
		{ServerAddress: "registry.example.com", Username: "bot", Password: "tok-123"},
	}
	if got := r.registryCredentials(realm); !reflect.DeepEqual(got, want) {
		t.Errorf("registryCredentials() = %+v, want %+v", got, want)
	}
	if got := r.registryCredentials(intmodel.Realm{}); got != nil {
		t.Errorf("registryCredentials(no entries) = %+v, want nil", got)
	}
}

// TestKeepInlinePasswords: an inline entry applied without a password keeps
// the stored one for the same server and username, and nothing else does.
func TestKeepInlinePasswords(t *testing.T) {
	existing := []intmodel.RegistryCredentials{
		{ServerAddress: "ghcr.io", Username: "octo", Password: "stored"},
		{ServerAddress: "docker.io", Username: "me", Password: "other"},
	}
	desired := []intmodel.RegistryCredentials{
		{ServerAddress: "ghcr.io", Username: "octo"},                  // read back and re-applied
		{ServerAddress: "docker.io", Username: "me", Password: "new"}, // rotated
		{ServerAddress: "docker.io", Username: "someone-else"},        // a different login
		{ServerAddress: "quay.io", SecretRef: &intmodel.ContainerSecretRef{Name: "registry-quay"}},
	}
	got := keepInlinePasswords(desired, existing)
	if got[0].Password != "stored" || got[1].Password != "new" || got[2].Password != "" || got[3].Password != "" {
		t.Errorf("keepInlinePasswords() = %+v", got)
	}
	if desired[0].Password != "" {
		t.Error("keepInlinePasswords modified its input")
	}
}
