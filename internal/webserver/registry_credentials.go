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
	Action   string `json:"action"`
}

// registerRegistryCredentialRoutes upserts a realm's pull credential by
// server, mirroring `kuke create registry-credential` (#1130, #1407): read
// the realm, replace the entry with the same server (or append), and
// re-apply the realm through the daemon's write-through apply path.
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
			action, err := upsertRealmCredential(ctx, c, realm, input)
			if err != nil {
				return nil, redactCreationError(err, []string{input.Password})
			}
			return registryCredentialResult{
				Realm: realm, Server: input.Server, Username: input.Username, Action: action,
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
	input registryCredentialRequest,
) (string, error) {
	current, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: realm}})
	if err != nil {
		return "", err
	}
	if !current.MetadataExists {
		return "", errdefs.ErrRealmNotFound
	}
	// The apply diff replaces registryCredentials wholesale, so the other
	// entries are carried forward from the live document.
	desired := current.Realm
	desired.APIVersion = v1beta1.APIVersionV1Beta1
	desired.Kind = v1beta1.KindRealm
	desired.Status = v1beta1.RealmStatus{}
	desired.Spec.RegistryCredentials = upsertRegistryCredential(
		append([]v1beta1.RegistryCredentials(nil), desired.Spec.RegistryCredentials...),
		v1beta1.RegistryCredentials{
			Username:      input.Username,
			Password:      input.Password,
			ServerAddress: input.Server,
		},
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

// upsertRegistryCredential matches `kuke create registry-credential`: exact
// ServerAddress match replaces the entry, otherwise the entry is appended,
// so the empty-server default upserts against itself.
func upsertRegistryCredential(
	existing []v1beta1.RegistryCredentials,
	cred v1beta1.RegistryCredentials,
) []v1beta1.RegistryCredentials {
	for i := range existing {
		if existing[i].ServerAddress == cred.ServerAddress {
			existing[i] = cred
			return existing
		}
	}
	return append(existing, cred)
}

// redactRealm clears registry passwords from browser responses, including
// the full-document view. Server and username stay visible.
func redactRealm(doc v1beta1.RealmDoc) v1beta1.RealmDoc {
	if len(doc.Spec.RegistryCredentials) == 0 {
		return doc
	}
	doc.Spec.RegistryCredentials = append([]v1beta1.RegistryCredentials(nil), doc.Spec.RegistryCredentials...)
	for i := range doc.Spec.RegistryCredentials {
		doc.Spec.RegistryCredentials[i].Password = ""
	}
	return doc
}
