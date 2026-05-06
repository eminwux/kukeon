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

// Package doctor hosts `kuke doctor`, a parent command for host-level
// pre-flight checks invoked before `kuke init` to surface environmental
// problems that would otherwise be diagnosed only via cryptic mid-bootstrap
// failures (e.g., missing cgroup-v2 controller delegation).
package doctor

import (
	cgroupscmd "github.com/eminwux/kukeon/cmd/kuke/doctor/cgroups"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds the `kuke doctor` parent command.
func NewDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run host pre-flight checks before kuke init",
		Long: "Run host-level pre-flight checks before `kuke init`.\n\n" +
			"These checks read the host environment (cgroup hierarchy, controller\n" +
			"delegation, ...) and fail fast with an actionable remediation when\n" +
			"the host would otherwise produce a cryptic mid-bootstrap error.",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(cgroupscmd.NewCgroupsCmd())
	return cmd
}
