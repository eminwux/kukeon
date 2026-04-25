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

package ctr

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// SbshCacheSubdir is the host-side directory under the run path that holds
// per-arch sbsh binaries. The full layout is:
//
//	<runPath>/cache/sbsh/<arch>/sbsh
//
// The foundation slice (#57) ships a stub: the daemon expects a single
// host-arch binary placed manually. The multi-arch resolver lands in #67.
const SbshCacheSubdir = "cache/sbsh"

// SbshBinaryName is the basename of the static sbsh binary inside each
// per-arch cache directory.
const SbshBinaryName = "sbsh"

// SbshCachePath returns the host path of the sbsh binary for the given
// architecture under the configured run path. Architecture is the GOARCH-
// style string ("amd64", "arm64") that comes from the image's
// ocispec.Image.Architecture, not the host's runtime.GOARCH — the cache must
// match the *image* it'll be injected into, since the image and the binary
// share the in-container ELF interpreter.
func SbshCachePath(baseRunPath, arch string) string {
	return filepath.Join(baseRunPath, SbshCacheSubdir, arch, SbshBinaryName)
}

// ResolveSbshCachePath looks up the cached sbsh binary for an already-pulled
// image. The arch is read from the image's OCI config (post-pull), which is
// the regression this helper guards against: keying off runtime.GOARCH would
// break cross-arch images that happen to run via emulation.
//
// Returns the host path on success; ErrInvalidImage when the arch cannot be
// resolved from the image config.
func (c *client) ResolveSbshCachePath(image containerd.Image, baseRunPath string) (string, error) {
	if image == nil {
		return "", fmt.Errorf("%w: image is nil", errdefs.ErrInvalidImage)
	}
	arch, err := imageArchitecture(c.namespaceCtx(), image)
	if err != nil {
		return "", err
	}
	return SbshCachePath(baseRunPath, arch), nil
}

// imageArchitecture returns the GOARCH-style architecture for an image,
// reading it from the image's OCI config. Falls back to runtime.GOARCH only
// when the image config is unreadable AND the image declares no platform —
// kept narrow on purpose so silent host/image arch mismatches still surface.
func imageArchitecture(ctx context.Context, image containerd.Image) (string, error) {
	cfg, err := image.Spec(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: read image spec: %w", errdefs.ErrInvalidImage, err)
	}
	arch := strings.TrimSpace(cfg.Architecture)
	if arch == "" {
		// An image with no arch in its config is a malformed manifest, not
		// something we should silently paper over with the host arch.
		return "", fmt.Errorf("%w: image %q has no Architecture in its OCI config", errdefs.ErrInvalidImage, image.Name())
	}
	return arch, nil
}

