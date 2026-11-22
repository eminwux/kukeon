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

// Package gitimport discovers the operator's git identity on the host — the
// global git configuration and an SSH key under ~/.ssh — and turns it into a
// git connector identity: one Secret tagged `git` (issue #1591). With a key it
// is an ssh-key identity that clones, pushes and, when the operator signs with
// that key, signs; without one it is an author-only (opaque) identity.
//
// The package reads the host and builds Secret documents; it talks to no
// daemon. `kuke connector import git` and the web UI's "Import from host" both
// call Import and store what it returns. Key material is only ever in the
// returned documents: no error, note or summary carries it.
package gitimport

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	// KeyName is the default name of an imported key identity.
	KeyName = "git-ssh"
	// AuthorName is the default name of an imported author-only identity.
	AuthorName = "git-author"
	// SignersName is the Secret holding the imported allowed-signers file.
	SignersName = "git-allowed-signers"
	// DefaultRealm is where an imported identity is stored by default.
	DefaultRealm = "default"
	// DefaultHost is the host a key identity authenticates when none is named.
	DefaultHost = "github.com"
)

// Options says where to read the identity and how to store it.
type Options struct {
	// Home is the home directory whose ~/.ssh is read. Required.
	Home string
	// Git reads the global git configuration. Nil runs git (CommandGit),
	// which reads the configuration of the account the process runs as.
	Git GitReader
	// Key is the private key to import. Empty selects the key the global git
	// configuration signs with, else the first of ~/.ssh/id_ed25519,
	// id_ecdsa, id_rsa; when none exists the identity is author-only.
	Key string
	// PassphraseFile holds the key's passphrase, required for an encrypted key.
	PassphraseFile string
	// KnownHostsFile pins host keys for the key. Empty relies on the built-in
	// keys of github.com, gitlab.com and bitbucket.org.
	KnownHostsFile string
	// Hosts are the hosts or URL prefixes the key authenticates. Empty is
	// github.com.
	Hosts []string
	// Name is the identity Secret's name. Empty is git-ssh for a key and
	// git-author for an author-only identity.
	Name string
	// Realm is where the Secrets are stored. Empty is default.
	Realm string
}

// Result is an import: the Secrets to store and what they say, for a dry run
// or a confirmation. Only Secrets carries key material.
type Result struct {
	// Home is the home directory that was read: whose ~/.ssh and, with
	// CommandGit, whose global git configuration.
	Home string `json:"home"`
	// Name, Realm and Type are the identity Secret's.
	Name  string `json:"name"`
	Realm string `json:"realm"`
	Type  string `json:"type"`
	// Identity are the identity Secret's readable fields.
	Identity map[string]string `json:"identity"`
	// KeyPath is the private key imported; empty for an author-only identity.
	KeyPath string `json:"keyPath,omitempty"`
	// AllowedSignersPath is the allowed-signers file imported, if any.
	AllowedSignersPath string `json:"allowedSignersPath,omitempty"`
	// Notes are operator-facing remarks: settings that were not imported.
	Notes []string `json:"notes,omitempty"`
	// Secrets are the documents to store, in order: the allowed-signers file
	// before the identity that names it, the identity last. They carry the
	// key material: never print or serialize them.
	Secrets []v1beta1.SecretDoc `json:"-"`
}

// Ref is the identity Secret's reference.
func (r Result) Ref() v1beta1.ContainerSecretRef {
	return v1beta1.ContainerSecretRef{Name: r.Name, Realm: r.Realm}
}

// Check checks the identity Secret against the git connector's definition —
// the one the catalog kuke ships carries — exactly as `kuke create secret
// --connector git` does.
func (r Result) Check(desc v1beta1.BlueprintConnector) error {
	if len(r.Secrets) == 0 {
		return fmt.Errorf("%w: nothing to store", errdefs.ErrGitImport)
	}
	identity := r.Secrets[len(r.Secrets)-1].Spec
	if err := connector.CheckSecret(desc, identity); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrGitImport, err)
	}
	if err := connector.CheckGitAuthorOnly(identity); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrGitImport, err)
	}
	return nil
}

// Import reads the operator's git identity and builds the Secrets that store
// it. Nothing is written.
func Import(ctx context.Context, opts Options) (Result, error) {
	home := strings.TrimSpace(opts.Home)
	if home == "" {
		return Result{}, fmt.Errorf("%w: no home directory to read", errdefs.ErrGitImport)
	}
	realm := strings.TrimSpace(opts.Realm)
	if realm == "" {
		realm = DefaultRealm
	}
	hosts, err := checkHosts(opts.Hosts)
	if err != nil {
		return Result{}, err
	}
	reader := opts.Git
	if reader == nil {
		reader = CommandGit{}
	}
	git, notes, err := discover(ctx, reader)
	if err != nil {
		return Result{}, err
	}
	result := Result{Home: home, Realm: realm, Identity: map[string]string{}, Notes: notes}
	if git.name != "" {
		person := connector.FormatPerson(git.name, git.email)
		if _, _, parseErr := connector.ParsePerson(person); parseErr == nil {
			result.Identity[connector.GitFieldAuthor] = person
			result.Identity[connector.GitFieldCommitter] = person
		} else {
			result.Notes = append(result.Notes,
				"global git user.name/user.email do not read as `Name <email>`; the identity carries no author.")
		}
	}

	keyPath, found := keyPathFor(git, home, opts.Key)
	if !found {
		return authorOnly(result, git, opts)
	}
	if err = importKey(&result, git, keyPath, hosts, opts); err != nil {
		return Result{}, err
	}
	if err = checkRef(result.Name, realm); err != nil {
		return Result{}, err
	}
	return result, nil
}

// authorOnly builds the identity of a host with no SSH key: an opaque Secret
// with the author and committer and nothing else.
func authorOnly(result Result, git settings, opts Options) (Result, error) {
	if result.Identity[connector.GitFieldAuthor] == "" {
		return Result{}, fmt.Errorf(
			"%w: no SSH private key under %s (name one with --key) and no git author "+
				"(set user.name and user.email in the global git configuration)",
			errdefs.ErrGitImport, filepath.Join(result.Home, ".ssh"))
	}
	result.Name = strings.TrimSpace(opts.Name)
	if result.Name == "" {
		result.Name = AuthorName
	}
	if err := checkRef(result.Name, result.Realm); err != nil {
		return Result{}, err
	}
	result.Type = v1beta1.SecretTypeOpaque
	result.Notes = append(result.Notes, fmt.Sprintf(
		"no SSH private key found under %s; storing an author-only identity: "+
			"cells get the author and committer but no credential and no signing.",
		filepath.Join(result.Home, ".ssh")))
	if len(git.sign) > 0 {
		result.Notes = append(result.Notes, "git signing was not imported: there is no SSH key to sign with.")
	}
	for _, ignored := range []struct {
		set  bool
		flag string
	}{
		{len(opts.Hosts) > 0, "--hosts"},
		{opts.PassphraseFile != "", "--passphrase-file"},
		{opts.KnownHostsFile != "", "--known-hosts"},
	} {
		if ignored.set {
			result.Notes = append(result.Notes, ignored.flag+" was ignored: an author-only identity has no key.")
		}
	}
	spec := v1beta1.SecretSpec{
		Type:      v1beta1.SecretTypeOpaque,
		Connector: connector.GitIdentityTag,
		Data:      connector.GitAuthorOnlyValue,
		Identity:  maps.Clone(result.Identity),
	}
	if err := connector.CheckGitAuthorOnly(spec); err != nil {
		return Result{}, fmt.Errorf("%w: %w", errdefs.ErrGitImport, err)
	}
	result.Secrets = []v1beta1.SecretDoc{secretDoc(result.Name, result.Realm, spec)}
	return result, nil
}

// importKey reads the key and builds the ssh-key identity: the hosts it
// authenticates, the author, and — when the operator signs with this key —
// signing and the allowed-signers file.
func importKey(result *Result, git settings, keyPath string, hosts []string, opts Options) error {
	result.Name = strings.TrimSpace(opts.Name)
	if result.Name == "" {
		result.Name = KeyName
	}
	result.Type, result.KeyPath = v1beta1.SecretTypeSSHKey, keyPath
	private, err := readKeyFile(keyPath, "private key")
	if err != nil {
		return err
	}
	if !strings.Contains(private, "PRIVATE KEY") {
		return fmt.Errorf("%w: %s is not a private key", errdefs.ErrGitImport, keyPath)
	}
	key := &v1beta1.SecretSSHKey{PrivateKey: private}
	if opts.PassphraseFile != "" {
		if key.Passphrase, err = readKeyFile(expandHome(opts.PassphraseFile, result.Home), "passphrase"); err != nil {
			return err
		}
		key.Passphrase = strings.TrimRight(key.Passphrase, "\r\n")
	}
	if gitcred.PrivateKeyEncrypted(private) && key.Passphrase == "" {
		return fmt.Errorf("%w: %s is passphrase-protected; pass --passphrase-file", errdefs.ErrGitImport, keyPath)
	}
	// The public key and certificate sit next to the private key by convention.
	if public, readErr := os.ReadFile(keyPath + ".pub"); readErr == nil {
		key.PublicKey = strings.TrimSpace(string(public))
	}
	if certificate, readErr := os.ReadFile(keyPath + "-cert.pub"); readErr == nil {
		key.Certificate = strings.TrimSpace(string(certificate))
	}
	if opts.KnownHostsFile != "" {
		if key.KnownHosts, err = readKeyFile(expandHome(opts.KnownHostsFile, result.Home), "known_hosts"); err != nil {
			return err
		}
	}
	result.Identity[connector.GitFieldHosts] = strings.Join(hosts, ",")

	// Signing follows only when the operator already signs, and signs with
	// the key being imported. A GPG signer, or one whose user.signingkey is a
	// different key, must not end up SSH-signing with this one: the commits
	// would show as unverified.
	sign := git.sign
	if len(sign) > 0 && !signsWith(git.signingKey, result.Home, keyPath, key.PublicKey) {
		result.Notes = append(result.Notes, fmt.Sprintf(
			"git signing was not imported: the configured signing key is not the imported SSH key %s "+
				"(a GPG key, or a different SSH key). Cells will not sign; to sign, rerun with --key "+
				"naming the signing key.",
			keyPath,
		))
		sign = nil
	}
	if len(sign) > 0 {
		result.Identity[connector.GitFieldSign] = strings.Join(sign, ",")
	}
	var signers string
	if git.allowedSigners != "" {
		path := expandHome(git.allowedSigners, result.Home)
		if signers, err = readKeyFile(path, "allowed signers file"); err != nil {
			return err
		}
		result.AllowedSignersPath = path
		result.Identity[connector.GitFieldAllowedSignersRef] = SignersName
	}
	if signers != "" {
		result.Secrets = append(result.Secrets, secretDoc(SignersName, result.Realm, v1beta1.SecretSpec{Data: signers}))
	}
	result.Secrets = append(result.Secrets, secretDoc(result.Name, result.Realm, v1beta1.SecretSpec{
		Type: v1beta1.SecretTypeSSHKey, Connector: connector.GitIdentityTag, SSHKey: key,
		Identity: maps.Clone(result.Identity),
	}))
	return nil
}

func secretDoc(name, realm string, spec v1beta1.SecretSpec) v1beta1.SecretDoc {
	return v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{Name: name, Realm: realm},
		Spec:     spec,
	}
}

// checkHosts reads the hosts option: github.com when empty, every entry a
// bare host or host/path prefix.
func checkHosts(in []string) ([]string, error) {
	var hosts []string
	for _, entry := range in {
		for _, host := range connector.ParseList(entry) {
			if strings.ContainsAny(host, " \t\"'\\@") || strings.Contains(host, "://") {
				return nil, fmt.Errorf(
					"%w: host %q: use a bare host or host/path prefix (no scheme or user)", errdefs.ErrGitImport, host)
			}
			if !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	if len(hosts) == 0 {
		hosts = []string{DefaultHost}
	}
	return hosts, nil
}

// checkRef validates the identity's name and realm as a Secret reference.
func checkRef(name, realm string) error {
	if _, err := connector.ParseSecretRef(name+"@"+realm, v1beta1.ContainerSecretRef{}); err != nil ||
		strings.HasPrefix(name, ".") {
		return fmt.Errorf("%w: %q in realm %q is not a valid secret name", errdefs.ErrGitImport, name, realm)
	}
	return nil
}

// keyPathFor chooses the key to import: the explicit one, else the key git
// signs with, else the first of id_ed25519, id_ecdsa, id_rsa. An explicit key
// is always "found": a missing one fails when it is read.
func keyPathFor(git settings, home, explicit string) (string, bool) {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return expandHome(explicit, home), true
	}
	var candidates []string
	// user.signingkey names the public half (or the key itself).
	if signing := expandHome(git.signingKey, home); filepath.IsAbs(signing) {
		candidates = append(candidates, strings.TrimSuffix(signing, ".pub"))
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		candidates = append(candidates, filepath.Join(home, ".ssh", name))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	return "", false
}

// signsWith reports whether the operator's user.signingkey names the SSH key
// being imported: its path (private or .pub), a file holding its public half,
// or the public key itself (git's literal and `key::` forms). A GPG key ID
// matches none of them.
func signsWith(signingKey, home, keyPath, publicKey string) bool {
	signingKey = strings.TrimSpace(signingKey)
	if signingKey == "" {
		return false
	}
	if samePublicKey(strings.TrimPrefix(signingKey, "key::"), publicKey) {
		return true
	}
	signingPath := expandHome(signingKey, home)
	if !filepath.IsAbs(signingPath) {
		return false
	}
	if filepath.Clean(strings.TrimSuffix(signingPath, ".pub")) == filepath.Clean(keyPath) {
		return true
	}
	content, err := os.ReadFile(signingPath)
	return err == nil && samePublicKey(string(content), publicKey)
}

// samePublicKey compares two OpenSSH public key lines by type and key blob,
// ignoring the trailing comment.
func samePublicKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	return len(fa) >= 2 && len(fb) >= 2 && fa[0] == fb[0] && fa[1] == fb[1]
}

func expandHome(path, home string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// readKeyFile reads operator key material. Errors name the path, never content.
func readKeyFile(path, what string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: cannot read %s %s; check its path and permissions", errdefs.ErrGitImport, what, path)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("%w: %s %s is empty", errdefs.ErrGitImport, what, path)
	}
	return string(raw), nil
}
