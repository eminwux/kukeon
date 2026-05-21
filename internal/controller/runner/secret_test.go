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
