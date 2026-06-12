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

package version_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/version"
)

func TestNewVersionCmd(t *testing.T) {
	cmd := version.NewVersionCmd()

	if cmd.Use != "version" {
		t.Errorf("Use mismatch: got %q, want %q", cmd.Use, "version")
	}

	if cmd.Short != "Print the version number" {
		t.Errorf("Short mismatch: got %q, want %q", cmd.Short, "Print the version number")
	}

	// Should have RunE (not Run)
	if cmd.RunE == nil {
		t.Error("expected RunE to be set")
	}

	// Verify flags exist
	for _, flag := range []string{"no-daemon", "strict"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("expected %q flag", flag)
		}
	}
}

func TestNewVersionCmd_AutocompleteRegistration(t *testing.T) {
	cmd := version.NewVersionCmd()

	// version command takes no positional args, so ValidArgsFunction should be nil
	if cmd.ValidArgsFunction != nil {
		t.Fatal("expected ValidArgsFunction to be nil for version command (no positional args)")
	}
}

type fakeDaemonClient struct {
	pingVersionFn func(ctx context.Context) (string, error)
}

func (f *fakeDaemonClient) PingVersion(ctx context.Context) (string, error) {
	if f.pingVersionFn == nil {
		return "", errors.New("unexpected PingVersion call")
	}
	return f.pingVersionFn(ctx)
}

func (f *fakeDaemonClient) Close() error {
	return nil
}

func TestVersionCmdRun(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		mock      *fakeDaemonClient
		wantOut   string
		wantErr   string
		wantErrFn func(err error) bool
	}{
		{
			name:    "--no-daemon prints client version only",
			args:    []string{"--no-daemon"},
			wantOut: "Client: " + config.Version + "\n",
		},
		{
			name: "daemon up, matching versions",
			mock: &fakeDaemonClient{
				pingVersionFn: func(_ context.Context) (string, error) {
					return config.Version, nil
				},
			},
			wantOut: "Client: " + config.Version + "\nDaemon: " + config.Version + "\n",
		},
		{
			name: "daemon up, mismatch warns to stderr, exit 0",
			mock: &fakeDaemonClient{
				pingVersionFn: func(_ context.Context) (string, error) {
					return "dummy-version", nil
				},
			},
			wantOut: "Client: " + config.Version + "\nDaemon: dummy-version\n",
		},
		{
			name: "daemon up, mismatch with --strict returns error",
			args: []string{"--strict"},
			mock: &fakeDaemonClient{
				pingVersionFn: func(_ context.Context) (string, error) {
					return "dummy-version", nil
				},
			},
			wantErr: "version mismatch: client=" + config.Version + " daemon=dummy-version",
		},
		{
			name: "daemon unreachable, warns to stderr, exit 0",
			mock: &fakeDaemonClient{
				pingVersionFn: func(_ context.Context) (string, error) {
					return "", errors.New("connection refused")
				},
			},
			wantOut: "Client: " + config.Version + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := version.NewVersionCmd()
			outBuf := &bytes.Buffer{}
			errBuf := &bytes.Buffer{}
			cmd.SetOut(outBuf)
			cmd.SetErr(errBuf)

			ctx := context.Background()
			if tt.mock != nil {
				ctx = context.WithValue(ctx, version.MockDaemonClientKey{}, tt.mock)
			}
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantOut != "" && outBuf.String() != tt.wantOut {
				t.Errorf("stdout mismatch:\n  got:  %q\n  want: %q", outBuf.String(), tt.wantOut)
			}
		})
	}
}

// TestVersionCmdRun_DaemonUnreachableNoMock tests the path where no mock is
// injected and the real daemon dial creates a UnixClient but PingVersion
// fails (no daemon running). This exercises the real code path.
func TestVersionCmdRun_DaemonUnreachableNoMock(t *testing.T) {
	cmd := version.NewVersionCmd()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected nil error (daemon down is non-fatal), got: %v", err)
	}

	expectedOut := "Client: " + config.Version + "\n"
	if outBuf.String() != expectedOut {
		t.Errorf("stdout mismatch:\n  got:  %q\n  want: %q", outBuf.String(), expectedOut)
	}

	// Should contain a warning on stderr that the daemon is unreachable
	if !strings.Contains(errBuf.String(), "Warning: daemon unreachable") {
		t.Errorf("expected daemon unreachable warning on stderr; got: %q", errBuf.String())
	}
}

// TestVersionCmdRun_MismatchStderr verifies that a version mismatch prints a
// warning to stderr (even when exit code is 0).
func TestVersionCmdRun_MismatchStderr(t *testing.T) {
	cmd := version.NewVersionCmd()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	ctx := context.WithValue(context.Background(), version.MockDaemonClientKey{}, &fakeDaemonClient{
		pingVersionFn: func(_ context.Context) (string, error) {
			return "dummy", nil
		},
	})
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected nil error (mismatch without --strict), got: %v", err)
	}

	if !strings.Contains(errBuf.String(), "Warning: version mismatch") {
		t.Errorf("expected version mismatch warning on stderr; got: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), config.Version) {
		t.Errorf("stderr should contain client version; got: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "dummy") {
		t.Errorf("stderr should contain daemon version; got: %q", errBuf.String())
	}
}

// TestVersionCmdRun_StrictMismatchNoUsage verifies that the --strict error path
// emits neither cobra's usage/help block nor its duplicate "Error:" line, leaving
// only the command's own "Warning: version mismatch" on stderr. Regression guard
// for the installer skew handler (scripts/install.sh handle_running_daemon), which
// captures `kuke version --strict 2>&1` and re-prints it verbatim.
func TestVersionCmdRun_StrictMismatchNoUsage(t *testing.T) {
	cmd := version.NewVersionCmd()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	ctx := context.WithValue(context.Background(), version.MockDaemonClientKey{}, &fakeDaemonClient{
		pingVersionFn: func(_ context.Context) (string, error) {
			return "dummy", nil
		},
	})
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--strict"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on --strict mismatch, got nil")
	}

	// The command's own warning must still be present.
	if !strings.Contains(errBuf.String(), "Warning: version mismatch") {
		t.Errorf("expected version mismatch warning on stderr; got: %q", errBuf.String())
	}
	// No cobra usage/help dump (SilenceUsage) and no duplicate "Error:" line
	// (SilenceErrors).
	for _, unwanted := range []string{"Usage:", "Global Flags:", "help for version", "Error:"} {
		if strings.Contains(errBuf.String(), unwanted) {
			t.Errorf("stderr should not contain %q on --strict error path; got: %q", unwanted, errBuf.String())
		}
	}
}
