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

package apischeme_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func gitRef(name string) *ext.ContainerSecretRef {
	return &ext.ContainerSecretRef{Name: name, Realm: "r"}
}

// storedGit is a ContainerGit using every #1472 field: stored signing key,
// allowed signers and one credential per transport.
func storedGit() *ext.ContainerGit {
	return &ext.ContainerGit{
		Author:    &ext.GitIdentity{Name: "Dev", Email: "dev@example.com"},
		Committer: &ext.GitIdentity{Name: "Bot", Email: "bot@example.com"},
		Sign:      []string{ext.GitSignCommits, ext.GitSignTags},
		Signing: &ext.GitSigning{
			SecretRef:         gitRef("signing-key"),
			AllowedSignersRef: &ext.ContainerSecretRef{Name: "signers", Realm: "r", Space: "s"},
		},
		Credentials: []ext.GitCredential{
			{
				Host: "github.com", SecretRef: gitRef("gh-key"),
				User: "git", Port: 2222, HostKeyPolicy: ext.GitHostKeyPolicyAcceptNew,
			},
			{Host: "https://gitlab.example.com/team", SecretRef: gitRef("gl-token")},
		},
	}
}

func storedRepoGit() *ext.RepoGit {
	return &ext.RepoGit{
		Author:    &ext.GitIdentity{Name: "Repo Dev", Email: "repo@example.com"},
		Committer: &ext.GitIdentity{Name: "Repo Bot", Email: "repobot@example.com"},
		Credential: &ext.GitCredential{
			SecretRef:     gitRef("repo-key"),
			User:          "git",
			HostKeyPolicy: ext.GitHostKeyPolicyStrict,
		},
		Signing: &ext.GitSigning{SecretRef: gitRef("repo-signing")},
		Sign:    []string{ext.GitSignCommits},
	}
}

func TestGitRoundTrip(t *testing.T) {
	in := storedGit()
	back := apischeme.GitToExternal(apischeme.GitToInternal(in))
	if !reflect.DeepEqual(back, in) {
		t.Errorf("ContainerGit round trip lost fields:\n got %+v\nwant %+v", back, in)
	}
	if apischeme.GitToInternal(nil) != nil || apischeme.GitToExternal(nil) != nil {
		t.Errorf("nil git must convert to nil")
	}
}

func TestRepoGitRoundTrip(t *testing.T) {
	in := storedRepoGit()
	back := apischeme.RepoGitToExternal(apischeme.RepoGitToInternal(in))
	if !reflect.DeepEqual(back, in) {
		t.Errorf("RepoGit round trip lost fields:\n got %+v\nwant %+v", back, in)
	}
	if apischeme.RepoGitToInternal(nil) != nil || apischeme.RepoGitToExternal(nil) != nil {
		t.Errorf("nil repo git must convert to nil")
	}
}

// TestGitToInternal_DeepCopiesAndTrims asserts the converted block shares no
// pointer or slice with its source, and that the credential's free-text fields
// are trimmed on the way in.
func TestGitToInternal_DeepCopiesAndTrims(t *testing.T) {
	src := storedGit()
	src.Credentials[0].Host = "  github.com "
	src.Credentials[0].User = " git "
	src.Credentials[0].HostKeyPolicy = " " + ext.GitHostKeyPolicyAcceptNew + " "
	internal := apischeme.GitToInternal(src)

	cred := internal.Credentials[0]
	if cred.Host != "github.com" || cred.User != "git" || cred.HostKeyPolicy != ext.GitHostKeyPolicyAcceptNew {
		t.Errorf("credential not trimmed: %+v", cred)
	}

	src.Credentials[0].SecretRef.Name = "mutated"
	src.Signing.SecretRef.Name = "mutated"
	src.Signing.AllowedSignersRef.Name = "mutated"
	src.Sign[0] = "mutated"
	if cred.SecretRef.Name != "gh-key" {
		t.Errorf("credential secretRef shared with source: %q", cred.SecretRef.Name)
	}
	if internal.Signing.SecretRef.Name != "signing-key" || internal.Signing.AllowedSignersRef.Name != "signers" {
		t.Errorf("signing refs shared with source: %+v", internal.Signing)
	}
	if internal.Sign[0] != ext.GitSignCommits {
		t.Errorf("sign slice shared with source: %v", internal.Sign)
	}
}

// TestContainerDoc_GitRoundTrip covers the full container path: the default
// git block and a repo's own git both survive external → internal → external.
func TestContainerDoc_GitRoundTrip(t *testing.T) {
	doc := gitContainerDoc(storedGit())
	doc.Spec.Repos = []ext.ContainerRepo{{
		Name: "src", Target: "/workspace/src", URL: "git@github.com:o/r.git", Git: storedRepoGit(),
	}}
	internal, version, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	out, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(out.Spec.Git, doc.Spec.Git) {
		t.Errorf("container git round trip:\n got %+v\nwant %+v", out.Spec.Git, doc.Spec.Git)
	}
	if len(out.Spec.Repos) != 1 || !reflect.DeepEqual(out.Spec.Repos[0].Git, doc.Spec.Repos[0].Git) {
		t.Errorf("repo git round trip:\n got %+v\nwant %+v", out.Spec.Repos, doc.Spec.Repos)
	}
}

// TestScopeDefaults_GitRoundTrip covers defaults.git and a repo fill's git on
// the ScopeDefaults converters every scope kind shares.
func TestScopeDefaults_GitRoundTrip(t *testing.T) {
	in := &ext.ScopeDefaults{
		Git: storedGit(),
		Repos: map[string]ext.CellConfigRepoFill{
			"src": {URL: "git@github.com:o/r.git", Branch: "main", Git: storedRepoGit()},
		},
	}
	back := apischeme.BuildScopeDefaultsExternalFromInternal(apischeme.ConvertScopeDefaultsToInternal(in))
	if !reflect.DeepEqual(back, in) {
		t.Errorf("ScopeDefaults round trip:\n got %+v\nwant %+v", back, in)
	}
}

func TestValidateGit(t *testing.T) {
	cred := func(mut func(*ext.GitCredential)) []ext.GitCredential {
		c := ext.GitCredential{Host: "github.com", SecretRef: gitRef("k")}
		mut(&c)
		return []ext.GitCredential{c}
	}
	tests := []struct {
		name    string
		git     *ext.ContainerGit
		wantErr string
	}{
		{name: "nil", git: nil},
		{name: "stored block", git: storedGit()},
		{
			name: "signing without sign is valid",
			git:  &ext.ContainerGit{Signing: &ext.GitSigning{SecretRef: gitRef("k")}},
		},
		{
			name:    "sign without any key",
			git:     &ext.ContainerGit{Sign: []string{ext.GitSignTags}},
			wantErr: "git.sign requires git.signingKey or git.signing",
		},
		{
			name: "signing combined with signingKey",
			git: &ext.ContainerGit{
				SigningKey: "/k.pub", Signing: &ext.GitSigning{SecretRef: gitRef("k")},
			},
			wantErr: "git.signing cannot be combined",
		},
		{
			name: "signing combined with allowedSigners path",
			git: &ext.ContainerGit{
				AllowedSigners: "/allowed", Signing: &ext.GitSigning{SecretRef: gitRef("k")},
			},
			wantErr: "git.signing cannot be combined",
		},
		{
			name:    "signing without secretRef",
			git:     &ext.ContainerGit{Signing: &ext.GitSigning{}},
			wantErr: "git.signing.secretRef requires name and realm",
		},
		{
			name: "signing allowedSignersRef without realm",
			git: &ext.ContainerGit{Signing: &ext.GitSigning{
				SecretRef: gitRef("k"), AllowedSignersRef: &ext.ContainerSecretRef{Name: "s"},
			}},
			wantErr: "git.signing.allowedSignersRef requires name and realm",
		},
		{
			name:    "multi-line identity",
			git:     &ext.ContainerGit{Author: &ext.GitIdentity{Name: "a\n[core]", Email: "a@example.com"}},
			wantErr: "single-line",
		},
		{
			name:    "credential missing host",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.Host = " " })},
			wantErr: "git.credentials[0].host is required",
		},
		{
			name:    "credential host with space",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.Host = "git hub.com" })},
			wantErr: "without spaces or quotes",
		},
		{
			name:    "credential host with quote",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.Host = `github.com"` })},
			wantErr: "without spaces or quotes",
		},
		{
			name: "duplicate credential host",
			git: &ext.ContainerGit{Credentials: []ext.GitCredential{
				{Host: "github.com", SecretRef: gitRef("a")},
				{Host: " github.com ", SecretRef: gitRef("b")},
			}},
			wantErr: "git.credentials[1].host \"github.com\": duplicate credential host",
		},
		{
			name:    "credential missing secretRef",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.SecretRef = nil })},
			wantErr: "git.credentials[0].secretRef requires name and realm",
		},
		{
			name: "credential secretRef without realm",
			git: &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) {
				c.SecretRef = &ext.ContainerSecretRef{Name: "k"}
			})},
			wantErr: "requires name and realm",
		},
		{
			name: "credential secretRef incomplete scope",
			git: &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) {
				c.SecretRef = &ext.ContainerSecretRef{Name: "k", Realm: "r", Stack: "st"}
			})},
			wantErr: "scope is incomplete",
		},
		{
			name: "credential secretRef path traversal",
			git: &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) {
				c.SecretRef = &ext.ContainerSecretRef{Name: "../etc", Realm: "r"}
			})},
			wantErr: "must not contain path separators",
		},
		{
			name:    "credential bad hostKeyPolicy",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.HostKeyPolicy = "off" })},
			wantErr: "git.credentials[0].hostKeyPolicy \"off\"",
		},
		{
			name:    "credential port out of range",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.Port = 70000 })},
			wantErr: "git.credentials[0].port 70000",
		},
		{
			name:    "credential negative port",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.Port = -1 })},
			wantErr: "git.credentials[0].port -1",
		},
		{
			name: "credential user breaks out of ssh config",
			git: &ext.ContainerGit{
				Credentials: cred(func(c *ext.GitCredential) { c.User = "git -oProxyCommand=x" }),
			},
			wantErr: "must be a login name",
		},
		{
			name:    "credential user leading dash",
			git:     &ext.ContainerGit{Credentials: cred(func(c *ext.GitCredential) { c.User = "-oProxyCommand" })},
			wantErr: "must be a login name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGitErr(t, apischeme.ValidateGit(tt.git), tt.wantErr)
		})
	}
}

func TestValidateRepoGit(t *testing.T) {
	tests := []struct {
		name    string
		git     *ext.RepoGit
		wantErr string
	}{
		{name: "nil", git: nil},
		{name: "full block", git: storedRepoGit()},
		{
			// A repo may sign with the cell's default key, so sign needs no
			// signing of its own.
			name: "sign without own signing",
			git:  &ext.RepoGit{Sign: []string{ext.GitSignCommits}},
		},
		{
			name:    "author missing email",
			git:     &ext.RepoGit{Author: &ext.GitIdentity{Name: "Dev"}},
			wantErr: "git.author requires both name and email",
		},
		{
			name:    "committer missing name",
			git:     &ext.RepoGit{Committer: &ext.GitIdentity{Email: "d@example.com"}},
			wantErr: "git.committer requires both name and email",
		},
		{
			name:    "unknown sign target",
			git:     &ext.RepoGit{Sign: []string{"branches"}},
			wantErr: "must be one of",
		},
		{
			name:    "signing without secretRef",
			git:     &ext.RepoGit{Signing: &ext.GitSigning{}},
			wantErr: "git.signing.secretRef requires name and realm",
		},
		{
			name:    "credential names a host",
			git:     &ext.RepoGit{Credential: &ext.GitCredential{Host: "github.com", SecretRef: gitRef("k")}},
			wantErr: "git.credential.host must be empty",
		},
		{
			name:    "credential missing secretRef",
			git:     &ext.RepoGit{Credential: &ext.GitCredential{}},
			wantErr: "git.credential.secretRef requires name and realm",
		},
		{
			name:    "credential bad hostKeyPolicy",
			git:     &ext.RepoGit{Credential: &ext.GitCredential{SecretRef: gitRef("k"), HostKeyPolicy: "no"}},
			wantErr: "git.credential.hostKeyPolicy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGitErr(t, apischeme.ValidateRepoGit(tt.git), tt.wantErr)
		})
	}
}

func assertGitErr(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want substring %q", err.Error(), want)
	}
}

// TestContainerDoc_RejectsInvalidRepoGit asserts the container converter runs
// the per-repo check and names the offending repo.
func TestContainerDoc_RejectsInvalidRepoGit(t *testing.T) {
	doc := gitContainerDoc(nil)
	doc.Spec.Repos = []ext.ContainerRepo{{
		Name: "src", Target: "/workspace/src", URL: "git@github.com:o/r.git",
		Git: &ext.RepoGit{Credential: &ext.GitCredential{Host: "github.com", SecretRef: gitRef("k")}},
	}}
	_, err := apischeme.ConvertContainerDocToInternal(doc)
	if err == nil || !strings.Contains(err.Error(), `"src"`) ||
		!strings.Contains(err.Error(), "git.credential.host must be empty") {
		t.Fatalf("err = %v, want the repo-scoped credential.host rejection naming src", err)
	}
}

// TestScopeDocs_ValidateDefaultsGit asserts Realm, Space and Stack documents
// reject an invalid defaults.git and an invalid repo-fill git at conversion.
func TestScopeDocs_ValidateDefaultsGit(t *testing.T) {
	badGit := &ext.ScopeDefaults{Git: &ext.ContainerGit{Sign: []string{ext.GitSignCommits}}}
	badRepo := &ext.ScopeDefaults{Repos: map[string]ext.CellConfigRepoFill{
		"src": {URL: "u", Git: &ext.RepoGit{Signing: &ext.GitSigning{}}},
	}}
	convert := map[string]func(*ext.ScopeDefaults) error{
		"realm": func(d *ext.ScopeDefaults) error {
			_, err := apischeme.ConvertRealmDocToInternal(ext.RealmDoc{
				Metadata: ext.RealmMetadata{Name: "r"},
				Spec:     ext.RealmSpec{Namespace: "r.kukeon.io", Defaults: d},
			})
			return err
		},
		"space": func(d *ext.ScopeDefaults) error {
			_, err := apischeme.ConvertSpaceDocToInternal(ext.SpaceDoc{
				Metadata: ext.SpaceMetadata{Name: "s"},
				Spec: ext.SpaceSpec{RealmID: "r", Defaults: &ext.SpaceDefaults{
					Git: d.Git, Repos: d.Repos,
				}},
			})
			return err
		},
		"stack": func(d *ext.ScopeDefaults) error {
			_, err := apischeme.ConvertStackDocToInternal(ext.StackDoc{
				Metadata: ext.StackMetadata{Name: "st"},
				Spec:     ext.StackSpec{RealmID: "r", SpaceID: "s", Defaults: d},
			})
			return err
		},
	}
	for kind, fn := range convert {
		t.Run(kind, func(t *testing.T) {
			err := fn(badGit)
			if err == nil || !strings.Contains(err.Error(), kind+" spec.defaults.git.sign requires") {
				t.Errorf("defaults.git: err = %v, want %s spec.defaults.git.sign rejection", err, kind)
			}
			err = fn(badRepo)
			if err == nil || !strings.Contains(err.Error(), kind+` spec.defaults.repos["src"]`) {
				t.Errorf("defaults.repos git: err = %v, want %s spec.defaults.repos[\"src\"] rejection", err, kind)
			}
			if err = fn(&ext.ScopeDefaults{Git: storedGit()}); err != nil {
				t.Errorf("valid defaults.git rejected: %v", err)
			}
		})
	}
}

// TestSecretConversion covers the typed Secret spec: material reaches the
// internal model intact, and the external view the get/list verbs return
// reports the type only — never any material field.
func TestSecretConversion(t *testing.T) {
	doc := ext.SecretDoc{
		Metadata: ext.SecretMetadata{Name: "key", Realm: "r", Space: "s"},
		Spec: ext.SecretSpec{
			Type: " " + ext.SecretTypeSSHKey + " ",
			SSHKey: &ext.SecretSSHKey{
				PrivateKey: "PRIV", Passphrase: "PASS", PublicKey: "PUB", Certificate: "CERT", KnownHosts: "KH",
			},
		},
	}
	internal, err := apischeme.ConvertSecretDocToInternal(doc)
	if err != nil {
		t.Fatalf("ConvertSecretDocToInternal: %v", err)
	}
	if internal.Spec.Type != ext.SecretTypeSSHKey {
		t.Errorf("internal type = %q, want trimmed %q", internal.Spec.Type, ext.SecretTypeSSHKey)
	}
	key := internal.Spec.SSHKey
	if key == nil || key.PrivateKey != "PRIV" || key.Passphrase != "PASS" || key.PublicKey != "PUB" ||
		key.Certificate != "CERT" || key.KnownHosts != "KH" {
		t.Errorf("internal sshKey = %+v, want every field carried", key)
	}

	basic, err := apischeme.ConvertSecretDocToInternal(ext.SecretDoc{
		Metadata: ext.SecretMetadata{Name: "tok", Realm: "r"},
		Spec: ext.SecretSpec{
			Type:      ext.SecretTypeBasicAuth,
			BasicAuth: &ext.SecretBasicAuth{Username: "u", Password: "p", Token: "t"},
		},
	})
	if err != nil {
		t.Fatalf("ConvertSecretDocToInternal: %v", err)
	}
	if ba := basic.Spec.BasicAuth; ba == nil || ba.Username != "u" || ba.Password != "p" || ba.Token != "t" {
		t.Errorf("internal basicAuth = %+v, want every field carried", ba)
	}

	for _, in := range []struct {
		name string
		spec ext.SecretSpec
	}{
		{"ssh-key", apischeme.ConvertSecretToExternal(internal).Spec},
		{"basic-auth", apischeme.ConvertSecretToExternal(basic).Spec},
		{"list", apischeme.ConvertSecretListToExternal([]intmodel.Secret{internal})[0].Spec},
	} {
		if in.spec.Data != "" || in.spec.BasicAuth != nil || in.spec.SSHKey != nil {
			t.Errorf("%s: external view carries secret material: %+v", in.name, in.spec)
		}
		if in.spec.Type == "" {
			t.Errorf("%s: external view dropped the type", in.name)
		}
	}
}
