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

package ctr

import (
	"errors"
	"strings"

	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// formatError recursively unwraps errors and formats the full error chain.
// Returns a string in the format "error1: error2: error3" showing all nested errors.
func formatError(err error) string {
	if err == nil {
		return "<nil>"
	}

	var parts []string
	current := err

	for current != nil {
		parts = append(parts, current.Error())
		current = errors.Unwrap(current)
	}

	// Join all error messages with ": " separator
	result := ""
	var resultSb61 strings.Builder
	for i, part := range parts {
		if i > 0 {
			resultSb61.WriteString(": ")
		}
		resultSb61.WriteString(part)
	}
	result += resultSb61.String()

	return result
}

// buildResolver creates a remotes.Resolver with optional credentials.
// If creds is empty, returns a default resolver without authentication (supports anonymous pulls).
// If creds are provided, returns a resolver with Docker authorizer configured to match credentials by host.
func buildResolver(creds []RegistryCredentials) remotes.Resolver {
	if len(creds) == 0 {
		// Return default resolver without authentication (anonymous pulls)
		return docker.NewResolver(docker.ResolverOptions{})
	}

	// Create resolver with credentials that match by host
	return docker.NewResolver(docker.ResolverOptions{
		Authorizer: docker.NewDockerAuthorizer(
			docker.WithAuthCreds(func(host string) (string, string, error) {
				// First, try to find exact match by ServerAddress
				for _, cred := range creds {
					if cred.ServerAddress != "" && host == cred.ServerAddress {
						return cred.Username, cred.Password, nil
					}
				}
				// If no exact match, try credentials with empty ServerAddress (default/fallback)
				for _, cred := range creds {
					if cred.ServerAddress == "" {
						return cred.Username, cred.Password, nil
					}
				}
				// No matching credentials found for this host
				return "", "", nil
			}),
		),
	})
}

// ConvertRealmCredentials converts modelhub RegistryCredentials slice to ctr RegistryCredentials slice.
func ConvertRealmCredentials(creds []intmodel.RegistryCredentials) []RegistryCredentials {
	if len(creds) == 0 {
		return nil
	}
	result := make([]RegistryCredentials, len(creds))
	for i, cred := range creds {
		result[i] = RegistryCredentials{
			Username:      cred.Username,
			Password:      cred.Password,
			ServerAddress: cred.ServerAddress,
		}
	}
	return result
}
