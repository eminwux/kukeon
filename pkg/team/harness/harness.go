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
	"maps"
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
	// Secrets are the secret slots the harness exposes in every mode — name
	// plus the fixed env destination. A team role fills one by naming it.
	Secrets []api.BlueprintSecretSlot `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
	// Modes are the ways the harness can be started (interactive, headless,
	// …). The mode is picked per cell at create time; a mode's parameters and
	// secrets apply only to cells created in it. spec.onInit stages run first
	// in every mode, followed by the mode's own. A harness without modes
	// starts from spec.onInit alone.
	Modes  map[string]Mode `json:"modes,omitempty"       yaml:"modes,omitempty"`
	Images []Image         `json:"images"                yaml:"images"`
}

// Mode is one way to start the harness.
type Mode struct {
	Description string                       `json:"description,omitempty" yaml:"description,omitempty"`
	OnInit      []Script                     `json:"onInit"                yaml:"onInit"`
	Parameters  []api.CellBlueprintParameter `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	Environment map[string]string            `json:"environment,omitempty" yaml:"environment,omitempty"`
	Secrets     []api.BlueprintSecretSlot    `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
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

// Image is one image a harness runs on. An entry with a build is an image the
// harness builds and publishes; an entry with no build names a published
// shared base (base-user or base-root) the harness runs on as-is.
type Image struct {
	Name         string   `json:"name"         yaml:"name"`
	Build        Build    `json:"build"        yaml:"build"`
	Capabilities []string `json:"capabilities" yaml:"capabilities"`
}

// sharedBases are the published base images a build-less image entry may name.
//
//nolint:gochecknoglobals // package-level membership set, read-only.
var sharedBases = map[string]bool{"base-user": true, "base-root": true}

// Shared reports whether the entry names a published shared base rather than
// an image the harness builds.
func (i Image) Shared() bool { return i.Build == (Build{}) }

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
	if h.Spec.SkillPath == "" || (h.Spec.Shell != "/bin/bash" && h.Spec.Shell != "/bin/sh") {
		return errors.New("require skillPath and shell (/bin/bash or /bin/sh)")
	}
	if len(h.Spec.OnInit) == 0 && len(h.Spec.Modes) == 0 {
		return errors.New("require nonempty ordered spec.onInit scripts or spec.modes")
	}
	if err := scripts("spec.onInit", h.Spec.OnInit); err != nil {
		return err
	}
	// PROMPT stays a built-in parameter only for a harness without modes; a
	// harness with modes declares it on the mode that reads it.
	namesOfParams := map[string]bool{"AGENT_NAME": true}
	if len(h.Spec.Modes) == 0 {
		namesOfParams["PROMPT"] = true
	}
	if err := parameters("spec.parameters", h.Spec.Parameters, namesOfParams); err != nil {
		return err
	}
	if err := environment("spec.environment", h.Spec.Environment); err != nil {
		return err
	}
	if err := secretSlots("spec.secrets", h.Spec.Secrets, map[string]bool{}, map[string]bool{}); err != nil {
		return err
	}
	for name, mode := range h.Spec.Modes {
		field := "spec.modes." + name
		if !namePattern.MatchString(name) {
			return fmt.Errorf("%s: invalid mode name", field)
		}
		if len(mode.OnInit) == 0 {
			return fmt.Errorf("%s.onInit: at least one script required", field)
		}
		if err := scripts(field+".onInit", mode.OnInit); err != nil {
			return err
		}
		// A mode may redeclare nothing the harness already declares.
		if err := parameters(field+".parameters", mode.Parameters, maps.Clone(namesOfParams)); err != nil {
			return err
		}
		if err := environment(field+".environment", mode.Environment); err != nil {
			return err
		}
		for key := range mode.Environment {
			if _, dup := h.Spec.Environment[key]; dup {
				return fmt.Errorf("%s.environment: %s is already set by spec.environment", field, key)
			}
		}
		names, destinations := map[string]bool{}, map[string]bool{}
		for _, slot := range h.Spec.Secrets {
			names[slot.Name], destinations[slot.EnvName] = true, true
		}
		if err := secretSlots(field+".secrets", mode.Secrets, names, destinations); err != nil {
			return err
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
		if image.Shared() {
			if !sharedBases[image.Name] {
				return fmt.Errorf(
					"image %q: an entry with no build must name a shared base (base-user or base-root)",
					image.Name,
				)
			}
		} else if !relative(image.Build.Context) || !relative(image.Build.Dockerfile) {
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

func scripts(field string, stages []Script) error {
	for _, stage := range stages {
		if strings.TrimSpace(stage.Script) == "" {
			return fmt.Errorf("%s: script must not be empty", field)
		}
	}
	return nil
}

func parameters(field string, list []api.CellBlueprintParameter, seen map[string]bool) error {
	for _, param := range list {
		if !envPattern.MatchString(param.Name) || seen[param.Name] {
			return fmt.Errorf("%s: invalid, reserved or duplicate name", field)
		}
		seen[param.Name] = true
	}
	return nil
}

func environment(field string, values map[string]string) error {
	for name := range values {
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
					"GIT_CONFIG_SYSTEM",
					"IS_SANDBOX",
				},
				name,
			) {
			return fmt.Errorf("%s: invalid or reserved input name", field)
		}
	}
	return nil
}

// secretSlots checks env-destination secret slots; names and destinations
// accumulate so a mode cannot collide with the harness-wide slots.
func secretSlots(field string, slots []api.BlueprintSecretSlot, names, destinations map[string]bool) error {
	for _, slot := range slots {
		if !namePattern.MatchString(slot.Name) || names[slot.Name] {
			return fmt.Errorf("%s: invalid or duplicate secret name %q", field, slot.Name)
		}
		names[slot.Name] = true
		if (slot.Mode != "" && slot.Mode != api.BlueprintSecretModeEnv) || slot.MountPath != "" {
			return fmt.Errorf("%s: secret %q: only environment destinations are supported", field, slot.Name)
		}
		if !envPattern.MatchString(slot.EnvName) || destinations[slot.EnvName] {
			return fmt.Errorf("%s: secret %q: invalid or duplicate envName", field, slot.Name)
		}
		destinations[slot.EnvName] = true
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
