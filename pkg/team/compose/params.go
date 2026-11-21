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
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
)

// roleSourceRepoFact names the composed fact holding the role source's
// `<owner>/<repo>`, for a role's needs.params defaults to reference.
const roleSourceRepoFact = "ROLE_SOURCE_REPO"

var repoSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// sourceRepo reduces a clone URL to `<owner>/<repo>`: scp-like
// (`git@host:owner/repo`), ssh:// and http(s):// URLs, and the
// `github.com/owner/repo` shorthand, each with or without a trailing `.git`.
// A URL of any other shape (a local path, a nested group path) reduces to "".
func sourceRepo(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "github.com/") {
		raw = "https://" + raw
	}
	remote := gitcred.ParseRemote(raw)
	if remote.Transport == gitcred.TransportOther || remote.Host == "" {
		return ""
	}
	repoPath := strings.TrimSuffix(strings.TrimSuffix(remote.Path, "/"), ".git")
	owner, repo, ok := strings.Cut(repoPath, "/")
	if !ok {
		return ""
	}
	for _, segment := range []string{owner, repo} {
		if !repoSegment.MatchString(segment) || segment == "." || segment == ".." {
			return ""
		}
	}
	return owner + "/" + repo
}

// roleDefaults resolves the pair's role-declared parameter defaults against
// the composed facts, and notes each one that resolved empty although it
// names a value. A default the operator overrides is checked but not noted.
func roleDefaults(plan *pairPlan, facts map[string]string, overrides map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(plan.paramDefaults)) {
		raw := plan.paramDefaults[name]
		var unknown []string
		value := inputToken.ReplaceAllStringFunc(raw, func(token string) string {
			fact := token[2 : len(token)-1]
			v, ok := facts[fact]
			if !ok {
				unknown = append(unknown, fact)
			}
			return v
		})
		if len(unknown) > 0 {
			return nil, fmt.Errorf("role needs.params %s: default references unknown fact %q", name, unknown[0])
		}
		out[name] = value
		if _, set := overrides[name]; set || value != "" || raw == "" {
			continue
		}
		note := fmt.Sprintf("role %s: needs.params %s default %q resolved empty", plan.selection.Name, name, raw)
		if strings.Contains(raw, "${"+roleSourceRepoFact+"}") && facts[roleSourceRepoFact] == "" {
			note += fmt.Sprintf(
				"; role source URL %q does not reduce to <owner>/<repo>", plan.roleSource.URL,
			)
		}
		plan.notes = append(plan.notes, note+"; set spec.parameters."+name+" to supply it")
	}
	return out, nil
}
