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
