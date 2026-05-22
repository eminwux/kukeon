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

package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// GetSecretResult reports the metadata-only view of a single `kind: Secret`
// (issue #622). The bytes are never carried — Secret.Spec is left zero so the
// never-round-tripped contract from #619 holds end to end.
type GetSecretResult struct {
	Secret         intmodel.Secret
	MetadataExists bool
}

// DeleteSecretResult reports the outcome of removing a single Secret's
// daemon-stored file.
type DeleteSecretResult struct {
	Secret  intmodel.Secret
	Deleted bool
}

// GetSecret retrieves the metadata-only view of one named, scoped Secret. The
// scope coordinates are validated for completeness (a deeper coordinate
// requires every shallower one); realm and name are mandatory. Returns a
// result with MetadataExists=false (and no error) when the secret is absent,
// mirroring GetRealm's "report, don't error on not-found" shape.
func (b *Exec) GetSecret(secret intmodel.Secret) (GetSecretResult, error) {
	var res GetSecretResult

	if err := validateSecretLookup(secret.Metadata); err != nil {
		return res, err
	}

	got, err := b.runner.GetSecret(secret)
	if err != nil {
		if errors.Is(err, errdefs.ErrSecretNotFound) {
			res.MetadataExists = false
			return res, nil
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetSecret, err)
	}

	res.MetadataExists = true
	res.Secret = got
	return res, nil
}

// ListSecrets lists the metadata of every Secret bound to the filter scope or
// any scope nested within it. An empty realm lists across all realms; the
// filter coordinates must still be contiguous (no gap below a set level).
func (b *Exec) ListSecrets(realmName, spaceName, stackName, cellName string) ([]intmodel.Secret, error) {
	if err := validateSecretScopeFilter(realmName, spaceName, stackName, cellName); err != nil {
		return nil, err
	}
	return b.runner.ListSecrets(
		strings.TrimSpace(realmName),
		strings.TrimSpace(spaceName),
		strings.TrimSpace(stackName),
		strings.TrimSpace(cellName),
	)
}

// DeleteSecret removes a single named, scoped Secret's daemon-stored file.
// Returns a "not found" error when the secret does not exist, matching the
// DeleteRealm contract.
//
// Live-reference safety gate (issue #623, AC5): before unlinking, scan every
// persisted cell's container specs for a secretRef pointing at this secret. If
// any references it, refuse with ErrSecretInUse naming the referencing cells —
// deleting the bytes out from under a live container would break it on its next
// start. This mirrors the DeleteRealm dependency guard.
func (b *Exec) DeleteSecret(secret intmodel.Secret) (DeleteSecretResult, error) {
	var res DeleteSecretResult

	if err := validateSecretLookup(secret.Metadata); err != nil {
		return res, err
	}

	refs, err := b.secretReferences(secret.Metadata)
	if err != nil {
		return res, fmt.Errorf("%w: check live references: %w", errdefs.ErrDeleteSecret, err)
	}
	if len(refs) > 0 {
		return res, fmt.Errorf(
			"%w: secret %q is referenced by %d cell(s): %s. Delete or detach them before deleting the secret",
			errdefs.ErrSecretInUse, secret.Metadata.Name, len(refs), strings.Join(refs, ", "),
		)
	}

	if err := b.runner.DeleteSecret(secret); err != nil {
		if errors.Is(err, errdefs.ErrSecretNotFound) {
			return res, fmt.Errorf("secret %q not found", secret.Metadata.Name)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteSecret, err)
	}

	res.Deleted = true
	res.Secret = secret
	return res, nil
}

// secretReferences returns the scope paths of every persisted cell whose
// container specs reference the given secret via secretRef. The match is by
// resolved storage path (fs.SecretPath), so a reference and the target secret
// are "the same secret" exactly when they resolve to the same on-disk file —
// the identity the resolver (internal/ctr.readSecretRefValue) reads at
// container start. Each referencing cell is listed once, even when several of
// its containers reference the secret.
func (b *Exec) secretReferences(md intmodel.SecretMetadata) ([]string, error) {
	targetPath := fs.SecretPath(b.opts.RunPath, md.Realm, md.Space, md.Stack, md.Cell, md.Name)

	cells, err := b.runner.ListCells("", "", "")
	if err != nil {
		return nil, err
	}

	var refs []string
	seen := make(map[string]struct{})
	for _, cell := range cells {
		scope := fmt.Sprintf("%s/%s/%s/%s",
			cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName, cell.Metadata.Name)
		for _, c := range cell.Spec.Containers {
			for _, s := range c.Secrets {
				if s.SecretRef == nil {
					continue
				}
				ref := s.SecretRef
				refPath := fs.SecretPath(b.opts.RunPath, ref.Realm, ref.Space, ref.Stack, ref.Cell, ref.Name)
				if refPath != targetPath {
					continue
				}
				if _, ok := seen[scope]; ok {
					continue
				}
				seen[scope] = struct{}{}
				refs = append(refs, scope)
			}
		}
	}
	return refs, nil
}

// validateSecretLookup enforces the scope contract for a single-secret get or
// delete: name and realm are mandatory and the scope coordinates must be
// contiguous (a cell-scoped secret must also name its stack, space, and realm).
func validateSecretLookup(md intmodel.SecretMetadata) error {
	if strings.TrimSpace(md.Name) == "" {
		return errdefs.ErrSecretNameRequired
	}
	if strings.TrimSpace(md.Realm) == "" {
		return errdefs.ErrSecretRealmRequired
	}
	return validateSecretScopeContiguity(md.Space, md.Stack, md.Cell)
}

// validateSecretScopeFilter enforces the scope contract for a list filter:
// realm is optional (an empty realm lists across all realms), but a set deeper
// coordinate still requires every shallower one to be set.
func validateSecretScopeFilter(realm, space, stack, cell string) error {
	if strings.TrimSpace(realm) == "" &&
		(strings.TrimSpace(space) != "" || strings.TrimSpace(stack) != "" || strings.TrimSpace(cell) != "") {
		return fmt.Errorf("%w (scope set without realm)", errdefs.ErrSecretScopeIncomplete)
	}
	return validateSecretScopeContiguity(space, stack, cell)
}

// validateSecretScopeContiguity rejects a scope gap below the realm level: a
// set cell requires a set stack, and a set stack requires a set space. Mirrors
// the parser's apply-time validateSecretScope check (issue #619).
func validateSecretScopeContiguity(space, stack, cell string) error {
	space = strings.TrimSpace(space)
	stack = strings.TrimSpace(stack)
	cell = strings.TrimSpace(cell)

	if cell != "" && stack == "" {
		return fmt.Errorf("%w (cell set without stack)", errdefs.ErrSecretScopeIncomplete)
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrSecretScopeIncomplete)
	}
	return nil
}
