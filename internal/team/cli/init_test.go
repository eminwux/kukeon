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

package cli_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestInitStoresTheKukeTeamAndInitialDefaults is AC7: init applies the kuke
// Team, and creates the TeamDefaults in the same team apply when the realm
// has none.
func TestInitStoresTheKukeTeamAndInitialDefaults(t *testing.T) {
	h := newHarness(t)
	out, err := h.run(t, "init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stored team kuke") || !strings.Contains(out, "No cells created or started") {
		t.Fatalf("output:\n%s", out)
	}
	// The default team follows the kuke team (issue #1692).
	if !strings.Contains(out, "stored team default") ||
		strings.Index(out, "stored team kuke") > strings.Index(out, "stored team default") {
		t.Fatalf("output does not report the default team after kuke:\n%s", out)
	}
	if h.store.team != "default" || h.store.teams["kuke"] == nil || h.store.teams["default"] == nil {
		t.Fatalf("last team apply for %q; stored %v", h.store.team, h.store.teams)
	}
	team, err := config.DecodeTeam(bytes.NewReader(h.store.teams["kuke"]))
	if err != nil || team.Metadata.Name != "kuke" || team.Metadata.Realm != "default" {
		t.Fatalf("stored kuke Team: %v %+v", err, team)
	}
	defaults := h.store.storedDefaults(t)
	if defaults.Spec.Defaults.Sources != nil ||
		len(defaults.Spec.Defaults.Harnesses) != 2 || defaults.Spec.Defaults.Roles != nil {
		t.Fatalf("initial TeamDefaults: %+v", defaults.Spec.Defaults)
	}
	// The kukeon Source is a stored document of its own (#1643).
	if raw := string(h.store.sources[config.KukeonSourceName]); !strings.Contains(raw, "kind: Source") {
		t.Fatalf("Source kukeon not stored: %q", raw)
	}
	if !h.store.closed {
		t.Fatal("client left open")
	}
}

// TestInitNeverChangesExistingDefaults is AC7: an existing TeamDefaults is
// kept, with and without --force.
func TestInitNeverChangesExistingDefaults(t *testing.T) {
	for _, args := range [][]string{{"init"}, {"init", "--force"}} {
		h := newHarness(t)
		h.store.putDefaults(t, initialize.InitialDefaults("default"))
		h.store.defaults = append([]byte("# the operator's own\n"), h.store.defaults...)
		before := string(h.store.defaults)
		if len(args) > 1 {
			h.store.putTeam(t, config.Majordomo())
		}
		if _, err := h.run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if string(h.store.defaults) != before || strings.Contains(string(h.store.applied), "kind: TeamDefaults") {
			t.Fatalf("%v changed the TeamDefaults:\n%s", args, h.store.applied)
		}
	}
}

// TestInitAlreadyInitialized is AC7: "already initialized" is the stored kuke
// Team, and nothing is applied.
func TestInitAlreadyInitialized(t *testing.T) {
	h := newHarness(t)
	h.store.putTeam(t, config.Majordomo())
	_, err := h.run(t, "init")
	if err == nil || !strings.Contains(err.Error(), "already initialized") ||
		!strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "--reapply") {
		t.Fatalf("error = %v", err)
	}
	if h.store.writes != 0 {
		t.Fatalf("writes = %d", h.store.writes)
	}
}

// TestInitReapplyRecomposesTheStoredTeam is AC7/AC12: --reapply is kuke team
// apply kuke, and needs a stored kuke Team.
func TestInitReapplyRecomposesTheStoredTeam(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(t, "init", "--reapply"); err == nil || !strings.Contains(err.Error(), "kuke team init first") {
		t.Fatalf("reapply without a stored team: %v", err)
	}
	h.store.putTeam(t, config.Majordomo())
	h.store.putDefaults(t, initialize.InitialDefaults("default"))
	out, err := h.run(t, "init", "--reapply")
	if err != nil || !strings.Contains(out, "stored team kuke") {
		t.Fatalf("reapply: %v\n%s", err, out)
	}
}

func TestInitReapplyExcludesForce(t *testing.T) {
	h := newHarness(t)
	_, err := h.run(t, "init", "--reapply", "--force")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v", err)
	}
	if len(h.connect) != 0 {
		t.Fatal("dialled kukeond for invalid flags")
	}
}

func TestInitUpgradeExcludesForceAndReapply(t *testing.T) {
	for _, other := range []string{"--force", "--reapply"} {
		h := newHarness(t)
		_, err := h.run(t, "init", "--upgrade", other)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") ||
			!strings.Contains(err.Error(), "--upgrade") {
			t.Fatalf("%s: error = %v", other, err)
		}
		if len(h.connect) != 0 {
			t.Fatalf("%s: dialled kukeond for invalid flags", other)
		}
	}
}

// TestInitHasNoGitFlags: init reads no git identity, so it takes no flag to
// import one, and its help points at the git connector.
func TestInitHasNoGitFlags(t *testing.T) {
	h := newHarness(t)
	for _, flag := range []string{"--import-git-identity", "--git-key", "--git-key-passphrase-file", "--git-known-hosts"} {
		if _, err := h.run(t, "init", flag); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("%s: err=%v", flag, err)
		}
	}
	out, err := h.run(t, "init", "--help")
	if err != nil || !strings.Contains(out, "kuke connector import git --default") {
		t.Fatalf("help: %v\n%s", err, out)
	}
	if len(h.connect) != 0 {
		t.Fatal("dialled kukeond for help or a bad flag")
	}
}

func TestInitHomeFailure(t *testing.T) {
	h := newHarness(t)
	h.homeErr = errors.New("home unavailable")
	if _, err := h.run(t, "init"); !errors.Is(err, h.homeErr) || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("error = %v", err)
	}
}

// TestConnectionFlagsAreForwarded: every verb dials through the same
// endpoint flags (G2).
func TestConnectionFlagsAreForwarded(t *testing.T) {
	h := newHarness(t)
	h.store.putTeam(t, config.Majordomo())
	_, _ = h.run(t, "init", "--host", "unix:///chosen.sock", "--configuration", "/chosen/kuke.yaml")
	want := initialize.ConnectOptions{
		Root: filepath.Join(h.home, ".kuke"), Host: "unix:///chosen.sock", Configuration: "/chosen/kuke.yaml",
	}
	if len(h.connect) != 1 || h.connect[0] != want {
		t.Fatalf("connect = %+v, want %+v", h.connect, want)
	}
}
