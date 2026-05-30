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

package cellblueprint_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestParseParamArgs_HappyPath(t *testing.T) {
	got, err := cellblueprint.ParseParamArgs([]string{
		"A=alpha",
		"B=value with spaces",
		"C=",
		"D=has=equals",
	})
	if err != nil {
		t.Fatalf("ParseParamArgs: %v", err)
	}
	want := map[string]string{
		"A": "alpha",
		"B": "value with spaces",
		"C": "",
		"D": "has=equals",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseParamArgs_MissingEquals_Errors(t *testing.T) {
	_, err := cellblueprint.ParseParamArgs([]string{"NOEQ"})
	if !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err=%v want ErrBlueprintInvalid", err)
	}
}

func TestParseParamArgs_InvalidName_Errors(t *testing.T) {
	_, err := cellblueprint.ParseParamArgs([]string{"1BAD=x"})
	if !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err=%v want ErrBlueprintInvalid", err)
	}
}

func TestParseParamFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.params")
	body := "# leading comment\n" +
		"\n" +
		"  # indented comment\n" +
		"A=alpha\n" +
		"B=value with spaces\n" +
		"C=\n" +
		"D=has=equals\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := cellblueprint.ParseParamFile(path)
	if err != nil {
		t.Fatalf("ParseParamFile: %v", err)
	}
	want := map[string]string{
		"A": "alpha",
		"B": "value with spaces",
		"C": "",
		"D": "has=equals",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseParamFile_BadLine_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.params")
	if err := os.WriteFile(path, []byte("OK=1\nBAD-LINE\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := cellblueprint.ParseParamFile(path)
	if !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err=%v want ErrBlueprintInvalid", err)
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("err %q must name the offending line number", err)
	}
}

func TestMergeParams_OverridesWin(t *testing.T) {
	got := cellblueprint.MergeParams(
		map[string]string{"A": "from-base", "B": "kept"},
		map[string]string{"A": "from-override", "C": "added"},
	)
	want := map[string]string{
		"A": "from-override",
		"B": "kept",
		"C": "added",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
