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
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

const opensshMagic = "openssh-key-v1\x00"

// PrivateKeyEncrypted reports whether an SSH private key needs a passphrase.
func PrivateKeyEncrypted(privateKey string) bool { return privateKeyEncrypted(privateKey) }

// privateKeyEncrypted reports whether a private key needs a passphrase. The
// OpenSSH format names its cipher in the unencrypted header; the legacy PEM
// formats mark encryption in the armor.
func privateKeyEncrypted(privateKey string) bool {
	if body, ok := opensshBody(privateKey); ok {
		cipher, _, cipherOK := readSSHString(body[len(opensshMagic):])
		return cipherOK && string(cipher) != "none"
	}
	return strings.Contains(privateKey, "ENCRYPTED")
}

// derivePublicKey returns the public key line of an OpenSSH-format private key
// ("<type> <base64>"), or "" for any other format. The public half sits in the
// file's unencrypted header, so this works for passphrase-protected keys too
// and needs no key parsing.
func derivePublicKey(privateKey string) string {
	body, ok := opensshBody(privateKey)
	if !ok {
		return ""
	}
	rest := body[len(opensshMagic):]
	// ciphername, kdfname, kdfoptions
	for range 3 {
		if _, rest, ok = readSSHString(rest); !ok {
			return ""
		}
	}
	const countLen = 4
	if len(rest) < countLen || binary.BigEndian.Uint32(rest) < 1 {
		return ""
	}
	blob, _, ok := readSSHString(rest[countLen:])
	if !ok {
		return ""
	}
	keyType, _, ok := readSSHString(blob)
	if !ok || len(keyType) == 0 {
		return ""
	}
	return string(keyType) + " " + base64.StdEncoding.EncodeToString(blob)
}

// opensshBody decodes the base64 body of an "OPENSSH PRIVATE KEY" armor.
func opensshBody(privateKey string) ([]byte, bool) {
	const begin, end = "-----BEGIN OPENSSH PRIVATE" + " KEY-----", "-----END OPENSSH PRIVATE" + " KEY-----"
	start := strings.Index(privateKey, begin)
	stop := strings.Index(privateKey, end)
	if start < 0 || stop < start {
		return nil, false
	}
	encoded := strings.Join(strings.Fields(privateKey[start+len(begin):stop]), "")
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.HasPrefix(body, []byte(opensshMagic)) {
		return nil, false
	}
	return body, true
}

// readSSHString reads one length-prefixed SSH wire string.
func readSSHString(in []byte) ([]byte, []byte, bool) {
	const lengthLen = 4
	if len(in) < lengthLen {
		return nil, nil, false
	}
	n := int(binary.BigEndian.Uint32(in))
	if n < 0 || len(in)-lengthLen < n {
		return nil, nil, false
	}
	return in[lengthLen : lengthLen+n], in[lengthLen+n:], true
}
