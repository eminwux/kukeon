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

// Package buildinfo exposes the kukeon build version to internal packages
// that cannot import cmd/config (which itself imports the controller).
// cmd/config registers the ldflags-injected version at init.
package buildinfo

import "sync/atomic"

var version atomic.Value

// SetVersion records the build version. Called from cmd/config's init.
func SetVersion(v string) { version.Store(v) }

// Version returns the registered build version, or "" when none was set.
func Version() string {
	v, _ := version.Load().(string)
	return v
}
