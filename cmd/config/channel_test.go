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

import "testing"

func TestParseVersionChannelAndImageTag(t *testing.T) {
	cases := []struct {
		in      string
		channel Channel
		tag     string
	}{
		{"v0.10.0", ChannelStable, "v0.10.0"},
		{"v0.10.0-77-gabc1234", ChannelEdge, "sha-abc1234"},
		{"v0.11.0-0-gabc1234", ChannelEdge, "sha-abc1234"},
		{"v0.10.0-77-gabc1234-dirty", ChannelNone, ""},
		{"v0.11.0-rc.1", ChannelNone, ""},
		{"0.1.0", ChannelNone, ""},
		{"v0.0.0-dev", ChannelNone, ""},
		{"", ChannelNone, ""},
		{"v0.10.0-77-gABC1234", ChannelNone, ""},
		{"v0.10.0-77-gabc12", ChannelNone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := ParseVersion(tc.in)
			if got.Channel != tc.channel {
				t.Errorf("ParseVersion(%q).Channel = %q, want %q", tc.in, got.Channel, tc.channel)
			}
			if tag := got.ImageTag(tc.in); tag != tc.tag {
				t.Errorf("ImageTag(%q) = %q, want %q", tc.in, tag, tc.tag)
			}
		})
	}
}

func TestParseVersionFields(t *testing.T) {
	got := ParseVersion("v1.22.3-45-g0123abcd")
	want := BuildVersion{Channel: ChannelEdge, Major: 1, Minor: 22, Patch: 3, Ahead: 45, Short: "0123abcd"}
	if got != want {
		t.Fatalf("ParseVersion = %+v, want %+v", got, want)
	}
	got = ParseVersion("v2.0.1")
	want = BuildVersion{Channel: ChannelStable, Major: 2, Minor: 0, Patch: 1}
	if got != want {
		t.Fatalf("ParseVersion = %+v, want %+v", got, want)
	}
}

func TestBuildVersionCompareOrdering(t *testing.T) {
	// Each entry is strictly greater than the previous one, except where
	// equal is marked.
	ordered := []struct {
		v     string
		equal bool // equal to the previous entry
	}{
		{v: "v0.10.0"},
		{v: "v0.10.0-1-gaaaaaaa"},
		{v: "v0.10.0-77-gbbbbbbb"},
		{v: "v0.11.0"},
		{v: "v0.11.0-0-gccccccc", equal: true},
		{v: "v0.11.0-3-gddddddd"},
		{v: "v1.0.0"},
	}
	for i := 1; i < len(ordered); i++ {
		prev := ParseVersion(ordered[i-1].v)
		cur := ParseVersion(ordered[i].v)
		want := 1
		if ordered[i].equal {
			want = 0
		}
		if got := cur.Compare(prev); got != want {
			t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i].v, ordered[i-1].v, got, want)
		}
		if got := prev.Compare(cur); got != -want {
			t.Errorf("Compare(%s, %s) = %d, want %d", ordered[i-1].v, ordered[i].v, got, -want)
		}
	}
}
