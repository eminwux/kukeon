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
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ParseCapabilities splits an io.kukeon.capabilities label value into its
// capability names: comma- or whitespace-separated, trimmed, de-duplicated and
// sorted.
func ParseCapabilities(label string) []string {
	fields := strings.FieldsFunc(label, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	seen := map[string]struct{}{}
	var out []string
	for _, f := range fields {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// MissingCapabilities returns the required capabilities absent from have.
func MissingCapabilities(have, required []string) []string {
	var missing []string
	for _, capability := range required {
		if !slices.Contains(have, capability) {
			missing = append(missing, capability)
		}
	}
	return missing
}

// imageConfigLabels reads the labels of the image's OCI config for the
// platform containerd resolves by default.
func (c *client) imageConfigLabels(namespace string, img containerd.Image) (map[string]string, error) {
	nsCtx := c.namespaceCtx(namespace)
	desc, err := img.Config(nsCtx)
	if err != nil {
		return nil, fmt.Errorf("resolve image config: %w", err)
	}
	raw, err := content.ReadBlob(nsCtx, img.ContentStore(), desc)
	if err != nil {
		return nil, fmt.Errorf("read image config: %w", err)
	}
	var config ocispec.Image
	if err = json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}
	return config.Config.Labels, nil
}

// imageCapabilities returns the capabilities the image declares, or nil when
// it declares none or its config cannot be read (a listing must not fail on
// one unreadable image).
func (c *client) imageCapabilities(namespace string, img containerd.Image) []string {
	labels, err := c.imageConfigLabels(namespace, img)
	if err != nil {
		c.logger.DebugContext(c.ctx, "failed to read image capabilities",
			"namespace", namespace, "image", img.Name(), "err", formatError(err))
		return nil
	}
	label, ok := labels[v1beta1.ImageLabelCapabilities]
	if !ok {
		return nil
	}
	return ParseCapabilities(label)
}

// checkImageCapabilities enforces a cell's required capabilities against the
// image's io.kukeon.capabilities label (issue #1472, step 9).
//
// An image that declares the label and lacks a required capability is refused
// with ErrImageCapabilityMissing. An image that carries no such label at all
// is allowed with a warning: images built before the label existed (and
// arbitrary third-party images) declare nothing, and refusing them would break
// every cell that ran on them the day a blueprint gained `requires`.
func (c *client) checkImageCapabilities(namespace string, img containerd.Image, required []string) error {
	if len(required) == 0 {
		return nil
	}
	labels, err := c.imageConfigLabels(namespace, img)
	if err != nil {
		return fmt.Errorf("check image capabilities for %s: %w", img.Name(), err)
	}
	label, declared := labels[v1beta1.ImageLabelCapabilities]
	if !declared {
		c.logger.WarnContext(c.ctx,
			"image declares no capabilities; required capabilities not verified",
			"namespace", namespace, "image", img.Name(),
			"label", v1beta1.ImageLabelCapabilities, "required", strings.Join(required, ","))
		return nil
	}
	have := ParseCapabilities(label)
	if missing := MissingCapabilities(have, required); len(missing) > 0 {
		return fmt.Errorf(
			"%w: image %s provides [%s] but the cell requires [%s] (missing: %s); "+
				"pick an image whose %s label lists them, or drop the requirement from the config",
			internalerrdefs.ErrImageCapabilityMissing, img.Name(),
			strings.Join(have, ", "), strings.Join(required, ", "), strings.Join(missing, ", "),
			v1beta1.ImageLabelCapabilities,
		)
	}
	return nil
}
