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
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// inventoryImage is the browser view of one containerd image (issue #1406).
// Size is null when containerd cannot resolve it locally, so an unknown
// size is never rendered as zero.
type inventoryImage struct {
	Name      string            `json:"name"`
	Size      *int64            `json:"size"`
	CreatedAt *time.Time        `json:"createdAt"`
	Digest    string            `json:"digest"`
	MediaType string            `json:"mediaType"`
	Labels    map[string]string `json:"labels"`
	// Capabilities are the image's io.kukeon.capabilities; the create form
	// offers an image only when it covers the blueprint's requires.
	Capabilities []string `json:"capabilities"`
}

type imageInventory struct {
	Realm     string           `json:"realm"`
	Namespace string           `json:"namespace"`
	Images    []inventoryImage `json:"images"`
}

type imageInventoryDetail struct {
	Realm     string         `json:"realm"`
	Namespace string         `json:"namespace"`
	Image     inventoryImage `json:"image"`
}

// registerImageInventoryRoutes proxies the read-only ListImages / GetImage
// RPCs. kuke serve never opens the containerd socket itself; kukeond reads
// the realm's containerd namespace over its Unix-socket API. The detail
// route takes the remaining path as the image reference because references
// contain "/" (registry/repository) and ":" (tag).
func registerImageInventoryRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc("GET /api/realms/{realm}/image-inventory",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			realm, err := inventoryRealm(r)
			if err != nil {
				return nil, err
			}
			result, err := c.ListImages(ctx, realm)
			if err != nil {
				return nil, err
			}
			out := imageInventory{
				Realm:     result.Realm,
				Namespace: result.Namespace,
				Images:    make([]inventoryImage, 0, len(result.Images)),
			}
			for _, img := range result.Images {
				out.Images = append(out.Images, toInventoryImage(img))
			}
			return out, nil
		}))
	mux.HandleFunc("GET /api/realms/{realm}/image-inventory/{ref...}",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			realm, err := inventoryRealm(r)
			if err != nil {
				return nil, err
			}
			ref := r.PathValue("ref")
			if strings.TrimSpace(ref) == "" || strings.ContainsRune(ref, 0) {
				return nil, fmt.Errorf("%w: invalid image reference", errdefs.ErrConfig)
			}
			result, err := c.GetImage(ctx, realm, ref)
			if err != nil {
				return nil, err
			}
			return imageInventoryDetail{
				Realm:     result.Realm,
				Namespace: result.Namespace,
				Image:     toInventoryImage(result.Image),
			}, nil
		}))
}

func inventoryRealm(r *http.Request) (string, error) {
	realm := r.PathValue("realm")
	if realm == "" || !safePathSegment(realm) {
		return "", fmt.Errorf("%w: invalid realm", errdefs.ErrConfig)
	}
	return realm, nil
}

func toInventoryImage(img kukeonv1.ImageInfo) inventoryImage {
	out := inventoryImage{
		Name:      img.Name,
		Digest:    img.Digest,
		MediaType: img.MediaType,
		Labels:    img.Labels,
	}
	if img.Size >= 0 {
		size := img.Size
		out.Size = &size
	}
	if !img.CreatedAt.IsZero() {
		created := img.CreatedAt
		out.CreatedAt = &created
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	out.Capabilities = img.Capabilities
	if out.Capabilities == nil {
		out.Capabilities = []string{}
	}
	return out
}
