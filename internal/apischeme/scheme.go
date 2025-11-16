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

package apischeme

import (
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Supported versions.
const (
	VersionV1Beta1 = ext.APIVersionV1Beta1
)

// ConvertRealmDocToInternal converts an external RealmDoc to the internal hub type.
func ConvertRealmDocToInternal(in ext.RealmDoc) (intmodel.Realm, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Realm{
			Metadata: intmodel.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.RealmSpec{
				Namespace: in.Spec.Namespace,
			},
			Status: intmodel.RealmStatus{
				State: intmodel.RealmState(in.Status.State),
			},
		}, nil
	default:
		return intmodel.Realm{}, fmt.Errorf("unsupported apiVersion for Realm: %s", in.APIVersion)
	}
}

// BuildRealmExternalFromInternal emits an external RealmDoc for a given version from an internal hub object.
func BuildRealmExternalFromInternal(in intmodel.Realm, apiVersion ext.Version) (ext.RealmDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		return ext.RealmDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata: ext.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.RealmSpec{
				Namespace: in.Spec.Namespace,
			},
			Status: ext.RealmStatus{
				State: ext.RealmState(in.Status.State),
			},
		}, nil
	default:
		return ext.RealmDoc{}, fmt.Errorf("unsupported output apiVersion for Realm: %s", apiVersion)
	}
}

// NormalizeRealm takes an external RealmDoc request and returns an internal object and chosen apiVersion.
// For now, defaulting is minimal; future versions can enrich defaults here.
func NormalizeRealm(req ext.RealmDoc) (intmodel.Realm, ext.Version, error) {
	version := req.APIVersion
	if version == "" {
		version = VersionV1Beta1
	}
	internal, err := ConvertRealmDocToInternal(req)
	if err != nil {
		return intmodel.Realm{}, "", err
	}
	return internal, version, nil
}
