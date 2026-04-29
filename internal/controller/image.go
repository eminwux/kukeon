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
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// ImageInfo is the controller-layer view of a containerd image. The fields
// are a re-export of internal/ctr's ImageInfo so transports above the
// controller never need to import the ctr package.
type ImageInfo struct {
	Name      string
	Size      int64
	CreatedAt time.Time
	Digest    string
	MediaType string
	Labels    map[string]string
}

// ListImagesResult reports the images present in a realm's containerd
// namespace.
type ListImagesResult struct {
	Realm     string
	Namespace string
	Images    []ImageInfo
}

// GetImageResult reports the metadata of one named image in a realm.
type GetImageResult struct {
	Realm     string
	Namespace string
	Image     ImageInfo
}

// ListImages enumerates images in the realm's containerd namespace. The
// realm is validated up-front so callers see ErrRealmNotFound before any
// containerd round-trip; the namespace mapping mirrors LoadImage.
func (b *Exec) ListImages(realm string) (ListImagesResult, error) {
	var res ListImagesResult

	realmName := strings.TrimSpace(realm)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
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
	imgs, err := b.runner.ListImages(namespace)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrListImages, err)
	}

	res.Realm = realmName
	res.Namespace = namespace
	res.Images = make([]ImageInfo, 0, len(imgs))
	for _, img := range imgs {
		res.Images = append(res.Images, ctrImageToControllerImage(img))
	}
	return res, nil
}

// GetImage returns metadata for the named image ref in the realm's
// containerd namespace. errdefs.ErrImageNotFound is propagated unchanged
// so callers (CLI, RPC) can map it to a clean "image not found" message.
func (b *Exec) GetImage(realm, ref string) (GetImageResult, error) {
	var res GetImageResult

	realmName := strings.TrimSpace(realm)
	if realmName == "" {
		return res, errdefs.ErrRealmNameRequired
	}
	imageRef := strings.TrimSpace(ref)
	if imageRef == "" {
		return res, errdefs.ErrImageNotFound
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
	img, err := b.runner.GetImage(namespace, imageRef)
	if err != nil {
		if errors.Is(err, errdefs.ErrImageNotFound) {
			return res, err
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetImage, err)
	}

	res.Realm = realmName
	res.Namespace = namespace
	res.Image = ctrImageToControllerImage(img)
	return res, nil
}

func ctrImageToControllerImage(img ctr.ImageInfo) ImageInfo {
	return ImageInfo{
		Name:      img.Name,
		Size:      img.Size,
		CreatedAt: img.CreatedAt,
		Digest:    img.Digest,
		MediaType: img.MediaType,
		Labels:    img.Labels,
	}
}
