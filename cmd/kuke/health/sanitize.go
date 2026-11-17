// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"regexp"
	"strings"
)

// redacted replaces anything Sanitize removes.
const redacted = "[redacted]"

// secretShapes match text that is a credential by its form. They run before
// the key=value rule so a known token is redacted wherever it appears.
var secretShapes = []*regexp.Regexp{
	// PEM private keys, whole block.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
	// GitHub tokens: classic and fine-grained.
	regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	// Provider API keys: Anthropic/OpenAI/OpenRouter style, Slack, AWS, Google.
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{30,}\b`),
	// JSON web tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	// Authorization header values.
	regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{16,}`),
}

// urlUserinfo matches the credentials of a URL: scheme://user:password@host.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@:]+(:[^\s/@]*)?@`)

// keyedSecret matches key=value and key: value pairs whose key names a
// credential, keeping the key and redacting the value.
var keyedSecret = regexp.MustCompile(
	`(?i)\b([A-Za-z0-9_.-]*(password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|authorization|credential)[A-Za-z0-9_.-]*)` +
		`(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)

// Sanitize removes secret values and credential-bearing URLs from text that
// is about to leave the host in a filed issue. extra lists literal values to
// redact as well, such as the token the filing itself uses.
func Sanitize(text string, extra ...string) string {
	out := text
	for _, literal := range extra {
		if len(literal) >= 8 {
			out = strings.ReplaceAll(out, literal, redacted)
		}
	}
	for _, shape := range secretShapes {
		out = shape.ReplaceAllString(out, redacted)
	}
	out = urlUserinfo.ReplaceAllString(out, "${1}"+redacted+"@")
	out = keyedSecret.ReplaceAllString(out, "${1}${3}"+redacted)
	return out
}
