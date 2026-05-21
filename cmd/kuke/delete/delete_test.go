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

package deletecmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	deletecmd "github.com/eminwux/kukeon/cmd/kuke/delete"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewDeleteCmdMetadata(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "use statement",
			check: func(t *testing.T, cmd *cobra.Command) {
				if cmd.Use != "delete [name]" {
					t.Fatalf("expected Use to be %q, got %q", "delete", cmd.Use)
				}
			},
		},
		{
			name: "short description",
			check: func(t *testing.T, cmd *cobra.Command) {
				expected := "Delete Kukeon resources (realm, space, stack, cell, container, secret)"
				if cmd.Short != expected {
					t.Fatalf("expected Short to be %q, got %q", expected, cmd.Short)
				}
			},
		},
		{
			name: "run invokes help",
			check: func(t *testing.T, cmd *cobra.Command) {
				buf := &bytes.Buffer{}
				cmd.SetOut(buf)
				cmd.SetErr(buf)

				// RunE should show help when no -f flag is provided
				err := cmd.RunE(cmd, nil)
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}

				output := buf.String()
				if !strings.Contains(output, "Usage:") {
					t.Fatalf("expected help output to contain %q, got %q", "Usage:", output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()
			tt.check(t, cmd)
		})
	}
}

func TestNewDeleteCmdPersistentFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		flagName    string
		defaultVal  bool
		description string
		viperKey    string
	}{
		{
			name:        "cascade flag",
			flagName:    "cascade",
			defaultVal:  false,
			description: "Automatically delete child resources recursively (does not apply to containers)",
			viperKey:    config.KUKE_DELETE_CASCADE.ViperKey,
		},
		{
			name:        "force flag",
			flagName:    "force",
			defaultVal:  false,
			description: "Skip validation and attempt deletion anyway",
			viperKey:    config.KUKE_DELETE_FORCE.ViperKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()

			flag := cmd.PersistentFlags().Lookup(tt.flagName)
			if flag == nil {
				t.Fatalf("expected persistent flag %q to be registered", tt.flagName)
			}

			if flag.Usage != tt.description {
				t.Fatalf("expected flag %q description to be %q, got %q", tt.flagName, tt.description, flag.Usage)
			}

			// Check default value
			val, err := cmd.PersistentFlags().GetBool(tt.flagName)
			if err != nil {
				t.Fatalf("failed to get flag %q: %v", tt.flagName, err)
			}
			if val != tt.defaultVal {
				t.Fatalf("expected flag %q default to be %v, got %v", tt.flagName, tt.defaultVal, val)
			}
		})
	}
}

func TestNewDeleteCmdViperBindings(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name     string
		flagName string
		viperKey string
		value    bool
	}{
		{
			name:     "cascade flag binding",
			flagName: "cascade",
			viperKey: config.KUKE_DELETE_CASCADE.ViperKey,
			value:    true,
		},
		{
			name:     "force flag binding",
			flagName: "force",
			viperKey: config.KUKE_DELETE_FORCE.ViperKey,
			value:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := deletecmd.NewDeleteCmd()

			if err := cmd.PersistentFlags().Set(tt.flagName, "true"); err != nil {
				t.Fatalf("failed to set flag %q: %v", tt.flagName, err)
			}

			got := viper.GetBool(tt.viperKey)
			if got != tt.value {
				t.Fatalf("expected viper key %q to be %v, got %v", tt.viperKey, tt.value, got)
			}
		})
	}
}

func TestNewDeleteCmdRegistersSubcommands(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "realm"},
		{name: "space"},
		{name: "stack"},
		{name: "cell"},
		{name: "container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := deletecmd.NewDeleteCmd()
			if findSubCommand(cmd, tt.name) == nil {
				t.Fatalf("expected %q subcommand to be registered", tt.name)
			}
		})
	}
}

func TestNewDeleteCmd_AutocompleteRegistration(t *testing.T) {
	cmd := deletecmd.NewDeleteCmd()

	// Verify that ValidArgsFunction is registered (completion function registration is verified by Cobra)
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be registered")
	}

	// Test the completion function directly
	completions, _ := cmd.ValidArgsFunction(cmd, []string{}, "")
	expected := []string{"realm", "space", "stack", "cell", "container", "secret"}
	if len(completions) != len(expected) {
		t.Fatalf("expected %d completions, got %d", len(expected), len(completions))
	}

	for _, exp := range expected {
		found := false
		for _, comp := range completions {
			if comp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected completion %q not found", exp)
		}
	}

	// Test prefix filtering
	filtered, _ := cmd.ValidArgsFunction(cmd, []string{}, "c")
	expectedFiltered := []string{"cell", "container"}
	if len(filtered) != len(expectedFiltered) {
		t.Fatalf("expected %d filtered completions, got %d", len(expectedFiltered), len(filtered))
	}

	for _, exp := range expectedFiltered {
		found := false
		for _, comp := range filtered {
			if comp == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected filtered completion %q not found", exp)
		}
	}
}

func findSubCommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, sc := range cmd.Commands() {
		if sc.Name() == name || sc.HasAlias(name) {
			return sc
		}
	}
	return nil
}

// fakeClient is the daemon-aware test seam for `kuke delete -f`. Embedding
// kukeonv1.FakeClient defaults every unimplemented method to ErrUnexpectedCall,
// which is the regression guard against this code path silently invoking
// anything other than DeleteDocuments. The bug fixed in #574 is exactly this
// invariant: `delete -f` must route through the Client (so `--host`/daemon
// routing applies), not bypass it to construct an in-process controller.
type fakeClient struct {
	kukeonv1.FakeClient

	deleteFn func(raw []byte, cascade, force bool) (kukeonv1.DeleteDocumentsResult, error)
}

func (f *fakeClient) DeleteDocuments(
	_ context.Context,
	raw []byte,
	cascade, force bool,
) (kukeonv1.DeleteDocumentsResult, error) {
	if f.deleteFn == nil {
		return kukeonv1.DeleteDocumentsResult{}, errors.New("unexpected DeleteDocuments call")
	}
	return f.deleteFn(raw, cascade, force)
}

func TestNewDeleteCmd_FileFlag(t *testing.T) {
	cmd := deletecmd.NewDeleteCmd()
	fileFlag := cmd.Flags().Lookup("file")
	if fileFlag == nil {
		t.Fatal("expected 'file' flag to exist")
	}
	if fileFlag.Usage != "File to read YAML from (use - for stdin)" {
		t.Errorf("unexpected file flag usage: %q", fileFlag.Usage)
	}
}

func TestNewDeleteCmd_OutputFlag(t *testing.T) {
	cmd := deletecmd.NewDeleteCmd()
	outputFlag := cmd.Flags().Lookup("output")
	if outputFlag == nil {
		t.Fatal("expected 'output' flag to exist")
	}
	if outputFlag.Usage != "Output format: json, yaml (default: human-readable)" {
		t.Errorf("unexpected output flag usage: %q", outputFlag.Usage)
	}
}

func TestNewDeleteCmd_RunE_FileNotFound(t *testing.T) {
	cmd := deletecmd.NewDeleteCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", "/nonexistent/file.yaml"})
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open file") {
		t.Errorf("expected error about file opening, got: %v", err)
	}
}

func TestNewDeleteCmd_RunE_InvalidYAML(t *testing.T) {
	// Create temporary file with invalid YAML
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	const invalid = "invalid: yaml: ["
	if _, err = tmpFile.WriteString(invalid); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	// Surface the invalid YAML through the client by having the fake echo
	// the same shape the daemon would: validation rejection bubbled as an
	// error from DeleteDocuments.
	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{}, errors.New("failed to parse YAML: invalid")
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	err = cmd.Execute()

	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse YAML") {
		t.Errorf("expected error about YAML parsing, got: %v", err)
	}
}

func TestNewDeleteCmd_RunE_Success(t *testing.T) {
	// Create temporary file with valid YAML
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(raw []byte, cascade, force bool) (kukeonv1.DeleteDocumentsResult, error) {
			if !bytes.Contains(raw, []byte("test-realm")) {
				t.Errorf("expected raw YAML to contain test-realm, got %q", string(raw))
			}
			if cascade {
				t.Error("expected cascade to be false")
			}
			if force {
				t.Error("expected force to be false")
			}
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "deleted"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, `Realm "test-realm": deleted`) {
		t.Errorf("expected output to contain 'Realm \"test-realm\": deleted', got: %q", output)
	}
}

func TestNewDeleteCmd_RunE_NotFound(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "not found"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error for not found (idempotent), got: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, `Realm "test-realm": not found`) {
		t.Errorf("expected output to contain 'Realm \"test-realm\": not found', got: %q", output)
	}
}

func TestNewDeleteCmd_RunE_CascadeFlag(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, cascade, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			if !cascade {
				t.Error("expected cascade to be true")
			}
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{
						Index:    0,
						Kind:     "Realm",
						Name:     "test-realm",
						Action:   "deleted",
						Cascaded: []string{"default"},
					},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name(), "--cascade"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, `Realm "test-realm": deleted`) {
		t.Errorf("expected output to contain 'Realm \"test-realm\": deleted', got: %q", output)
	}
	if !strings.Contains(output, "1 space(s) deleted (cascade)") {
		t.Errorf("expected output to contain cascaded resources, got: %q", output)
	}
}

func TestNewDeleteCmd_RunE_ForceFlag(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, force bool) (kukeonv1.DeleteDocumentsResult, error) {
			if !force {
				t.Error("expected force to be true")
			}
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "deleted"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name(), "--force"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestNewDeleteCmd_RunE_FailedExit(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "failed", Error: "boom"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected non-nil error when daemon reports failed action")
	}
	if !strings.Contains(err.Error(), "some resources failed to delete") {
		t.Errorf("expected aggregated failure error, got: %v", err)
	}
	if !strings.Contains(outBuf.String(), "Error: boom") {
		t.Errorf("expected per-resource error in output, got: %q", outBuf.String())
	}
}

func TestNewDeleteCmd_RunE_ClientError(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{}, errors.New("dial unix: server unavailable")
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected error to bubble from client, got nil")
	}
	if !strings.Contains(err.Error(), "server unavailable") {
		t.Errorf("expected wrapped client error, got: %v", err)
	}
}

func TestNewDeleteCmd_RunE_JSONOutput(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "deleted"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name(), "--output", "json"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := outBuf.String()
	// JSON keys must remain lowercase (preserved via DeleteResourceResult tags
	// — net/rpc gob wire is unaffected).
	if !strings.Contains(output, `"kind"`) || !strings.Contains(output, "Realm") {
		t.Errorf("expected JSON output with kind and Realm, got: %q", output)
	}
	if !strings.Contains(output, `"name"`) || !strings.Contains(output, "test-realm") {
		t.Errorf("expected JSON output with name and test-realm, got: %q", output)
	}
	if !strings.Contains(output, `"action"`) || !strings.Contains(output, "deleted") {
		t.Errorf("expected JSON output with action and deleted, got: %q", output)
	}
}

func TestNewDeleteCmd_RunE_Stdin(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`

	cmd := deletecmd.NewDeleteCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeClient{
		deleteFn: func(raw []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			if !bytes.Contains(raw, []byte("test-realm")) {
				t.Errorf("expected raw YAML to contain test-realm, got %q", string(raw))
			}
			return kukeonv1.DeleteDocumentsResult{
				Resources: []kukeonv1.DeleteResourceResult{
					{Index: 0, Kind: "Realm", Name: "test-realm", Action: "deleted"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	// Create a pipe to simulate stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}

	go func() {
		defer w.Close()
		_, _ = w.WriteString(yaml)
	}()

	// Replace os.Stdin temporarily
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		r.Close()
	}()

	cmd.SetArgs([]string{"-f", "-"})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestNewDeleteCmd_RunE_ValidationError(t *testing.T) {
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: ""
spec:
  namespace: test-ns
`
	if _, err = tmpFile.WriteString(yaml); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := deletecmd.NewDeleteCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	// Validation now happens client-side (local) or server-side (daemon);
	// either path returns the validation error through DeleteDocuments.
	fakeCtrl := &fakeClient{
		deleteFn: func(_ []byte, _, _ bool) (kukeonv1.DeleteDocumentsResult, error) {
			return kukeonv1.DeleteDocumentsResult{}, errors.New("validation failed:\n  metadata.name is required")
		},
	}
	ctx = context.WithValue(ctx, deletecmd.MockControllerKey{}, kukeonv1.Client(fakeCtrl))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	err = cmd.Execute()

	if err == nil {
		t.Fatal("expected error for validation failure, got nil")
	}
	if !strings.Contains(err.Error(), "validation") {
		t.Errorf("expected error about validation, got: %v", err)
	}
}
