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

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	kuke = "kuke"
	ctr  = "ctr"
)

var (
	// nameSeed is a per-process random base for unique test-resource names. Seeding
	// from crypto/rand ensures a realm/namespace left behind by a crashed prior run
	// cannot collide with a fresh run's first-allocated name (e.g. e-r-1).
	nameSeed = func() uint64 {
		var b [2]byte
		if _, err := rand.Read(b[:]); err != nil {
			return uint64(time.Now().UnixNano() & 0xFFFF)
		}
		return uint64(binary.BigEndian.Uint16(b[:]))
	}()
	// nameCounter is a process-wide monotonic counter shared by every generator, so
	// no two names allocated within a single `go test` run can collide regardless of
	// how closely in time they are requested.
	nameCounter atomic.Uint64
)

// uniqueNameID returns a short, collision-free identifier for test-resource names.
// The seed+counter sum is rendered in base36 (lowercase 0-9a-z) to stay a valid
// containerd namespace label while keeping names within the namespace-label and
// /opt/kukeon/data metadata-path length limits the harness already fights.
func uniqueNameID() string {
	return strconv.FormatUint(nameSeed+nameCounter.Add(1), 36)
}

// runReturningBinary runs the provided binary with args, fails the test on non-zero exit or empty output.
// If the binary file does not exist, the test is skipped.
func runReturningBinary(t *testing.T, _ []string, command string, args ...string) []byte {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".." // or detect repo root
	}
	bin := filepath.Join(dir, command)

	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %s %v failed: %v\noutput:\n%s", bin, args, err, string(out))
	}
	if len(out) == 0 {
		t.Fatalf("no output from %s %v", bin, args)
	}

	return out
}

// runBinary executes binary and returns exit code, stdout, stderr separately.
func runBinary(t *testing.T, env []string, command string, args ...string) (int, []byte, []byte) {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".."
	}
	bin := filepath.Join(dir, command)

	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	if env != nil {
		cmd.Env = env
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			t.Fatalf("failed to run %s %v: %v", bin, args, err)
		}
	}

	return exitCode, []byte(stdoutBuf.String()), []byte(stderrBuf.String())
}

// getRandomRunPath returns a unique, absolute run path for the test. When
// the t.TempDir-rooted path would push the *deep* per-container kuketty
// socket path (`<runPath>/data/<realm>/<space>/<stack>/<cell>/<container>/
// tty/socket`) over Linux's UNIX_PATH_MAX, the helper falls back to a
// short /tmp prefix and registers a cleanup so the suite stays SUN_PATH-
// safe regardless of how Go names the t.TempDir parent (issue #521).
//
// The architectural fix (issue #521) routes `kuke attach` through a short
// symlink, so the deep path length no longer breaks `connect(2)` —
// but the fallback is deliberate defense-in-depth: a future regression
// that accidentally re-introduces the deep-path dial would fail loudly
// here instead of in CI.
//
// Each call returns its own path so parallel-sibling tests don't race
// MkdirAll/Cleanup-RemoveAll on a shared parent (issue #491's regression
// class).
func getRandomRunPath(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	if deepSocketPathFits(tempDir) {
		return tempDir
	}
	// t.TempDir() is the standard, but here it is exactly what overflows
	// SUN_PATH because Go derives the temp basename from the test
	// function name (`TestKuke_AttachDetach_KeepsTaskRunning` is 39
	// bytes). Drop to a short, name-free /tmp prefix instead.
	short, err := os.MkdirTemp("/tmp", "ke-") //nolint:usetesting // intentional shorter prefix; see comment
	if err != nil {
		t.Fatalf("MkdirTemp(/tmp, ke-): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	return short
}

// deepSocketPathFits reports whether the longest plausible per-container
// kuketty socket path under runPath fits SUN_PATH. The longest e2e suffix
// is `/data/<realm>/<space>/<stack>/<cell>/<container>/tty/socket` with
// realm/space/stack/cell names produced by generateUnique*Name (`e-r-XX`,
// `e-sp-XX`, `e-st-XX`, `e-ce-XX`) and the container name fixed to
// "work" by the attach fixture — 52 bytes of suffix all in. The check is
// against the deep path, not the SUN_PATH-safe symlink the runner stages,
// because deep-path regressions are the failure mode this fallback
// defends against (the symlink budget is comfortably wider).
func deepSocketPathFits(runPath string) bool {
	const deepSocketSuffixLen = len("/data/e-r-XX/e-sp-XX/e-st-XX/e-ce-XX/work/tty/socket")
	return len(runPath)+deepSocketSuffixLen <= consts.KukeonMaxSocketPath
}

// buildKukeRunPathArgs returns a `--run-path <X>` prefix for callers that
// must run in the in-process controller path rather than dial a daemon.
// Use buildKukeDaemonArgs (paired with startKukeondDaemon) for workload
// commands — per-test daemon-mode is the suite's default now that phase 2
// (#565) has landed, and phase 3 (#566) made the workload verbs (apply,
// create *, run, attach, delete *, kill *) daemon-only at the code-path
// level, so they cannot use the in-process helper at all.
//
// Legitimate survivors of this in-process helper:
//   - `get realms --no-daemon`: the daemon-parity smoke (AGENTS.md's
//     regression guard contrasts the in-process realm view with the daemon's
//     view; routing both sides through the daemon would defeat the check).
//   - `purge` (any kind): purge is a host-mutating verb that must work
//     regardless of daemon state, so e2e cleanups never depend on the
//     per-test daemon still being alive. The attach test's cleanup helpers
//     (cleanup{Realm,Space,Stack,Cell}NoDaemon) call `purge --cascade` for
//     this reason after #566 took `delete *` off the in-process branch.
//   - `init` / `uninstall`: bootstrap/teardown verbs that bring up or tear
//     down the daemon itself; they cannot route through it.
//   - `kuke daemon …`: the daemon-lifecycle group; in-process by definition
//     per the #217 categorization.
//
// The in-process promotion comes from applyRunPathImpliesNoDaemon
// (cmd/kuke/kuke.go), which auto-sets --no-daemon=true whenever --run-path
// is explicit. The flag is no longer accepted at root (#222 / #567 retired
// it from workload commands and demoted it to per-command local
// registration), so spelling it out here would now mis-parse as an unknown
// root flag and shift the subcommand token.
func buildKukeRunPathArgs(runPath string) []string {
	return []string{"--run-path", runPath}
}

// loadKukeondImageIntoContainerd stages the local kukeond image into the
// kuke-system containerd namespace and returns the fully-qualified image
// reference to pass via `kuke init --kukeond-image`. It skips the test when
// the harness env vars are unset (running `go test ./e2e` without the
// `make e2e` wrapper) or when `docker` / `ctr` aren't on PATH. `kuke init`
// otherwise resolves the kukeond image from `git describe` output, which
// fails on dirty/untagged dev trees because that ref does not exist in any
// registry.
func loadKukeondImageIntoContainerd(t *testing.T) string {
	t.Helper()

	image := os.Getenv("KUKEON_E2E_IMAGE")
	dockerName := os.Getenv("KUKEON_E2E_IMAGE_DOCKER_NAME")
	if image == "" || dockerName == "" {
		t.Skip(
			"KUKEON_E2E_IMAGE / KUKEON_E2E_IMAGE_DOCKER_NAME unset; " +
				"run via 'make e2e' (which builds the local kukeond image) " +
				"or set both env vars manually",
		)
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("docker binary not found, skipping kukeond image staging: %v", err)
	}
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Skipf("ctr binary not found, skipping kukeond image staging: %v", err)
	}

	tarPath := filepath.Join(t.TempDir(), "kukeond.tar")

	saveCtx, saveCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer saveCancel()
	saveCmd := exec.CommandContext(saveCtx, dockerPath, "save", "-o", tarPath, dockerName)
	if out, saveErr := saveCmd.CombinedOutput(); saveErr != nil {
		t.Fatalf(
			"docker save %q failed: %v\noutput:\n%s\n"+
				"hint: 'make e2e' builds this image; if running go test directly, build it first",
			dockerName, saveErr, string(out),
		)
	}

	importCtx, importCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer importCancel()
	importCmd := exec.CommandContext(
		importCtx, ctrPath,
		"--namespace", consts.RealmNamespace(consts.KukeSystemRealmName),
		"images", "import", tarPath,
	)
	if out, importErr := importCmd.CombinedOutput(); importErr != nil {
		t.Fatalf(
			"ctr -n %s images import %q failed: %v\noutput:\n%s",
			consts.RealmNamespace(consts.KukeSystemRealmName), tarPath, importErr, string(out),
		)
	}

	return image
}

// verifyContainerdNamespace verifies containerd namespace exists by running ctr ns ls.
func verifyContainerdNamespace(t *testing.T, namespace string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping containerd namespace verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "ns", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr ns ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if namespace appears in output
	output := string(out)
	return strings.Contains(output, namespace)
}

// verifyContainerTaskInCellCgroup checks that a container's task cgroup is
// nested under its cell cgroup and contains at least one PID. The on-disk
// expectation is the runtime invariant established by two stacked fixes:
//
//   - Issue #312 / fix #316: BuildContainerSpec must set Linux.CgroupsPath
//     from cell.Status.CgroupPath so runc creates <cellGroup>/<containerdID>/
//     instead of placing the task cgroup under the containerd namespace.
//   - Issue #340: StartContainer must drain the just-started task PID out of
//     <cellGroup>/<containerdID>/cgroup.procs into a <containerdID>/_payload
//     leaf so the no-internal-process rule lets later runtimes widen
//     subtree_control on the cell cgroup.
//
// Post-#340, the parent <containerdID>/cgroup.procs is intentionally empty
// and the PID lives at <containerdID>/_payload/cgroup.procs — but only the
// nested layout proves both fixes are still wired. Falls back to the parent
// path so the helper still passes on a build that predates #340's leaf
// relocation; either layout is a positive #312 signal.
func verifyContainerTaskInCellCgroup(t *testing.T, cellGroup, containerdID string) bool {
	t.Helper()

	if cellGroup == "" || containerdID == "" {
		return false
	}

	mountpoint := consts.CgroupFilesystemPath
	relativeCell := strings.TrimPrefix(cellGroup, "/")
	taskCgroup := filepath.Join(mountpoint, relativeCell, containerdID)

	for _, procsPath := range []string{
		filepath.Join(taskCgroup, "_payload", "cgroup.procs"),
		filepath.Join(taskCgroup, "cgroup.procs"),
	} {
		data, err := os.ReadFile(procsPath)
		if err != nil {
			t.Logf("read %s: %v", procsPath, err)
			continue
		}
		pids := strings.Fields(string(data))
		if len(pids) == 0 {
			t.Logf("cell-rooted task cgroup %s exists but cgroup.procs is empty", procsPath)
			continue
		}
		t.Logf("verified container task PIDs %v in %s", pids, procsPath)
		return true
	}
	return false
}

// verifyCgroupPathExists verifies cgroup path exists in filesystem.
func verifyCgroupPathExists(t *testing.T, cgroupPath string) bool {
	t.Helper()

	if cgroupPath == "" {
		return false
	}

	// Build full filesystem path by joining mountpoint with group path
	// Pattern: filepath.Join(mountpoint, strings.TrimPrefix(group, "/"))
	// as used in internal/ctr/cgroups.go:129
	mountpoint := consts.CgroupFilesystemPath
	relativePath := strings.TrimPrefix(cgroupPath, "/")
	fullPath := filepath.Join(mountpoint, relativePath)

	// Check if cgroup path exists as a directory
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Logf("cgroup path check failed: fullPath=%q, err=%v", fullPath, err)
		return false
	}

	if !info.IsDir() {
		t.Logf("cgroup path exists but is not a directory: fullPath=%q", fullPath)
		return false
	}

	return true
}

// generateUniqueSpaceName generates a unique space name for tests.
func generateUniqueSpaceName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("e-sp-%s", uniqueNameID())
}

// verifySpaceCNIConfigExists verifies CNI config file exists.
func verifySpaceCNIConfigExists(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	confPath, err := fs.SpaceNetworkConfigPath(runPath, realmName, spaceName)
	if err != nil {
		t.Logf("failed to build CNI config path: %v", err)
		return false
	}

	_, err = os.Stat(confPath)
	return err == nil
}

// verifySpaceMetadataExists verifies space metadata file exists.
func verifySpaceMetadataExists(t *testing.T, runPath, realmName, spaceName string) bool {
	t.Helper()

	metadataPath := fs.SpaceMetadataPath(runPath, realmName, spaceName)
	_, err := os.Stat(metadataPath)
	return err == nil
}

// parseSpaceListJSON parses kuke get space --output json output.
func parseSpaceListJSON(t *testing.T, output []byte) ([]v1beta1.SpaceDoc, error) {
	t.Helper()

	var spaces []v1beta1.SpaceDoc
	if err := json.Unmarshal(output, &spaces); err != nil {
		return nil, fmt.Errorf("failed to parse space list JSON: %w", err)
	}

	return spaces, nil
}

// parseSpaceJSON parses kuke get space <name> --output json output.
func parseSpaceJSON(t *testing.T, output []byte) (*v1beta1.SpaceDoc, error) {
	t.Helper()

	var space v1beta1.SpaceDoc
	if err := json.Unmarshal(output, &space); err != nil {
		return nil, fmt.Errorf("failed to parse space JSON: %w", err)
	}

	return &space, nil
}

// verifySpaceInList verifies space appears in kuke get space list.
func verifySpaceInList(t *testing.T, host, realmName, spaceName string) bool {
	t.Helper()

	args := append(buildKukeDaemonArgs(host), "get", "space", "--realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	spaces, err := parseSpaceListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse space list: %v", err)
		return false
	}

	for _, space := range spaces {
		if space.Metadata.Name == spaceName {
			return true
		}
	}

	return false
}

// verifySpaceExists verifies space can be retrieved individually.
func verifySpaceExists(t *testing.T, host, realmName, spaceName string) bool {
	t.Helper()

	args := append(buildKukeDaemonArgs(host), "get", "space", spaceName, "--realm", realmName, "--output", "json")
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	space, err := parseSpaceJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse space JSON: %v", err)
		return false
	}

	return space.Metadata.Name == spaceName
}

// generateUniqueStackName generates a unique stack name for tests.
func generateUniqueStackName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("e-st-%s", uniqueNameID())
}

// verifyStackMetadataExists verifies stack metadata file exists.
func verifyStackMetadataExists(t *testing.T, runPath, realmName, spaceName, stackName string) bool {
	t.Helper()

	metadataPath := fs.StackMetadataPath(runPath, realmName, spaceName, stackName)
	_, err := os.Stat(metadataPath)
	return err == nil
}

// parseStackListJSON parses kuke get stack --output json output.
func parseStackListJSON(t *testing.T, output []byte) ([]v1beta1.StackDoc, error) {
	t.Helper()

	var stacks []v1beta1.StackDoc
	if err := json.Unmarshal(output, &stacks); err != nil {
		return nil, fmt.Errorf("failed to parse stack list JSON: %w", err)
	}

	return stacks, nil
}

// parseStackJSON parses kuke get stack <name> --output json output.
func parseStackJSON(t *testing.T, output []byte) (*v1beta1.StackDoc, error) {
	t.Helper()

	var stack v1beta1.StackDoc
	if err := json.Unmarshal(output, &stack); err != nil {
		return nil, fmt.Errorf("failed to parse stack JSON: %w", err)
	}

	return &stack, nil
}

// verifyStackInList verifies stack appears in kuke get stack list.
func verifyStackInList(t *testing.T, host, realmName, spaceName, stackName string) bool {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get",
		"stack",
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	stacks, err := parseStackListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse stack list: %v", err)
		return false
	}

	for _, stack := range stacks {
		if stack.Metadata.Name == stackName {
			return true
		}
	}

	return false
}

// verifyStackExists verifies stack can be retrieved individually.
func verifyStackExists(t *testing.T, host, realmName, spaceName, stackName string) bool {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get",
		"stack",
		stackName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--output",
		"json",
	)
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	stack, err := parseStackJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse stack JSON: %v", err)
		return false
	}

	return stack.Metadata.Name == stackName
}

// generateUniqueCellName generates a unique cell name for tests.
func generateUniqueCellName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("e-ce-%s", uniqueNameID())
}

// generateUniqueContainerName generates a unique container name for tests.
func generateUniqueContainerName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("e-co-%s", uniqueNameID())
}

// verifyCellMetadataExists verifies cell metadata file exists.
func verifyCellMetadataExists(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) bool {
	t.Helper()

	metadataPath := fs.CellMetadataPath(runPath, realmName, spaceName, stackName, cellName)
	_, err := os.Stat(metadataPath)
	return err == nil
}

// parseCellListJSON parses kuke get cell --output json output.
func parseCellListJSON(t *testing.T, output []byte) ([]v1beta1.CellDoc, error) {
	t.Helper()

	var cells []v1beta1.CellDoc
	if err := json.Unmarshal(output, &cells); err != nil {
		return nil, fmt.Errorf("failed to parse cell list JSON: %w", err)
	}

	return cells, nil
}

// parseCellJSON parses kuke get cell <name> --output json output.
func parseCellJSON(t *testing.T, output []byte) (*v1beta1.CellDoc, error) {
	t.Helper()

	var cell v1beta1.CellDoc
	if err := json.Unmarshal(output, &cell); err != nil {
		return nil, fmt.Errorf("failed to parse cell JSON: %w", err)
	}

	return &cell, nil
}

// verifyCellInList verifies cell appears in kuke get cell list.
func verifyCellInList(t *testing.T, host, realmName, spaceName, stackName, cellName string) bool {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get",
		"cell",
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cells, err := parseCellListJSON(t, output)
	if err != nil {
		t.Logf("failed to parse cell list: %v", err)
		return false
	}

	for _, cell := range cells {
		if cell.Metadata.Name == cellName {
			return true
		}
	}

	return false
}

// verifyCellExists verifies cell can be retrieved individually.
func verifyCellExists(t *testing.T, host, realmName, spaceName, stackName, cellName string) bool {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	exitCode, stdout, _ := runBinary(t, nil, kuke, args...)

	if exitCode != 0 {
		return false
	}

	cell, err := parseCellJSON(t, stdout)
	if err != nil {
		t.Logf("failed to parse cell JSON: %v", err)
		return false
	}

	return cell.Metadata.Name == cellName
}

// getRealmNamespace gets realm namespace from realm JSON.
func getRealmNamespace(t *testing.T, host, realmName string) (string, error) {
	t.Helper()

	args := append(buildKukeDaemonArgs(host), "get", "realm", realmName, "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	realm, err := parseRealmJSON(t, output)
	if err != nil {
		return "", fmt.Errorf("failed to parse realm JSON: %w", err)
	}

	return realm.Spec.Namespace, nil
}

// getCgroupRoot returns the cgroup root the daemon is using, derived from
// realmName's reported Status.CgroupPath. Mirrors the getRealmNamespace
// pattern: ask the daemon for state rather than importing
// consts.KukeonCgroupRoot, so the e2e helpers run unmodified against a
// non-default ClientConfiguration / ServerConfiguration profile (e.g. the
// dev-init dev profile under /kukeon-dev) — the daemon's response is the
// authoritative source. The returned root retains any host
// cgroup-hierarchy prefix kukeon's mount may sit under, so callers can
// compare against a nested object's Status.CgroupPath via
// strings.HasSuffix or by joining with the realm/space/stack/cell names.
func getCgroupRoot(t *testing.T, host, realmName string) (string, error) {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get", "realm", realmName, "--output", "json",
	)
	output := runReturningBinary(t, nil, kuke, args...)
	realm, err := parseRealmJSON(t, output)
	if err != nil {
		return "", fmt.Errorf("failed to parse realm JSON: %w", err)
	}
	return strings.TrimSuffix(realm.Status.CgroupPath, "/"+realm.Metadata.Name), nil
}

// verifyRootContainerExists verifies root container exists in containerd namespace.
func verifyRootContainerExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()
	return verifyContainerExists(t, namespace, containerID)
}

// verifyRootContainerTaskExists verifies root container task exists in containerd namespace.
func verifyRootContainerTaskExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()
	return verifyContainerTaskExists(t, namespace, containerID)
}

// verifyContainerExists verifies container exists in containerd namespace.
func verifyContainerExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping container verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "container", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr container ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyContainerTaskExists verifies container task exists in containerd namespace.
func verifyContainerTaskExists(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping container task verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr task ls: %v, output: %s", err, string(out))
		return false
	}

	// Check if container ID appears in output
	output := string(out)
	return strings.Contains(output, containerID)
}

// verifyRootContainerTaskIsStopped verifies root container task is stopped (not running) in containerd namespace.
// It returns true if the task is stopped or doesn't exist, false if the task is still running.
func verifyRootContainerTaskIsStopped(t *testing.T, namespace, containerID string) bool {
	t.Helper()

	// Check if ctr binary exists
	ctrPath, err := exec.LookPath(ctr)
	if err != nil {
		t.Logf("ctr binary not found, skipping root container task status verification")
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use ctr task ls to check task status
	// Tasks that are stopped still appear in the list but with STOPPED status
	cmd := exec.CommandContext(ctx, ctrPath, "--namespace", namespace, "task", "ls")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("failed to run ctr task ls: %v, output: %s", err, string(out))
		return false
	}

	output := string(out)
	lines := strings.Split(output, "\n")

	// Look for the container ID in the task list
	for _, line := range lines {
		if !strings.Contains(line, containerID) {
			continue
		}

		// Task exists - check if it's running
		// ctr task ls output format: TASK PID STATUS
		// Status can be RUNNING, STOPPED, etc.
		// If line contains RUNNING, task is still running
		if strings.Contains(line, "RUNNING") {
			return false // Task is still running
		}

		// Task exists but is not RUNNING (could be STOPPED, CREATED, etc.)
		// For a kill operation, the task should be STOPPED
		return true // Task is stopped
	}

	// Task not found in list - either doesn't exist or was deleted
	// After kill, task might not exist if it was cleaned up, which is also considered stopped
	return true
}

// getCellID gets cell ID from cell JSON.
func getCellID(t *testing.T, host, realmName, spaceName, stackName, cellName string) (string, error) {
	t.Helper()

	args := append(
		buildKukeDaemonArgs(host),
		"get",
		"cell",
		cellName,
		"--realm",
		realmName,
		"--space",
		spaceName,
		"--stack",
		stackName,
		"--output",
		"json",
	)
	output := runReturningBinary(t, nil, kuke, args...)

	cell, err := parseCellJSON(t, output)
	if err != nil {
		return "", fmt.Errorf("failed to parse cell JSON: %w", err)
	}

	if cell.Spec.ID == "" {
		return "", errors.New("cell ID is empty")
	}

	return cell.Spec.ID, nil
}
