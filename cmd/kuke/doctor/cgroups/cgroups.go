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

// Package cgroups implements `kuke doctor cgroups`: a pre-flight that
// classifies the host root cgroup's controller availability against the
// set kukeon's cell-creation path will require, with actionable output
// for the contributor running `make dev-init`.
package cgroups

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cgroupcheck"
	"github.com/spf13/cobra"
)

// NewCgroupsCmd builds the `kuke doctor cgroups` subcommand.
func NewCgroupsCmd() *cobra.Command {
	var (
		root    string
		nested  bool
		probe   bool
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "cgroups",
		Short: "Verify host cgroup-v2 controller delegation before kuke init",
		Long: "Pre-flight that compares the host root cgroup's available + delegated\n" +
			"controllers against the set kukeon will enable on the kukeond\n" +
			"bootstrap cell. Distinguishes \"kernel does not support\" from \"parent\n" +
			"did not delegate\" so the remediation suggestion is always correct.\n\n" +
			"Exit code 0: every required controller is enabled (or was enabled by\n" +
			"the optional --probe write). Non-zero: at least one controller is\n" +
			"missing or the host root cgroup could not be read.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd.OutOrStdout(), cmd.ErrOrStderr(), root, nested, probe, verbose)
		},
		// A failed pre-flight is a host-environment problem, not a CLI
		// usage error — the structured remediation is the message we
		// want the operator to read; cobra's auto-printed Usage block
		// after an error would only bury it.
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&root, "root", cgroupcheck.DefaultHostRoot(),
		"path to the cgroup-v2 root (default: "+cgroupcheck.DefaultHostRoot()+")")
	cmd.Flags().BoolVar(&nested, "nested-cgroup-runtime", false,
		"check the controller set required when the kukeond cell opts into NestedCgroupRuntime")
	cmd.Flags().BoolVar(&probe, "probe", false,
		"attempt a +<ctrl> write to cgroup.subtree_control for missing controllers; "+
			"disambiguates the cgroup-namespace trap. Idempotent — leaves the host in a strictly better state on success.")
	cmd.Flags().BoolVar(&verbose, "verbose-status", false,
		"print per-controller status even when the pre-flight passes")

	return cmd
}

func runDoctor(stdout, stderr io.Writer, root string, nested, probe, verbose bool) error {
	required, err := resolveRequired(root, nested)
	if err != nil {
		return err
	}

	var prober cgroupcheck.Prober
	if probe {
		prober = cgroupcheck.DefaultProber
	}

	res, err := cgroupcheck.Check(root, required, prober)
	if err != nil {
		return fmt.Errorf("cgroup pre-flight: %w", err)
	}

	if res.OK() {
		// Silence on success keeps the dev-init.sh tail clean — only
		// surface details when --verbose-status is set.
		if verbose {
			printStatus(stdout, res)
		}
		return nil
	}

	printStatus(stderr, res)
	fmt.Fprint(stderr, "\n")
	fmt.Fprint(stderr, cgroupcheck.FormatRemediation(res))
	return fmt.Errorf("cgroup pre-flight: missing controllers at %s",
		filepath.Join(root, "cgroup.subtree_control"))
}

// resolveRequired returns the controller set the pre-flight must verify.
// nested=true reads the host-advertised set live, mirroring how
// EnableCellAllSubtreeControllers will behave at cell-creation time —
// this is the "no second source of truth" guarantee #324 calls for.
func resolveRequired(root string, nested bool) ([]string, error) {
	if !nested {
		return cgroupcheck.RequiredForKukeond(false, nil), nil
	}
	advertised, err := cgroupcheck.HostAdvertised(root)
	if err != nil {
		return nil, fmt.Errorf("cgroup pre-flight: %w", err)
	}
	return cgroupcheck.RequiredForKukeond(true, advertised), nil
}

func printStatus(w io.Writer, res cgroupcheck.Result) {
	fmt.Fprintf(w, "host root: %s\n", res.HostRoot)
	fmt.Fprintf(w, "required:  %s\n", joinControllers(res.Required))
	for _, c := range res.Required {
		fmt.Fprintf(w, "  %s: %s\n", c, res.Status[c])
	}
}

func joinControllers(in []string) string {
	if len(in) == 0 {
		return "(none)"
	}
	return strings.Join(in, ", ")
}
