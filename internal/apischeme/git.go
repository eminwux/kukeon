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

package apischeme

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// gitSSHUserRE bounds an SSH login user to what an ssh config `User` line and a
// URL userinfo can carry unquoted.
var gitSSHUserRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// GitToInternal copies the external git block into the internal model,
// deep-copying every pointer and slice. Issue #618, #1472.
func GitToInternal(in *ext.ContainerGit) *intmodel.ContainerGit {
	if in == nil {
		return nil
	}
	out := &intmodel.ContainerGit{
		Author:         gitIdentityToInternal(in.Author),
		Committer:      gitIdentityToInternal(in.Committer),
		SigningKey:     in.SigningKey,
		Sign:           cloneStringSlice(in.Sign),
		AllowedSigners: in.AllowedSigners,
		Signing:        gitSigningToInternal(in.Signing),
		IdentityRef:    secretRefToInternal(in.IdentityRef),
	}
	if len(in.Credentials) > 0 {
		out.Credentials = make([]intmodel.GitCredential, len(in.Credentials))
		for i := range in.Credentials {
			out.Credentials[i] = *gitCredentialToInternal(&in.Credentials[i])
		}
	}
	return out
}

// GitToExternal is the inverse of GitToInternal.
func GitToExternal(in *intmodel.ContainerGit) *ext.ContainerGit {
	if in == nil {
		return nil
	}
	out := &ext.ContainerGit{
		Author:         gitIdentityToExternal(in.Author),
		Committer:      gitIdentityToExternal(in.Committer),
		SigningKey:     in.SigningKey,
		Sign:           cloneStringSlice(in.Sign),
		AllowedSigners: in.AllowedSigners,
		Signing:        gitSigningToExternal(in.Signing),
		IdentityRef:    secretRefToExternal(in.IdentityRef),
	}
	if len(in.Credentials) > 0 {
		out.Credentials = make([]ext.GitCredential, len(in.Credentials))
		for i := range in.Credentials {
			out.Credentials[i] = *gitCredentialToExternal(&in.Credentials[i])
		}
	}
	return out
}

// RepoGitToInternal copies a per-source git identity into the internal model.
func RepoGitToInternal(in *ext.RepoGit) *intmodel.RepoGit {
	if in == nil {
		return nil
	}
	return &intmodel.RepoGit{
		Author:      gitIdentityToInternal(in.Author),
		Committer:   gitIdentityToInternal(in.Committer),
		Credential:  gitCredentialToInternal(in.Credential),
		Signing:     gitSigningToInternal(in.Signing),
		Sign:        cloneStringSlice(in.Sign),
		IdentityRef: secretRefToInternal(in.IdentityRef),
	}
}

// RepoGitToExternal is the inverse of RepoGitToInternal.
func RepoGitToExternal(in *intmodel.RepoGit) *ext.RepoGit {
	if in == nil {
		return nil
	}
	return &ext.RepoGit{
		Author:      gitIdentityToExternal(in.Author),
		Committer:   gitIdentityToExternal(in.Committer),
		Credential:  gitCredentialToExternal(in.Credential),
		Signing:     gitSigningToExternal(in.Signing),
		Sign:        cloneStringSlice(in.Sign),
		IdentityRef: secretRefToExternal(in.IdentityRef),
	}
}

func gitIdentityToInternal(in *ext.GitIdentity) *intmodel.GitIdentity {
	if in == nil {
		return nil
	}
	return &intmodel.GitIdentity{Name: in.Name, Email: in.Email}
}

func gitIdentityToExternal(in *intmodel.GitIdentity) *ext.GitIdentity {
	if in == nil {
		return nil
	}
	return &ext.GitIdentity{Name: in.Name, Email: in.Email}
}

func gitCredentialToInternal(in *ext.GitCredential) *intmodel.GitCredential {
	if in == nil {
		return nil
	}
	return &intmodel.GitCredential{
		Host:          strings.TrimSpace(in.Host),
		SecretRef:     secretRefToInternal(in.SecretRef),
		User:          strings.TrimSpace(in.User),
		Port:          in.Port,
		HostKeyPolicy: strings.TrimSpace(in.HostKeyPolicy),
	}
}

func gitCredentialToExternal(in *intmodel.GitCredential) *ext.GitCredential {
	if in == nil {
		return nil
	}
	return &ext.GitCredential{
		Host:          in.Host,
		SecretRef:     secretRefToExternal(in.SecretRef),
		User:          in.User,
		Port:          in.Port,
		HostKeyPolicy: in.HostKeyPolicy,
	}
}

func gitSigningToInternal(in *ext.GitSigning) *intmodel.GitSigning {
	if in == nil {
		return nil
	}
	return &intmodel.GitSigning{
		SecretRef:         secretRefToInternal(in.SecretRef),
		AllowedSignersRef: secretRefToInternal(in.AllowedSignersRef),
	}
}

func gitSigningToExternal(in *intmodel.GitSigning) *ext.GitSigning {
	if in == nil {
		return nil
	}
	return &ext.GitSigning{
		SecretRef:         secretRefToExternal(in.SecretRef),
		AllowedSignersRef: secretRefToExternal(in.AllowedSignersRef),
	}
}

// validateContainerGit enforces the container-level git invariants (issue
// #618, #1472): the ContainerGit default block plus every repo's own git.
func validateContainerGit(spec ext.ContainerSpec) error {
	if err := ValidateGit(spec.Git); err != nil {
		return fmt.Errorf("container %q: %w", spec.ID, err)
	}
	for i := range spec.Repos {
		if err := ValidateRepoGit(spec.Repos[i].Git); err != nil {
			return fmt.Errorf("container %q: repos[%d] %q: %w", spec.ID, i, spec.Repos[i].Name, err)
		}
		if g := spec.Repos[i].Git; g != nil && g.IdentityRef != nil {
			return fmt.Errorf(
				"container %q: repos[%d] %q: git.identityRef is resolved from a CellConfig repo fill; "+
					"a cell states the repo's author, credential and signing directly",
				spec.ID, i, spec.Repos[i].Name)
		}
	}
	return nil
}

// ValidateGit enforces the invariants of a complete git block — the one a
// container actually runs with, after every level has been merged:
//   - everything ValidateGitLayer checks, and
//   - sign requires a signing key (signingKey path or signing.secretRef).
func ValidateGit(git *ext.ContainerGit) error {
	if err := ValidateGitLayer(git); err != nil {
		return err
	}
	if git != nil && len(git.Sign) > 0 && git.SigningKey == "" && git.Signing == nil {
		return errors.New("git.sign requires git.signingKey or git.signing to be set")
	}
	if git != nil && git.IdentityRef != nil {
		return errors.New(
			"git.identityRef is resolved from a CellConfig or a Realm, Space or Stack default; " +
				"a cell or blueprint container states author, credentials and signing directly")
	}
	return nil
}

// ValidateGitLayer enforces the invariants of one level of the git hierarchy
// (a scope's defaults.git, a CellConfig's git, a blueprint container's git):
//   - author and committer each require both name and email when present;
//   - each sign entry is a known target (commits or tags);
//   - the two signing-key forms are mutually exclusive;
//   - every credential names a host and a secret, with a known host-key policy
//     and a valid port, and no two credentials share a host.
//
// It does not require sign to come with a signing key: a level may turn
// signing on for a key a shallower level supplies (#1472, step 6). The merged
// block is checked with ValidateGit when the cell is materialized.
func ValidateGitLayer(git *ext.ContainerGit) error {
	if git == nil {
		return nil
	}
	if err := validateGitIdentity("author", git.Author); err != nil {
		return err
	}
	if err := validateGitIdentity("committer", git.Committer); err != nil {
		return err
	}
	if err := validateGitSign("git.sign", git.Sign); err != nil {
		return err
	}
	if err := validateGitSigning("git.signing", git.Signing); err != nil {
		return err
	}
	if git.Signing != nil && (git.SigningKey != "" || git.AllowedSigners != "") {
		return errors.New("git.signing cannot be combined with git.signingKey or git.allowedSigners")
	}
	if git.IdentityRef != nil {
		if err := validateGitSecretRef("git.identityRef", git.IdentityRef); err != nil {
			return err
		}
	}
	hosts := map[string]struct{}{}
	for i := range git.Credentials {
		cred := &git.Credentials[i]
		field := fmt.Sprintf("git.credentials[%d]", i)
		host := strings.TrimSpace(cred.Host)
		if host == "" {
			return fmt.Errorf("%s.host is required", field)
		}
		if strings.ContainsAny(host, " \t\n\r\"'\\") {
			return fmt.Errorf("%s.host %q: must be a host or URL prefix without spaces or quotes", field, host)
		}
		if _, dup := hosts[host]; dup {
			return fmt.Errorf("%s.host %q: duplicate credential host", field, host)
		}
		hosts[host] = struct{}{}
		if err := validateGitCredential(field, cred); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRepoGit enforces the per-source git invariants.
func ValidateRepoGit(git *ext.RepoGit) error {
	if git == nil {
		return nil
	}
	if err := validateGitIdentity("author", git.Author); err != nil {
		return err
	}
	if err := validateGitIdentity("committer", git.Committer); err != nil {
		return err
	}
	if err := validateGitSign("git.sign", git.Sign); err != nil {
		return err
	}
	if err := validateGitSigning("git.signing", git.Signing); err != nil {
		return err
	}
	if git.IdentityRef != nil {
		if err := validateGitSecretRef("git.identityRef", git.IdentityRef); err != nil {
			return err
		}
	}
	if git.Credential != nil {
		if strings.TrimSpace(git.Credential.Host) != "" {
			return errors.New("git.credential.host must be empty: the repo url selects the remote")
		}
		if err := validateGitCredential("git.credential", git.Credential); err != nil {
			return err
		}
	}
	return nil
}

func validateGitCredential(field string, cred *ext.GitCredential) error {
	if err := validateGitSecretRef(field+".secretRef", cred.SecretRef); err != nil {
		return err
	}
	switch strings.TrimSpace(cred.HostKeyPolicy) {
	case "", ext.GitHostKeyPolicyStrict, ext.GitHostKeyPolicyAcceptNew:
	default:
		return fmt.Errorf("%s.hostKeyPolicy %q: must be %q or %q",
			field, cred.HostKeyPolicy, ext.GitHostKeyPolicyStrict, ext.GitHostKeyPolicyAcceptNew)
	}
	if cred.Port < 0 || cred.Port > 65535 {
		return fmt.Errorf("%s.port %d: must be between 1 and 65535", field, cred.Port)
	}
	if user := strings.TrimSpace(cred.User); user != "" && !gitSSHUserRE.MatchString(user) {
		return fmt.Errorf("%s.user %q: must be a login name", field, cred.User)
	}
	return nil
}

func validateGitSigning(field string, signing *ext.GitSigning) error {
	if signing == nil {
		return nil
	}
	if err := validateGitSecretRef(field+".secretRef", signing.SecretRef); err != nil {
		return err
	}
	if signing.AllowedSignersRef != nil {
		return validateGitSecretRef(field+".allowedSignersRef", signing.AllowedSignersRef)
	}
	return nil
}

func validateGitSecretRef(field string, ref *ext.ContainerSecretRef) error {
	if ref == nil || strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Realm) == "" {
		return fmt.Errorf("%s requires name and realm", field)
	}
	if (strings.TrimSpace(ref.Stack) != "" && strings.TrimSpace(ref.Space) == "") ||
		(strings.TrimSpace(ref.Cell) != "" && strings.TrimSpace(ref.Stack) == "") {
		return fmt.Errorf("%s scope is incomplete: a deeper coordinate requires all shallower ones", field)
	}
	for _, segment := range []string{ref.Name, ref.Realm, ref.Space, ref.Stack, ref.Cell} {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\x00") {
			return fmt.Errorf("%s: name and scope must not contain path separators or '.'/'..'", field)
		}
	}
	return nil
}

func validateGitSign(field string, sign []string) error {
	for _, s := range sign {
		if s != ext.GitSignCommits && s != ext.GitSignTags {
			return fmt.Errorf("%s %q: must be one of %q, %q", field, s, ext.GitSignCommits, ext.GitSignTags)
		}
	}
	return nil
}

// validateGitIdentity rejects a git identity that sets only one of name/email
// — both are required so the rendered name/email pair is never half-empty —
// or one that would break out of its gitconfig line.
func validateGitIdentity(role string, id *ext.GitIdentity) error {
	if id == nil {
		return nil
	}
	if id.Name == "" || id.Email == "" {
		return fmt.Errorf("git.%s requires both name and email", role)
	}
	if strings.ContainsAny(id.Name+id.Email, "\n\r\x00") {
		return fmt.Errorf("git.%s name and email must be single-line", role)
	}
	return nil
}
