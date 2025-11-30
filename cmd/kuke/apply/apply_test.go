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

package apply_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/apply"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
)

type fakeApplyController struct {
	applyDocumentsFn func(docs []parser.Document) (controller.ApplyResult, error)
}

func (f *fakeApplyController) ApplyDocuments(docs []parser.Document) (controller.ApplyResult, error) {
	if f.applyDocumentsFn == nil {
		return controller.ApplyResult{}, nil
	}
	return f.applyDocumentsFn(docs)
}

func TestNewApplyCmd(t *testing.T) {
	cmd := apply.NewApplyCmd()
	if cmd == nil {
		t.Fatal("expected command to be created")
	}
	if cmd.Use != "apply -f <file>" {
		t.Errorf("expected Use to be 'apply -f <file>', got %q", cmd.Use)
	}
}

func TestNewApplyCmd_FileFlag(t *testing.T) {
	cmd := apply.NewApplyCmd()
	fileFlag := cmd.Flags().Lookup("file")
	if fileFlag == nil {
		t.Fatal("expected 'file' flag to exist")
	}
	if fileFlag.Usage != "File to read YAML from (use - for stdin)" {
		t.Errorf("unexpected file flag usage: %q", fileFlag.Usage)
	}
}

func TestNewApplyCmd_RunE_ValidationError(t *testing.T) {
	cmd := apply.NewApplyCmd()
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

func TestNewApplyCmd_RunE_InvalidYAML(t *testing.T) {
	// Create temporary file with invalid YAML
	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("invalid: yaml: [")
	if err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := apply.NewApplyCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
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

func TestNewApplyCmd_RunE_Success(t *testing.T) {
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
	_, err = tmpFile.WriteString(yaml)
	if err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	cmd := apply.NewApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeApplyController{
		applyDocumentsFn: func(docs []parser.Document) (controller.ApplyResult, error) {
			if len(docs) != 1 {
				t.Errorf("expected 1 document, got %d", len(docs))
			}
			return controller.ApplyResult{
				Resources: []controller.ResourceResult{
					{
						Index:  0,
						Kind:   "Realm",
						Name:   "test-realm",
						Action: "created",
					},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, fakeCtrl)
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", tmpFile.Name()})
	err = cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, "Realm \"test-realm\": created") {
		t.Errorf("expected output to contain 'Realm \"test-realm\": created', got: %q", output)
	}
}

func TestNewApplyCmd_RunE_Stdin(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: Realm
metadata:
  name: test-realm
spec:
  namespace: test-ns
`

	cmd := apply.NewApplyCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fakeCtrl := &fakeApplyController{
		applyDocumentsFn: func(_ []parser.Document) (controller.ApplyResult, error) {
			return controller.ApplyResult{
				Resources: []controller.ResourceResult{
					{
						Index:  0,
						Kind:   "Realm",
						Name:   "test-realm",
						Action: "created",
					},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, fakeCtrl)
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
	err = cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestNewApplyCmd_TestdataFixtures(t *testing.T) {
	testdataDir := "testdata"
	files := []string{
		"realm.yaml",
		"space.yaml",
		"stack.yaml",
		"cell.yaml",
		"cell-updated.yaml",
		"cell-removed-container.yaml",
		"multi-resource.yaml",
	}

	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(testdataDir, file)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("test fixture %q not found", path)
			}

			// Just verify the file can be parsed
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("failed to open fixture: %v", err)
			}
			defer f.Close()

			docs, err := parser.ParseDocuments(f)
			if err != nil {
				t.Fatalf("failed to parse fixture: %v", err)
			}

			if len(docs) == 0 {
				t.Fatal("expected at least one document")
			}

			// Parse and validate each document
			for i, rawDoc := range docs {
				doc, parseErr := parser.ParseDocument(i, rawDoc)
				if parseErr != nil {
					t.Fatalf("failed to parse document %d: %v", i, parseErr)
				}

				validationErr := parser.ValidateDocument(doc)
				if validationErr != nil {
					t.Fatalf("document %d failed validation: %v", i, validationErr)
				}
			}
		})
	}
}
