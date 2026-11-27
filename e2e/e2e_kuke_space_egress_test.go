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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// egressCellManifest is a busybox cell whose work container idles, for
// `kuke exec` to run ip and ping in.
func egressCellManifest(realm, space, stack, cell string) string {
	return fmt.Sprintf(`apiVersion: v1beta1
kind: Cell
metadata:
  name: %[4]s
spec:
  id: %[4]s
  realmId: %[1]s
  spaceId: %[2]s
  stackId: %[3]s
  containers:
    - id: root
      root: true
      image: ghcr.io/containerd/busybox:latest
      command: sh
      args: ["-c", "trap 'exit 0' TERM; sleep 3600 & wait"]
    - id: work
      image: ghcr.io/containerd/busybox:latest
      command: sh
      args: ["-c", "trap 'exit 0' TERM; sleep 3600 & wait"]
`, realm, space, stack, cell)
}

var ipv4AddrRE = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)/`)

// TestKukeApply_Space_EgressPolicyUpdate pins #1703: `kuke apply -f` of an
// existing Space whose only change is spec.network.egress used to report
// "unchanged", keep the old stored policy and leave the space's iptables
// chain as it was. The apply now reports updated, stores the policy and
// rebuilds the chain at once: an added cidr rule admits traffic to a cell in
// another space, a removed rule stops admitting it, allow → deny starts
// dropping, and omitting spec.network makes the space unconstrained.
func TestKukeApply_Space_EgressPolicyUpdate(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not found: the daemon installs no egress chains")
	}

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realm := generateUniqueRealmName(t)
	srcSpace, dstSpace := generateUniqueSpaceName(t), generateUniqueSpaceName(t)
	srcStack, dstStack := generateUniqueStackName(t), generateUniqueStackName(t)
	srcCell, dstCell := generateUniqueCellName(t), generateUniqueCellName(t)
	daemon := buildKukeDaemonArgs(host)

	t.Cleanup(func() {
		cleanupCell(t, host, realm, srcSpace, srcStack, srcCell)
		cleanupCell(t, host, realm, dstSpace, dstStack, dstCell)
		cleanupStack(t, host, realm, srcSpace, srcStack)
		cleanupStack(t, host, realm, dstSpace, dstStack)
		cleanupSpace(t, host, realm, srcSpace)
		cleanupSpace(t, host, realm, dstSpace)
		cleanupRealm(t, host, realm)
	})

	manifestDir := t.TempDir()
	writeDoc := func(name, content string) string {
		t.Helper()
		path := filepath.Join(manifestDir, name+".yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		return path
	}
	applyDoc := func(name, content string) string {
		t.Helper()
		return string(runReturningBinary(t, nil, kuke, append(daemon, "apply", "-f", writeDoc(name, content))...))
	}
	runCell := func(space, stack, cell string) {
		t.Helper()
		path := writeDoc(cell, egressCellManifest(realm, space, stack, cell))
		runReturningBinary(t, nil, kuke, append(daemon, "run", "-f", path, "-d")...)
	}
	srcSpaceDoc := func(network string) string {
		return fmt.Sprintf("apiVersion: v1beta1\nkind: Space\nmetadata:\n  name: %s\nspec:\n  realmId: %s\n%s",
			srcSpace, realm, network)
	}
	applySrcSpace := func(network, wantAction string) {
		t.Helper()
		out := applyDoc("src-space", srcSpaceDoc(network))
		want := fmt.Sprintf("Space %q: %s", srcSpace, wantAction)
		if !strings.Contains(out, want) {
			t.Fatalf("kuke apply: want %q in output:\n%s", want, out)
		}
	}
	execIn := func(space, stack, cell string, command ...string) (int, string) {
		t.Helper()
		args := append(append(daemon, "exec", cell, "--realm", realm, "--space", space, "--stack", stack, "--"),
			command...)
		code, stdout, stderr := runBinary(t, nil, kuke, args...)
		return code, string(stdout) + string(stderr)
	}

	runReturningBinary(t, nil, kuke, append(daemon, "create", "realm", realm)...)

	// The destination: a cell in an unconstrained space.
	runReturningBinary(t, nil, kuke, append(daemon, "create", "space", dstSpace, "--realm", realm)...)
	runReturningBinary(t, nil, kuke,
		append(daemon, "create", "stack", dstStack, "--realm", realm, "--space", dstSpace)...)
	runCell(dstSpace, dstStack, dstCell)
	code, out := execIn(dstSpace, dstStack, dstCell, "ip", "-4", "addr", "show", "eth0")
	m := ipv4AddrRE.FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("read the destination cell's address: exit %d\n%s", code, out)
	}
	dstIP := m[1]

	// The source: a cell in a deny-all space.
	const denyAll = "  network:\n    egress:\n      default: deny\n"
	allowDst := fmt.Sprintf(
		"  network:\n    egress:\n      default: deny\n      allow:\n        - cidr: %s/32\n",
		dstIP,
	)
	applySrcSpace(denyAll, "created")
	runReturningBinary(t, nil, kuke,
		append(daemon, "create", "stack", srcStack, "--realm", realm, "--space", srcSpace)...)
	runCell(srcSpace, srcStack, srcCell)

	chain := func() string {
		t.Helper()
		save, err := exec.CommandContext(t.Context(), "iptables-save", "-t", "filter").Output()
		if err != nil {
			t.Fatalf("iptables-save: %v", err)
		}
		tag := fmt.Sprintf("kukeon:%s:%s:", realm, srcSpace)
		var lines []string
		for _, line := range strings.Split(string(save), "\n") {
			if strings.Contains(line, tag) {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	reach := func(step string, want bool) {
		t.Helper()
		pingCode, pingOut := execIn(srcSpace, srcStack, srcCell, "ping", "-c", "2", "-W", "2", dstIP)
		if got := pingCode == 0; got != want {
			t.Fatalf("%s: ping %s reachable=%v, want %v\n%s\nchain:\n%s", step, dstIP, got, want, pingOut, chain())
		}
		t.Logf("%s: ping %s reachable=%v\nchain:\n%s", step, dstIP, want, chain())
	}

	reach("deny-all at create", false)

	applySrcSpace(allowDst, "updated")
	if !strings.Contains(chain(), "-d "+dstIP+"/32") {
		t.Fatalf("no ACCEPT rule for %s/32 after the apply:\n%s", dstIP, chain())
	}
	reach("allow rule added", true)

	getOut := string(runReturningBinary(t, nil, kuke,
		append(daemon, "get", "space", srcSpace, "--realm", realm, "-o", "yaml")...))
	if !strings.Contains(getOut, dstIP+"/32") {
		t.Errorf("kuke get space does not show the new allow rule:\n%s", getOut)
	}

	applySrcSpace(allowDst, "unchanged")

	applySrcSpace(denyAll, "updated")
	reach("allow rule removed", false)

	applySrcSpace("  network:\n    egress:\n      default: allow\n", "updated")
	reach("default allow", true)

	applySrcSpace(denyAll, "updated")
	reach("allow to deny", false)

	applySrcSpace("", "updated")
	reach("spec.network omitted", true)
}
