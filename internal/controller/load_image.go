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
	"io"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// LoadImageResult reports the outcome of a `kuke image load` import.
type LoadImageResult struct {
	Realm     string
	Namespace string
	Images    []string
}

// LoadImage imports an OCI/docker image tarball into the realm's containerd
// namespace. The realm name is mapped to a containerd namespace via
// consts.RealmNamespace, the same source of truth used by `kuke init` and
// every other realm operation.
func (b *Exec) LoadImage(realm string, reader io.Reader) (LoadImageResult, error) {
	var res LoadImageResult

	realmName := strings.TrimSpace(realm)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	if reader == nil {
		return res, errdefs.ErrTarballRequired
	}

	lookup := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: realmName},
	}
	if _, err := b.runner.GetRealm(lookup); err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return res, fmt.Errorf("%w: %s", errdefs.ErrRealmNotFound, realmName)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	namespace := consts.RealmNamespace(realmName)
	images, err := b.runner.LoadImage(namespace, reader)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrLoadImage, err)
	}

	res.Realm = realmName
	res.Namespace = namespace
	res.Images = images
	return res, nil
}
