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
	"encoding/json"
	"fmt"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) readRealmInternal(filePath string) (intmodel.Realm, error) {
	raw, err := metadata.ReadRaw(r.ctx, r.logger, filePath)
	if err != nil {
		return intmodel.Realm{}, err
	}
	version, err := fs.DetectMetadataVersion(raw)
	if err != nil {
		return intmodel.Realm{}, wrapConversionErr(err)
	}
	switch version {
	case apischeme.VersionV1Beta1:
		var realmDoc v1beta1.RealmDoc
		if unmarshalErr := json.Unmarshal(raw, &realmDoc); unmarshalErr != nil {
			return intmodel.Realm{}, wrapConversionErr(unmarshalErr)
		}
		internalRealm, convertErr := apischeme.ConvertRealmDocToInternal(realmDoc)
		if convertErr != nil {
			return intmodel.Realm{}, wrapConversionErr(convertErr)
		}
		return internalRealm, nil
	default:
		return intmodel.Realm{}, wrapConversionErr(fmt.Errorf("unsupported apiVersion %q", version))
	}
}

func (r *Exec) readSpaceInternal(filePath string) (intmodel.Space, error) {
	raw, err := metadata.ReadRaw(r.ctx, r.logger, filePath)
	if err != nil {
		return intmodel.Space{}, err
	}
	version, err := fs.DetectMetadataVersion(raw)
	if err != nil {
		return intmodel.Space{}, wrapConversionErr(err)
	}
	switch version {
	case apischeme.VersionV1Beta1:
		var spaceDoc v1beta1.SpaceDoc
		if unmarshalErr := json.Unmarshal(raw, &spaceDoc); unmarshalErr != nil {
			return intmodel.Space{}, wrapConversionErr(unmarshalErr)
		}
		internalSpace, convertErr := apischeme.ConvertSpaceDocToInternal(spaceDoc)
		if convertErr != nil {
			return intmodel.Space{}, wrapConversionErr(convertErr)
		}
		return internalSpace, nil
	default:
		return intmodel.Space{}, wrapConversionErr(fmt.Errorf("unsupported apiVersion %q", version))
	}
}

func (r *Exec) readStackInternal(filePath string) (intmodel.Stack, error) {
	raw, err := metadata.ReadRaw(r.ctx, r.logger, filePath)
	if err != nil {
		return intmodel.Stack{}, err
	}
	version, err := fs.DetectMetadataVersion(raw)
	if err != nil {
		return intmodel.Stack{}, wrapConversionErr(err)
	}
	switch version {
	case apischeme.VersionV1Beta1:
		var stackDoc v1beta1.StackDoc
		if unmarshalErr := json.Unmarshal(raw, &stackDoc); unmarshalErr != nil {
			return intmodel.Stack{}, wrapConversionErr(unmarshalErr)
		}
		internalStack, convertErr := apischeme.ConvertStackDocToInternal(stackDoc)
		if convertErr != nil {
			return intmodel.Stack{}, wrapConversionErr(convertErr)
		}
		return internalStack, nil
	default:
		return intmodel.Stack{}, wrapConversionErr(fmt.Errorf("unsupported apiVersion %q", version))
	}
}

func (r *Exec) readCellInternal(filePath string) (intmodel.Cell, error) {
	raw, err := metadata.ReadRaw(r.ctx, r.logger, filePath)
	if err != nil {
		return intmodel.Cell{}, err
	}
	version, err := fs.DetectMetadataVersion(raw)
	if err != nil {
		return intmodel.Cell{}, wrapConversionErr(err)
	}
	switch version {
	case apischeme.VersionV1Beta1:
		var cellDoc v1beta1.CellDoc
		if unmarshalErr := json.Unmarshal(raw, &cellDoc); unmarshalErr != nil {
			return intmodel.Cell{}, wrapConversionErr(unmarshalErr)
		}
		internalCell, convertErr := apischeme.ConvertCellDocToInternal(cellDoc)
		if convertErr != nil {
			return intmodel.Cell{}, wrapConversionErr(convertErr)
		}
		return internalCell, nil
	default:
		return intmodel.Cell{}, wrapConversionErr(fmt.Errorf("unsupported apiVersion %q", version))
	}
}
