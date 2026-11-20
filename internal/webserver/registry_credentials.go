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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const maxRegistryCredentialBody = 16 << 10

// registryCredentialRequest is the write-only browser payload. The token
// travels only in this JSON body; it is never read back or echoed.
type registryCredentialRequest struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type registryCredentialResult struct {
	Realm    string `json:"realm"`
	Server   string `json:"server"`
	Username string `json:"username"`
	// Secret is the realm Secret the credential was stored in.
	Secret string `json:"secret"`
	Action string `json:"action"`
}

// registerRegistryCredentialRoutes upserts a realm's pull credential by
// server, mirroring `kuke create registry-credential` (#1130, #1407, #1548):
// store the credential as a basic-auth Secret of the realm, read the realm,
// bind the server to that Secret (replacing the entry with the same server, or
// appending), and re-apply the realm through the daemon's write-through apply
// path. The realm document never carries the token.
func registerRegistryCredentialRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc("POST /api/realms/{realm}/registry-credentials",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			realm, err := inventoryRealm(r)
			if err != nil {
				return nil, err
			}
			input, err := decodeRegistryCredential(r)
			if err != nil {
				return nil, err
			}
			secret := connector.RegistrySecretName(input.Server)
			action, err := upsertRealmCredential(ctx, c, realm, secret, input)
			if err != nil {
				return nil, redactCreationError(err, []string{input.Password})
			}
			return registryCredentialResult{
				Realm: realm, Server: input.Server, Username: input.Username, Secret: secret, Action: action,
			}, nil
		}))
}

func decodeRegistryCredential(r *http.Request) (registryCredentialRequest, error) {
	var input registryCredentialRequest
	// Mutations require JSON; the shared access policy guards Host/Origin.
	if r.Header.Get("Content-Type") != jsonContentType {
		return input, fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRegistryCredentialBody))
	decoder.DisallowUnknownFields()
	// Decoder errors can quote the offending input, so they are not
	// forwarded: a malformed body may carry the token.
	if err := decoder.Decode(&input); err != nil {
		return registryCredentialRequest{}, fmt.Errorf("%w: invalid registry credential request", errdefs.ErrConfig)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return registryCredentialRequest{}, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
	}
	input.Server = strings.TrimSpace(input.Server)
	input.Username = strings.TrimSpace(input.Username)
	input.Password = strings.TrimRight(input.Password, "\r\n")
	switch {
	case strings.ContainsAny(input.Server, " \t\r\n\x00/"):
		return registryCredentialRequest{}, fmt.Errorf(
			"%w: server must be a registry host such as ghcr.io",
			errdefs.ErrConfig,
		)
	case input.Username == "":
		return registryCredentialRequest{}, fmt.Errorf("%w: username is required", errdefs.ErrConfig)
	case strings.ContainsAny(input.Username, "\r\n\x00"):
		return registryCredentialRequest{}, fmt.Errorf("%w: username must be a single line", errdefs.ErrConfig)
	case input.Password == "":
		return registryCredentialRequest{}, fmt.Errorf("%w: token is required", errdefs.ErrConfig)
	}
	return input, nil
}

func upsertRealmCredential(
	ctx context.Context,
	c kukeonv1.Client,
	realm string,
	secret string,
	input registryCredentialRequest,
) (string, error) {
	current, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: realm}})
	if err != nil {
		return "", err
	}
	if !current.MetadataExists {
		return "", errdefs.ErrRealmNotFound
	}
	// The credential itself is a Secret of the realm; creating one that
	// exists replaces its value, so saving again rotates the token.
	if _, err = c.CreateSecret(ctx, connector.RegistrySecret(
		realm, secret, input.Username, input.Password,
	)); err != nil {
		return "", err
	}
	// The apply diff replaces registryCredentials wholesale, so the other
	// entries are carried forward from the live document.
	desired := current.Realm
	desired.APIVersion = v1beta1.APIVersionV1Beta1
	desired.Kind = v1beta1.KindRealm
	desired.Status = v1beta1.RealmStatus{}
	desired.Spec.RegistryCredentials = connector.UpsertRegistryCredential(
		desired.Spec.RegistryCredentials, input.Server, realm, secret,
	)
	rawYAML, err := yaml.Marshal(desired)
	if err != nil {
		return "", fmt.Errorf("marshal realm document: %w", err)
	}
	result, err := c.ApplyDocuments(ctx, rawYAML)
	if err != nil {
		return "", err
	}
	for _, resource := range result.Resources {
		if resource.Kind != string(v1beta1.KindRealm) {
			continue
		}
		if resource.Action == "failed" {
			if resource.Error != "" {
				return "", fmt.Errorf("apply realm %q: %s", resource.Name, resource.Error)
			}
			return "", errors.New("apply realm failed")
		}
		return resource.Action, nil
	}
	return "applied", nil
}

// redactRealm clears registry passwords from browser responses, including
// the full-document view. Server and username stay visible.
func redactRealm(doc v1beta1.RealmDoc) v1beta1.RealmDoc {
	doc.Spec.RegistryCredentials = connector.RedactRegistryPasswords(doc.Spec.RegistryCredentials)
	return doc
}
