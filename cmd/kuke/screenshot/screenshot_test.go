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

package screenshot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/internal/terminaltest"
	screenshotcmd "github.com/eminwux/kukeon/cmd/kuke/screenshot"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func testScreen() api.ScreenshotResult {
	return api.ScreenshotResult{
		Cols:          80,
		Rows:          24,
		CursorX:       2,
		CursorY:       1,
		CursorVisible: true,
		AltScreen:     false,
		Text:          "$ echo hi\nhi\n",
		ANSI:          "$ echo \x1b[31mhi\x1b[0m\nhi\n",
	}
}

func newCmd(t *testing.T, fc *terminaltest.Client) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	t.Cleanup(viper.Reset)
	t.Cleanup(func() { _ = fc.Close() })

	cmd := screenshotcmd.NewScreenshotCmd()
	ctx := context.WithValue(context.Background(), screenshotcmd.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd, out
}

func newScreenClient() *terminaltest.Client {
	fc := terminaltest.NewClient()
	fc.Terminal.Screen = testScreen()
	return fc
}

func TestScreenshot_Formats(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "default is text", args: []string{"c1"}, want: testScreen().Text},
		{name: "text", args: []string{"c1", "-o", "text"}, want: testScreen().Text},
		{name: "ansi", args: []string{"c1", "-o", "ansi"}, want: testScreen().ANSI},
		{name: "case-insensitive", args: []string{"c1", "-o", "ANSI"}, want: testScreen().ANSI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newScreenClient()
			cmd, out := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScreenshot_JSON_StableSchema pins the documented -o json field names;
// renaming one is a breaking change for scripts and Majordomo.
func TestScreenshot_JSON_StableSchema(t *testing.T) {
	fc := newScreenClient()
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"c1", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"altScreen", "ansi", "cols", "cursorVisible", "cursorX", "cursorY", "rows", "text"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("json keys = %v, want %v", keys, want)
	}

	var got screenshotcmd.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode Result: %v", err)
	}
	if got != screenshotcmd.Result(testScreen()) {
		t.Errorf("Result = %+v, want %+v", got, testScreen())
	}
	if !strings.HasSuffix(out.String(), "}\n") {
		t.Errorf("json output should end with a newline: %q", out.String())
	}
}

func TestScreenshot_InvalidFormat_FailsBeforeConnecting(t *testing.T) {
	fc := newScreenClient()
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"c1", "-o", "yaml"})
	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrInvalidScreenshotFormat) {
		t.Fatalf("err = %v, want ErrInvalidScreenshotFormat", err)
	}
	if n := len(fc.Connects()); n != 0 {
		t.Errorf("ConnectContainerTerminal calls = %d, want 0", n)
	}
}

// TestScreenshot_DialsThroughDaemon pins the route: the target resolves
// like `kuke attach` (auto-picked work container, flag coordinates) and the
// one Screenshot RPC rides the preflight ConnectContainerTerminal
// connection — no socket path, no extra round trip.
func TestScreenshot_DialsThroughDaemon(t *testing.T) {
	fc := newScreenClient()
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"--realm", "r1", "--space", "s1", "--stack", "st1", "c1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	connects := fc.Connects()
	if len(connects) != 1 {
		t.Fatalf("ConnectContainerTerminal calls = %d, want 1 (preflight reused by the RPC)", len(connects))
	}
	doc := connects[0]
	got := []string{doc.Metadata.Name, doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID, doc.Spec.CellID}
	want := []string{"work", "r1", "s1", "st1", "c1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("connect target = %v, want %v", got, want)
	}
}

func TestScreenshot_ExplicitContainer_SkipsPicker(t *testing.T) {
	fc := newScreenClient()
	fc.ListContainersFn = nil // an unexpected ListContainers call errors
	cmd, _ := newCmd(t, fc)
	cmd.SetArgs([]string{"c1", "--container", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := fc.Connects()[0].Metadata.Name; got != "claude" {
		t.Errorf("connect container = %q, want claude", got)
	}
}

// TestScreenshot_RefusalsMatchAttach pins the AC: non-Attachable and
// not-Running targets fail with `kuke attach`'s errors.
func TestScreenshot_RefusalsMatchAttach(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(fc *terminaltest.Client)
		args    []string
		wantErr error
		wantMsg string
	}{
		{
			name: "not attachable",
			setup: func(fc *terminaltest.Client) {
				fc.ConnectFn = func(v1beta1.ContainerDoc) error { return errdefs.ErrAttachNotSupported }
			},
			args:    []string{"c1", "--container", "side"},
			wantErr: errdefs.ErrAttachNotSupported,
			wantMsg: `container "side" in cell "c1" is not attachable`,
		},
		{
			name: "task not running",
			setup: func(fc *terminaltest.Client) {
				fc.ConnectFn = func(v1beta1.ContainerDoc) error {
					return fmt.Errorf("%w: container \"work\" has state Stopped", errdefs.ErrAttachTaskNotRunning)
				}
			},
			args:    []string{"c1"},
			wantErr: errdefs.ErrAttachTaskNotRunning,
		},
		{
			name: "container not found",
			setup: func(fc *terminaltest.Client) {
				fc.ConnectFn = func(v1beta1.ContainerDoc) error { return errdefs.ErrContainerNotFound }
			},
			args:    []string{"c1", "--container", "ghost"},
			wantErr: errdefs.ErrContainerNotFound,
			wantMsg: `container "ghost" not found in cell "c1"`,
		},
		{
			name: "no attachable candidate",
			setup: func(fc *terminaltest.Client) {
				fc.ListContainersFn = func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					return []v1beta1.ContainerSpec{{ID: "root", Root: true}, {ID: "side"}}, nil
				}
			},
			args:    []string{"c1"},
			wantErr: errdefs.ErrAttachNoCandidate,
		},
		{
			name: "ambiguous candidates",
			setup: func(fc *terminaltest.Client) {
				fc.ListContainersFn = func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
					return []v1beta1.ContainerSpec{
						{ID: "root", Root: true},
						{ID: "shell", Attachable: true},
						{ID: "claude", Attachable: true},
					}, nil
				}
			},
			args:    []string{"c1"},
			wantErr: errdefs.ErrAttachAmbiguous,
			wantMsg: "claude, shell",
		},
		{
			name: "missing cell",
			setup: func(fc *terminaltest.Client) {
				fc.GetCellFn = func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{}, nil
				}
			},
			args:    []string{"c1"},
			wantMsg: `cell "c1" does not exist`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newScreenClient()
			tc.setup(fc)
			cmd, out := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute returned nil, want an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v in chain", err, tc.wantErr)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty on refusal", out.String())
			}
		})
	}
}

// TestScreenshot_OlderDaemon_FallsBackToSocketPath pins version skew: a
// kukeond without ConnectContainerTerminal still serves AttachContainer,
// so a host kuke screenshots by path.
func TestScreenshot_OlderDaemon_FallsBackToSocketPath(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "socket")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	term := &terminaltest.Terminal{Screen: testScreen()}
	go func() {
		for {
			c, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			term.Serve(c)
		}
	}()

	fc := terminaltest.NewClient()
	fc.ConnectFn = func(v1beta1.ContainerDoc) error {
		return fmt.Errorf("%w: KukeonV1.ConnectContainerTerminal", errdefs.ErrDaemonMethodUnsupported)
	}
	fc.AttachContainerFn = func(v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
		return kukeonv1.AttachContainerResult{HostSocketPath: sock}, nil
	}
	cmd, out := newCmd(t, fc)
	cmd.SetArgs([]string{"c1"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != testScreen().Text {
		t.Errorf("output = %q, want %q", got, testScreen().Text)
	}
}

func TestScreenshot_RequiresCell(t *testing.T) {
	cmd, _ := newCmd(t, newScreenClient())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute with no cell returned nil")
	}
}
