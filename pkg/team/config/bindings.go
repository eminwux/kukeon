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

package config

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// ValidName reports whether a name can safely identify a resource and appear in
// copied launch templates. Shell metacharacters and path traversal are excluded.
func ValidName(value string) bool {
	return regexp.MustCompile(`^[a-z0-9]+([a-z0-9.-]*[a-z0-9])?$`).MatchString(value)
}

// SafePath permits absolute Unix paths suitable for unquoted template paths.
func SafePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value &&
		regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(value)
}

func validateBindings(s *Spec) error {
	secrets := map[string]bool{}
	for i, binding := range s.Secrets {
		field := fmt.Sprintf("spec.secrets[%d]", i)
		if err := unique(field+".name", binding.Name, secrets); err != nil {
			return err
		}
		if err := validateSecret(field, binding); err != nil {
			return err
		}
	}
	return validateParameters(s)
}

func validateSecret(field string, binding SecretBinding) error {
	ref := binding.SecretRef
	if !ValidName(binding.Name) || !ValidName(ref.Name) || !ValidName(ref.Realm) ||
		(ref.Space != "" && !ValidName(ref.Space)) || (ref.Stack != "" && !ValidName(ref.Stack)) ||
		(ref.Cell != "" && !ValidName(ref.Cell)) {
		return fmt.Errorf("%s: require valid requirement, secret name, and scope", field)
	}
	if (ref.Stack != "" && ref.Space == "") || (ref.Cell != "" && ref.Stack == "") {
		return fmt.Errorf("%s.secretRef: deeper scope requires all parent coordinates", field)
	}
	if !validEnv(binding.EnvName) {
		return fmt.Errorf("%s.envName: require an environment variable identifier", field)
	}
	if src := binding.Source; src != nil {
		if (src.Env == "") == (src.File == "") {
			return fmt.Errorf("%s.source: require exactly one env or file declaration", field)
		}
		if src.Env != "" && !validEnv(src.Env) {
			return fmt.Errorf("%s.source.env: require an environment variable identifier", field)
		}
		if src.File != "" && !path.IsAbs(src.File) {
			return fmt.Errorf("%s.source.file: require an absolute path", field)
		}
	}
	return nil
}

func validateParameters(s *Spec) error {
	for name := range s.Parameters {
		if !validEnv(name) {
			return errors.New("spec.parameters: require environment variable identifiers")
		}
		for _, secret := range s.Secrets {
			if strings.EqualFold(name, secret.EnvName) {
				return errors.New("spec.parameters: secret destinations must use structural secret bindings")
			}
		}
	}
	return nil
}

func validEnv(value string) bool {
	return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(value)
}
