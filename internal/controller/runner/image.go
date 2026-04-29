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

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// ListImages enumerates images in the given containerd namespace. The
// caller (controller) is responsible for resolving the realm to a
// namespace and ensuring the realm exists; this method only routes the
// call onto a connected containerd client.
func (r *Exec) ListImages(namespace string) ([]ctr.ImageInfo, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, errdefs.ErrCheckNamespaceExists
	}
	if err := r.ensureClientConnected(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	r.ctrClient.SetNamespace(namespace)
	return r.ctrClient.ListImages()
}

// GetImage returns metadata for the named image ref in the given
// containerd namespace. errdefs.ErrImageNotFound is propagated unchanged
// so callers can use errors.Is for not-found detection.
func (r *Exec) GetImage(namespace, ref string) (ctr.ImageInfo, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return ctr.ImageInfo{}, errdefs.ErrCheckNamespaceExists
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ctr.ImageInfo{}, errdefs.ErrImageNotFound
	}
	if err := r.ensureClientConnected(); err != nil {
		return ctr.ImageInfo{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	r.ctrClient.SetNamespace(namespace)
	return r.ctrClient.GetImage(ref)
}

// DeleteImage removes the named image ref from the given containerd
// namespace. errdefs.ErrImageNotFound is propagated unchanged so callers
// can use errors.Is for not-found detection.
func (r *Exec) DeleteImage(namespace, ref string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return errdefs.ErrCheckNamespaceExists
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errdefs.ErrImageNotFound
	}
	if err := r.ensureClientConnected(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	r.ctrClient.SetNamespace(namespace)
	return r.ctrClient.DeleteImage(ref)
}
