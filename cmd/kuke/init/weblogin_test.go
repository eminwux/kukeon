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

package init

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/spf13/cobra"
)

// memSecretStore keeps secrets in memory, keyed by realm/name.
type memSecretStore struct {
	secrets map[string]intmodel.Secret
	creates int
	getErr  error
}

func (m *memSecretStore) key(md intmodel.SecretMetadata) string { return md.Realm + "/" + md.Name }

func (m *memSecretStore) GetSecret(s intmodel.Secret) (controller.GetSecretResult, error) {
	if m.getErr != nil {
		return controller.GetSecretResult{}, m.getErr
	}
	got, ok := m.secrets[m.key(s.Metadata)]
	return controller.GetSecretResult{Secret: intmodel.Secret{Metadata: got.Metadata}, MetadataExists: ok}, nil
}

func (m *memSecretStore) CreateSecret(s intmodel.Secret) (controller.CreateSecretResult, error) {
	if err := secretstore.ValidateSpec(s.Spec); err != nil {
		return controller.CreateSecretResult{}, err
	}
	m.creates++
	m.secrets[m.key(s.Metadata)] = s
	return controller.CreateSecretResult{Secret: s, Created: true}, nil
}

func printedLogin(login webUILogin) string {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	printWebUILogin(cmd, login)
	return buf.String()
}

// TestEnsureWebUILogin pins issue #1652: the first init creates the realm-scoped
// basic-auth login secret with a 32-hex-character password and prints it; a
// second init keeps the secret (same password) and prints only the read-back
// command.
func TestEnsureWebUILogin(t *testing.T) {
	store := &memSecretStore{secrets: map[string]intmodel.Secret{}}

	first, err := ensureWebUILogin(store)
	if err != nil {
		t.Fatalf("ensureWebUILogin() error = %v", err)
	}
	if !first.Created {
		t.Fatal("first run did not create the secret")
	}
	stored, ok := store.secrets[consts.KukeSystemRealmName+"/"+consts.WebUIAdminSecretName]
	if !ok {
		t.Fatalf("secret not stored; have %v", store.secrets)
	}
	md := stored.Metadata
	if md.Space != "" || md.Stack != "" || md.Cell != "" {
		t.Errorf("secret scope = %+v, want realm-scoped", md)
	}
	if stored.Spec.Type != intmodel.SecretTypeBasicAuth || stored.Spec.Connector != "" {
		t.Errorf(
			"secret type/connector = %q/%q, want basic-auth and no connector",
			stored.Spec.Type,
			stored.Spec.Connector,
		)
	}
	auth := stored.Spec.BasicAuth
	if auth == nil || auth.Username != "root" || auth.Token != "" {
		t.Fatalf("basicAuth = %+v, want username root and a password", auth)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(auth.Password) {
		t.Errorf("password = %q, want 32 hex characters", auth.Password)
	}
	if first.Password != auth.Password {
		t.Errorf("reported password %q != stored %q", first.Password, auth.Password)
	}

	out := printedLogin(first)
	for _, want := range []string{
		"Web UI login (kuke serve):",
		"user:     root",
		"password: " + auth.Password,
		"read it again: kuke get secret web-ui-admin --realm kuke-system -o yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("created block missing %q:\n%s", want, out)
		}
	}

	second, err := ensureWebUILogin(store)
	if err != nil {
		t.Fatalf("second ensureWebUILogin() error = %v", err)
	}
	if second.Created || second.Password != "" || store.creates != 1 {
		t.Errorf("second run = %+v (creates %d), want no create and no password", second, store.creates)
	}
	if store.secrets[consts.KukeSystemRealmName+"/"+consts.WebUIAdminSecretName].Spec.BasicAuth.Password != auth.Password {
		t.Error("second run changed the password")
	}
	out = printedLogin(second)
	if strings.Contains(out, "password:") || strings.Contains(out, auth.Password) {
		t.Errorf("existing-secret block prints the password:\n%s", out)
	}
	if !strings.Contains(out, "read it again: kuke get secret web-ui-admin --realm kuke-system -o yaml") {
		t.Errorf("existing-secret block lost the read-back command:\n%s", out)
	}
}

func TestEnsureWebUILogin_ReadErrorDoesNotCreate(t *testing.T) {
	store := &memSecretStore{secrets: map[string]intmodel.Secret{}, getErr: errors.New("boom")}
	if _, err := ensureWebUILogin(store); err == nil {
		t.Fatal("ensureWebUILogin() error = nil, want the read error")
	}
	if store.creates != 0 {
		t.Errorf("creates = %d, want 0 after a failed read", store.creates)
	}
}
