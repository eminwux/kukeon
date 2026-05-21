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

package config_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeCompletionClient is a kukeonv1.Client whose List* methods return canned
// data, modelling the kukeond daemon's authoritative view. It embeds
// kukeonv1.FakeClient so the methods the completers don't touch return
// ErrUnexpectedCall.
type fakeCompletionClient struct {
	kukeonv1.FakeClient

	realms []v1beta1.RealmDoc
	cells  []v1beta1.CellDoc

	cellCalls int
}

func (f *fakeCompletionClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return f.realms, nil
}

func (f *fakeCompletionClient) ListCells(_ context.Context, _, _, _ string) ([]v1beta1.CellDoc, error) {
	f.cellCalls++
	return f.cells, nil
}

// daemonCmd builds a bare cobra command with a logger in context and the
// daemon (non-no-daemon) completion path selected. When client is non-nil it
// is injected via MockControllerKey so completionClient resolves through it.
func daemonCmd(t *testing.T, client kukeonv1.Client) *cobra.Command {
	t.Helper()

	t.Cleanup(viper.Reset)
	viper.Reset()
	// Daemon path is the default; be explicit so the intent is clear and a
	// leaked viper key from another test can't flip us onto the in-process path.
	viper.Set(config.KUKEON_ROOT_NO_DAEMON.ViperKey, false)

	cmd := &cobra.Command{Use: "test"}
	ctx := context.WithValue(context.Background(), types.CtxLogger, discardLogger())
	if client != nil {
		ctx = context.WithValue(ctx, config.MockControllerKey{}, client)
	}
	cmd.SetContext(ctx)
	return cmd
}

// TestCompleteCellNames_DaemonBackedClient asserts the happy path: a completer
// returns names resolved through the daemon-backed client rather than the
// metadata-direct controller.Exec the older code used.
func TestCompleteCellNames_DaemonBackedClient(t *testing.T) {
	fake := &fakeCompletionClient{
		cells: []v1beta1.CellDoc{
			{Metadata: v1beta1.CellMetadata{Name: "alpha"}},
			{Metadata: v1beta1.CellMetadata{Name: "bravo"}},
			{Metadata: v1beta1.CellMetadata{Name: "charlie"}},
		},
	}
	cmd := daemonCmd(t, fake)

	names, directive := config.CompleteCellNames(cmd, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
	}
	if fake.cellCalls != 1 {
		t.Errorf("expected exactly one ListCells call through the daemon client, got %d", fake.cellCalls)
	}
	assertNames(t, names, []string{"alpha", "bravo", "charlie"})
}

// TestCompleteRealmNames_DaemonBackedClient mirrors the above for realms so the
// daemon path is exercised at the top of the resource hierarchy too.
func TestCompleteRealmNames_DaemonBackedClient(t *testing.T) {
	fake := &fakeCompletionClient{
		realms: []v1beta1.RealmDoc{
			{Metadata: v1beta1.RealmMetadata{Name: "default"}},
			{Metadata: v1beta1.RealmMetadata{Name: "kuke-system"}},
		},
	}
	cmd := daemonCmd(t, fake)

	names, directive := config.CompleteRealmNames(cmd, []string{}, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
	}
	assertNames(t, names, []string{"default", "kuke-system"})
}

// TestCompleteCellNames_DegradesWhenDaemonUnavailable asserts the
// graceful-degradation criterion: with the daemon socket absent, the completer
// returns an empty list and the standard directive quickly, without hanging or
// surfacing an error at the prompt.
func TestCompleteCellNames_DegradesWhenDaemonUnavailable(t *testing.T) {
	cmd := daemonCmd(t, nil) // no mock: completionClient dials a real socket
	// Point at a socket that does not exist; the dial fails immediately.
	missing := "unix://" + filepath.Join(t.TempDir(), "absent-kukeond.sock")
	viper.Set(config.KUKEON_ROOT_HOST.ViperKey, missing)

	start := time.Now()
	names, directive := config.CompleteCellNames(cmd, []string{}, "")
	elapsed := time.Since(start)

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
	}
	if len(names) != 0 {
		t.Errorf("expected empty completion when daemon is unavailable, got %v", names)
	}
	// A missing unix socket fails the dial without blocking; well under the
	// completion timeout. Generous bound to stay non-flaky under load.
	if elapsed > 5*time.Second {
		t.Errorf("completion took %v with daemon absent; expected prompt degradation", elapsed)
	}
}

// TestCompleteCellNames_FollowsDaemonNotOnDisk models the reported bug: the
// daemon returns cell names that a metadata-direct read of the run path could
// not (the unprivileged-user-vs-root-daemon case). The daemon-backed path
// surfaces them; the in-process path over the same empty run path surfaces
// nothing — proving completion now follows the daemon, not the on-disk tree.
func TestCompleteCellNames_FollowsDaemonNotOnDisk(t *testing.T) {
	runPath := t.TempDir() // deliberately empty: no on-disk cell metadata

	// Daemon path: the daemon knows about "alpha"/"bravo".
	fake := &fakeCompletionClient{
		cells: []v1beta1.CellDoc{
			{Metadata: v1beta1.CellMetadata{Name: "alpha"}},
			{Metadata: v1beta1.CellMetadata{Name: "bravo"}},
		},
	}
	daemon := daemonCmd(t, fake)
	viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)

	daemonNames, _ := config.CompleteCellNames(daemon, []string{}, "")
	assertNames(t, daemonNames, []string{"alpha", "bravo"})

	// In-process path over the same empty run path: no metadata, no names.
	// This is what the unprivileged user effectively saw before #634 (an
	// unreadable/empty direct read), and what the daemon path now fixes.
	onDisk := setupTestCommand(t, runPath, false)
	onDiskNames, _ := config.CompleteCellNames(onDisk, []string{}, "")
	assertNames(t, onDiskNames, []string{})
}

func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %d names %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
