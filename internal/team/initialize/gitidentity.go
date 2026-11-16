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

package initialize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const (
	// gitKeySecret holds the operator's imported SSH key.
	gitKeySecret = "git-ssh"
	// gitSignersSecret holds the operator's imported allowed-signers file.
	gitSignersSecret = "git-allowed-signers"
	importRealm      = "default"
)

// GitImport describes the operator's consent to copy their SSH key into a
// stored secret. Nothing is read from ~/.ssh unless Enabled is set.
type GitImport struct {
	Enabled bool
	// Key is the private key to import. Empty selects the key the global git
	// configuration signs with, else the first of ~/.ssh/id_ed25519,
	// id_ecdsa, id_rsa.
	Key string
	// PassphraseFile holds the key's passphrase, required for an encrypted key.
	PassphraseFile string
	// KnownHostsFile pins host keys for the imported credential. Empty relies
	// on the built-in keys of github.com, gitlab.com and bitbucket.org.
	KnownHostsFile string
}

// importGitIdentity reads the operator's key material and rewrites cfg's git
// block to name stored secrets instead of host paths. It returns the secrets
// to create; nothing is written here, and the key never appears in cfg.
func importGitIdentity(
	cfg *config.TeamsConfig, sources []config.Source, home string, imp GitImport,
) ([]api.SecretDoc, error) {
	if cfg.Spec.Git == nil {
		cfg.Spec.Git = &config.Git{}
	}
	git := cfg.Spec.Git
	keyPath, err := gitKeyPath(git, home, imp.Key)
	if err != nil {
		return nil, err
	}
	private, err := readKeyFile(keyPath, "private key")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(private, "PRIVATE KEY") {
		return nil, fmt.Errorf("import git identity: %s is not a private key", keyPath)
	}
	key := &api.SecretSSHKey{PrivateKey: private}
	if imp.PassphraseFile != "" {
		if key.Passphrase, err = readKeyFile(imp.PassphraseFile, "passphrase"); err != nil {
			return nil, err
		}
		key.Passphrase = strings.TrimRight(key.Passphrase, "\r\n")
	}
	if gitcred.PrivateKeyEncrypted(private) && key.Passphrase == "" {
		return nil, fmt.Errorf(
			"import git identity: %s is passphrase-protected; pass --git-key-passphrase-file", keyPath,
		)
	}
	// The public key and certificate sit next to the private key by convention.
	if public, readErr := os.ReadFile(keyPath + ".pub"); readErr == nil {
		key.PublicKey = strings.TrimSpace(string(public))
	}
	if certificate, readErr := os.ReadFile(keyPath + "-cert.pub"); readErr == nil {
		key.Certificate = strings.TrimSpace(string(certificate))
	}
	if imp.KnownHostsFile != "" {
		if key.KnownHosts, err = readKeyFile(imp.KnownHostsFile, "known_hosts"); err != nil {
			return nil, err
		}
	}
	keyRef := config.SecretRef{Name: gitKeySecret, Realm: importRealm}
	docs := []api.SecretDoc{{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
		Metadata: api.SecretMetadata{Name: keyRef.Name, Realm: keyRef.Realm},
		Spec:     api.SecretSpec{Type: api.SecretTypeSSHKey, SSHKey: key},
	}}

	// Bind the key to every host the team clones from.
	for _, host := range sourceHosts(sources) {
		i := slices.IndexFunc(git.Credentials, func(c config.GitCredential) bool { return c.Host == host })
		if i < 0 {
			git.Credentials = append(git.Credentials, config.GitCredential{Host: host, SecretRef: keyRef})
		}
	}
	// Signing follows only when the operator already signs.
	if len(git.Sign) > 0 && git.Signing == nil {
		git.Signing = &config.GitSigning{SecretRef: keyRef}
		if git.AllowedSigners != "" {
			signers, readErr := readKeyFile(expandHome(git.AllowedSigners, home), "allowed signers file")
			if readErr != nil {
				return nil, readErr
			}
			ref := config.SecretRef{Name: gitSignersSecret, Realm: importRealm}
			git.Signing.AllowedSignersRef = &ref
			docs = append(docs, api.SecretDoc{
				APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
				Metadata: api.SecretMetadata{Name: ref.Name, Realm: ref.Realm},
				Spec:     api.SecretSpec{Data: signers},
			})
		}
	}
	// The host paths are now stored secrets.
	git.SigningKey, git.AllowedSigners = "", ""
	return docs, nil
}

func gitKeyPath(git *config.Git, home, explicit string) (string, error) {
	if explicit != "" {
		return expandHome(explicit, home), nil
	}
	var candidates []string
	// user.signingkey names the public half (or the key itself).
	if signing := expandHome(git.SigningKey, home); filepath.IsAbs(signing) {
		candidates = append(candidates, strings.TrimSuffix(signing, ".pub"))
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		candidates = append(candidates, filepath.Join(home, ".ssh", name))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("import git identity: no SSH private key found under ~/.ssh; name one with --git-key")
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
		return "", fmt.Errorf("import git identity: cannot read %s %s; check its path and permissions", what, path)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("import git identity: %s %s is empty", what, path)
	}
	return string(raw), nil
}

// sourceHosts lists the hosts of the team's sources, github.com when none
// parses to one.
func sourceHosts(sources []config.Source) []string {
	var hosts []string
	for _, source := range sources {
		url := source.URL
		if strings.HasPrefix(url, "github.com/") {
			url = "https://" + url
		}
		host := strings.ToLower(gitcred.ParseRemote(url).Host)
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		hosts = []string{"github.com"}
	}
	return hosts
}
