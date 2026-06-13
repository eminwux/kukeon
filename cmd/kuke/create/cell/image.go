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

package cell

import (
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	// ImageContainerID is the id of the single user container synthesized for
	// the imperative `--image` source. The runner synthesizes the root
	// container at create time (the path docs/examples/hello-world.yaml relies
	// on), so the synthesized cell carries only this one user entry.
	ImageContainerID = "main"

	// ImageDefaultCommand is the entrypoint baked into the synthesized
	// container when `--command` is omitted: an interactive shell, so a bare
	// `kuke run --image <ref>` drops the operator into a usable terminal.
	ImageDefaultCommand = "/bin/sh"
)

// SynthesizeFromImage builds a minimal single-container CellDoc from an
// imperative image ref — the shared synthesis behind `kuke run --image` and
// `kuke create cell --image` (epic:first-run #1244/#1245). The cell carries one
// attachable user container running the given image; `command` overrides its
// entrypoint, falling back to ImageDefaultCommand when empty. The runner
// synthesizes the root container at create time.
//
// The returned doc has no name and no realm/space/stack scope — the caller
// finalizes both (run/create resolve the name via the shared allocator and
// overlay scope from --realm/--space/--stack or session defaults) before
// persisting. Keeping naming + scope out of synthesis is what lets the two
// verbs share one entrypoint without colliding on a command-specific flag set.
func SynthesizeFromImage(image, command string) (v1beta1.CellDoc, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return v1beta1.CellDoc{}, errdefs.ErrImageRequired
	}
	command = strings.TrimSpace(command)
	if command == "" {
		command = ImageDefaultCommand
	}
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Spec: v1beta1.CellSpec{
			Containers: []v1beta1.ContainerSpec{
				{
					ID:         ImageContainerID,
					Image:      image,
					Command:    command,
					Attachable: true,
				},
			},
		},
	}, nil
}

// ImageNamePrefix derives the `<prefix>-<6hex>` name prefix for a cell
// synthesized from an image ref, used when `kuke run --image` is invoked
// without an explicit `--name`. It takes the image's final path segment with
// any tag/digest stripped (`docker.io/library/alpine:3` → `alpine`,
// `localhost:5000/myapp:dev` → `myapp`), lower-cases it, and replaces any
// character outside [a-z0-9-] with `-`. An image that reduces to nothing usable
// falls back to ImageNameFallbackPrefix so the generator always has a seed.
func ImageNamePrefix(image string) string {
	ref := strings.TrimSpace(image)
	// Drop any digest (`@sha256:...`) first so a digest colon is never mistaken
	// for a tag separator.
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref = ref[:at]
	}
	// The last path segment is the repository's short name; everything before
	// the final `/` is registry host + namespace (which may carry a `:port`).
	if slash := strings.LastIndexByte(ref, '/'); slash >= 0 {
		ref = ref[slash+1:]
	}
	// A remaining colon now separates the short name from its tag.
	if colon := strings.IndexByte(ref, ':'); colon >= 0 {
		ref = ref[:colon]
	}
	ref = sanitizeNamePrefix(ref)
	if ref == "" {
		return ImageNameFallbackPrefix
	}
	return ref
}

// ImageNameFallbackPrefix is the seed ImageNamePrefix returns when an image ref
// has no usable short name (e.g. it is all separators).
const ImageNameFallbackPrefix = "cell"

// sanitizeNamePrefix lower-cases s and rewrites every rune outside [a-z0-9-] to
// `-`, then trims leading/trailing `-` so the generated `<prefix>-<6hex>` keeps
// a single separator before the suffix.
func sanitizeNamePrefix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
