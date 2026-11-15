// Copyright 2026 Emiliano Spinella (eminwux)
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

// Package harness owns source Harness declarations and generated release catalogs.
// These are kuketeams composition inputs, not kukeon daemon documents.
package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/distribution/reference"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const APIVersion = "kuketeams.io/v1"

type Harness struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind"       yaml:"kind"`
	Metadata   model.Metadata `json:"metadata"   yaml:"metadata"`
	Spec       Spec           `json:"spec"       yaml:"spec"`
}

type Spec struct {
	SkillPath   string                       `json:"skillPath"             yaml:"skillPath"`
	Renderer    string                       `json:"renderer"              yaml:"renderer"`
	Shell       string                       `json:"shell"                 yaml:"shell"`
	OnInit      []Script                     `json:"onInit"                yaml:"onInit"`
	Environment map[string]string            `json:"environment,omitempty" yaml:"environment,omitempty"`
	Parameters  []api.CellBlueprintParameter `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	StatePath   string                       `json:"statePath,omitempty"   yaml:"statePath,omitempty"`
	Mounts      []Mount                      `json:"mounts,omitempty"      yaml:"mounts,omitempty"`
	Seeds       []model.HarnessSeed          `json:"seeds,omitempty"       yaml:"seeds,omitempty"`
	Images      []Image                      `json:"images"                yaml:"images"`
}

// Script is opaque shell source, never a Go template. New lifecycle hooks are
// intentionally unsupported by this version of the renderer contract.
type Script struct {
	Script string `json:"script" yaml:"script"`
}
type Mount struct {
	Source   string `json:"source"             yaml:"source"`
	Target   string `json:"target"             yaml:"target"`
	ReadOnly bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

type Image struct {
	Name         string   `json:"name"         yaml:"name"`
	Build        Build    `json:"build"        yaml:"build"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

type Build struct {
	Context    string `json:"context"    yaml:"context"`
	Dockerfile string `json:"dockerfile" yaml:"dockerfile"`
}

type ImageCatalog struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind"       yaml:"kind"`
	Metadata   model.Metadata `json:"metadata"   yaml:"metadata"`
	Spec       CatalogSpec    `json:"spec"       yaml:"spec"`
}

type CatalogSpec struct {
	Images []CatalogImage `json:"images" yaml:"images"`
}

type CatalogImage struct {
	Image        string   `json:"image"        yaml:"image"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

var envPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// Load decodes one strict document and checks the authored build contract.
func Load(source fs.FS, directory string) (*Harness, error) {
	filename := path.Join(directory, "harness.yaml")
	var h Harness
	if err := read(source, filename, &h); err != nil {
		return nil, err
	}
	if err := h.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	return &h, nil
}

func (h *Harness) Validate() error {
	if h == nil || h.APIVersion != APIVersion || h.Kind != "Harness" || !namePattern.MatchString(h.Metadata.Name) {
		return errors.New("require kuketeams.io/v1 Harness with valid metadata.name")
	}
	if h.Spec.Renderer != "kukeon/v1beta1" {
		return fmt.Errorf(
			"spec.renderer: unsupported %q; select bundled kukeon/v1beta1 (migrate legacy template/makeTarget to renderer/onInit)",
			h.Spec.Renderer,
		)
	}
	if h.Spec.SkillPath == "" || (h.Spec.Shell != "/bin/bash" && h.Spec.Shell != "/bin/sh") || len(h.Spec.OnInit) == 0 {
		return errors.New("require skillPath, shell (/bin/bash or /bin/sh), and nonempty ordered spec.onInit scripts")
	}
	for _, stage := range h.Spec.OnInit {
		if strings.TrimSpace(stage.Script) == "" {
			return errors.New("spec.onInit: script must not be empty")
		}
	}
	namesOfParams := map[string]bool{"PROMPT": true, "AGENT_NAME": true}
	for _, param := range h.Spec.Parameters {
		if !envPattern.MatchString(param.Name) || namesOfParams[param.Name] {
			return errors.New("spec.parameters: invalid, reserved or duplicate name")
		}
		namesOfParams[param.Name] = true
	}
	for name := range h.Spec.Environment {
		if !envPattern.MatchString(name) ||
			slices.Contains(
				[]string{
					"ROLE_SOURCE_DIR",
					"ROLE_PATH",
					"INITIAL_PATH",
					"HARNESS_SETTINGS",
					"PROMPT",
					"AGENT_NAME",
					"HOME",
					"GIT_SSH_COMMAND",
					"IS_SANDBOX",
				},
				name,
			) {
			return errors.New("spec.environment: invalid or reserved input name")
		}
	}
	if len(h.Spec.Images) == 0 {
		return errors.New("spec.images: at least one image required")
	}
	names := map[string]bool{}
	for _, image := range h.Spec.Images {
		if !namePattern.MatchString(image.Name) || names[image.Name] || len(image.Name) > 128 {
			return fmt.Errorf("spec.images: duplicate or invalid image name %q", image.Name)
		}
		names[image.Name] = true
		if !relative(image.Build.Context) || !relative(image.Build.Dockerfile) {
			return fmt.Errorf(
				"image %q: build context must be repository-relative and Dockerfile context-relative",
				image.Name,
			)
		}
		if err := capabilities(image.Capabilities, h.Metadata.Name); err != nil {
			return fmt.Errorf("image %q: %w", image.Name, err)
		}
	}
	return nil
}

func relative(value string) bool {
	return value != "" && fs.ValidPath(value) && !strings.Contains(value, `\`)
}

func capabilities(values []string, name string) error {
	if !slices.Contains(values, name) {
		return fmt.Errorf("capabilities must include harness %q", name)
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !namePattern.MatchString(value) || seen[value] {
			return fmt.Errorf("duplicate or invalid capability %q", value)
		}
		seen[value] = true
	}
	return nil
}

// Reference shares naming between release generation and local image builds.
// An explicitly empty registry selects local names without a leading slash.
func Reference(registry, name, tag string) (string, error) {
	prefix := ""
	if registry != "" {
		prefix = registry + "/"
	}
	ref := prefix + name + ":" + tag
	if !namePattern.MatchString(name) || tag == "" || strings.HasSuffix(registry, "/") {
		return "", fmt.Errorf("invalid image registry/name/tag: %q", ref)
	}
	if _, err := reference.ParseNormalizedNamed(ref); err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	return ref, nil
}

func Generate(h *Harness, registry, tag string) (*ImageCatalog, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	catalog := &ImageCatalog{
		APIVersion: APIVersion,
		Kind:       "HarnessImageCatalog",
		Metadata:   model.Metadata{Name: h.Metadata.Name},
	}
	for _, image := range h.Spec.Images {
		ref, err := Reference(registry, image.Name, tag)
		if err != nil {
			return nil, err
		}
		catalog.Spec.Images = append(
			catalog.Spec.Images,
			CatalogImage{Image: ref, Capabilities: slices.Clone(image.Capabilities)},
		)
	}
	return catalog, nil
}

func LoadCatalog(source fs.FS, directory, name string) (*ImageCatalog, error) {
	filename := path.Join(directory, "images.yaml")
	var c ImageCatalog
	if err := read(source, filename, &c); err != nil {
		return nil, err
	}
	if err := c.Validate(name); err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	return &c, nil
}

func (c *ImageCatalog) Validate(name string) error {
	if c == nil || c.APIVersion != APIVersion || c.Kind != "HarnessImageCatalog" || c.Metadata.Name != name ||
		!namePattern.MatchString(name) {
		return fmt.Errorf("require kuketeams.io/v1 HarnessImageCatalog with metadata.name %q", name)
	}
	if len(c.Spec.Images) == 0 {
		return errors.New("spec.images: at least one image required")
	}
	seen := map[string]bool{}
	for _, image := range c.Spec.Images {
		if _, err := reference.ParseNormalizedNamed(image.Image); err != nil || image.Image == "" || seen[image.Image] {
			return fmt.Errorf("duplicate or invalid image reference %q", image.Image)
		}
		seen[image.Image] = true
		if err := capabilities(image.Capabilities, name); err != nil {
			return fmt.Errorf("image %q: %w", image.Image, err)
		}
	}
	return nil
}

// MarshalCatalog fixes indentation and declaration order for drift checking.
func MarshalCatalog(c *ImageCatalog) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	const indent = 2
	enc.SetIndent(indent)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func read(source fs.FS, filename string, out any) error {
	raw, err := fs.ReadFile(source, filename)
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if decodeErr := d.Decode(out); decodeErr != nil {
		return fmt.Errorf(
			"%s: %w; legacy template/makeTarget must migrate to renderer/onInit; unknown requested fields are unsupported",
			filename,
			decodeErr,
		)
	}
	var extra any
	if extraErr := d.Decode(&extra); !errors.Is(extraErr, io.EOF) {
		return fmt.Errorf("%s: expected exactly one YAML document", filename)
	}
	return nil
}
