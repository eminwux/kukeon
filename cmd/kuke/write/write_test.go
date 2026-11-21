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

package write

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/internal/terminaltest"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func Test_NewWriteCmd_Metadata(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd := NewWriteCmd()
	if cmd.Use != "write <cell> [text|-]" {
		t.Errorf("Use = %q", cmd.Use)
	}
	for _, f := range []string{"raw", "realm", "space", "stack", "container"} {
		if cmd.Flags().Lookup(f) == nil {
			t.Errorf("--%s flag not registered", f)
		}
	}
}

// Test_ResolvePayload mirrors sbsh's cmd/sb/write/write_test.go
// Test_ResolveWriteOpts payload cases, plus stdin.
func Test_ResolvePayload(t *testing.T) {
	cases := []struct {
		name        string
		rest        []string
		stdin       string
		raw         bool
		wantErr     error
		wantPayload []byte
	}{
		{name: "no text yields empty payload", rest: nil, wantPayload: nil},
		{name: "caret parsed", rest: []string{"hi^M"}, wantPayload: append([]byte("hi"), 0x0D)},
		{name: "invalid caret", rest: []string{"^!"}, wantErr: errdefs.ErrInvalidWritePayload},
		{name: "shell escape rejected", rest: []string{`echo\n`}, wantErr: errdefs.ErrInvalidWritePayload},
		{name: "raw verbatim", rest: []string{`^C\n`}, raw: true, wantPayload: []byte(`^C\n`)},
		{name: "no newline added", rest: []string{"ls"}, wantPayload: []byte("ls")},
		{
			name:        "stdin with caret",
			rest:        []string{"-"},
			stdin:       "echo hi^M",
			wantPayload: append([]byte("echo hi"), 0x0D),
		},
		{name: "stdin raw", rest: []string{"-"}, stdin: "a^Mb\n", raw: true, wantPayload: []byte("a^Mb\n")},
		{name: "stdin invalid caret", rest: []string{"-"}, stdin: "^", wantErr: errdefs.ErrInvalidWritePayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePayload(strings.NewReader(tc.stdin), tc.rest, tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tc.wantPayload) {
				t.Fatalf("payload = %q, want %q", got, tc.wantPayload)
			}
		})
	}
}

func newCmd(t *testing.T, fc *terminaltest.Client, stdin string) *cobra.Command {
	t.Helper()
	t.Cleanup(viper.Reset)
	t.Cleanup(func() { _ = fc.Close() })

	cmd := NewWriteCmd()
	ctx := context.WithValue(context.Background(), MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd
}

// TestWrite_DeliversPayload drives the real sbsh terminal client against
// the fake terminal: each mode lands exactly one Write RPC carrying the
// expected bytes over the daemon-passed connection.
func TestWrite_DeliversPayload(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  []byte
	}{
		{name: "caret expanded", args: []string{"c1", "echo hi^M"}, want: []byte("echo hi\r")},
		{name: "ctrl-c", args: []string{"c1", "^C"}, want: []byte{0x03}},
		{name: "hex escape", args: []string{"c1", `\x1b[A`}, want: []byte("\x1b[A")},
		{name: "raw", args: []string{"c1", "--raw", `a^M\n`}, want: []byte(`a^M\n`)},
		{name: "stdin", args: []string{"c1", "-"}, stdin: "line one^M", want: []byte("line one\r")},
		{name: "stdin raw", args: []string{"c1", "--raw", "-"}, stdin: "x\ny\n", want: []byte("x\ny\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := terminaltest.NewClient()
			cmd := newCmd(t, fc, tc.stdin)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			writes := fc.Terminal.Writes()
			if len(writes) != 1 {
				t.Fatalf("Write RPCs = %d, want 1", len(writes))
			}
			if !bytes.Equal(writes[0], tc.want) {
				t.Errorf("written = %q, want %q", writes[0], tc.want)
			}
			if n := len(fc.Connects()); n != 1 {
				t.Errorf("ConnectContainerTerminal calls = %d, want 1 (preflight reused by the RPC)", n)
			}
		})
	}
}

// TestWrite_NothingToSend_NoRoundTrip matches `sb write`: no text (or an
// empty one) is a no-op that never reaches the daemon.
func TestWrite_NothingToSend_NoRoundTrip(t *testing.T) {
	for _, args := range [][]string{{"c1"}, {"c1", ""}} {
		fc := terminaltest.NewClient()
		cmd := newCmd(t, fc, "")
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute(%q): %v", args, err)
		}
		if n := len(fc.Connects()); n != 0 {
			t.Errorf("Execute(%q): ConnectContainerTerminal calls = %d, want 0", args, n)
		}
	}
}

func TestWrite_InvalidPayload_FailsBeforeConnecting(t *testing.T) {
	fc := terminaltest.NewClient()
	cmd := newCmd(t, fc, "")
	cmd.SetArgs([]string{"c1", `hello\n`})
	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrInvalidWritePayload) {
		t.Fatalf("err = %v, want ErrInvalidWritePayload", err)
	}
	if n := len(fc.Connects()); n != 0 {
		t.Errorf("ConnectContainerTerminal calls = %d, want 0", n)
	}
}

// TestWrite_RefusalsMatchAttach pins the AC: non-Attachable and
// not-Running targets fail with `kuke attach`'s errors, and nothing is
// written.
func TestWrite_RefusalsMatchAttach(t *testing.T) {
	cases := []struct {
		name    string
		connect error
		args    []string
		wantErr error
		wantMsg string
	}{
		{
			name:    "not attachable",
			connect: errdefs.ErrAttachNotSupported,
			args:    []string{"c1", "--container", "side", "x"},
			wantErr: errdefs.ErrAttachNotSupported,
			wantMsg: `container "side" in cell "c1" is not attachable`,
		},
		{
			name:    "task not running",
			connect: fmt.Errorf("%w: container \"work\" has state Stopped", errdefs.ErrAttachTaskNotRunning),
			args:    []string{"c1", "x"},
			wantErr: errdefs.ErrAttachTaskNotRunning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := terminaltest.NewClient()
			fc.ConnectFn = func(v1beta1.ContainerDoc) error { return tc.connect }
			cmd := newCmd(t, fc, "")
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v in chain", err, tc.wantErr)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
			if n := len(fc.Terminal.Writes()); n != 0 {
				t.Errorf("Write RPCs = %d, want 0", n)
			}
		})
	}
}

func TestWrite_StoppedCell_RefusedByLivenessGuard(t *testing.T) {
	fc := terminaltest.NewClient()
	fc.GetCellFn = func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
		return kukeonv1.GetCellResult{
			Cell:           v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped}},
			MetadataExists: true,
		}, nil
	}
	cmd := newCmd(t, fc, "")
	cmd.SetArgs([]string{"c1", "x"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute on a Stopped cell returned nil")
	}
	if n := len(fc.Connects()); n != 0 {
		t.Errorf("ConnectContainerTerminal calls = %d, want 0", n)
	}
}
