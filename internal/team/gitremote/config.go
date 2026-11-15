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

package gitremote

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type transportConfigKey struct{}

type configEntry struct {
	key, value string
}

// WithRepositoryConfig captures transport settings where native Git would read
// them, including conditional includes and linked-worktree configuration. Only
// authentication/transport settings travel to temporary repositories; repository
// layout, hooks and other checkout-specific settings must stay behind. Values
// remain in memory and child environments, never in generated team declarations.
// Git config also works outside a repository, retaining global configuration for
// directory inputs with an explicit URL and revision.
func WithRepositoryConfig(ctx context.Context, dir string) (context.Context, error) {
	output, err := Run(ctx, "-C", dir, "config", "--includes", "--null", "--list")
	if err != nil {
		return nil, err
	}
	var entries []configEntry
	for _, entry := range strings.Split(string(output), "\x00") {
		key, value, hasValue := strings.Cut(entry, "\n")
		if !hasValue {
			value = "true" // Git treats a key without an equals sign as boolean true.
		}
		if transportKey(key) {
			entries = append(entries, configEntry{key, value})
		}
	}
	if err = resolveTransportPaths(ctx, dir, entries); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, transportConfigKey{}, entries), nil
}

// Git expands home/prefix notation for path-valued settings. Resolve any
// remaining relative paths before a later -C changes their working directory.
// Read each key's full value list to retain duplicate ordering and URL scopes.
func resolveTransportPaths(ctx context.Context, dir string, entries []configEntry) error {
	base, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	paths := map[string][]string{}
	for i, entry := range entries {
		if !transportPathKey(entry.key) {
			continue
		}
		values, ok := paths[entry.key]
		if !ok {
			output, runErr := Run(
				ctx,
				"-C",
				dir,
				"config",
				"--includes",
				"--null",
				"--type=path",
				"--get-all",
				entry.key,
			)
			if runErr != nil {
				return runErr
			}
			values = strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
		}
		value := values[0]
		paths[entry.key] = values[1:]
		if value != "" && !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		entries[i].value = value
	}
	return nil
}

func transportPathKey(key string) bool {
	if !strings.HasPrefix(key, "http.") {
		return false
	}
	// A URL subsection can itself contain dots; the variable is the last part.
	switch key[strings.LastIndexByte(key, '.')+1:] {
	case "sslcainfo", "sslcapath", "sslcert", "sslkey", "proxysslcainfo", "proxysslcert", "proxysslkey", "cookiefile":
		return true
	default:
		return false
	}
}

func transportKey(key string) bool {
	return key == "core.sshcommand" || key == "core.gitproxy" ||
		strings.HasPrefix(key, "credential.") || strings.HasPrefix(key, "http.") ||
		strings.HasPrefix(key, "url.") || strings.HasPrefix(key, "ssh.") ||
		strings.HasPrefix(key, "protocol.")
}

func transportEnv(ctx context.Context) []string {
	env := os.Environ()
	entries, ok := ctx.Value(transportConfigKey{}).([]configEntry)
	if !ok {
		return env
	}
	// Replay the effective configuration exactly once. Reading system/global
	// files again in the temporary directory would both lose conditional includes
	// and duplicate multi-valued helpers/headers. Existing command-scope settings
	// are already part of the snapshot, in Git's original precedence order.
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_PARAMETERS=", "GIT_CONFIG_COUNT="+strconv.Itoa(len(entries)))
	for i, entry := range entries {
		suffix := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+suffix+"="+entry.key,
			"GIT_CONFIG_VALUE_"+suffix+"="+entry.value)
	}
	return env
}
