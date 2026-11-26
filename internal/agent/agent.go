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

// Package agent is New agent (issue #1692): it copies an agent template — a
// CellConfig of the system default team, labelled kukeon.io/template — into a
// Config of its own and creates and starts that Config's first cell. The web
// server (POST /api/agents) and `kuke create agent` both call it, with a
// client they own.
package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// templateRole is the role of the default team the templates run.
const templateRole = "agent"

// templateScope is where the default team composes its Configs: the
// composition default, realm, space and stack default.
const templateScope = "default"

// maxNameAttempts bounds the retries of a generated Config name that is
// already taken. A 24-bit suffix makes even one collision unlikely.
const maxNameAttempts = 8

// Harnesses lists the harnesses New agent offers, sorted: those of the
// default team's declaration, which has a template for each.
func Harnesses() []string {
	harnesses := config.DefaultTeam().Spec.Harnesses
	names := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		names = append(names, h.Name)
	}
	slices.Sort(names)
	return names
}

// TemplateName is the name of the agent template of harness:
// default-agent-<harness>, the Config the default team composes for the
// agent role on that harness.
func TemplateName(harness string) (string, error) {
	if !slices.Contains(Harnesses(), harness) {
		return "", fmt.Errorf("%w: unknown harness %q; New agent offers %s",
			errdefs.ErrConfig, harness, strings.Join(Harnesses(), " and "))
	}
	return config.DefaultTeamName + "-" + templateRole + "-" + harness, nil
}

// TemplateRef locates the agent template of harness.
func TemplateRef(harness string) (v1beta1.CellConfigMetadata, error) {
	name, err := TemplateName(harness)
	if err != nil {
		return v1beta1.CellConfigMetadata{}, err
	}
	return v1beta1.CellConfigMetadata{
		Name: name, Realm: config.DefaultRealm, Space: templateScope, Stack: templateScope,
	}, nil
}

// Template is one harness's agent template and whether it is stored.
type Template struct {
	Harness string
	Config  v1beta1.CellConfigMetadata
	Exists  bool
}

// Templates reports, for every harness New agent offers, its template and
// whether it is stored as an agent template.
func Templates(ctx context.Context, c kukeonv1.Client) ([]Template, error) {
	out := make([]Template, 0, len(Harnesses()))
	for _, harness := range Harnesses() {
		ref, err := TemplateRef(harness)
		if err != nil {
			return nil, err
		}
		got, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: ref})
		if err != nil && !errors.Is(err, errdefs.ErrConfigNotFound) {
			return nil, err
		}
		exists := err == nil && got.MetadataExists && isTemplate(got.Config)
		out = append(out, Template{Harness: harness, Config: ref, Exists: exists})
	}
	return out, nil
}

func isTemplate(doc v1beta1.CellConfigDoc) bool {
	return doc.Metadata.Labels[v1beta1.LabelTemplate] == "true"
}

// Request is one New agent.
type Request struct {
	// Harness is claude or codex.
	Harness string
	// DisplayName is the agent's name, stored as kukeon.io/display-name.
	// Empty: none.
	DisplayName string
	// Realm, Space and Stack place the new Config. Empty: the template's
	// scope. A deeper coordinate requires every shallower one.
	Realm, Space, Stack string
}

// Step names the part of New agent that failed after the Config was stored.
type Step string

// The steps after the Config is stored.
const (
	StepCreateCell Step = "create-cell"
	StepStartCell  Step = "start-cell"
)

// StepError is a failure after the Config was stored: the Config is kept, and
// the error names it and the step that failed, so the caller can report what
// exists and retry the rest.
type StepError struct {
	Step   Step
	Config v1beta1.CellConfigMetadata
	// Cell is the cell that was created, for a failed start.
	Cell *v1beta1.CellDoc
	Err  error
}

func (e *StepError) Error() string {
	switch e.Step {
	case StepStartCell:
		name := ""
		if e.Cell != nil {
			name = e.Cell.Metadata.Name
		}
		return fmt.Sprintf("config %s stored and cell %s created, but starting it failed: %v",
			e.Config.Name, name, e.Err)
	case StepCreateCell:
		return fmt.Sprintf("config %s stored, but creating its first cell failed: %v", e.Config.Name, e.Err)
	default:
		return fmt.Sprintf("config %s stored, but creating its first cell failed: %v", e.Config.Name, e.Err)
	}
}

func (e *StepError) Unwrap() error { return e.Err }

// NewConfig is the Config New agent stores for template: its spec verbatim
// (values included, STATE_VOLUME* too, so every agent of a harness shares one
// login), at name and scope, with the template's labels but kukeon.io/team
// and kukeon.io/template, and with annotations kukeon.io/agent-template (the
// template's name) and, when displayName is set, kukeon.io/display-name.
func NewConfig(
	template v1beta1.CellConfigDoc,
	name string,
	scope v1beta1.CellConfigMetadata,
	displayName string,
) v1beta1.CellConfigDoc {
	labels := maps.Clone(template.Metadata.Labels)
	delete(labels, v1beta1.LabelTeam)
	delete(labels, v1beta1.LabelTemplate)
	if len(labels) == 0 {
		labels = nil
	}
	annotations := maps.Clone(template.Metadata.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	delete(annotations, v1beta1.AnnotationDisplayName)
	annotations[v1beta1.AnnotationAgentTemplate] = template.Metadata.Name
	if displayName != "" {
		annotations[v1beta1.AnnotationDisplayName] = displayName
	}
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name: name, Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack,
			Labels: labels, Annotations: annotations,
		},
		Spec: template.Spec,
	}
}

// NameFunc returns a candidate name for a copy of template: the default is
// "<template>-<6 hex>".
type NameFunc func(template string) (string, error)

// GenerateName is the default NameFunc.
func GenerateName(template string) (string, error) {
	suffix, err := naming.RandomHexSuffix(naming.DefaultCellNameSuffixBytes)
	if err != nil {
		return "", fmt.Errorf("generate agent name: %w", err)
	}
	return template + "-" + suffix, nil
}

// Options tune StoreConfig; the zero value is the default.
type Options struct {
	// Name replaces GenerateName (tests).
	Name NameFunc
}

// StoreConfig stores the Config of req: a copy of its harness's agent
// template under a generated name, retried on a collision. It returns the
// stored Config.
func StoreConfig(ctx context.Context, c kukeonv1.Client, req Request, opts Options) (v1beta1.CellConfigDoc, error) {
	var empty v1beta1.CellConfigDoc
	ref, err := TemplateRef(req.Harness)
	if err != nil {
		return empty, err
	}
	displayName, err := v1beta1.NormalizeDisplayName(req.DisplayName)
	if err != nil {
		return empty, fmt.Errorf("%w: %w", errdefs.ErrInvalidDisplayName, err)
	}
	got, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: ref})
	if err != nil && !errors.Is(err, errdefs.ErrConfigNotFound) {
		return empty, err
	}
	if err != nil || !got.MetadataExists || !isTemplate(got.Config) {
		return empty, fmt.Errorf("%w: %s (it is composed by the default team; run kuke team init --reapply)",
			errdefs.ErrAgentTemplateNotFound, ref.Name)
	}
	template := got.Config
	scope, err := targetScope(template.Metadata, req)
	if err != nil {
		return empty, err
	}
	generate := opts.Name
	if generate == nil {
		generate = GenerateName
	}
	for range maxNameAttempts {
		name, nameErr := generate(template.Metadata.Name)
		if nameErr != nil {
			return empty, nameErr
		}
		if err = naming.ValidateHierarchyName("config", name); err != nil {
			return empty, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
		doc := NewConfig(template, name, scope, displayName)
		created, createErr := c.CreateConfig(ctx, doc)
		if errors.Is(createErr, errdefs.ErrConfigExists) {
			continue
		}
		if createErr != nil {
			return empty, createErr
		}
		if !created.Created {
			continue
		}
		return doc, nil
	}
	return empty, fmt.Errorf("%w: no free name for a copy of %s after %d attempts",
		errdefs.ErrConfigExists, template.Metadata.Name, maxNameAttempts)
}

// targetScope is the scope the new Config is stored at: the template's,
// unless req names one.
func targetScope(template v1beta1.CellConfigMetadata, req Request) (v1beta1.CellConfigMetadata, error) {
	if req.Realm == "" && req.Space == "" && req.Stack == "" {
		return v1beta1.CellConfigMetadata{Realm: template.Realm, Space: template.Space, Stack: template.Stack}, nil
	}
	if req.Realm == "" || (req.Stack != "" && req.Space == "") {
		return v1beta1.CellConfigMetadata{}, fmt.Errorf(
			"%w: a scope needs a realm, and a stack needs a space", errdefs.ErrConfig)
	}
	for _, s := range []struct{ kind, value string }{
		{"realm", req.Realm}, {"space", req.Space}, {"stack", req.Stack},
	} {
		if s.value == "" {
			continue
		}
		if !filepath.IsLocal(s.value) || strings.ContainsAny(s.value, `/\`) {
			return v1beta1.CellConfigMetadata{}, fmt.Errorf("%w: unsafe %s %q", errdefs.ErrConfig, s.kind, s.value)
		}
		if err := naming.ValidateHierarchyName(s.kind, s.value); err != nil {
			return v1beta1.CellConfigMetadata{}, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
	}
	return v1beta1.CellConfigMetadata{Realm: req.Realm, Space: req.Space, Stack: req.Stack}, nil
}

// CreateCell creates the first cell of a stored agent Config, stopped, with
// the daemon's generated name. A failure is a StepError.
func CreateCell(ctx context.Context, c kukeonv1.Client, cfg v1beta1.CellConfigMetadata) (v1beta1.CellDoc, error) {
	created, err := c.CreateCellFromConfig(ctx, kukeonv1.CellFromConfigRequest{
		Config: v1beta1.CellConfigMetadata{Name: cfg.Name, Realm: cfg.Realm, Space: cfg.Space, Stack: cfg.Stack},
	})
	if err != nil {
		return v1beta1.CellDoc{}, &StepError{Step: StepCreateCell, Config: cfg, Err: err}
	}
	return created.Cell, nil
}

// StartCell starts an agent's cell. A failure is a StepError.
func StartCell(
	ctx context.Context, c kukeonv1.Client, cfg v1beta1.CellConfigMetadata, cell v1beta1.CellDoc,
) (v1beta1.CellDoc, error) {
	started, err := c.StartCell(ctx, cell)
	if err != nil {
		return v1beta1.CellDoc{}, &StepError{Step: StepStartCell, Config: cfg, Cell: &cell, Err: err}
	}
	if started.Cell.Metadata.Name == "" {
		return cell, nil
	}
	return started.Cell, nil
}

// Result is a New agent's stored Config and its running first cell.
type Result struct {
	Config v1beta1.CellConfigDoc
	Cell   v1beta1.CellDoc
}

// Create is New agent from start to end with one context: store the Config,
// create its first cell and start it.
func Create(ctx context.Context, c kukeonv1.Client, req Request, opts Options) (Result, error) {
	cfg, err := StoreConfig(ctx, c, req, opts)
	if err != nil {
		return Result{}, err
	}
	cell, err := CreateCell(ctx, c, cfg.Metadata)
	if err != nil {
		return Result{Config: cfg}, err
	}
	started, err := StartCell(ctx, c, cfg.Metadata, cell)
	if err != nil {
		return Result{Config: cfg, Cell: cell}, err
	}
	return Result{Config: cfg, Cell: started}, nil
}
