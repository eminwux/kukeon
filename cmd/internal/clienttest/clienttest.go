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

// Package clienttest provides test helpers for suites that run the real
// `kuke` root PersistentPreRunE, which reads (and on first run writes) the
// client configuration.
package clienttest

import (
	"os"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
)

// IsolateHome points HOME at an empty temporary directory and unsets
// KUKE_CONFIGURATION for the rest of the test, so the client configuration
// the root command resolves is a fresh default under that directory rather
// than the developer's ~/.kuke/kuke.yaml. Without it the test reads whatever
// file the host has there and creates it when absent. Call it before
// building the root command: the --configuration flag default is computed
// from HOME at construction time. It returns the temporary HOME.
func IsolateHome(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// t.Setenv records the prior value so it is restored at cleanup; the
	// Unsetenv that follows keeps the variable absent during the test.
	key := config.KUKE_CONFIGURATION.EnvVar()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	return home
}
