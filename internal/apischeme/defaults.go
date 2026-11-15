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
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ConvertScopeDefaultsToInternal maps the external ScopeDefaults (#1418).
func ConvertScopeDefaultsToInternal(in *ext.ScopeDefaults) *intmodel.ScopeDefaults {
	if in == nil {
		return nil
	}
	return &intmodel.ScopeDefaults{
		Values:  copyStringMap(in.Values),
		Repos:   repoFillsToInternal(in.Repos),
		Secrets: secretFillsToInternal(in.Secrets),
	}
}

// BuildScopeDefaultsExternalFromInternal is the inverse of
// ConvertScopeDefaultsToInternal.
func BuildScopeDefaultsExternalFromInternal(in *intmodel.ScopeDefaults) *ext.ScopeDefaults {
	if in == nil {
		return nil
	}
	return &ext.ScopeDefaults{
		Values:  copyStringMap(in.Values),
		Repos:   repoFillsToExternal(in.Repos),
		Secrets: secretFillsToExternal(in.Secrets),
	}
}

// SpaceDefaultsFill returns the CellConfig-fill part of the internal Space
// defaults in the external ScopeDefaults shape (nil when there is none).
func SpaceDefaultsFill(in *intmodel.SpaceDefaults) *ext.ScopeDefaults {
	if in == nil {
		return nil
	}
	return BuildScopeDefaultsExternalFromInternal(&intmodel.ScopeDefaults{
		Values:  in.Values,
		Repos:   in.Repos,
		Secrets: in.Secrets,
	})
}

func repoFillsToInternal(in map[string]ext.CellConfigRepoFill) map[string]intmodel.RepoFill {
	if in == nil {
		return nil
	}
	out := make(map[string]intmodel.RepoFill, len(in))
	for k, v := range in {
		out[k] = intmodel.RepoFill{URL: v.URL, Branch: v.Branch, Ref: v.Ref}
	}
	return out
}

func repoFillsToExternal(in map[string]intmodel.RepoFill) map[string]ext.CellConfigRepoFill {
	if in == nil {
		return nil
	}
	out := make(map[string]ext.CellConfigRepoFill, len(in))
	for k, v := range in {
		out[k] = ext.CellConfigRepoFill{URL: v.URL, Branch: v.Branch, Ref: v.Ref}
	}
	return out
}

func secretFillsToInternal(in map[string]ext.CellConfigSecretFill) map[string]intmodel.SecretFill {
	if in == nil {
		return nil
	}
	out := make(map[string]intmodel.SecretFill, len(in))
	for k, v := range in {
		out[k] = intmodel.SecretFill{SecretRef: secretRefToInternal(v.SecretRef)}
	}
	return out
}

func secretFillsToExternal(in map[string]intmodel.SecretFill) map[string]ext.CellConfigSecretFill {
	if in == nil {
		return nil
	}
	out := make(map[string]ext.CellConfigSecretFill, len(in))
	for k, v := range in {
		out[k] = ext.CellConfigSecretFill{SecretRef: secretRefToExternal(v.SecretRef)}
	}
	return out
}

func realmImagesToInternal(in []ext.RealmImage) []intmodel.RealmImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.RealmImage, len(in))
	for i, img := range in {
		out[i] = intmodel.RealmImage{Name: img.Name, Ref: img.Ref}
		if img.Build != nil {
			out[i].Build = &intmodel.RealmImageBuild{
				Context:    img.Build.Context,
				Dockerfile: img.Build.Dockerfile,
				Args:       copyStringMap(img.Build.Args),
				Tag:        img.Build.Tag,
			}
		}
	}
	return out
}

func realmImagesToExternal(in []intmodel.RealmImage) []ext.RealmImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.RealmImage, len(in))
	for i, img := range in {
		out[i] = ext.RealmImage{Name: img.Name, Ref: img.Ref}
		if img.Build != nil {
			out[i].Build = &ext.RealmImageBuild{
				Context:    img.Build.Context,
				Dockerfile: img.Build.Dockerfile,
				Args:       copyStringMap(img.Build.Args),
				Tag:        img.Build.Tag,
			}
		}
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// validateRealmImages enforces the RealmImage contract (#1418): every entry
// has a unique non-empty name and exactly one of ref or build, and a build
// names its context.
func validateRealmImages(images []ext.RealmImage) error {
	seen := map[string]struct{}{}
	for i, img := range images {
		name := strings.TrimSpace(img.Name)
		if name == "" {
			return fmt.Errorf("spec.images[%d]: name is required", i)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("spec.images[%d]: duplicate image name %q", i, name)
		}
		seen[name] = struct{}{}
		hasRef := strings.TrimSpace(img.Ref) != ""
		hasBuild := img.Build != nil
		switch {
		case hasRef == hasBuild:
			return fmt.Errorf("spec.images[%d] (%s): exactly one of ref or build is required", i, name)
		case hasBuild && strings.TrimSpace(img.Build.Context) == "":
			return fmt.Errorf("spec.images[%d] (%s): build.context is required", i, name)
		}
	}
	return nil
}
