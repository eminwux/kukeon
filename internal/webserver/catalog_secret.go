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
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// secretWriteRequest creates an untagged Secret or, with Replace, replaces
// the material of a stored one. The scope comes from the query, the name from
// the path. The material fields are write-only and match identityRequest.
type secretWriteRequest struct {
	// Type is the Secret type; empty is opaque on create and the stored type
	// on replace.
	Type string `json:"type,omitempty"`
	// Replace selects replace-only: a missing Secret is not created.
	Replace bool `json:"replace,omitempty"`

	Value       string `json:"value,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	PrivateKey  string `json:"privateKey,omitempty"`
	Passphrase  string `json:"passphrase,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	KnownHosts  string `json:"knownHosts,omitempty"`
}

// identity is the request's material in the identity route's shape, so both
// routes build and mask material the same way.
func (r secretWriteRequest) identity() identityRequest {
	return identityRequest{
		Value: r.Value, Username: r.Username, Password: r.Password,
		PrivateKey: r.PrivateKey, Passphrase: r.Passphrase, PublicKey: r.PublicKey,
		Certificate: r.Certificate, KnownHosts: r.KnownHosts,
	}
}

// secretWriteResult is the route's response: coordinates and type only.
type secretWriteResult struct {
	Name  string    `json:"name"`
	Scope [4]string `json:"scope"`
	Type  string    `json:"type"`
	// Action is created, or updated when a stored value was replaced.
	Action string `json:"action"`
}

// The actions a secret write reports.
const (
	secretActionCreated = "created"
	secretActionUpdated = "updated"
)

// secretWriteAction names what a secret write did: created, or updated when
// a stored value was replaced.
func secretWriteAction(created bool) string {
	if created {
		return secretActionCreated
	}
	return secretActionUpdated
}

func registerCatalogSecretRoute(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc("POST /api/catalog/secrets/{name}", resourceRoute(dial, writeCatalogSecret))
}

// writeCatalogSecret is the web UI's New Secret and Replace value. Create
// refuses an existing Secret and replace refuses a missing one; a Secret
// tagged with a connector is not written here, because the daemon's overwrite
// is wholesale and would erase the tag — its identity route rotates it. These
// guards are a read before the write, not atomic: a concurrent write to the
// same name between the two calls is not detected. The daemon's own
// validation errors are untyped over the RPC, so the material is validated
// here first and a rejection is a 400.
func writeCatalogSecret(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	var input secretWriteRequest
	if err := decodeJSONBody(r, maxIdentityBody, &input); err != nil {
		return nil, err
	}
	material := input.identity().material()
	scope, err := catalogScope(r, true, true)
	if err != nil {
		return nil, err
	}
	name := r.PathValue("name")
	// Guarded inline so the check dominates the name for static
	// path-injection analysis: see checkScope.
	if !filepath.IsLocal(name) || !safePathSegment(name) || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("%w: unsafe secret name", errdefs.ErrConfig)
	}
	if !input.Replace && scope[3] != "" {
		return nil, fmt.Errorf("%w: a new secret is stored at realm, space or stack level", errdefs.ErrConfig)
	}
	metadata := v1beta1.SecretMetadata{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2], Cell: scope[3]}
	secretType, err := secretWriteType(ctx, c, metadata, input)
	if err != nil {
		return nil, redactCreationError(err, material)
	}
	doc, err := secretWriteDoc(metadata, secretType, input)
	if err != nil {
		return nil, redactCreationError(err, material)
	}
	created, err := c.CreateSecret(ctx, doc)
	if err != nil {
		return nil, redactCreationError(err, material)
	}
	action := secretWriteAction(created.Created)
	return secretWriteResult{Name: name, Scope: scope, Type: secretType, Action: action}, nil
}

// secretWriteType reads the stored Secret and decides the type to write:
// create requires that none exists, replace requires one that no connector
// tags and keeps its type.
func secretWriteType(
	ctx context.Context, c kukeonv1.Client, metadata v1beta1.SecretMetadata, input secretWriteRequest,
) (string, error) {
	requested := strings.TrimSpace(input.Type)
	stored, err := c.GetSecret(ctx, v1beta1.SecretDoc{Metadata: metadata})
	if err != nil {
		return "", err
	}
	if !input.Replace {
		if stored.MetadataExists {
			return "", fmt.Errorf("%w: %s", errdefs.ErrSecretAlreadyExists, secretPath(metadata))
		}
		if requested == "" {
			return v1beta1.SecretTypeOpaque, nil
		}
		return requested, nil
	}
	if !stored.MetadataExists {
		return "", fmt.Errorf("%w: %s", errdefs.ErrSecretNotFound, secretPath(metadata))
	}
	if tag := stored.Secret.Spec.Connector; tag != "" {
		return "", fmt.Errorf(
			"%w: secret %s is an identity of connector %q; rotate it on that connector's page",
			errdefs.ErrConfig, secretPath(metadata), tag)
	}
	current := stored.Secret.Spec.Type
	if current == "" {
		current = v1beta1.SecretTypeOpaque
	}
	if requested != "" && requested != current {
		return "", fmt.Errorf(
			"%w: secret %s is %s; replacing keeps the type — delete it and create a %s secret instead",
			errdefs.ErrConfig, secretPath(metadata), current, requested)
	}
	return current, nil
}

// secretWriteDoc builds the untagged Secret document and validates it the
// way kukeond would, so a bad request is a 400 instead of an untyped daemon
// failure.
func secretWriteDoc(
	metadata v1beta1.SecretMetadata, secretType string, input secretWriteRequest,
) (v1beta1.SecretDoc, error) {
	spec, err := identityMaterial(input.identity(), secretType, false)
	if err != nil {
		return v1beta1.SecretDoc{}, err
	}
	doc := v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata:   metadata,
		Spec:       spec,
	}
	internal, err := apischeme.ConvertSecretDocToInternal(doc)
	if err != nil {
		return v1beta1.SecretDoc{}, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	if err = secretstore.ValidateSpec(internal.Spec); err != nil {
		return v1beta1.SecretDoc{}, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	return doc, nil
}

// secretPath names a Secret by its scope path, for messages.
func secretPath(md v1beta1.SecretMetadata) string {
	parts := []string{}
	for _, part := range []string{md.Realm, md.Space, md.Stack, md.Cell, md.Name} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " / ")
}
