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
	"slices"
	"strings"

	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// secretBindings resolves requirement bindings by precedence: recorded TeamEntry
// binding, then global spec.secrets, then a generated default in the team realm.
type secretBindings struct {
	recorded, global []config.SecretBinding
	realm            string
	used             map[string]config.SecretBinding
	added            []string
}

func newSecretBindings(
	recorded []config.SecretBinding,
	cfg *config.TeamsConfig,
	realm string,
) (*secretBindings, error) {
	if err := config.ValidateSecrets("secrets", recorded); err != nil {
		return nil, err
	}
	return &secretBindings{
		recorded: recorded,
		global:   cfg.Spec.Secrets,
		realm:    realm,
		used:     map[string]config.SecretBinding{},
	}, nil
}

func (s *secretBindings) lookup(name string) (config.SecretBinding, error) {
	if binding, ok := s.used[name]; ok {
		return binding, nil
	}
	binding, ok := findBinding(s.recorded, name)
	if !ok {
		binding, ok = findBinding(s.global, name)
	}
	if !ok {
		var err error
		if binding, err = config.DefaultSecretBinding(name, s.realm); err != nil {
			return config.SecretBinding{}, err
		}
	}
	if binding.Source != nil {
		source := *binding.Source
		binding.Source = &source
	}
	s.used[name] = binding
	if !slices.ContainsFunc(s.recorded, func(b config.SecretBinding) bool { return b.Name == name }) {
		s.added = append(s.added, name)
	}
	return binding, nil
}

// component binds a component secret slot. The destination is the slot's, not
// the team's: a shared blueprint fixes it. The runtime secret comes from the
// team's binding for the slot, then from one for an alias, then from the
// component's default secret in the team realm.
func (s *secretBindings) component(slot component.Secret) (config.SecretBinding, error) {
	for _, name := range append([]string{slot.Name}, slot.Aliases...) {
		if _, ok := findBinding(s.recorded, name); ok {
			return s.lookup(name)
		}
		if _, ok := findBinding(s.global, name); ok {
			return s.lookup(name)
		}
	}
	if binding, ok := s.used[slot.Name]; ok {
		return binding, nil
	}
	name := slot.SecretName
	if name == "" {
		name = slot.Name
	}
	binding := config.SecretBinding{
		Name: slot.Name, SecretRef: config.SecretRef{Name: name, Realm: s.realm}, EnvName: slot.EnvName,
	}
	if err := config.ValidateSecrets("secret "+slot.Name, []config.SecretBinding{binding}); err != nil {
		return config.SecretBinding{}, err
	}
	s.used[slot.Name] = binding
	s.added = append(s.added, slot.Name)
	return binding, nil
}

// entry keeps recorded bindings in their order, appends newly used ones, and
// drops bindings for requirements no selected pair uses.
func (s *secretBindings) entry() []config.SecretBinding {
	var out []config.SecretBinding
	for _, binding := range s.recorded {
		if _, ok := s.used[binding.Name]; ok {
			out = append(out, s.used[binding.Name])
		}
	}
	for _, name := range s.added {
		out = append(out, s.used[name])
	}
	return out
}

// checkParameters keeps the global rule that parameters cannot target a secret
// destination, now applied to every binding the team actually uses.
func (s *secretBindings) checkParameters(parameters map[string]string) error {
	for name := range parameters {
		for _, binding := range s.used {
			if strings.EqualFold(name, binding.EnvName) {
				return errors.New("spec.parameters: secret destinations must use structural secret bindings")
			}
		}
	}
	return nil
}

func findBinding(bindings []config.SecretBinding, name string) (config.SecretBinding, bool) {
	for _, binding := range bindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return config.SecretBinding{}, false
}
