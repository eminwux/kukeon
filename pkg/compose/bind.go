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

package compose

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/config"
)

func bind(
	p *Pair,
	cfg *config.TeamsConfig,
	selection config.Role,
	role *model.Role,
	sources []config.Source,
	team string,
) error {
	cc, bp := p.Config, p.Blueprint
	if err := bindParameters(p, cfg, selection, role); err != nil {
		return err
	}
	cc.Spec.Repos = map[string]api.CellConfigRepoFill{}
	cc.Spec.Secrets = map[string]api.CellConfigSecretFill{}
	required := union(role.Spec.Needs.Secrets, role.Spec.Harnesses[p.Harness].Secrets)
	boundSecrets := map[string]bool{}
	boundRepos := map[string]bool{}
	ids := map[string]bool{}
	for i := range bp.Spec.Cell.Containers {
		container := &bp.Spec.Cell.Containers[i]
		if container.ID == "" || ids[container.ID] || !published(container.Image) {
			return errors.New("template requires unique container IDs and published images")
		}
		ids[container.ID] = true
		// Carry configured identity, and only
		// keep in-cell signing paths when the role explicitly requests that mount.
		container.Git = containerGit(cfg.Spec.Git, slices.Contains(role.Spec.Needs.Mounts, "ssh"))
		for j := range container.Volumes {
			mount := &container.Volumes[j]
			if mount.Source == "" && mount.Kind != api.VolumeKindTmpfs && mount.VolumeRef == nil {
				return errors.New("template produced an unbound mount source")
			}
			if mount.Kind == api.VolumeKindVolume && mount.VolumeRef == nil {
				if mount.Source == role.Metadata.Name+"-"+p.Harness {
					mount.Source = stateVolumeName(team, selection.Name, p.Harness)
				} else {
					mount.Source = stateVolumeName(team, selection.Name, p.Harness, mount.Source)
				}
				mount.Ensure = true
			}
		}
		if err := bindRepos(container, sources, cc, boundRepos); err != nil {
			return err
		}
		if err := bindSecrets(p, cfg, required, container, boundSecrets); err != nil {
			return err
		}
	}
	for _, name := range required {
		if !boundSecrets[name] {
			return fmt.Errorf("template did not declare required secret slot %q", name)
		}
	}
	for _, name := range union(role.Spec.Needs.Repos, []string{selection.Source}) {
		if !boundRepos[name] {
			return fmt.Errorf("template did not declare required repository slot %q", name)
		}
	}

	return nil
}

func secretBinding(cfg *config.TeamsConfig, name string) (config.SecretBinding, bool) {
	for _, binding := range cfg.Spec.Secrets {
		if binding.Name == name {
			return binding, true
		}
	}
	return config.SecretBinding{}, false
}

func containerGit(g *config.Git, ssh bool) *api.ContainerGit {
	if g == nil {
		return nil
	}
	out := &api.ContainerGit{}
	if g.Author != nil {
		out.Author = &api.GitIdentity{Name: g.Author.Name, Email: g.Author.Email}
	}
	if g.Committer != nil {
		out.Committer = &api.GitIdentity{Name: g.Committer.Name, Email: g.Committer.Email}
	}
	if ssh {
		if g.SigningKey != "" {
			out.SigningKey = "/home/user/.ssh/" + path.Base(g.SigningKey)
		}
		out.Sign = slices.Clone(g.Sign)
		if g.AllowedSigners != "" {
			out.AllowedSigners = "/home/user/.ssh/" + path.Base(g.AllowedSigners)
		}
	}
	return out
}

func bindParameters(p *Pair, cfg *config.TeamsConfig, selection config.Role, role *model.Role) error {
	cc, bp := p.Config, p.Blueprint
	cc.Spec.Values = map[string]string{}
	parameters := map[string]bool{}
	for _, param := range bp.Spec.Parameters {
		if parameters[param.Name] {
			return fmt.Errorf("duplicate blueprint parameter %q", param.Name)
		}
		parameters[param.Name] = true
		if value, ok := cfg.Spec.Parameters[param.Name]; ok {
			cc.Spec.Values[param.Name] = value
		} else {
			switch {
			case param.Name == "ROLE":
				cc.Spec.Values[param.Name] = selection.Path
			case param.Name == "AGENT_NAME":
				cc.Spec.Values[param.Name] = cc.Metadata.Name
			case param.Required && param.Default == nil:
				return fmt.Errorf("spec.parameters: missing required parameter %q", param.Name)
			}
		}
	}
	for _, name := range role.Spec.Needs.Params {
		if !parameters[name] {
			return fmt.Errorf("template did not declare required parameter %q", name)
		}
	}

	return nil
}

func bindRepos(
	container *api.BlueprintContainer,
	sources []config.Source,
	cc *api.CellConfigDoc,
	boundRepos map[string]bool,
) error {
	for j := range container.Repos {
		slot := &container.Repos[j]
		r, ok := runtimeSource(sources, slot.Name)
		if !ok {
			return fmt.Errorf("sources: missing clonePath for template repo %q", slot.Name)
		}
		slot.URL = ""
		slot.Target = r.ClonePath
		slot.Branch = ""
		slot.Ref = ""
		ref := r.Tag
		if r.Commit != "" {
			ref = r.Commit
		}
		url := r.URL
		if strings.HasPrefix(url, "github.com/") {
			url = "https://" + url
		}
		cc.Spec.Repos[slot.Name] = api.CellConfigRepoFill{URL: url, Branch: r.Branch, Ref: ref}
		boundRepos[slot.Name] = true
	}

	return nil
}

func bindSecrets(
	p *Pair,
	cfg *config.TeamsConfig,
	required []string,
	container *api.BlueprintContainer,
	boundSecrets map[string]bool,
) error {
	cc := p.Config
	destinations := map[string]bool{}
	for j := range container.Secrets {
		slot := &container.Secrets[j]
		if !slices.Contains(required, slot.Name) {
			return fmt.Errorf("template secret %q is not required by selected role/harness", slot.Name)
		}
		binding, ok := secretBinding(cfg, slot.Name)
		if !ok {
			return fmt.Errorf("spec.secrets: missing structural binding for required secret %q", slot.Name)
		}
		if slot.Mode != "" && slot.Mode != "env" {
			return errors.New("only environment secret destinations are supported")
		}
		slot.Mode = "env"
		slot.EnvName = binding.EnvName
		slot.MountPath = ""
		if destinations[slot.EnvName] {
			return errors.New("conflicting secret environment destinations")
		}
		destinations[slot.EnvName] = true
		for _, env := range container.Env {
			if envName(env) == slot.EnvName {
				return errors.New("secret destination conflicts with template environment")
			}
		}
		ref := binding.SecretRef
		cc.Spec.Secrets[slot.Name] = api.CellConfigSecretFill{
			SecretRef: &api.ContainerSecretRef{
				Name:  ref.Name,
				Realm: ref.Realm,
				Space: ref.Space,
				Stack: ref.Stack,
				Cell:  ref.Cell,
			},
		}
		var source *config.SecretSource
		if binding.Source != nil {
			copySource := *binding.Source
			source = &copySource
		}
		p.Secrets = append(
			p.Secrets,
			SecretRequirement{
				Identifier:  slot.Name,
				Container:   container.ID,
				Destination: *slot,
				Reference:   ref,
				Source:      source,
			},
		)
		boundSecrets[slot.Name] = true
	}
	return nil
}

func envName(value string) string { name, _, _ := strings.Cut(value, "="); return name }
