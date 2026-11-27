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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// getContainerDoc reads one container through the daemon.
func getContainerDoc(t *testing.T, host, realm, space, stack, cell, name string) v1beta1.ContainerDoc {
	t.Helper()
	out := runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host),
		"get", "container", name, "--cell", cell,
		"--realm", realm, "--space", space, "--stack", stack, "-o", "json")...)
	var doc v1beta1.ContainerDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decode kuke get container: %v\n%s", err, out)
	}
	return doc
}

// getCellDoc reads one cell through the daemon.
func getCellDoc(t *testing.T, host, realm, space, stack, cell string) v1beta1.CellDoc {
	t.Helper()
	out := runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host),
		"get", "cell", cell, "--realm", realm, "--space", space, "--stack", stack, "-o", "json")...)
	var doc v1beta1.CellDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decode kuke get cell: %v\n%s", err, out)
	}
	return doc
}

// TestKuke_RequiredRepoUnresolvable_ReportsRepoFailed is the 2026-11-26 case
// (issue #1711): an attachable container whose required repo cannot be
// cloned. kuketty exits 70 before its terminal starts; the failure must be
// visible through kukeond alone — in the container's status, the cell's
// reason, `kuke health` and `kuke log` — without reading tty/kuketty.log on
// the host.
func TestKuke_RequiredRepoUnresolvable_ReportsRepoFailed(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
		cleanupStackNoDaemon(t, runPath, realmName, spaceName, stackName)
		cleanupSpaceNoDaemon(t, runPath, realmName, spaceName)
		cleanupRealmNoDaemon(t, runPath, realmName)
	})

	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)

	tmpFile := renderAttachableCellFixture(t, "attachable-cell-required-repo-unresolvable.yaml",
		realmName, spaceName, stackName, cellName)
	res := <-startKukeApplyAsync(t, host, tmpFile)
	if res.err == nil {
		t.Fatalf("kuke apply succeeded; want failure from the unresolvable required repo\noutput:\n%s", res.output)
	}

	// The container: repos[], supervisor and reason, read from kuketty's
	// supervisor document. Poll briefly: depending on how fast the clone
	// fails, the failure is recorded on the start path or by the next
	// reconcile.
	var st v1beta1.ContainerStatus
	deadline := time.Now().Add(30 * time.Second)
	for {
		st = getContainerDoc(t, host, realmName, spaceName, stackName, cellName, "work").Status
		if st.Reason != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if st.ExitCode != 70 {
		t.Errorf("status.exitCode = %d, want 70", st.ExitCode)
	}
	if len(st.Repos) != 1 || st.Repos[0].Name != "unresolvable" ||
		st.Repos[0].State != v1beta1.RepoStateFailed || st.Repos[0].Error == "" {
		t.Errorf("status.repos = %+v, want one failed repo carrying git's error", st.Repos)
	}
	if st.Supervisor == nil {
		t.Fatalf("status.supervisor is empty; status = %+v", st)
	}
	if st.Supervisor.Phase != v1beta1.SupervisorPhaseRepos || st.Supervisor.Current != "unresolvable" {
		t.Errorf("status.supervisor phase/current = %q/%q, want Repos/unresolvable",
			st.Supervisor.Phase, st.Supervisor.Current)
	}
	if st.Supervisor.Exit == nil || st.Supervisor.Exit.Code != 70 {
		t.Errorf("status.supervisor.exit = %+v, want code 70", st.Supervisor.Exit)
	}
	if st.Reason != v1beta1.ContainerReasonRepoFailed || !strings.Contains(st.Message, `"unresolvable"`) {
		t.Errorf("status reason/message = %q/%q, want RepoFailed naming the repo", st.Reason, st.Message)
	}

	// The cell takes its failing container's reason.
	cell := getCellDoc(t, host, realmName, spaceName, stackName, cellName)
	if cell.Status.State != v1beta1.CellStateError && cell.Status.State != v1beta1.CellStateFailed {
		t.Errorf("cell state = %v, want Error or Failed", cell.Status.State)
	}
	if cell.Status.Reason != v1beta1.ContainerReasonRepoFailed ||
		!strings.Contains(cell.Status.Message, "unresolvable") {
		t.Errorf(
			"cell reason/message = %q/%q, want RepoFailed naming the repo",
			cell.Status.Reason,
			cell.Status.Message,
		)
	}

	// kuke health lists the cell with the same reason.
	code, stdout, stderr := runBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), "health", "-o", "json")...)
	if code != 0 {
		t.Fatalf("kuke health exit %d\nstderr:\n%s", code, stderr)
	}
	var digest kukeonv1.HealthDigest
	if err := json.Unmarshal(stdout, &digest); err != nil {
		t.Fatalf("decode kuke health: %v\n%s", err, stdout)
	}
	found := false
	for _, c := range digest.Cells {
		if c.Name == cellName && c.Realm == realmName {
			found = true
			if c.Reason != v1beta1.ContainerReasonRepoFailed {
				t.Errorf("kuke health reason = %q, want RepoFailed (message %q)", c.Reason, c.Message)
			}
		}
	}
	if !found {
		t.Errorf("kuke health does not list cell %q: %+v", cellName, digest.Cells)
	}

	// kuke log no longer says "try again" for a container that stopped
	// before its terminal started.
	_, logOut, logErr := runBinary(t, nil, kuke, append(buildKukeDaemonArgs(host),
		"log", cellName, "--container", "work",
		"--realm", realmName, "--space", spaceName, "--stack", stackName)...)
	combined := string(logOut) + string(logErr)
	if strings.Contains(combined, "try again") {
		t.Errorf("kuke log on a stopped container still says try again:\n%s", combined)
	}
}

// TestKuke_AttachableSupervisorRunning checks the healthy case: a running
// attachable container reports supervisor phase Running with its attached
// terminal Ready.
func TestKuke_AttachableSupervisorRunning(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
		cleanupStackNoDaemon(t, runPath, realmName, spaceName, stackName)
		cleanupSpaceNoDaemon(t, runPath, realmName, spaceName)
		cleanupRealmNoDaemon(t, runPath, realmName)
	})

	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)
	applyAttachableCell(t, host, "attachable-cell.yaml", realmName, spaceName, stackName, cellName)

	// kuketty polls its terminal about once a second.
	var st v1beta1.ContainerStatus
	deadline := time.Now().Add(30 * time.Second)
	for {
		st = getContainerDoc(t, host, realmName, spaceName, stackName, cellName, "work").Status
		sup := st.Supervisor
		if sup != nil && sup.Phase == v1beta1.SupervisorPhaseRunning &&
			len(sup.Terminals) == 1 && sup.Terminals[0].State == "Ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor did not reach Running with terminal Ready: %+v", sup)
		}
		time.Sleep(time.Second)
	}
	if st.Supervisor.AttachedTerminal != "main" || st.Supervisor.Terminals[0].ID != "main" {
		t.Errorf("attachedTerminal/terminal id = %q/%q, want main", st.Supervisor.AttachedTerminal,
			st.Supervisor.Terminals[0].ID)
	}
	if st.Supervisor.Exit != nil || st.Reason != "" {
		t.Errorf("running container has exit %+v / reason %q", st.Supervisor.Exit, st.Reason)
	}
}
