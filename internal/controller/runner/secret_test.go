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

//nolint:testpackage // tests the unexported WriteSecret path against a temp RunPath
package runner

import (
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestWriteSecret_CreatesRootOnlyFile pins the issue #619 storage contract:
// the bytes land at <RunPath>/data/<scope>/secrets/<name>, the file is 0o600,
// the secrets/ directory is 0o700, and the first write reports created=true.
func TestWriteSecret_CreatesRootOnlyFile(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "anthropic-token", Realm: "kuke-system"},
		Spec:     intmodel.SecretSpec{Data: "s3cr3t-bytes"},
	}

	created, err := r.WriteSecret(secret)
	if err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first write")
	}

	path := fs.SecretPath(runPath, "kuke-system", "", "", "", "anthropic-token")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written secret: %v", err)
	}
	if string(got) != "s3cr3t-bytes" {
		t.Errorf("secret bytes = %q, want %q", got, "s3cr3t-bytes")
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("secret file mode = %o, want 600", perm)
	}

	dirInfo, err := os.Stat(fs.SecretsDir(runPath, "kuke-system", "", "", ""))
	if err != nil {
		t.Fatalf("stat secrets dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("secrets dir mode = %o, want 700", perm)
	}
}

// TestWriteSecret_OverwriteReportsUpdated confirms a re-apply overwrites the
// bytes and reports created=false (the write-through "updated" path).
func TestWriteSecret_OverwriteReportsUpdated(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "v1"},
	}
	if _, err := r.WriteSecret(secret); err != nil {
		t.Fatalf("first WriteSecret() error = %v", err)
	}

	secret.Spec.Data = "v2"
	created, err := r.WriteSecret(secret)
	if err != nil {
		t.Fatalf("second WriteSecret() error = %v", err)
	}
	if created {
		t.Errorf("created = true, want false on overwrite")
	}

	path := fs.SecretPath(runPath, "default", "", "", "", "tok")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading overwritten secret: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("secret bytes = %q, want %q", got, "v2")
	}
}

// TestWriteSecret_DeeperScopeNestsUnderScopeDir confirms a space-scoped secret
// lands under the space metadata dir, not the realm dir — so scope teardown
// (os.RemoveAll on the scope dir) reclaims it.
func TestWriteSecret_DeeperScopeNestsUnderScopeDir(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "db-pw", Realm: "default", Space: "team-a"},
		Spec:     intmodel.SecretSpec{Data: "x"},
	}
	if _, err := r.WriteSecret(secret); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}

	spaceScoped := fs.SecretPath(runPath, "default", "team-a", "", "", "db-pw")
	if _, err := os.Stat(spaceScoped); err != nil {
		t.Errorf("space-scoped secret not found at %s: %v", spaceScoped, err)
	}
	realmScoped := fs.SecretPath(runPath, "default", "", "", "", "db-pw")
	if _, err := os.Stat(realmScoped); !os.IsNotExist(err) {
		t.Errorf("secret leaked into realm scope at %s (err=%v)", realmScoped, err)
	}
}

// TestGetSecret_MetadataOnly confirms GetSecret returns the requested secret's
// scope coordinates without ever reading the bytes, and reports
// ErrSecretNotFound for an absent name (issue #622).
func TestGetSecret_MetadataOnly(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	stored := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default", Space: "team-a"},
		Spec:     intmodel.SecretSpec{Data: "do-not-echo"},
	}
	if _, err := r.WriteSecret(stored); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}

	got, err := r.GetSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default", Space: "team-a"},
	})
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got.Metadata.Name != "tok" || got.Metadata.Realm != "default" || got.Metadata.Space != "team-a" {
		t.Errorf("metadata = %+v, want name=tok realm=default space=team-a", got.Metadata)
	}
	if got.Spec.Data != "" {
		t.Errorf("Spec.Data = %q, want empty (bytes must never be echoed)", got.Spec.Data)
	}
}

func TestGetSecret_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	_, err := r.GetSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrSecretNotFound) {
		t.Errorf("GetSecret() error = %v, want ErrSecretNotFound", err)
	}
}

// TestListSecrets_SubtreeFilter pins the subtree-filter list semantics: an
// empty filter lists every scope, a set realm bounds the walk to that realm
// (and everything nested under it), and a set deeper coordinate excludes
// shallower scopes. The bytes are never read; only scope + name surface.
func TestListSecrets_SubtreeFilter(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seed := []intmodel.Secret{
		{Metadata: intmodel.SecretMetadata{Name: "realm-tok", Realm: "default"}},
		{Metadata: intmodel.SecretMetadata{Name: "space-tok", Realm: "default", Space: "team-a"}},
		{
			Metadata: intmodel.SecretMetadata{
				Name:  "cell-tok",
				Realm: "default",
				Space: "team-a",
				Stack: "web",
				Cell:  "api",
			},
		},
		{Metadata: intmodel.SecretMetadata{Name: "other-realm", Realm: "kuke-system"}},
	}
	for _, s := range seed {
		s.Spec = intmodel.SecretSpec{Data: "x"}
		if _, err := r.WriteSecret(s); err != nil {
			t.Fatalf("WriteSecret(%s) error = %v", s.Metadata.Name, err)
		}
	}

	names := func(secrets []intmodel.Secret) []string {
		got := make([]string, 0, len(secrets))
		for _, s := range secrets {
			got = append(got, s.Metadata.Name)
		}
		sort.Strings(got)
		return got
	}
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	t.Run("no_filter_lists_all", func(t *testing.T) {
		out, err := r.ListSecrets("", "", "", "")
		if err != nil {
			t.Fatalf("ListSecrets() error = %v", err)
		}
		want := []string{"cell-tok", "other-realm", "realm-tok", "space-tok"}
		if got := names(out); !eq(got, want) {
			t.Errorf("names = %v, want %v", got, want)
		}
	})

	t.Run("realm_filter_bounds_subtree", func(t *testing.T) {
		out, err := r.ListSecrets("default", "", "", "")
		if err != nil {
			t.Fatalf("ListSecrets() error = %v", err)
		}
		want := []string{"cell-tok", "realm-tok", "space-tok"}
		if got := names(out); !eq(got, want) {
			t.Errorf("names = %v, want %v", got, want)
		}
	})

	t.Run("space_filter_excludes_realm_scope", func(t *testing.T) {
		out, err := r.ListSecrets("default", "team-a", "", "")
		if err != nil {
			t.Fatalf("ListSecrets() error = %v", err)
		}
		want := []string{"cell-tok", "space-tok"}
		if got := names(out); !eq(got, want) {
			t.Errorf("names = %v, want %v", got, want)
		}
	})

	t.Run("verifies_bytes_never_surface", func(t *testing.T) {
		out, err := r.ListSecrets("", "", "", "")
		if err != nil {
			t.Fatalf("ListSecrets() error = %v", err)
		}
		for _, s := range out {
			if s.Spec.Data != "" {
				t.Errorf("secret %q carried bytes %q, want empty", s.Metadata.Name, s.Spec.Data)
			}
		}
	})
}

// TestListSecrets_SkipsTempFiles confirms an in-flight ".secret-*.tmp" temp
// file left by a concurrent atomic write never surfaces as a secret name.
func TestListSecrets_SkipsTempFiles(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	if _, err := r.WriteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "real", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "x"},
	}); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}

	dir := fs.SecretsDir(runPath, "default", "", "", "")
	if err := os.WriteFile(dir+"/.secret-1234.tmp", []byte("partial"), 0o600); err != nil {
		t.Fatalf("seeding temp file: %v", err)
	}

	out, err := r.ListSecrets("default", "", "", "")
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}
	if len(out) != 1 || out[0].Metadata.Name != "real" {
		t.Errorf("ListSecrets() = %+v, want only [real]", out)
	}
}

// TestListSecrets_IgnoresReservedSubdirs confirms the walk never mistakes a
// sibling secrets/, blueprints/, or configs/ reserved subdirectory for a child
// space, stack, or cell. The blueprints/ and configs/ exclusions are the cases
// the old secret childScopeNames missed (it only skipped secrets/); issue #734
// unified all three walkers on the same reserved-subdir set, so a phantom
// "blueprints" or "configs" space must never surface from the realm subtree
// walk.
func TestListSecrets_IgnoresReservedSubdirs(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	// A realm-scoped secret creates default/secrets/; a realm-scoped blueprint
	// creates default/blueprints/; a realm-scoped config creates
	// default/configs/. None must surface as a child space.
	if _, err := r.WriteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "realm-tok", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "v"},
	}); err != nil {
		t.Fatalf("seed WriteSecret error = %v", err)
	}
	seedBlueprint(t, r, "bp", "default", "", "")
	seedConfig(t, r, "cfg", "default", "", "")
	// Drop an in-flight temp file alongside the realm secret; it must be
	// skipped, not surfaced as a secret named ".secret-xyz.tmp".
	dir := fs.SecretsDir(runPath, "default", "", "", "")
	if err := os.WriteFile(dir+"/.secret-xyz.tmp", []byte("partial"), 0o600); err != nil {
		t.Fatalf("seed temp file error = %v", err)
	}

	out, err := r.ListSecrets("", "", "", "")
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}
	got := make([]string, 0, len(out))
	for _, s := range out {
		got = append(
			got,
			s.Metadata.Realm+"/"+s.Metadata.Space+"/"+s.Metadata.Stack+"/"+s.Metadata.Cell+"/"+s.Metadata.Name,
		)
	}
	sort.Strings(got)
	want := []string{"default////realm-tok"}
	if !equalStrings(got, want) {
		t.Errorf("ListSecrets(all) = %v, want %v (reserved subdirs + temp file must be ignored)", got, want)
	}
}

// TestDeleteSecret removes the bytes file and reports ErrSecretNotFound on a
// second delete of the same name (issue #622).
func TestDeleteSecret(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	stored := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "x"},
	}
	if _, err := r.WriteSecret(stored); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}

	if err := r.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	}); err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}

	path := fs.SecretPath(runPath, "default", "", "", "", "tok")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("secret file still present after delete (err=%v)", err)
	}

	if err := r.DeleteSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default"},
	}); !errors.Is(err, errdefs.ErrSecretNotFound) {
		t.Errorf("second DeleteSecret() error = %v, want ErrSecretNotFound", err)
	}
}

// typedSSHKeySecret is a stored ssh-key secret used by the #1472 typed-secret
// storage tests.
func typedSSHKeySecret(name string) intmodel.Secret {
	return intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: name, Realm: "default"},
		Spec: intmodel.SecretSpec{
			Type: intmodel.SecretTypeSSHKey,
			SSHKey: &intmodel.SecretSSHKey{
				PrivateKey: "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nYm9keQ==\n-----END OPENSSH PRIVATE" + " KEY-----\n",
				Passphrase: "do-not-echo",
			},
		},
	}
}

// TestWriteSecret_TypedRoundTrip pins the typed storage contract (#1472): the
// material lands in the same root-only bytes file, the type is recorded in the
// .types sidecar, and secretstore.Read returns the full typed spec.
func TestWriteSecret_TypedRoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := typedSSHKeySecret("deploy-key")
	created, err := r.WriteSecret(secret)
	if err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first write")
	}

	info, err := os.Stat(fs.SecretPath(runPath, "default", "", "", "", "deploy-key"))
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("typed secret file mode = %o, want 600", perm)
	}
	sidecar, err := os.ReadFile(secretstore.TypePath(runPath, secret.Metadata))
	if err != nil {
		t.Fatalf("reading type sidecar: %v", err)
	}
	if string(sidecar) != intmodel.SecretTypeSSHKey {
		t.Errorf("type sidecar = %q, want %q", sidecar, intmodel.SecretTypeSSHKey)
	}

	got, err := secretstore.Read(runPath, intmodel.ContainerSecretRef{Name: "deploy-key", Realm: "default"})
	if err != nil {
		t.Fatalf("secretstore.Read() error = %v", err)
	}
	if got.Type != intmodel.SecretTypeSSHKey || got.SSHKey == nil || *got.SSHKey != *secret.Spec.SSHKey {
		t.Errorf("stored spec = %+v, want the ssh-key material written", got)
	}
}

// TestGetAndListSecrets_ReportType confirms get and list surface a secret's
// type from the sidecar and still never carry any material.
func TestGetAndListSecrets_ReportType(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	basic := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "forge-token", Realm: "default"},
		Spec: intmodel.SecretSpec{
			Type:      intmodel.SecretTypeBasicAuth,
			BasicAuth: &intmodel.SecretBasicAuth{Username: "u", Token: "do-not-echo"},
		},
	}
	opaque := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "plain", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "do-not-echo"},
	}
	for _, s := range []intmodel.Secret{typedSSHKeySecret("deploy-key"), basic, opaque} {
		if _, err := r.WriteSecret(s); err != nil {
			t.Fatalf("WriteSecret(%s) error = %v", s.Metadata.Name, err)
		}
	}

	want := map[string]string{
		"deploy-key":  intmodel.SecretTypeSSHKey,
		"forge-token": intmodel.SecretTypeBasicAuth,
		"plain":       intmodel.SecretTypeOpaque,
	}
	assertMetadataOnly := func(source string, s intmodel.Secret) {
		t.Helper()
		if s.Spec.Type != want[s.Metadata.Name] {
			t.Errorf("%s %q type = %q, want %q", source, s.Metadata.Name, s.Spec.Type, want[s.Metadata.Name])
		}
		if s.Spec.HasMaterial() {
			t.Errorf("%s %q carries secret material: %+v", source, s.Metadata.Name, s.Spec)
		}
	}
	for name := range want {
		got, err := r.GetSecret(intmodel.Secret{Metadata: intmodel.SecretMetadata{Name: name, Realm: "default"}})
		if err != nil {
			t.Fatalf("GetSecret(%s) error = %v", name, err)
		}
		assertMetadataOnly("get", got)
	}

	listed, err := r.ListSecrets("default", "", "", "")
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}
	// The .types sidecar directory must never surface as a secret.
	if len(listed) != len(want) {
		t.Fatalf("ListSecrets() returned %d secrets, want %d: %+v", len(listed), len(want), listed)
	}
	for _, s := range listed {
		assertMetadataOnly("list", s)
	}
}

// TestWriteSecret_RetypeClearsSidecar confirms overwriting a typed secret with
// an opaque one drops the type marker, so the new bytes are never decoded as
// the old type.
func TestWriteSecret_RetypeClearsSidecar(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := typedSSHKeySecret("tok")
	if _, err := r.WriteSecret(secret); err != nil {
		t.Fatalf("WriteSecret(typed) error = %v", err)
	}
	created, err := r.WriteSecret(intmodel.Secret{
		Metadata: secret.Metadata,
		Spec:     intmodel.SecretSpec{Data: "now-opaque"},
	})
	if err != nil {
		t.Fatalf("WriteSecret(opaque) error = %v", err)
	}
	if created {
		t.Errorf("created = true, want false on overwrite")
	}
	if _, statErr := os.Stat(secretstore.TypePath(runPath, secret.Metadata)); !os.IsNotExist(statErr) {
		t.Errorf("type sidecar still present after retyping to opaque (err=%v)", statErr)
	}
	got, err := secretstore.Read(runPath, intmodel.ContainerSecretRef{Name: "tok", Realm: "default"})
	if err != nil {
		t.Fatalf("secretstore.Read() error = %v", err)
	}
	if got.Type != intmodel.SecretTypeOpaque || got.Data != "now-opaque" {
		t.Errorf("stored spec = %+v, want opaque now-opaque", got)
	}
}

// TestDeleteSecret_RemovesTypeSidecar confirms delete reclaims the sidecar, so
// a later opaque secret of the same name is not reported with the stale type.
func TestDeleteSecret_RemovesTypeSidecar(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	secret := typedSSHKeySecret("tok")
	if _, err := r.WriteSecret(secret); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}
	if err := r.DeleteSecret(intmodel.Secret{Metadata: secret.Metadata}); err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}
	if _, err := os.Stat(secretstore.TypePath(runPath, secret.Metadata)); !os.IsNotExist(err) {
		t.Errorf("type sidecar still present after delete (err=%v)", err)
	}

	if _, err := r.WriteSecret(intmodel.Secret{
		Metadata: secret.Metadata,
		Spec:     intmodel.SecretSpec{Data: "x"},
	}); err != nil {
		t.Fatalf("WriteSecret(opaque) error = %v", err)
	}
	got, err := r.GetSecret(intmodel.Secret{Metadata: secret.Metadata})
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if got.Spec.Type != intmodel.SecretTypeOpaque {
		t.Errorf("re-created secret type = %q, want opaque", got.Spec.Type)
	}
}

// TestSecretIdentity_RoundTrip confirms the readable identity facts of a
// secret (issue #1548) — its connector tag and identity fields — are stored
// next to the bytes, reported by get and list without any material, replaced
// on overwrite and reclaimed on delete.
func TestSecretIdentity_RoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	md := intmodel.SecretMetadata{Name: "work-token", Realm: "default"}
	tagged := intmodel.Secret{
		Metadata: md,
		Spec: intmodel.SecretSpec{
			Data: "do-not-echo", Connector: "forge",
			Identity: map[string]string{"account": "work"},
		},
	}
	plain := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "plain", Realm: "default"},
		Spec:     intmodel.SecretSpec{Data: "do-not-echo"},
	}
	for _, s := range []intmodel.Secret{tagged, plain} {
		if _, err := r.WriteSecret(s); err != nil {
			t.Fatalf("WriteSecret(%s) error = %v", s.Metadata.Name, err)
		}
	}

	check := func(source string, s intmodel.Secret) {
		t.Helper()
		if s.Spec.HasMaterial() {
			t.Errorf("%s %q carries secret material: %+v", source, s.Metadata.Name, s.Spec)
		}
		switch s.Metadata.Name {
		case "work-token":
			if s.Spec.Connector != "forge" || len(s.Spec.Identity) != 1 || s.Spec.Identity["account"] != "work" {
				t.Errorf("%s work-token identity = %q %v", source, s.Spec.Connector, s.Spec.Identity)
			}
		default:
			if s.Spec.Connector != "" || len(s.Spec.Identity) != 0 {
				t.Errorf("%s %q is tagged: %q %v", source, s.Metadata.Name, s.Spec.Connector, s.Spec.Identity)
			}
		}
	}
	got, err := r.GetSecret(intmodel.Secret{Metadata: md})
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	check("get", got)
	listed, err := r.ListSecrets("default", "", "", "")
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}
	// The .meta sidecar directory must never surface as a secret.
	if len(listed) != 2 {
		t.Fatalf("ListSecrets() returned %d secrets, want 2: %+v", len(listed), listed)
	}
	for _, s := range listed {
		check("list", s)
	}

	// Overwriting with an untagged secret drops the identity, so the new
	// bytes are never reported under the old account.
	if _, err = r.WriteSecret(intmodel.Secret{Metadata: md, Spec: intmodel.SecretSpec{Data: "rotated"}}); err != nil {
		t.Fatalf("WriteSecret(untagged) error = %v", err)
	}
	if got, err = r.GetSecret(intmodel.Secret{Metadata: md}); err != nil || got.Spec.Connector != "" ||
		len(got.Spec.Identity) != 0 {
		t.Fatalf("after an untagged overwrite: %+v, %v", got.Spec, err)
	}

	if _, err = r.WriteSecret(tagged); err != nil {
		t.Fatalf("WriteSecret(tagged) error = %v", err)
	}
	if err = r.DeleteSecret(intmodel.Secret{Metadata: md}); err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}
	if _, statErr := os.Stat(secretstore.MetaPath(runPath, md)); !os.IsNotExist(statErr) {
		t.Errorf("identity sidecar still present after delete (err=%v)", statErr)
	}

	// A malformed identity is refused before anything is written.
	bad := tagged
	bad.Spec.Identity = map[string]string{"account": "two\nlines"}
	if _, err = r.WriteSecret(bad); err == nil {
		t.Fatal("WriteSecret accepted a multi-line identity field")
	}
	if _, err = r.GetSecret(intmodel.Secret{Metadata: md}); err == nil {
		t.Fatal("a rejected secret was stored")
	}
}

// TestReadSecret_ReturnsMaterial pins issue #1652: ReadSecret carries the
// material of opaque and typed secrets plus the readable identity facts,
// while GetSecret on the same secret stays material-free.
func TestReadSecret_ReturnsMaterial(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	opaque := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "tok", Realm: "default", Space: "team-a"},
		Spec:     intmodel.SecretSpec{Data: "opaque-bytes"},
	}
	login := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "login", Realm: "kuke-system"},
		Spec: intmodel.SecretSpec{
			Type:      intmodel.SecretTypeBasicAuth,
			Connector: "git",
			Identity:  map[string]string{"author": "Root <root@example.com>"},
			BasicAuth: &intmodel.SecretBasicAuth{Username: "root", Password: "pw"},
		},
	}
	for _, s := range []intmodel.Secret{opaque, login} {
		if _, err := r.WriteSecret(s); err != nil {
			t.Fatalf("WriteSecret(%s) error = %v", s.Metadata.Name, err)
		}
	}

	got, err := r.ReadSecret(intmodel.Secret{Metadata: opaque.Metadata})
	if err != nil {
		t.Fatalf("ReadSecret(tok) error = %v", err)
	}
	if got.Spec.Data != "opaque-bytes" || got.Spec.EffectiveType() != intmodel.SecretTypeOpaque {
		t.Errorf("ReadSecret(tok) spec = %+v, want opaque data", got.Spec)
	}

	got, err = r.ReadSecret(intmodel.Secret{Metadata: login.Metadata})
	if err != nil {
		t.Fatalf("ReadSecret(login) error = %v", err)
	}
	if got.Spec.BasicAuth == nil || got.Spec.BasicAuth.Username != "root" || got.Spec.BasicAuth.Password != "pw" {
		t.Errorf("ReadSecret(login) basicAuth = %+v, want root/pw", got.Spec.BasicAuth)
	}
	if got.Spec.Connector != "git" || got.Spec.Identity["author"] == "" {
		t.Errorf(
			"ReadSecret(login) identity = %q %v, want connector and identity",
			got.Spec.Connector,
			got.Spec.Identity,
		)
	}

	meta, err := r.GetSecret(intmodel.Secret{Metadata: login.Metadata})
	if err != nil {
		t.Fatalf("GetSecret(login) error = %v", err)
	}
	if meta.Spec.BasicAuth != nil {
		t.Errorf("GetSecret(login) basicAuth = %+v, want nil", meta.Spec.BasicAuth)
	}
}

func TestReadSecret_NotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	_, err := r.ReadSecret(intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "ghost", Realm: "default"},
	})
	if !errors.Is(err, errdefs.ErrSecretNotFound) {
		t.Errorf("ReadSecret() error = %v, want ErrSecretNotFound", err)
	}
}
