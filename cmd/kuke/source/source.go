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

// Package source mounts `kuke source` (issue #1643): show reads a stored
// Source's repository. Storing, listing and deleting Sources use the generic
// verbs (kuke create source, kuke get sources, kuke delete source).
package source

import (
	"github.com/eminwux/kukeon/internal/team/cli"
	"github.com/spf13/cobra"
)

// NewSourceCmd builds `kuke source`.
func NewSourceCmd() *cobra.Command { return cli.NewSourceCommand() }
