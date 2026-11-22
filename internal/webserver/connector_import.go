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

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/gitimport"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// The git connector's "Import from host" action (issue #1591): the same import
// `kuke connector import git` runs, here on the server, as the account running
// `kuke serve` — its home directory's ~/.ssh and its global git configuration.
// Key material is read from disk and sent to the daemon only: no response,
// error or log carries it.

// gitImportPath is the route of the import; the git connector is the one
// whose identity can be discovered on the host.
const gitImportPath = "POST /api/connectors/" + intconnector.GitIdentityTag + "/import"

// gitImportRequest is the import's options. Key and PassphraseFile name files
// in the server account's home directory (`.ssh/id_work` or `~/.ssh/id_work`).
type gitImportRequest struct {
	Hosts          []string `json:"hosts,omitempty"`
	Name           string   `json:"name,omitempty"`
	Realm          string   `json:"realm,omitempty"`
	MakeDefault    bool     `json:"makeDefault,omitempty"`
	Key            string   `json:"key,omitempty"`
	PassphraseFile string   `json:"passphraseFile,omitempty"`
}

// gitImportSecret is one Secret the import stored.
type gitImportSecret struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
	// Action is created, or updated when a Secret of that name existed.
	Action string `json:"action"`
}

// gitImportDefault reports the Realm default an import with makeDefault set.
type gitImportDefault struct {
	Realm    string                      `json:"realm"`
	Identity *v1beta1.ContainerSecretRef `json:"identity"`
	Previous *v1beta1.ContainerSecretRef `json:"previous,omitempty"`
	Action   string                      `json:"action"`
}

type gitImportResult struct {
	gitimport.Result

	Secrets []gitImportSecret `json:"secrets"`
	Default *gitImportDefault `json:"default,omitempty"`
}

// homeFile resolves a request's file name inside home: a relative path or one
// starting with `~/`. Anything that could leave home is refused.
func homeFile(home, name, what string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	name = strings.TrimPrefix(name, "~/")
	// Guarded inline so the check visibly dominates the joined path for static
	// path-injection analysis: see scopedefaults.CheckScope.
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("%w: %s must name a file in the server's home directory (%s)",
			errdefs.ErrConfig, what, "for example .ssh/id_ed25519")
	}
	return filepath.Join(home, name), nil
}

// importGit discovers the server account's git identity, stores it, and — with
// makeDefault — makes it the Realm's default git identity.
func (a connectorAPI) importGit(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	conn, ok := a.catalog.Find(intconnector.GitIdentityTag)
	if !ok {
		return nil, fmt.Errorf("%w: the connector catalog has no %s connector",
			errdefs.ErrConfig, intconnector.GitIdentityTag)
	}
	var input gitImportRequest
	if err := decodeJSONBody(r, maxConnectorBody, &input); err != nil {
		return nil, err
	}
	realm := strings.TrimSpace(input.Realm)
	if realm == "" {
		realm = gitimport.DefaultRealm
	}
	scope, err := checkScope(scopeRef{Realm: realm})
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	if name != "" && (!filepath.IsLocal(name) || !safePathSegment(name) || strings.HasPrefix(name, ".")) {
		return nil, fmt.Errorf("%w: unsafe secret name", errdefs.ErrConfig)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("%w: the server has no home directory to read", errdefs.ErrConfig)
	}
	opts := gitimport.Options{Home: home, Hosts: input.Hosts, Name: name, Realm: scope.Realm}
	if opts.Key, err = homeFile(home, input.Key, "the key"); err != nil {
		return nil, err
	}
	if opts.PassphraseFile, err = homeFile(home, input.PassphraseFile, "the passphrase file"); err != nil {
		return nil, err
	}
	imported, err := gitimport.Import(ctx, opts)
	if err != nil {
		return nil, importError(err)
	}
	method, _ := conn.Method("")
	if err = imported.Check(conn.Descriptor(method)); err != nil {
		return nil, importError(err)
	}
	out := gitImportResult{Result: imported, Secrets: []gitImportSecret{}}
	for _, doc := range imported.Secrets {
		created, createErr := c.CreateSecret(ctx, doc)
		if createErr != nil {
			return nil, redactCreationError(createErr, importMaterial(doc))
		}
		action := "updated"
		if created.Created {
			action = "created"
		}
		out.Secrets = append(out.Secrets, gitImportSecret{
			Name: doc.Metadata.Name, Realm: doc.Metadata.Realm, Action: action,
		})
	}
	if !input.MakeDefault {
		return out, nil
	}
	ref := imported.Ref()
	set, err := scopedefaults.Set(ctx, c, a.catalog, conn, scope, "", &ref)
	if err != nil {
		return nil, fmt.Errorf("identity stored but the realm default was not set: %w", err)
	}
	out.Default = &gitImportDefault{
		Realm: set.Scope.Realm, Identity: set.Identity, Previous: set.Previous, Action: set.Action,
	}
	return out, nil
}

// importError classifies an import failure as a bad request: it is about the
// server's files or the request's options.
func importError(err error) error {
	if errors.Is(err, errdefs.ErrGitImport) {
		return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	return err
}

// importMaterial lists the key material of an imported Secret, for masking a
// daemon error that might quote it.
func importMaterial(doc v1beta1.SecretDoc) []string {
	key := doc.Spec.SSHKey
	if key == nil {
		return []string{doc.Spec.Data}
	}
	request := identityRequest{PrivateKey: key.PrivateKey, Passphrase: key.Passphrase}
	return request.material()
}
