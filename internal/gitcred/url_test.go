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

package gitcred_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/gitcred"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		raw  string
		want gitcred.Remote
	}{
		{"git@github.com:eminwux/kukeon.git", gitcred.Remote{
			Transport: gitcred.TransportSSH, User: "git", Host: "github.com", Path: "eminwux/kukeon.git",
		}},
		{"github.com:eminwux/kukeon", gitcred.Remote{
			Transport: gitcred.TransportSSH, Host: "github.com", Path: "eminwux/kukeon",
		}},
		{"ssh://gitea@Git.Internal:2222/ops/infra.git", gitcred.Remote{
			Transport: gitcred.TransportSSH, User: "gitea", Host: "git.internal", Port: 2222, Path: "ops/infra.git",
		}},
		{"https://user:pw@github.com/eminwux/kukeon", gitcred.Remote{
			Transport: gitcred.TransportHTTPS, User: "user", Host: "github.com", Path: "eminwux/kukeon",
		}},
		{"http://git.local:8080/r.git", gitcred.Remote{
			Transport: gitcred.TransportHTTP, Host: "git.local", Port: 8080, Path: "r.git",
		}},
		{"/srv/git/repo.git", gitcred.Remote{Transport: gitcred.TransportOther, Path: "/srv/git/repo.git"}},
		{"./relative:with:colon", gitcred.Remote{Transport: gitcred.TransportOther, Path: "./relative:with:colon"}},
		// Whitespace or a control character in the user or host is not an
		// scp-like remote: it must not reach the generated ssh_config.
		{"git@github.com\n  ProxyCommand evil:o/r.git", gitcred.Remote{
			Transport: gitcred.TransportOther, Path: "git@github.com\n  ProxyCommand evil:o/r.git",
		}},
		{"evil user@github.com:o/r.git", gitcred.Remote{
			Transport: gitcred.TransportOther, Path: "evil user@github.com:o/r.git",
		}},
		{"file:///srv/git/repo.git", gitcred.Remote{
			Transport: gitcred.TransportOther, Path: "file:///srv/git/repo.git",
		}},
	}
	for _, tt := range tests {
		if got := gitcred.ParseRemote(tt.raw); got != tt.want {
			t.Errorf("ParseRemote(%q) = %+v, want %+v", tt.raw, got, tt.want)
		}
	}
}

func TestRemoteURLs(t *testing.T) {
	r := gitcred.ParseRemote("https://token@github.com/eminwux/kukeon.git")
	if got := r.SSHURL("", 0); got != "git@github.com:eminwux/kukeon.git" {
		t.Errorf("SSHURL = %q", got)
	}
	if got := r.SSHURL("deploy", 2222); got != "ssh://deploy@github.com:2222/eminwux/kukeon.git" {
		t.Errorf("SSHURL with port = %q", got)
	}
	// Userinfo never survives onto an HTTP URL: the helper supplies it.
	if got := r.HTTPURL(gitcred.TransportHTTPS, 0); got != "https://github.com/eminwux/kukeon.git" {
		t.Errorf("HTTPURL = %q", got)
	}
}

func TestCloneURL(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"github.com/acme/app", "https://github.com/acme/app"},
		{"git.example.org/team/app", "https://git.example.org/team/app"},
		{"https://GitHub.com/acme/app/", "https://GitHub.com/acme/app/"},
		{"ssh://git@host:2222/acme/app.git", "ssh://git@host:2222/acme/app.git"},
		{"git@github.com:acme/app.git", "git@github.com:acme/app.git"},
		{"acme/app", "acme/app"},
		{"./local.dir/app", "./local.dir/app"},
		{"github.com/", "github.com/"},
		{"/srv/git/app.git", "/srv/git/app.git"},
	}
	for _, tt := range tests {
		if got := gitcred.CloneURL(tt.raw); got != tt.want {
			t.Errorf("CloneURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestSameRepository(t *testing.T) {
	const scp = "git@github.com:acme/app.git"
	tests := []struct {
		raw  string
		want bool
	}{
		{"github.com/acme/app", true},
		{"git.example.org/team/app", false},
		{"https://GitHub.com/acme/app/", true},
		{"ssh://git@host:2222/acme/app.git", false},
		{"acme/app", false},
		{"ssh://deploy@github.com:22/acme/app", true},
		{"https://github.com/acme/other", false},
	}
	for _, tt := range tests {
		if got := gitcred.SameRepository(tt.raw, scp); got != tt.want {
			t.Errorf("SameRepository(%q, %q) = %v, want %v", tt.raw, scp, got, tt.want)
		}
	}
	if !gitcred.SameRepository("/srv/git/app", "/srv/git/app") || gitcred.SameRepository("/srv/a", "/srv/b") {
		t.Error("local paths compare as written")
	}
}

func TestShortForm(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"git@github.com:eminwux/agents", "github.com/eminwux/agents"},
		{"https://github.com/eminwux/kukeon", "github.com/eminwux/kukeon"},
		{"github.com/eminwux/kukeon", "github.com/eminwux/kukeon"},
		{"https://git.example.org/team/app.git/", "git.example.org/team/app.git"},
		{"ssh://git@host.example:2222/acme/app.git", "ssh://git@host.example:2222/acme/app.git"},
		{"deploy@github.com:acme/app", "deploy@github.com:acme/app"},
		{"http://git.local/r.git", "http://git.local/r.git"},
		{"/srv/git/app", "/srv/git/app"},
	}
	for _, tt := range tests {
		if got := gitcred.ShortForm(tt.raw); got != tt.want {
			t.Errorf("ShortForm(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
