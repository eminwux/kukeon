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

// Package teambuild orchestrates the local `kuke team init --build` path: it
// walks the FROM directives of the catalog entries a project selected, derives
// the transitive base-before-leaves build order (the bases base/base-user/
// base-root are intermediates that live in the agents source tree, not in
// images.yaml), and invokes the standalone kukebuild binary once per image
// into the target realm's containerd namespace. Each image is tagged
// `kukeon.internal/<name>:<version>` and the `REGISTRY=kukeon.internal`
// build-arg is threaded so leaf FROMs of the form `${REGISTRY}/base-user:latest`
// resolve to the just-built in-realm base rather than a real registry pull.
//
// The kukebuild invocation is deliberately distinct from cmd/kuke/build's
// `kuke build` CLI shim: that shim replaces the process via syscall.Exec so
// kukebuild's exit code reaches the operator unmediated. The orchestrator
// here must build N images and then return to the caller (which proceeds to
// render and apply), so it spawns kukebuild with os/exec and waits for each
// to complete — exec-and-return, not exec-and-replace.
//
// Step 2 of the build-and-supply epic (#1063, this issue #1064): the
// build-invoke half. The bind decision (binding the locally-built
// `kukeon.internal/<name>:<version>` refs into the CellBlueprints instead of
// the published `entry.Image`) plus the runtime no-pull path for
// `kukeon.internal/...` refs are step 3 (#1068) and depend on this step's
// in-realm images.
package teambuild

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// InternalRegistry is the reserved, non-routable "host" every locally-built
// kuke image is tagged under. It is ICANN's `.internal` private-use TLD —
// pulling from it can never accidentally hit a real registry. Leaf Dockerfiles
// `FROM ${REGISTRY}/base-user:latest` are threaded with this value via
// `--build-arg REGISTRY=kukeon.internal` so the FROM resolves to the in-realm
// base just built, rather than the agents source's published default.
//
// Defined as a single source of truth in internal/consts so the build path
// here, the bind path (internal/teamrender), and the runtime local-only
// resolver (internal/ctr) cannot drift on the reserved host.
const InternalRegistry = consts.InternalImageRegistry

// baseImageDefaultTag is the tag a base image is built with when its leaf's
// FROM line did not supply one. The agents convention is `:latest` for the
// non-versioned bases (base/base-user/base-root), so an absent FROM tag falls
// through to this default.
const baseImageDefaultTag = "latest"

// kukebuildBinary is the binary name resolved on PATH. Matches cmd/kuke/build's
// constant.
const kukebuildBinary = "kukebuild"

// harnessesDir is the conventional subdir of a cloned agents source under
// which both leaf catalog entries' build.context paths and the intermediate
// bases live (e.g. `harnesses/base-user/Dockerfile`). Surfaced as a package
// constant so the FROM-walk's base lookup and any future relocation stay in
// one place.
const harnessesDir = "harnesses"

// lookPath / runStep are indirected as package vars so tests can drive the
// PATH-lookup and the kukebuild invocation without a real binary on disk. The
// production paths resolve kukebuild via exec.LookPath and shell it out via
// os/exec — exec-and-return, never replacing the parent process (that
// behaviour is reserved for the `kuke build` CLI shim).
//
//nolint:gochecknoglobals // test seams for the production PATH lookup + exec
var (
	lookPath = exec.LookPath
	runStep  = realRunStep
)

// Step is one image kukebuild builds: the resolved on-disk inputs (context
// dir + Dockerfile path), the tag the result lands under in the realm
// namespace, and the build-arg map passed to the Dockerfile frontend. Steps
// flow base-before-leaves through Plan.
type Step struct {
	// Name is the image's logical identifier — the catalog entry's ref for a
	// leaf, or the base directory name (e.g. `base-user`) for an intermediate.
	// Surfaces in progress logs and the resolved Tag's repo component.
	Name string
	// Version is the tag suffix (e.g. `v1.4.0`, `latest`). For leaves it is
	// the agents source's pinned ref so step 3's bind decision carries a
	// versioned ref into the CellBlueprint; for bases it mirrors the literal
	// tag the leaves' FROM line referenced (typically `latest`).
	Version string
	// Tag is the full image reference the build is tagged with —
	// `kukeon.internal/<Name>:<Version>` — passed to kukebuild's `--tag` flag.
	Tag string
	// Context is the absolute on-disk path to the build context root passed
	// as kukebuild's positional argument.
	Context string
	// Dockerfile is the absolute on-disk path to the Dockerfile. Passed to
	// kukebuild's `--file` flag when set.
	Dockerfile string
	// BuildArgs is the `--build-arg KEY=VALUE` set forwarded verbatim. Plan
	// always seeds `REGISTRY=kukeon.internal` so leaf FROMs of the form
	// `${REGISTRY}/base-user:latest` resolve to the in-realm base.
	BuildArgs map[string]string
	// IsLeaf records whether this step is a catalog leaf (vs. a transitive
	// base intermediate). Diagnostic only — the build invocation is identical.
	IsLeaf bool
}

// Plan resolves the build set for a project's selected catalog entries. For
// each leaf entry the planner reads its build.context + build.dockerfile,
// parses the Dockerfile's FROM directives, substitutes ${REGISTRY} with the
// kukeon.internal host, and follows any FROM that lands under
// `kukeon.internal/<name>:<tag>` as a transitive in-repo base
// (`<cacheDir>/harnesses/<name>/Dockerfile`). The walk is iterative and
// dedupes by image name so a base referenced by N leaves builds once. The
// returned slice is topologically ordered base-before-leaves so a kukebuild
// loop building it in order satisfies every FROM by the time the leaf builds.
//
// sourceRef is the agents source's pinned ref (tag/branch/commit value) used
// as the leaf images' `<version>` tag suffix. cacheDir is the materialized
// agents source root teamsource produced.
//
// External FROMs (anything not under kukeon.internal — debian:bookworm,
// docker.io/library/ubuntu, etc.) are pulled by kukebuild from their real
// registries at build time; the planner records them as inputs but does not
// produce a Step for them.
func Plan(
	cacheDir, sourceRef string,
	leaves []*model.ImageCatalogEntry,
) ([]Step, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return nil, errors.New("teambuild.Plan: cacheDir is required")
	}
	if strings.TrimSpace(sourceRef) == "" {
		return nil, errors.New("teambuild.Plan: sourceRef is required")
	}

	// Iterative BFS over the FROM graph. `nodes` carries the discovered build
	// targets keyed by Step.Name (so a base referenced by multiple leaves
	// dedupes); `deps` carries the parent→child edges used for topo-sort.
	nodes := map[string]*Step{}
	deps := map[string]map[string]struct{}{}

	addEdge := func(parent, child string) {
		if deps[parent] == nil {
			deps[parent] = map[string]struct{}{}
		}
		deps[parent][child] = struct{}{}
	}

	// Seed with the leaves. Each leaf's Step uses entry.Ref for Name and
	// sourceRef for Version per AC 3 — the leaf gets a versioned tag the
	// step-3 bind decision can rely on.
	queue := make([]string, 0, len(leaves))
	for _, e := range leaves {
		if e == nil {
			continue
		}
		ref := strings.TrimSpace(e.Ref)
		if ref == "" {
			return nil, fmt.Errorf("%w: catalog entry missing ref", errdefs.ErrTeamImageRefRequired)
		}
		if _, dup := nodes[ref]; dup {
			continue
		}
		ctxDir, dockerfile, err := resolveLeafPaths(cacheDir, e)
		if err != nil {
			return nil, err
		}
		nodes[ref] = &Step{
			Name:       ref,
			Version:    sourceRef,
			Tag:        formatTag(ref, sourceRef),
			Context:    ctxDir,
			Dockerfile: dockerfile,
			BuildArgs:  defaultBuildArgs(),
			IsLeaf:     true,
		}
		queue = append(queue, ref)
	}

	// Walk each known step's FROM directives. A FROM that lands under
	// kukeon.internal is an in-repo dep — look it up at
	// `harnesses/<name>/Dockerfile`, record an edge, and enqueue if new.
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		step := nodes[name]

		froms, err := readFromRefs(step.Dockerfile)
		if err != nil {
			return nil, err
		}
		for _, f := range froms {
			child, childTag, internal := resolveInternalDep(f)
			if !internal {
				// External base — kukebuild pulls it from its real registry
				// during the leaf build. No step needed.
				continue
			}
			addEdge(name, child)
			if _, seen := nodes[child]; seen {
				continue
			}
			baseCtx := filepath.Join(cacheDir, harnessesDir, child)
			baseDockerfile := filepath.Join(baseCtx, "Dockerfile")
			if _, statErr := os.Stat(baseDockerfile); statErr != nil {
				return nil, fmt.Errorf(
					"%w: %q references in-repo base %q but %q is missing: %w",
					errdefs.ErrTeamBuildBaseMissing, step.Dockerfile, child, baseDockerfile, statErr,
				)
			}
			nodes[child] = &Step{
				Name:       child,
				Version:    childTag,
				Tag:        formatTag(child, childTag),
				Context:    baseCtx,
				Dockerfile: baseDockerfile,
				BuildArgs:  defaultBuildArgs(),
				IsLeaf:     false,
			}
			queue = append(queue, child)
		}
	}

	return topoSort(nodes, deps)
}

// resolveLeafPaths resolves a catalog entry's build.context + build.dockerfile
// to absolute paths under cacheDir. A missing context or Dockerfile is a hard
// error: the leaf cannot build and the operator should know which agents-tree
// path is broken.
func resolveLeafPaths(cacheDir string, e *model.ImageCatalogEntry) (string, string, error) {
	ctxRel := strings.TrimSpace(e.Build.Context)
	dfRel := strings.TrimSpace(e.Build.Dockerfile)
	if ctxRel == "" || dfRel == "" {
		return "", "", fmt.Errorf(
			"%w: catalog entry %q missing build.context or build.dockerfile",
			errdefs.ErrTeamBuildContextMissing, e.Ref,
		)
	}
	ctxDir := filepath.Join(cacheDir, ctxRel)
	if _, err := os.Stat(ctxDir); err != nil {
		return "", "", fmt.Errorf(
			"%w: %q: %w", errdefs.ErrTeamBuildContextMissing, ctxDir, err,
		)
	}
	dockerfile := filepath.Join(ctxDir, dfRel)
	if _, err := os.Stat(dockerfile); err != nil {
		return "", "", fmt.Errorf(
			"%w: %q: %w", errdefs.ErrTeamBuildContextMissing, dockerfile, err,
		)
	}
	return ctxDir, dockerfile, nil
}

// fromPattern matches a Dockerfile `FROM` directive: optional `--platform=…`,
// the image reference, optional `AS <stage>`. Case-insensitive on `FROM`/`AS`.
var fromPattern = regexp.MustCompile(`(?i)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+\S+)?\s*$`)

// readFromRefs parses path's FROM directives. The reader is intentionally
// minimal — no support for line continuations (`\` at end of line) since the
// agents harness Dockerfiles do not split FROMs that way, and no parsing of
// the rest of the Dockerfile. Each FROM ref is returned in source order.
func readFromRefs(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // path was resolved + stat-checked by Plan
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var refs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Strip leading whitespace then a comment so `# FROM …` is ignored.
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := fromPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		refs = append(refs, m[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Dockerfile %q: %w", path, err)
	}
	return refs, nil
}

// registryVarPattern matches `${REGISTRY}` or `$REGISTRY` in a Dockerfile
// FROM ref. Only the REGISTRY var is substituted — other build-args are
// passed through to kukebuild verbatim and resolved at build time.
var registryVarPattern = regexp.MustCompile(`\$\{REGISTRY\}|\$REGISTRY`)

// resolveInternalDep classifies a Dockerfile FROM ref. The REGISTRY var is
// substituted with kukeon.internal so a `FROM ${REGISTRY}/base-user:latest`
// becomes `kukeon.internal/base-user:latest`. If the substituted ref starts
// with kukeon.internal/, the second return is the base's logical name and
// the third return is true; the dep is an in-repo intermediate. Otherwise
// the FROM is external (debian:bookworm, docker.io/...) and is not built.
func resolveInternalDep(rawFrom string) (name, tag string, internal bool) {
	resolved := registryVarPattern.ReplaceAllString(rawFrom, InternalRegistry)
	if !strings.HasPrefix(resolved, InternalRegistry+"/") {
		return "", "", false
	}
	repoTag := strings.TrimPrefix(resolved, InternalRegistry+"/")
	repo, t, hasTag := strings.Cut(repoTag, ":")
	if !hasTag {
		return repo, baseImageDefaultTag, true
	}
	if strings.TrimSpace(t) == "" {
		return repo, baseImageDefaultTag, true
	}
	return repo, t, true
}

// formatTag composes the full image reference for a build step. Delegates to
// consts.InternalImageRef so the tag a build lands under is byte-identical to
// the ref the bind path (internal/teamrender) binds into the CellBlueprint.
func formatTag(name, version string) string {
	return consts.InternalImageRef(name, version)
}

// defaultBuildArgs returns the build-arg map every step is seeded with — the
// REGISTRY=kukeon.internal threading per AC 3. Returned fresh each call so a
// caller-side mutation cannot leak into a sibling step's args.
func defaultBuildArgs() map[string]string {
	return map[string]string{"REGISTRY": InternalRegistry}
}

// topoSort produces a base-before-leaves ordering of nodes given the
// parent→child edges in deps. A child must build before any parent that
// FROMs it. Ties break by Name so the output is byte-stable across runs.
//
// In-degree-zero seeds are the bases nothing depends on; the walk emits each
// then removes its outgoing edges, exposing the next layer. A cycle (a
// Dockerfile cycle in the agents source) surfaces as
// ErrTeamBuildCycle naming the residual edges.
func topoSort(nodes map[string]*Step, deps map[string]map[string]struct{}) ([]Step, error) {
	// Reverse the parent→child edges into child→parents so we can compute
	// in-degree on the build-order graph (a parent depends on its children;
	// children must build first).
	indeg := make(map[string]int, len(nodes))
	for name := range nodes {
		indeg[name] = 0
	}
	for parent := range deps {
		for child := range deps[parent] {
			// child is a build-prereq of parent, so parent's in-degree on
			// the build graph (edges child→parent) is +1.
			indeg[parent]++
			_ = child
		}
	}

	// Ready set = nodes with no unresolved prereq.
	ready := make([]string, 0)
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	out := make([]Step, 0, len(nodes))
	for len(ready) > 0 {
		// Pop the lexicographically smallest ready node for byte-stable output.
		name := ready[0]
		ready = ready[1:]
		out = append(out, *nodes[name])

		// Remove the just-emitted node from every parent's deps set;
		// parents whose deps drop to zero become ready.
		for parent, children := range deps {
			if _, ok := children[name]; !ok {
				continue
			}
			delete(deps[parent], name)
			indeg[parent]--
			if indeg[parent] == 0 {
				ready = append(ready, parent)
			}
		}
		sort.Strings(ready)
	}

	if len(out) != len(nodes) {
		var residual []string
		for name, d := range indeg {
			if d != 0 {
				residual = append(residual, name)
			}
		}
		sort.Strings(residual)
		return nil, fmt.Errorf(
			"%w: residual %v", errdefs.ErrTeamBuildCycle, residual,
		)
	}
	return out, nil
}

// Run invokes kukebuild for a single step. It looks the binary up on PATH,
// composes the argv, and spawns it via os/exec with the parent's environment
// — exec-and-return semantics so the team-init lifecycle can continue after
// each build. Stdout/stderr are forwarded to the supplied writers (typically
// the operator's terminal) so kukebuild's progress is visible inline.
func Run(ctx context.Context, step Step, realm string, stdout, stderr io.Writer) error {
	binPath, err := lookPath(kukebuildBinary)
	if err != nil {
		return fmt.Errorf(
			"%w: `kuke team init --build` shells out to `%s`, which was not found on PATH — "+
				"install it (e.g. `make kukebuild` then put it on PATH)",
			errdefs.ErrKukebuildNotFound, kukebuildBinary,
		)
	}
	argv := BuildArgv(step, realm)
	return runStep(ctx, binPath, argv, stdout, stderr)
}

// BuildArgv composes the kukebuild argv for a step. argv[0] is the binary
// name by exec convention. The forwarded flags match cmd/kuke/build's
// buildArgv shape — `--tag`, `--realm`, `--file`, repeated `--build-arg`s,
// and the positional context — so the kukebuild surface stays single.
// build-arg keys are emitted in sorted order so the argv is byte-stable
// across runs (golang map iteration is randomized).
func BuildArgv(step Step, realm string) []string {
	argv := []string{
		kukebuildBinary,
		"--tag", step.Tag,
		"--realm", realm,
	}
	if strings.TrimSpace(step.Dockerfile) != "" {
		argv = append(argv, "--file", step.Dockerfile)
	}
	keys := make([]string, 0, len(step.BuildArgs))
	for k := range step.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		argv = append(argv, "--build-arg", k+"="+step.BuildArgs[k])
	}
	argv = append(argv, step.Context)
	return argv
}

// realRunStep is the production runStep — spawn kukebuild via os/exec and
// wait for it to complete. The exit code and stderr text from kukebuild
// surface as the wrapped error.
func realRunStep(ctx context.Context, binPath string, argv []string, stdout, stderr io.Writer) error {
	// argv[0] is the binary name (exec convention); pass argv[1:] as the real
	// args to exec.CommandContext, which fills argv[0] from binPath itself.
	cmd := exec.CommandContext(ctx, binPath, argv[1:]...) //nolint:gosec // binPath is from lookPath; argv is built by BuildArgv
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kukebuild %s: %w", strings.Join(argv[1:], " "), err)
	}
	return nil
}

// BuildAll plans the build set and runs each step in order, halting on the
// first failure. The caller hands the bundle's CacheDir, the source's
// pinned ref, the target realm, the selected catalog entries, and stdout/
// stderr writers; BuildAll handles the FROM-walk, per-step kukebuild
// invocation, and progress messaging. progressW receives one line per step
// announcing what is being built before the kukebuild progress stream
// begins, so an interrupted multi-image build is debuggable from the log.
//
// An empty leaves slice is a no-op — a roster with no harnesses selects no
// images, so there is nothing to build.
func BuildAll(
	ctx context.Context,
	cacheDir, sourceRef, realm string,
	leaves []*model.ImageCatalogEntry,
	progressW, stdout, stderr io.Writer,
) error {
	if len(leaves) == 0 {
		return nil
	}
	steps, err := Plan(cacheDir, sourceRef, leaves)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if progressW != nil {
			kind := "base"
			if step.IsLeaf {
				kind = "leaf"
			}
			fmt.Fprintf(progressW, "building %s %s (context=%s)\n", kind, step.Tag, step.Context)
		}
		if runErr := Run(ctx, step, realm, stdout, stderr); runErr != nil {
			return fmt.Errorf("build %s: %w", step.Tag, runErr)
		}
	}
	return nil
}
