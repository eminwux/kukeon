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

package kuketeams

import "strings"

// DefaultSourceHost is the git host assumed when TeamSource.Repo is a bare
// `<owner>/<repo>` with no host segment.
const DefaultSourceHost = "github.com"

// TeamSource is the structured agents-source reference shared by ProjectTeam
// and TeamEntry. It replaces the former `<owner>/<repo>@vX.Y.Z` string form:
// `repo` is host-explicit and exactly one of `tag`/`branch`/`commit` carries
// the ref intent. The key name IS the intent — `tag`/`commit` pin to a
// reproducible ref, `branch` floats (refetched on every init) — so
// pinned-vs-floating is unambiguous without interrogating git (a string `@ref`
// cannot distinguish a branch from a same-named tag).
type TeamSource struct {
	// Repo is the host-qualified repository, `<host>/<owner>/<repo>` (e.g.
	// github.com/eminwux/agents). A bare `<owner>/<repo>` defaults its host to
	// DefaultSourceHost, but any host may be expressed explicitly.
	Repo string `json:"repo"             yaml:"repo"`
	// Tag pins to an exact tag (reproducible). Mutually exclusive with Branch
	// and Commit.
	Tag string `json:"tag,omitempty"    yaml:"tag,omitempty"`
	// Branch floats: the branch tip is refetched and reset on every init.
	// Mutually exclusive with Tag and Commit.
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
	// Commit pins to an exact commit SHA (reproducible). Mutually exclusive
	// with Tag and Branch.
	Commit string `json:"commit,omitempty" yaml:"commit,omitempty"`
}

// Ref returns the single set ref value and its kind ("tag"/"branch"/"commit").
// When zero or more than one of Tag/Branch/Commit is set it returns ("", "")
// — callers treat that as the exactly-one-ref violation the parser rejects.
func (s TeamSource) Ref() (string, string) {
	var value, kind string
	n := 0
	if v := strings.TrimSpace(s.Tag); v != "" {
		n++
		value, kind = v, "tag"
	}
	if v := strings.TrimSpace(s.Branch); v != "" {
		n++
		value, kind = v, "branch"
	}
	if v := strings.TrimSpace(s.Commit); v != "" {
		n++
		value, kind = v, "commit"
	}
	if n != 1 {
		return "", ""
	}
	return value, kind
}

// Floating reports whether the source is a floating branch (refetched every
// init). Tag/commit refs are pinned and return false.
func (s TeamSource) Floating() bool {
	_, kind := s.Ref()
	return kind == "branch"
}

// KindLabel returns "floating" for a branch and "pinned" for a tag/commit —
// the operator-facing pinned-vs-floating signal `kuke team init` prints.
func (s TeamSource) KindLabel() string {
	if s.Floating() {
		return "floating"
	}
	return "pinned"
}

// Normalized splits Repo into its host and `<owner>/<repo>` halves, applying
// the DefaultSourceHost default for a bare `<owner>/<repo>`. A segment is
// treated as a host when it carries a "." or ":" (port) — so `github.com/...`
// is host-qualified while `eminwux/agents` is bare. The owner/repo half is
// everything after the host. The first return is the host, the second the
// owner/repo path.
func (s TeamSource) Normalized() (string, string) {
	repo := strings.TrimSpace(s.Repo)
	first, rest, found := strings.Cut(repo, "/")
	if !found {
		// Malformed (no "/"); return as-is so validation can reject it.
		return DefaultSourceHost, repo
	}
	if first != "." && first != ".." && strings.ContainsAny(first, ".:") {
		return first, rest
	}
	return DefaultSourceHost, repo
}
