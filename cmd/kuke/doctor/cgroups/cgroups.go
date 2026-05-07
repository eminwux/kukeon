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
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cgroupcheck"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in
// tests of the `--scope realm|space|stack|cell` paths. Mirrors the same
// pattern used by `cmd/kuke/get/...` subcommands so test setup is uniform.
type MockControllerKey struct{}

// scope kinds accepted by the --scope flag.
const (
	scopeRealm = "realm"
	scopeSpace = "space"
	scopeStack = "stack"
	scopeCell  = "cell"
)

// NewCgroupsCmd builds the `kuke doctor cgroups` subcommand.
func NewCgroupsCmd() *cobra.Command {
	var (
		root    string
		nested  bool
		probe   bool
		noProbe bool
		verbose bool

		scope string
		realm string
		space string
		stack string
	)

	cmd := &cobra.Command{
		Use:   "cgroups [name]",
		Short: "Verify host cgroup-v2 controller delegation before kuke init",
		Long: "Pre-flight that compares the host root cgroup's available + delegated\n" +
			"controllers against the set kukeon will enable on the kukeond\n" +
			"bootstrap cell. Distinguishes \"kernel does not support\" from \"parent\n" +
			"did not delegate\" so the remediation suggestion is always correct.\n\n" +
			"With --scope realm|space|stack|cell <name>, resolves the named\n" +
			"object's Status.CgroupPath via the daemon and runs the same check\n" +
			"against that directory instead of the host root — useful for\n" +
			"diagnosing mid-tree delegation gaps when a workload below a given\n" +
			"level fails to start.\n\n" +
			"By default, the pre-flight probes any controller missing from\n" +
			"cgroup.subtree_control with a +<ctrl> write so the cgroup-namespace\n" +
			"trap (advertised but not delegated, write returns EOPNOTSUPP) is\n" +
			"distinguished from \"merely needs the operator to enable it\". The\n" +
			"probe is idempotent on healthy hosts and harmless on trapped ones;\n" +
			"pass --no-probe to keep the pre-flight strictly read-only.\n\n" +
			"Exit code 0: every required controller is enabled (or was enabled by\n" +
			"the probe write). Non-zero: at least one controller is missing or the\n" +
			"cgroup directory could not be read.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := scopedTarget{
				kind:  strings.TrimSpace(scope),
				realm: strings.TrimSpace(realm),
				space: strings.TrimSpace(space),
				stack: strings.TrimSpace(stack),
			}
			if len(args) > 0 {
				target.name = strings.TrimSpace(args[0])
			}
			// --no-probe always wins over --probe so an explicit opt-out
			// is unambiguous regardless of flag ordering.
			effectiveProbe := probe && !noProbe
			return runDoctor(cmd, root, nested, effectiveProbe, verbose, target)
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
	cmd.Flags().BoolVar(&probe, "probe", true,
		"attempt a +<ctrl> write to cgroup.subtree_control for missing controllers; "+
			"disambiguates the cgroup-namespace trap (default true). Idempotent — leaves the host in a "+
			"strictly better state on success. Pass --no-probe to keep the pre-flight read-only.")
	cmd.Flags().BoolVar(&noProbe, "no-probe", false,
		"opt out of the +<ctrl> probe write; the pre-flight stays strictly read-only and "+
			"missing-but-advertised controllers are reported as needs-delegation without "+
			"distinguishing the cgroup-namespace trap. Wins over --probe when both are set.")
	cmd.Flags().BoolVar(&verbose, "verbose-status", false,
		"print per-controller status even when the pre-flight passes")
	cmd.Flags().StringVar(&scope, "scope", "",
		"verify a sub-tree instead of the host root: 'realm', 'space', 'stack', or 'cell'. "+
			"When set, resolves the named object's Status.CgroupPath via the daemon and "+
			"runs the controller-set check against that directory. "+
			"Empty (default) keeps the unchanged host-root behavior.")
	cmd.Flags().StringVar(&realm, "realm", "",
		"realm name (required for --scope space|stack|cell)")
	cmd.Flags().StringVar(&space, "space", "",
		"space name (required for --scope stack|cell)")
	cmd.Flags().StringVar(&stack, "stack", "",
		"stack name (required for --scope cell)")

	return cmd
}

func runDoctor(cmd *cobra.Command, root string, nested, probe, verbose bool, target scopedTarget) error {
	if target.kind == "" {
		// Without --scope, behavior is unchanged from PR #325.
		if target.name != "" {
			return fmt.Errorf("positional name %q is only valid with --scope realm|space|stack|cell", target.name)
		}
		return runCheck(cmd.OutOrStdout(), cmd.ErrOrStderr(), root, nested, probe, verbose, "")
	}

	if err := target.validate(); err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	cgroupPath, err := target.resolveCgroupPath(cmd.Context(), client)
	if err != nil {
		return err
	}

	// Status.CgroupPath is a relative path under the cgroup-v2 mountpoint
	// (e.g., "/kukeon/default") — join with --root to get the on-disk
	// path that cgroupcheck.Check reads cgroup.controllers /
	// cgroup.subtree_control from.
	scopedRoot := filepath.Join(root, cgroupPath)
	return runCheck(cmd.OutOrStdout(), cmd.ErrOrStderr(), scopedRoot, nested, probe, verbose, target.label())
}

// runCheck is the shared pre-flight body used by both the host-root and
// --scope paths. scopeLabel is empty for the host-root path; non-empty
// labels are printed as a "scope: ..." header so operators can tell which
// directory was checked when the failure output looks otherwise identical
// to the unscoped form.
func runCheck(stdout, stderr io.Writer, root string, nested, probe, verbose bool, scopeLabel string) error {
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
			printScope(stdout, scopeLabel)
			printStatus(stdout, res)
		}
		return nil
	}

	printScope(stderr, scopeLabel)
	printStatus(stderr, res)
	fmt.Fprint(stderr, "\n")
	fmt.Fprint(stderr, cgroupcheck.FormatRemediation(res))
	return fmt.Errorf("cgroup pre-flight: missing controllers at %s",
		filepath.Join(root, "cgroup.subtree_control"))
}

// resolveRequired returns the controller set the pre-flight must verify.
// nested=true reads the host-advertised set live, mirroring how
// EnableCellAllSubtreeControllers will behave at cell-creation time —
// this is the "no second source of truth" guarantee #324 calls for. When
// the caller is scoped, root is the scoped cgroup directory and the read
// returns "what this scope advertises", which is exactly the set the
// caller's children would inherit.
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

func printScope(w io.Writer, label string) {
	if label == "" {
		return
	}
	fmt.Fprintf(w, "scope: %s\n", label)
}

func joinControllers(in []string) string {
	if len(in) == 0 {
		return "(none)"
	}
	return strings.Join(in, ", ")
}

// scopedTarget identifies the resource whose cgroup is being inspected
// when --scope is set. Empty kind means "no scope; check the host root".
type scopedTarget struct {
	kind  string
	name  string
	realm string
	space string
	stack string
}

// validate ensures the parent-context flags appropriate for the scope kind
// are populated. Mirrors the same parent-flag requirements `kuke get
// space|stack|cell` enforce so operators do not have to learn a second
// addressing convention for the doctor.
func (t scopedTarget) validate() error {
	if t.name == "" {
		return fmt.Errorf("--scope %s requires a positional <name> argument", t.kind)
	}
	switch t.kind {
	case scopeRealm:
		return nil
	case scopeSpace:
		if t.realm == "" {
			return errors.New("--scope space requires --realm")
		}
		return nil
	case scopeStack:
		if t.realm == "" {
			return errors.New("--scope stack requires --realm")
		}
		if t.space == "" {
			return errors.New("--scope stack requires --space")
		}
		return nil
	case scopeCell:
		if t.realm == "" {
			return errors.New("--scope cell requires --realm")
		}
		if t.space == "" {
			return errors.New("--scope cell requires --space")
		}
		if t.stack == "" {
			return errors.New("--scope cell requires --stack")
		}
		return nil
	default:
		return fmt.Errorf("--scope must be one of: realm, space, stack, cell (got %q)", t.kind)
	}
}

// resolveCgroupPath calls the appropriate kukeonv1 Get method and returns
// the Status.CgroupPath of the named resource. Returns an error when the
// daemon does not know the resource or has not yet populated its cgroup —
// in either case the caller cannot run the scoped pre-flight.
func (t scopedTarget) resolveCgroupPath(ctx context.Context, c kukeonv1.Client) (string, error) {
	switch t.kind {
	case scopeRealm:
		res, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: t.name}})
		if err != nil {
			return "", err
		}
		if !res.MetadataExists {
			return "", fmt.Errorf("realm %q not found", t.name)
		}
		if res.Realm.Status.CgroupPath == "" {
			return "", fmt.Errorf("realm %q has no Status.CgroupPath yet", t.name)
		}
		return res.Realm.Status.CgroupPath, nil
	case scopeSpace:
		res, err := c.GetSpace(ctx, v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{Name: t.name},
			Spec:     v1beta1.SpaceSpec{RealmID: t.realm},
		})
		if err != nil {
			return "", err
		}
		if !res.MetadataExists {
			return "", fmt.Errorf("space %q not found in realm %q", t.name, t.realm)
		}
		if res.Space.Status.CgroupPath == "" {
			return "", fmt.Errorf("space %q has no Status.CgroupPath yet", t.name)
		}
		return res.Space.Status.CgroupPath, nil
	case scopeStack:
		res, err := c.GetStack(ctx, v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: t.name},
			Spec:     v1beta1.StackSpec{RealmID: t.realm, SpaceID: t.space},
		})
		if err != nil {
			return "", err
		}
		if !res.MetadataExists {
			return "", fmt.Errorf("stack %q not found in realm %q, space %q", t.name, t.realm, t.space)
		}
		if res.Stack.Status.CgroupPath == "" {
			return "", fmt.Errorf("stack %q has no Status.CgroupPath yet", t.name)
		}
		return res.Stack.Status.CgroupPath, nil
	case scopeCell:
		res, err := c.GetCell(ctx, v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: t.name},
			Spec: v1beta1.CellSpec{
				RealmID: t.realm,
				SpaceID: t.space,
				StackID: t.stack,
			},
		})
		if err != nil {
			return "", err
		}
		if !res.MetadataExists {
			return "", fmt.Errorf("cell %q not found in stack %q/%q/%q", t.name, t.realm, t.space, t.stack)
		}
		if res.Cell.Status.CgroupPath == "" {
			return "", fmt.Errorf("cell %q has no Status.CgroupPath yet", t.name)
		}
		return res.Cell.Status.CgroupPath, nil
	}
	return "", fmt.Errorf("--scope %q not supported", t.kind)
}

// label is the human-readable scope identifier surfaced as the new
// "scope:" header line so operators can tell which sub-tree was checked.
func (t scopedTarget) label() string {
	switch t.kind {
	case scopeRealm:
		return fmt.Sprintf("realm %q", t.name)
	case scopeSpace:
		return fmt.Sprintf("space %q in realm %q", t.name, t.realm)
	case scopeStack:
		return fmt.Sprintf("stack %q in realm %q, space %q", t.name, t.realm, t.space)
	case scopeCell:
		return fmt.Sprintf("cell %q in stack %q/%q/%q", t.name, t.realm, t.space, t.stack)
	default:
		return ""
	}
}

// resolveClient honors the same MockControllerKey injection convention
// used by `cmd/kuke/get/...`, then falls through to ClientFromCmd which
// picks --no-daemon vs RPC dial based on flags.
func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mock, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mock, nil
	}
	return shared.ClientFromCmd(cmd)
}
