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
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/util/buildinfo"
)

const (
	// KukeonSourceName and KukeonSourceURL identify the built-in source the
	// kuke team reads its majordomo role and shipped harnesses from.
	KukeonSourceName = "kukeon"
	KukeonSourceURL  = "github.com/eminwux/kukeon"
	// KukeonDevBranch is the fallback revision for a build that cannot name the
	// revision it was built from.
	KukeonDevBranch = "main"

	sha1CommitHexLength   = 40
	sha256CommitHexLength = 64
)

// releaseTag matches a version that is exactly a release tag (v0.10.0,
// v0.11.0-rc.1). git describe's post-tag form (v0.10.0-77-gabc) has a second
// hyphen and does not match; a -dirty build is excluded separately.
var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// KukeonSource returns the built-in kukeon source pinned to the revision this
// binary was built from (#1631). kuke validates every fetched document against
// its own schema version with no dual reading, so composing from whatever
// main holds at run time breaks init whenever the binary and main disagree.
func KukeonSource() Source {
	commit, modified := buildinfo.VCS()
	return kukeonSource(buildinfo.Version(), commit, modified)
}

// kukeonSource selects the revision: a release build's tag, else the clean
// commit a development build carries, else main for a build that knows
// neither (a dirty tree, or no VCS stamp and no release version).
func kukeonSource(version, commit string, modified bool) Source {
	source := Source{Name: KukeonSourceName, URL: KukeonSourceURL}
	switch {
	case releaseTag.MatchString(version) && !strings.HasSuffix(version, "-dirty"):
		source.Tag = version
	case !modified && fullCommit(commit):
		source.Commit = commit
	default:
		source.Branch = KukeonDevBranch
	}
	return source
}

func fullCommit(commit string) bool {
	if _, err := hex.DecodeString(commit); err != nil {
		return false
	}
	return len(commit) == sha1CommitHexLength || len(commit) == sha256CommitHexLength
}

// Revision renders a source's revision selector for messages: "tag v0.10.0",
// "commit <sha>", or "branch main".
func (s Source) Revision() string {
	switch {
	case s.Tag != "":
		return "tag " + s.Tag
	case s.Commit != "":
		return "commit " + s.Commit
	case s.Branch != "":
		return "branch " + s.Branch
	}
	return "default branch"
}
