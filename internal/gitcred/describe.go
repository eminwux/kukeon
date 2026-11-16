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

package gitcred

import (
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// SecretTyper reports a referenced secret's type without reading its material;
// "" when the secret does not exist (or its type cannot be read).
type SecretTyper func(ref intmodel.ContainerSecretRef) string

// Field is one resolved git setting of a source. FromSource reports that the
// source's own git block set it, rather than the container default.
type Field struct {
	Value      string
	FromSource bool
}

// SourceIdentity is the effective git identity of one source: what the cell
// will clone, commit, push and sign with in that checkout.
type SourceIdentity struct {
	Repo string
	// URL is the clone URL after its transport followed the credential.
	URL        string
	Author     Field
	Committer  Field
	Credential Field
	// CredentialHost is the GitCredential.Host of the default credential that
	// matched, "" for a source-specific or absent credential.
	CredentialHost string
	Signing        Field
}

// Describe resolves, without reading any secret material, the identity and
// credential every source of a container ends up with — the same resolution
// Build performs — so a preview can show it before anything is created.
//
//nolint:funlen // one loop resolving the same precedence Build applies, kept side by side with it.
func Describe(spec intmodel.ContainerSpec, typeOf SecretTyper) []SourceIdentity {
	git := spec.Git
	if git == nil {
		git = &intmodel.ContainerGit{}
	}
	out := make([]SourceIdentity, 0, len(spec.Repos))
	for i := range spec.Repos {
		repo := spec.Repos[i]
		remote := ParseRemote(repo.URL)
		id := SourceIdentity{
			Repo:      repo.Name,
			URL:       strings.TrimSpace(repo.URL),
			Author:    Field{Value: formatIdentity(git.Author)},
			Committer: Field{Value: formatIdentity(git.Committer)},
			Signing:   Field{Value: describeSigning(git)},
		}

		var cred *intmodel.GitCredential
		target := credentialTarget{}
		if rg := repo.Git; rg != nil {
			if rg.Author != nil {
				id.Author = Field{Value: formatIdentity(rg.Author), FromSource: true}
			}
			if rg.Committer != nil {
				id.Committer = Field{Value: formatIdentity(rg.Committer), FromSource: true}
			}
			if rg.Signing != nil {
				id.Signing = Field{Value: DescribeRef(rg.Signing.SecretRef), FromSource: true}
			}
			if rg.Credential != nil && remote.Transport != TransportOther {
				cred, target = rg.Credential, credentialTarget{}
				id.Credential.FromSource = true
			}
		}
		if cred == nil {
			bestLen := -1
			for j := range git.Credentials {
				t := parseCredentialTarget(git.Credentials[j].Host)
				if n, ok := t.matches(remote); ok && n > bestLen {
					cred, target, bestLen = &git.Credentials[j], t, n
					id.CredentialHost = git.Credentials[j].Host
				}
			}
		}
		if cred != nil && cred.SecretRef != nil {
			secretType := typeOf(*cred.SecretRef)
			label := secretType
			if label == "" {
				label = "missing"
			}
			id.Credential.Value = label + " " + DescribeRef(cred.SecretRef)
			switch secretType {
			case intmodel.SecretTypeSSHKey:
				port, user := cred.Port, cred.User
				if port == 0 {
					port = target.Port
				}
				if remote.Transport == TransportSSH {
					if port == 0 {
						port = remote.Port
					}
					if user == "" {
						user = remote.User
					}
				}
				id.URL = remote.SSHURL(user, port)
			case intmodel.SecretTypeBasicAuth:
				scheme, port := TransportHTTPS, target.Port
				if remote.Transport == TransportHTTP || target.Scheme == TransportHTTP {
					scheme = TransportHTTP
				}
				if port == 0 && remote.Transport != TransportSSH {
					port = remote.Port
				}
				id.URL = remote.HTTPURL(scheme, port)
			}
		}
		out = append(out, id)
	}
	return out
}

func formatIdentity(id *intmodel.GitIdentity) string {
	if id == nil {
		return ""
	}
	return id.Name + " <" + id.Email + ">"
}

func describeSigning(git *intmodel.ContainerGit) string {
	switch {
	case git.Signing != nil:
		return DescribeRef(git.Signing.SecretRef)
	case git.SigningKey != "":
		return git.SigningKey
	default:
		return ""
	}
}

// DescribeRef renders a secret reference as "secret:<scope>/<name>" — its
// coordinates, never its material.
func DescribeRef(ref *intmodel.ContainerSecretRef) string {
	if ref == nil {
		return ""
	}
	parts := []string{ref.Realm}
	for _, p := range []string{ref.Space, ref.Stack, ref.Cell} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return "secret:" + strings.Join(parts, "/") + "/" + ref.Name
}
