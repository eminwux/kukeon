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

package config

import (
	"cmp"
	"regexp"
	"strconv"
)

// Channel is the update channel a kuke build belongs to. It is never stored:
// it is read from the build's version string (issue #1638).
type Channel string

const (
	// ChannelNone is a build that belongs to no channel: a dirty or
	// prerelease build, the ldflags default, or anything else that is not
	// a release tag or a `git describe --long` form.
	ChannelNone Channel = ""
	// ChannelStable is a build of exactly a release tag (vX.Y.Z).
	ChannelStable Channel = "stable"
	// ChannelEdge is a build of a main commit (vX.Y.Z-N-g<sha>).
	ChannelEdge Channel = "edge"
)

// EdgeImageTagPrefix prefixes the immutable per-commit kukeond image tag the
// edge workflow publishes (ghcr.io/eminwux/kukeon:sha-<short>).
const EdgeImageTagPrefix = "sha-"

// BuildVersion is a parsed kuke version string.
type BuildVersion struct {
	Channel             Channel
	Major, Minor, Patch int
	// Ahead is the number of commits after the tag; 0 on stable.
	Ahead int
	// Short is the abbreviated commit, edge only.
	Short string
}

//nolint:gochecknoglobals // compiled once; read-only
var (
	stableVersion = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	edgeVersion   = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)-([0-9]+)-g([0-9a-f]{7,40})$`)
)

// ParseVersion parses a kuke version string. Channel is ChannelNone when the
// string is neither a release tag (stable) nor a `git describe --long` form
// (edge); the numeric fields are then zero.
func ParseVersion(v string) BuildVersion {
	if m := stableVersion.FindStringSubmatch(v); m != nil {
		major, minor, patch, ok := parseTriple(m[1], m[2], m[3])
		if !ok {
			return BuildVersion{}
		}
		return BuildVersion{Channel: ChannelStable, Major: major, Minor: minor, Patch: patch}
	}
	if m := edgeVersion.FindStringSubmatch(v); m != nil {
		major, minor, patch, ok := parseTriple(m[1], m[2], m[3])
		if !ok {
			return BuildVersion{}
		}
		ahead, err := strconv.Atoi(m[4])
		if err != nil {
			return BuildVersion{}
		}
		return BuildVersion{
			Channel: ChannelEdge,
			Major:   major, Minor: minor, Patch: patch,
			Ahead: ahead,
			Short: m[5],
		}
	}
	return BuildVersion{}
}

func parseTriple(a, b, c string) (int, int, int, bool) {
	major, errA := strconv.Atoi(a)
	minor, errB := strconv.Atoi(b)
	patch, errC := strconv.Atoi(c)
	if errA != nil || errB != nil || errC != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// Compare orders two versions by Major, Minor, Patch, then Ahead. It returns
// -1, 0 or +1. vX.Y.Z and vX.Y.Z-0-g<sha> compare equal: both are the tagged
// commit.
func (b BuildVersion) Compare(o BuildVersion) int {
	if c := cmp.Compare(b.Major, o.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(b.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(b.Patch, o.Patch); c != 0 {
		return c
	}
	return cmp.Compare(b.Ahead, o.Ahead)
}

// ImageTag returns the immutable kukeond image tag for the build: raw on
// stable (the release tag), sha-<short> on edge, empty for a build with no
// channel. raw is the unparsed version string b was parsed from.
func (b BuildVersion) ImageTag(raw string) string {
	switch b.Channel {
	case ChannelStable:
		return raw
	case ChannelEdge:
		return EdgeImageTagPrefix + b.Short
	case ChannelNone:
		return ""
	default:
		return ""
	}
}
