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
	"os"
	"testing"
	"time"

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
