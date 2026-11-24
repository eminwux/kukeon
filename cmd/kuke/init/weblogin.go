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
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/spf13/cobra"
)

// webUILoginTokenBytes is the random length of the web UI password: 16 bytes,
// printed as 32 hex characters.
const webUILoginTokenBytes = 16

// webUILoginReadBack is the command that prints the web UI login again.
const webUILoginReadBack = "kuke get secret " + consts.WebUIAdminSecretName +
	" --realm " + consts.KukeSystemRealmName + " -o yaml"

// loginSecretStore is the part of the controller ensureWebUILogin uses.
type loginSecretStore interface {
	GetSecret(secret intmodel.Secret) (controller.GetSecretResult, error)
	CreateSecret(secret intmodel.Secret) (controller.CreateSecretResult, error)
}

// webUILogin is the outcome of ensureWebUILogin. Password is set only when
// this run created the secret.
type webUILogin struct {
	Created  bool
	User     string
	Password string
}

// ensureWebUILogin creates the `kuke serve` login secret (issue #1652) when it
// does not exist: Secret consts.WebUIAdminSecretName in realm
// consts.KukeSystemRealmName, realm-scoped, basic-auth, username
// consts.WebUIAdminUser and a random 32-hex-character password. An existing
// secret is never overwritten, so re-running init keeps the password.
func ensureWebUILogin(store loginSecretStore) (webUILogin, error) {
	md := intmodel.SecretMetadata{Name: consts.WebUIAdminSecretName, Realm: consts.KukeSystemRealmName}
	login := webUILogin{User: consts.WebUIAdminUser}

	got, err := store.GetSecret(intmodel.Secret{Metadata: md})
	if err != nil {
		return login, fmt.Errorf("read web UI login secret: %w", err)
	}
	if got.MetadataExists {
		return login, nil
	}

	raw := make([]byte, webUILoginTokenBytes)
	if _, err = rand.Read(raw); err != nil {
		return login, fmt.Errorf("generate web UI password: %w", err)
	}
	password := hex.EncodeToString(raw)
	if _, err = store.CreateSecret(intmodel.Secret{
		Metadata: md,
		Spec: intmodel.SecretSpec{
			Type:      intmodel.SecretTypeBasicAuth,
			BasicAuth: &intmodel.SecretBasicAuth{Username: consts.WebUIAdminUser, Password: password},
		},
	}); err != nil {
		return login, fmt.Errorf("create web UI login secret: %w", err)
	}
	login.Created = true
	login.Password = password
	return login, nil
}

// printWebUILogin prints the login block. The password appears only on the
// run that created it; afterwards the read-back command retrieves it.
func printWebUILogin(cmd *cobra.Command, login webUILogin) {
	cmd.Println("Web UI login (kuke serve):")
	cmd.Printf("  user:     %s\n", login.User)
	if login.Created {
		cmd.Printf("  password: %s\n", login.Password)
	}
	cmd.Printf("  read it again: %s\n", webUILoginReadBack)
}
