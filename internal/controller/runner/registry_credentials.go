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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
)

// registryCredentials resolves a Realm's spec.registryCredentials into the
// credentials image pulls authenticate with — the registry delivery handler.
// A Secret-backed entry is read from the Realm's own secrets: a basic-auth
// Secret whose username and password (or token) are the registry login. An
// inline entry is used as written.
//
// The credentials accompany every container create in the Realm, whether or
// not the image still has to be pulled, so an entry that cannot be resolved —
// its Secret is gone, or is not a basic-auth registry credential — is logged
// and left out instead of failing the create: a cell whose image is already
// present still starts, and a pull that needed the entry fails as
// unauthenticated with the warning next to it in the daemon log.
func (r *Exec) registryCredentials(realm intmodel.Realm) []ctr.RegistryCredentials {
	entries := realm.Spec.RegistryCredentials
	if len(entries) == 0 {
		return nil
	}
	inline := make([]intmodel.RegistryCredentials, 0, len(entries))
	for _, entry := range entries {
		if entry.SecretRef == nil {
			inline = append(inline, entry)
			continue
		}
		resolved, err := resolveRegistrySecret(r.opts.RunPath, realm.Metadata.Name, entry)
		if err != nil {
			r.logger.WarnContext(r.ctx, "registry credential not usable; pulls from this registry are unauthenticated",
				"realm", realm.Metadata.Name, "server", entry.ServerAddress,
				"secret", entry.SecretRef.Name, "error", err)
			continue
		}
		inline = append(inline, resolved)
	}
	return ctr.ConvertRealmCredentials(inline)
}

// resolveRegistrySecret reads the basic-auth Secret a registry entry names.
// The Secret is always looked up at the Realm's own scope. The returned error
// never carries the Secret's material.
func resolveRegistrySecret(
	runPath, realm string, entry intmodel.RegistryCredentials,
) (intmodel.RegistryCredentials, error) {
	ref := intmodel.ContainerSecretRef{Name: strings.TrimSpace(entry.SecretRef.Name), Realm: realm}
	meta, err := secretstore.ReadMeta(runPath, intmodel.SecretMetadata(ref))
	if err != nil {
		return intmodel.RegistryCredentials{}, err
	}
	if tag := strings.TrimSpace(meta.Connector); tag != "" && tag != connector.RegistryIdentityTag {
		return intmodel.RegistryCredentials{}, fmt.Errorf(
			"%w: secret %q is an identity of connector %q, not %q",
			errdefs.ErrConnectorIdentity, ref.Name, tag, connector.RegistryIdentityTag)
	}
	spec, err := secretstore.Read(runPath, ref)
	if err != nil {
		return intmodel.RegistryCredentials{}, err
	}
	if spec.EffectiveType() != intmodel.SecretTypeBasicAuth || spec.BasicAuth == nil {
		return intmodel.RegistryCredentials{}, fmt.Errorf(
			"%w: secret %q is %s; a registry credential is a %s secret",
			errdefs.ErrSecretTypeMismatch, ref.Name, spec.EffectiveType(), intmodel.SecretTypeBasicAuth)
	}
	password := spec.BasicAuth.Password
	if password == "" {
		password = spec.BasicAuth.Token
	}
	return intmodel.RegistryCredentials{
		Username:      spec.BasicAuth.Username,
		Password:      password,
		ServerAddress: entry.ServerAddress,
	}, nil
}
