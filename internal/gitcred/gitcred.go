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

// Package gitcred renders a container's git identity, stored credentials and
// signing config (issue #1472, steps 2–7) into the directory the daemon stages
// on the host and mounts read-only at gitplan.MountPath:
//
//	gitconfig              cell-global config, selected with GIT_CONFIG_SYSTEM
//	ssh_config             per-host ssh config for default ssh-key credentials
//	known_hosts            built-in forge host keys
//	known_hosts.d/<cred>   the host keys one credential trusts
//	keys/<key>/key         private key, plus key.pub / key-cert.pub / passphrase
//	basic/<cred>/...       username, password and the credential helper script
//	repos/<repo>/...       ssh config of a source with its own credential
//	allowed_signers.d/...  allowed-signers files
//	plan.json              what kuketty does with all of the above
//
// Secret material only ever lands in those files: never in the environment,
// never in a URL, never in the plan.
package gitcred

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const (
	dirMode    os.FileMode = 0o700
	secretMode os.FileMode = 0o600
	// StagingSubdir is the per-container directory under the secrets staging
	// root that holds the rendered bundle.
	StagingSubdir = "git"
)

// SecretReader loads a referenced secret's full material.
type SecretReader func(ref intmodel.ContainerSecretRef) (intmodel.SecretSpec, error)

// File is one staged file, addressed relative to the bundle root.
type File struct {
	Path string
	Mode os.FileMode
	Data []byte
}

// Bundle is the rendered git directory of one container.
type Bundle struct {
	Files []File
	Plan  gitplan.Plan
}

// Needed reports whether a container declares any git identity, credential or
// signing config and therefore gets a staged bundle.
func Needed(spec intmodel.ContainerSpec) bool {
	if spec.Git != nil {
		return true
	}
	for i := range spec.Repos {
		if spec.Repos[i].Git != nil {
			return true
		}
	}
	return false
}

// StagingDir returns the host directory a container's bundle is staged in.
func StagingDir(stagingRoot, containerdID string) string {
	return filepath.Join(stagingRoot, containerdID, StagingSubdir)
}

type configEntry = gitplan.ConfigEntry

type stagedKey struct {
	dir        string
	encrypted  bool
	publicKey  string
	knownHosts string
	hasCert    bool
}

type resolvedCredential struct {
	id     string
	target credentialTarget
	cred   intmodel.GitCredential
	key    *stagedKey // ssh-key credential
	basic  string     // basic-auth credential directory
}

type builder struct {
	read   SecretReader
	files  map[string]File
	keys   map[string]*stagedKey
	basics map[string]string
	plan   gitplan.Plan
}

// Build renders the bundle for a container. It reads every referenced secret
// through read and fails — naming the secret, never its material — when one is
// missing, of the wrong type, or unusable.
func Build(spec intmodel.ContainerSpec, read SecretReader) (*Bundle, error) {
	b := &builder{
		read:   read,
		files:  map[string]File{},
		keys:   map[string]*stagedKey{},
		basics: map[string]string{},
	}
	git := spec.Git
	if git == nil {
		git = &intmodel.ContainerGit{}
	}

	entries := []configEntry{{Key: "include.path", Value: "/etc/gitconfig"}}
	entries = append(entries, identityEntries(git.Author, git.Committer)...)

	signing, err := b.defaultSigningEntries(git)
	if err != nil {
		return nil, err
	}
	entries = append(entries, signing...)

	defaults := make([]resolvedCredential, 0, len(git.Credentials))
	for i := range git.Credentials {
		cred := git.Credentials[i]
		rc, credErr := b.resolveCredential("c"+strconv.Itoa(i), cred, parseCredentialTarget(cred.Host))
		if credErr != nil {
			return nil, fmt.Errorf("git.credentials[%d] (%s): %w", i, cred.Host, credErr)
		}
		defaults = append(defaults, rc)
	}

	hostCreds := globalSSHCredentials(defaults)
	if len(hostCreds) > 0 {
		b.addFile(gitplan.SSHConfigFile, b.sshConfig(hostCreds))
		entries = append(entries, configEntry{
			Key: "core.sshCommand", Value: "ssh -F " + path.Join(gitplan.MountPath, gitplan.SSHConfigFile),
		})
		b.plan.SSHCommand = true
	}
	for i := range defaults {
		if defaults[i].basic != "" {
			entries = append(
				entries,
				basicHelperEntries(defaults[i], defaults[i].target.Scheme, defaults[i].target.Port)...)
		}
	}

	for i := range spec.Repos {
		if repoErr := b.planRepo(spec.Repos[i], i, git, defaults, hostCreds); repoErr != nil {
			return nil, fmt.Errorf("repos[%d] %q: %w", i, spec.Repos[i].Name, repoErr)
		}
	}

	b.addFile(gitplan.KnownHostsFile, gitplan.AllBuiltinKnownHosts())
	b.addFile(gitplan.ConfigFile, renderGitConfig(entries))
	planJSON, err := json.MarshalIndent(b.plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode git plan: %w", err)
	}
	b.addFile(gitplan.PlanFile, string(planJSON)+"\n")

	out := &Bundle{Plan: b.plan, Files: make([]File, 0, len(b.files))}
	for _, f := range b.files {
		out.Files = append(out.Files, f)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

func (b *builder) addFile(name string, content string) {
	b.files[name] = File{Path: name, Mode: secretMode, Data: []byte(content)}
}

// identityEntries maps an author/committer pair onto git config: author.* and
// committer.* carry the two roles, and user.* (what most tooling reads) takes
// the author, or the committer when no author is set.
func identityEntries(author, committer *intmodel.GitIdentity) []configEntry {
	var out []configEntry
	user := author
	if user == nil {
		user = committer
	}
	if user != nil {
		out = append(out,
			configEntry{Key: "user.name", Value: user.Name},
			configEntry{Key: "user.email", Value: user.Email},
		)
	}
	if author != nil {
		out = append(out,
			configEntry{Key: "author.name", Value: author.Name},
			configEntry{Key: "author.email", Value: author.Email},
		)
	}
	if committer != nil {
		out = append(out,
			configEntry{Key: "committer.name", Value: committer.Name},
			configEntry{Key: "committer.email", Value: committer.Email},
		)
	}
	return out
}

func identityEmail(author, committer *intmodel.GitIdentity) string {
	if committer != nil {
		return committer.Email
	}
	if author != nil {
		return author.Email
	}
	return ""
}

func signEntries(sign []string, defaultAll bool) []configEntry {
	commits, tags := defaultAll && len(sign) == 0, defaultAll && len(sign) == 0
	for _, s := range sign {
		switch s {
		case intmodel.GitSignCommits:
			commits = true
		case intmodel.GitSignTags:
			tags = true
		}
	}
	var out []configEntry
	if commits {
		out = append(out, configEntry{Key: "commit.gpgsign", Value: "true"})
	}
	if tags {
		out = append(out, configEntry{Key: "tag.gpgsign", Value: "true"})
	}
	return out
}

// defaultSigningEntries renders the container-level signing config: a stored
// key (git.signing) or the pre-#1472 in-container paths (git.signingKey /
// git.allowedSigners).
func (b *builder) defaultSigningEntries(git *intmodel.ContainerGit) ([]configEntry, error) {
	if git.Signing != nil {
		entries, err := b.signingEntries("default", git.Signing, identityEmail(git.Author, git.Committer))
		if err != nil {
			return nil, fmt.Errorf("git.signing: %w", err)
		}
		return append(entries, signEntries(git.Sign, true)...), nil
	}
	var out []configEntry
	if git.SigningKey != "" {
		out = append(out,
			configEntry{Key: "user.signingkey", Value: git.SigningKey},
			configEntry{Key: "gpg.format", Value: "ssh"},
		)
	}
	out = append(out, signEntries(git.Sign, false)...)
	if git.AllowedSigners != "" {
		out = append(out, configEntry{Key: "gpg.ssh.allowedSignersFile", Value: git.AllowedSigners})
	}
	return out, nil
}

// signingEntries stages the signing key and returns the config selecting it.
// An unencrypted key is named by its private key file; a passphrase-protected
// one by its public key, so ssh-keygen signs through the ssh-agent kuketty
// loaded it into.
func (b *builder) signingEntries(id string, signing *intmodel.GitSigning, email string) ([]configEntry, error) {
	if signing.SecretRef == nil {
		return nil, errors.New("secretRef is required")
	}
	key, err := b.addKey(*signing.SecretRef)
	if err != nil {
		return nil, err
	}
	keyPath := path.Join(key.dir, "key")
	if key.encrypted {
		if key.publicKey == "" {
			return nil, fmt.Errorf(
				"%w: signing with passphrase-protected secret %q needs spec.sshKey.publicKey",
				errdefs.ErrSecretTypeFields, signing.SecretRef.Name)
		}
		keyPath = path.Join(key.dir, "key.pub")
	}
	out := []configEntry{
		{Key: "gpg.format", Value: "ssh"},
		{Key: "user.signingkey", Value: keyPath},
	}

	allowed := ""
	switch {
	case signing.AllowedSignersRef != nil:
		spec, readErr := b.read(*signing.AllowedSignersRef)
		if readErr != nil {
			return nil, fmt.Errorf("allowedSignersRef: %w", readErr)
		}
		if spec.EffectiveType() != intmodel.SecretTypeOpaque {
			return nil, fmt.Errorf("%w: allowedSignersRef %q is %s, want opaque",
				errdefs.ErrSecretTypeMismatch, signing.AllowedSignersRef.Name, spec.EffectiveType())
		}
		allowed = ensureNewline(spec.Data)
	case key.publicKey != "" && email != "":
		allowed = email + ` namespaces="git" ` + key.publicKey + "\n"
	}
	if allowed != "" {
		name := path.Join("allowed_signers.d", id)
		b.addFile(name, allowed)
		out = append(out, configEntry{
			Key: "gpg.ssh.allowedSignersFile", Value: path.Join(gitplan.MountPath, name),
		})
	}
	return out, nil
}

// addKey stages an ssh-key secret once per bundle and returns where it lives.
func (b *builder) addKey(ref intmodel.ContainerSecretRef) (*stagedKey, error) {
	id := refID(ref)
	if key, ok := b.keys[id]; ok {
		return key, nil
	}
	spec, err := b.read(ref)
	if err != nil {
		return nil, err
	}
	if spec.EffectiveType() != intmodel.SecretTypeSSHKey || spec.SSHKey == nil {
		return nil, fmt.Errorf("%w: secret %q is %s, want %s",
			errdefs.ErrSecretTypeMismatch, ref.Name, spec.EffectiveType(), intmodel.SecretTypeSSHKey)
	}
	material := spec.SSHKey
	rel := path.Join("keys", id)
	key := &stagedKey{
		dir:        path.Join(gitplan.MountPath, rel),
		encrypted:  privateKeyEncrypted(material.PrivateKey),
		publicKey:  strings.TrimSpace(material.PublicKey),
		knownHosts: material.KnownHosts,
		hasCert:    strings.TrimSpace(material.Certificate) != "",
	}
	if key.encrypted && material.Passphrase == "" {
		return nil, fmt.Errorf(
			"%w: secret %q holds a passphrase-protected key but no spec.sshKey.passphrase",
			errdefs.ErrSecretTypeFields, ref.Name)
	}
	if key.publicKey == "" {
		key.publicKey = derivePublicKey(material.PrivateKey)
	}
	b.addFile(path.Join(rel, "key"), ensureNewline(material.PrivateKey))
	if key.publicKey != "" {
		b.addFile(path.Join(rel, "key.pub"), key.publicKey+"\n")
	}
	if key.hasCert {
		b.addFile(path.Join(rel, "key-cert.pub"), strings.TrimSpace(material.Certificate)+"\n")
	}
	if key.encrypted {
		b.addFile(path.Join(rel, "passphrase"), material.Passphrase+"\n")
		b.plan.AgentKeys = append(b.plan.AgentKeys, gitplan.AgentKey{
			Key:            path.Join(key.dir, "key"),
			PassphraseFile: path.Join(key.dir, "passphrase"),
		})
	}
	b.keys[id] = key
	return key, nil
}

// addBasic stages a basic-auth secret once per bundle: the username, the
// password (or token) and the helper script git runs to read them.
func (b *builder) addBasic(ref intmodel.ContainerSecretRef) (string, error) {
	id := refID(ref)
	if dir, ok := b.basics[id]; ok {
		return dir, nil
	}
	spec, err := b.read(ref)
	if err != nil {
		return "", err
	}
	if spec.EffectiveType() != intmodel.SecretTypeBasicAuth || spec.BasicAuth == nil {
		return "", fmt.Errorf("%w: secret %q is %s, want %s",
			errdefs.ErrSecretTypeMismatch, ref.Name, spec.EffectiveType(), intmodel.SecretTypeBasicAuth)
	}
	rel := path.Join("basic", id)
	dir := path.Join(gitplan.MountPath, rel)
	password := spec.BasicAuth.Password
	if password == "" {
		password = spec.BasicAuth.Token
	}
	b.addFile(path.Join(rel, "username"), spec.BasicAuth.Username+"\n")
	b.addFile(path.Join(rel, "password"), password+"\n")
	b.addFile(path.Join(rel, "helper"), credentialHelperScript(dir))
	b.basics[id] = dir
	return dir, nil
}

// credentialHelperScript is the git credential helper for one basic-auth
// secret. It is run as `sh <file> <operation>` (the staging mount may be
// noexec) and answers only `get`; store and erase are no-ops because the
// mounted secret is the source of truth.
func credentialHelperScript(dir string) string {
	return "#!/bin/sh\n" +
		"# kukeon git credential helper: answers `get` from the mounted secret.\n" +
		"[ \"$1\" = get ] || exit 0\n" +
		"d=" + dir + "\n" +
		"printf 'username=%s\\n' \"$(cat \"$d/username\")\"\n" +
		"printf 'password=%s\\n' \"$(cat \"$d/password\")\"\n"
}

func (b *builder) resolveCredential(
	id string, cred intmodel.GitCredential, target credentialTarget,
) (resolvedCredential, error) {
	rc := resolvedCredential{id: id, target: target, cred: cred}
	if cred.SecretRef == nil {
		return rc, errors.New("secretRef is required")
	}
	if target.Host == "" {
		return rc, errors.New("host is required")
	}
	spec, err := b.read(*cred.SecretRef)
	if err != nil {
		return rc, err
	}
	switch spec.EffectiveType() {
	case intmodel.SecretTypeSSHKey:
		if rc.key, err = b.addKey(*cred.SecretRef); err != nil {
			return rc, err
		}
		b.addFile(path.Join("known_hosts.d", id), rc.knownHosts())
	case intmodel.SecretTypeBasicAuth:
		if cred.User != "" || cred.Port != 0 || cred.HostKeyPolicy != "" {
			return rc, fmt.Errorf("%w: user, port and hostKeyPolicy apply to ssh-key credentials only",
				errdefs.ErrSecretTypeMismatch)
		}
		if rc.basic, err = b.addBasic(*cred.SecretRef); err != nil {
			return rc, err
		}
	default:
		return rc, fmt.Errorf("%w: secret %q is %s, want %s or %s",
			errdefs.ErrSecretTypeMismatch, cred.SecretRef.Name, spec.EffectiveType(),
			intmodel.SecretTypeBasicAuth, intmodel.SecretTypeSSHKey)
	}
	return rc, nil
}

func (rc resolvedCredential) sshPort() int {
	if rc.cred.Port != 0 {
		return rc.cred.Port
	}
	return rc.target.Port
}

// knownHosts is the set of host keys the credential trusts: the secret's
// pinned known_hosts when it has one, else the built-in keys of a known forge.
// An empty result leaves strict checking with nothing to match, so an unknown
// host is refused unless the credential opts in to accept-new.
func (rc resolvedCredential) knownHosts() string {
	if strings.TrimSpace(rc.key.knownHosts) != "" {
		return ensureNewline(rc.key.knownHosts)
	}
	return gitplan.BuiltinKnownHosts(rc.target.Host, rc.sshPort())
}

// globalSSHCredentials picks, per host, the ssh-key credential the cell-global
// ssh config uses: the host-wide one, or the first prefix-scoped one when the
// host has no host-wide credential.
func globalSSHCredentials(defaults []resolvedCredential) []resolvedCredential {
	chosen := map[string]int{}
	var order []string
	for i := range defaults {
		rc := defaults[i]
		if rc.key == nil {
			continue
		}
		prev, seen := chosen[rc.target.Host]
		switch {
		case !seen:
			chosen[rc.target.Host] = i
			order = append(order, rc.target.Host)
		case defaults[prev].target.Path != "" && rc.target.Path == "":
			chosen[rc.target.Host] = i
		}
	}
	out := make([]resolvedCredential, 0, len(order))
	for _, host := range order {
		out = append(out, defaults[chosen[host]])
	}
	return out
}

func sshHostBlock(rc resolvedCredential, user string, port int) string {
	if user == "" {
		user = defaultSSHUser
	}
	var s strings.Builder
	s.WriteString("Host " + rc.target.Host + "\n")
	s.WriteString("    HostName " + rc.target.Host + "\n")
	s.WriteString("    User " + user + "\n")
	if port != 0 {
		s.WriteString("    Port " + strconv.Itoa(port) + "\n")
	}
	s.WriteString("    IdentityFile " + path.Join(rc.key.dir, "key") + "\n")
	if rc.key.hasCert {
		s.WriteString("    CertificateFile " + path.Join(rc.key.dir, "key-cert.pub") + "\n")
	}
	s.WriteString("    IdentitiesOnly yes\n")
	s.WriteString("    BatchMode yes\n")
	hostKeys := path.Join(gitplan.MountPath, "known_hosts.d", rc.id)
	if rc.cred.HostKeyPolicy == intmodel.GitHostKeyPolicyAcceptNew {
		s.WriteString("    StrictHostKeyChecking accept-new\n")
		// The first file is the one ssh records a newly accepted key in.
		s.WriteString("    UserKnownHostsFile ~/.ssh/known_hosts " + hostKeys + "\n")
	} else {
		s.WriteString("    StrictHostKeyChecking yes\n")
		s.WriteString("    UserKnownHostsFile " + hostKeys + "\n")
	}
	return s.String()
}

// sshConfigTail covers the hosts no stored credential names: they are checked
// against the image's own known_hosts plus the built-in forge keys. The
// generated file is deliberately self-contained — it does not include
// ~/.ssh/config, whose ownership or syntax problems would otherwise break git
// for every credentialed host as well.
func sshConfigTail() string {
	return "Host *\n" +
		"    UserKnownHostsFile ~/.ssh/known_hosts ~/.ssh/known_hosts2 " +
		path.Join(gitplan.MountPath, gitplan.KnownHostsFile) + "\n"
}

func (b *builder) sshConfig(creds []resolvedCredential) string {
	var s strings.Builder
	s.WriteString("# Generated by kukeon from git.credentials[]; do not edit.\n")
	for _, rc := range creds {
		s.WriteString(sshHostBlock(rc, rc.cred.User, rc.sshPort()))
	}
	s.WriteString(sshConfigTail())
	return s.String()
}

// basicHelperEntries scopes a basic-auth credential's helper to its remote.
// The empty helper first clears any helper an outer scope configured, so the
// stored secret is the only source asked.
func basicHelperEntries(rc resolvedCredential, scheme string, port int) []configEntry {
	if scheme != TransportHTTP {
		scheme = TransportHTTPS
	}
	scope := scheme + "://" + hostPort(rc.target.Host, port)
	if rc.target.Path != "" {
		scope += "/" + rc.target.Path
	}
	out := []configEntry{
		{Key: "credential." + scope + ".helper", Value: ""},
		{Key: "credential." + scope + ".helper", Value: "!sh " + path.Join(rc.basic, "helper"), Add: true},
	}
	if rc.target.Path != "" {
		out = append(out, configEntry{Key: "credential." + scope + ".useHttpPath", Value: "true"})
	}
	return out
}

// matchCredential returns the default credential covering the remote, the
// longest path prefix winning and declaration order breaking ties.
func matchCredential(defaults []resolvedCredential, remote Remote) (resolvedCredential, bool) {
	best, bestLen, found := resolvedCredential{}, -1, false
	for i := range defaults {
		if n, ok := defaults[i].target.matches(remote); ok && n > bestLen {
			best, bestLen, found = defaults[i], n, true
		}
	}
	return best, found
}

// planRepo resolves one source: the credential that authenticates it, the URL
// whose transport follows that credential, and the repo-local config carrying
// whatever the source overrides.
//
//nolint:funlen // one repo, one decision tree: credential, transport, identity, signing.
func (b *builder) planRepo(
	repo intmodel.ContainerRepo, index int, git *intmodel.ContainerGit,
	defaults, hostCreds []resolvedCredential,
) error {
	remote := ParseRemote(repo.URL)
	repoID := "r" + strconv.Itoa(index)
	var entry gitplan.Repo

	var (
		rc              resolvedCredential
		bound, specific bool
	)
	if repo.Git != nil && repo.Git.Credential != nil {
		if remote.Transport == TransportOther {
			return errors.New("git.credential needs an ssh or http(s) url")
		}
		var err error
		rc, err = b.resolveCredential(repoID, *repo.Git.Credential, credentialTarget{Host: remote.Host})
		if err != nil {
			return fmt.Errorf("git.credential: %w", err)
		}
		bound, specific = true, true
	} else if rc, bound = matchCredential(defaults, remote); bound {
		// A prefix-scoped credential the cell-global config cannot express per
		// path is written into this checkout instead.
		specific = !isGlobal(rc, hostCreds)
	}

	if bound {
		switch {
		case rc.key != nil:
			port, user := rc.sshPort(), rc.cred.User
			if remote.Transport == TransportSSH {
				if port == 0 {
					port = remote.Port
				}
				if user == "" {
					user = remote.User
				}
			}
			entry.URL = remote.SSHURL(user, port)
			if specific {
				name := path.Join("repos", repoID, gitplan.SSHConfigFile)
				b.addFile(name,
					"# Generated by kukeon for repo "+strconv.Quote(repo.Name)+"; do not edit.\n"+
						sshHostBlock(rc, user, port)+sshConfigTail())
				entry.Config = append(entry.Config, configEntry{
					Key: "core.sshCommand", Value: "ssh -F " + path.Join(gitplan.MountPath, name),
				})
			}
		case rc.basic != "":
			scheme, port := TransportHTTPS, rc.target.Port
			if remote.Transport == TransportHTTP || rc.target.Scheme == TransportHTTP {
				scheme = TransportHTTP
			}
			if port == 0 && remote.Transport != TransportSSH {
				port = remote.Port
			}
			entry.URL = remote.HTTPURL(scheme, port)
			if specific {
				entry.Config = append(entry.Config, basicHelperEntries(rc, scheme, port)...)
			}
		}
	}

	if rg := repo.Git; rg != nil {
		author, committer := rg.Author, rg.Committer
		entry.Config = append(entry.Config, identityEntries(author, committer)...)
		if rg.Signing != nil {
			effAuthor, effCommitter := author, committer
			if effAuthor == nil {
				effAuthor = git.Author
			}
			if effCommitter == nil {
				effCommitter = git.Committer
			}
			signing, err := b.signingEntries(repoID, rg.Signing, identityEmail(effAuthor, effCommitter))
			if err != nil {
				return fmt.Errorf("git.signing: %w", err)
			}
			entry.Config = append(entry.Config, signing...)
			entry.Config = append(entry.Config, signEntries(rg.Sign, true)...)
		} else if len(rg.Sign) > 0 {
			entry.Config = append(entry.Config, explicitSignEntries(rg.Sign)...)
		}
	}

	if entry.URL == strings.TrimSpace(repo.URL) {
		entry.URL = ""
	}
	if entry.URL == "" && len(entry.Config) == 0 {
		return nil
	}
	if b.plan.Repos == nil {
		b.plan.Repos = map[string]gitplan.Repo{}
	}
	b.plan.Repos[repo.Name] = entry
	return nil
}

// explicitSignEntries states both signing switches, so a source that lists
// only "commits" turns tag signing off even when the default signs both.
func explicitSignEntries(sign []string) []configEntry {
	commits, tags := "false", "false"
	for _, s := range sign {
		switch s {
		case intmodel.GitSignCommits:
			commits = "true"
		case intmodel.GitSignTags:
			tags = "true"
		}
	}
	return []configEntry{
		{Key: "commit.gpgsign", Value: commits},
		{Key: "tag.gpgsign", Value: tags},
	}
}

func isGlobal(rc resolvedCredential, hostCreds []resolvedCredential) bool {
	if rc.basic != "" {
		return true // every default basic-auth credential has its own scoped helper section
	}
	for i := range hostCreds {
		if hostCreds[i].id == rc.id {
			return true
		}
	}
	return false
}

// renderGitConfig writes entries as a gitconfig file, grouping consecutive
// entries of one section under a single header.
func renderGitConfig(entries []configEntry) string {
	var s strings.Builder
	s.WriteString("# Generated by kukeon from the cell's git block; do not edit.\n")
	header := ""
	for _, e := range entries {
		section, subsection, name := splitConfigKey(e.Key)
		h := "[" + section
		if subsection != "" {
			h += " " + quoteConfig(subsection)
		}
		h += "]"
		if h != header {
			s.WriteString(h + "\n")
			header = h
		}
		s.WriteString("\t" + name + " = " + quoteConfig(e.Value) + "\n")
	}
	return s.String()
}

// splitConfigKey splits "section.sub.section.name" at the first and last dot.
func splitConfigKey(key string) (string, string, string) {
	first := strings.IndexByte(key, '.')
	last := strings.LastIndexByte(key, '.')
	if first < 0 {
		return key, "", ""
	}
	if first == last {
		return key[:first], "", key[first+1:]
	}
	return key[:first], key[first+1 : last], key[last+1:]
}

func quoteConfig(value string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(value) + `"`
}

func ensureNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// refID is a filesystem-safe, collision-free name for a secret reference.
func refID(ref intmodel.ContainerSecretRef) string {
	full := strings.Join([]string{ref.Realm, ref.Space, ref.Stack, ref.Cell, ref.Name}, "/")
	sum := sha256.Sum256([]byte(full))
	var name strings.Builder
	for _, r := range ref.Name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			name.WriteRune(r)
		} else {
			name.WriteRune('_')
		}
	}
	const digestLen = 8
	return name.String() + "-" + hex.EncodeToString(sum[:])[:digestLen]
}

// Stage writes the bundle into dir, replacing whatever a previous start left
// there. The directory itself is kept (it may be a live bind-mount source);
// only its contents are replaced. Everything is created root-only; the caller
// re-owns the tree to the container's process uid once that is known.
func Stage(dir string, bundle *Bundle) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("%w: create git staging dir: %w", errdefs.ErrSecretStagingFailed, err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("%w: chmod git staging dir: %w", errdefs.ErrSecretStagingFailed, err)
	}
	existing, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%w: read git staging dir: %w", errdefs.ErrSecretStagingFailed, err)
	}
	for _, entry := range existing {
		if err = os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("%w: clear git staging dir: %w", errdefs.ErrSecretStagingFailed, err)
		}
	}
	for _, f := range bundle.Files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err = os.MkdirAll(filepath.Dir(dst), dirMode); err != nil {
			return fmt.Errorf("%w: create %s: %w", errdefs.ErrSecretStagingFailed, filepath.Dir(f.Path), err)
		}
		if err = os.WriteFile(dst, f.Data, f.Mode); err != nil {
			return fmt.Errorf("%w: write %s: %w", errdefs.ErrSecretStagingFailed, f.Path, err)
		}
		if err = os.Chmod(dst, f.Mode); err != nil {
			return fmt.Errorf("%w: chmod %s: %w", errdefs.ErrSecretStagingFailed, f.Path, err)
		}
	}
	return nil
}

// Chown re-owns a staged bundle to the container's process uid so a non-root
// workload can read its own credentials (and ssh accepts the key file, which
// it refuses when owned by another user). Modes stay owner-only.
func Chown(dir string, uid int) error {
	return filepath.WalkDir(dir, func(p string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Lchown(p, uid, -1)
	})
}
