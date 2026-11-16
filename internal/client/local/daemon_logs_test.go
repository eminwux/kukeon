// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestDaemonLogLocateError_MissingScopeIsUnavailable(t *testing.T) {
	for _, sentinel := range []error{
		errdefs.ErrContainerNotFound, errdefs.ErrCellNotFound, errdefs.ErrStackNotFound,
		errdefs.ErrSpaceNotFound, errdefs.ErrRealmNotFound,
	} {
		err := daemonLogLocateError(sentinel)
		if !errors.Is(err, errdefs.ErrDaemonLogUnavailable) {
			t.Fatalf("%v: got %v", sentinel, err)
		}
		// Only one sentinel may travel, so the wire kind is unambiguous.
		if errors.Is(err, sentinel) {
			t.Fatalf("%v must not be wrapped alongside ErrDaemonLogUnavailable", sentinel)
		}
		if kind := kukeonv1.KindFromError(err); kind != "DaemonLogUnavailable" {
			t.Fatalf("wire kind %q", kind)
		}
	}
	other := errors.New("metadata read failed")
	if err := daemonLogLocateError(other); !errors.Is(err, other) || errors.Is(err, errdefs.ErrDaemonLogUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestReadDaemonLogFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "log")
	_, err := readDaemonLogFile(missing, kukeonv1.DaemonLogRequest{Offset: -1})
	if !errors.Is(err, errdefs.ErrDaemonLogUnavailable) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing: %v", err)
	}
	if err = os.WriteFile(missing, []byte("level=INFO msg=ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := readDaemonLogFile(missing, kukeonv1.DaemonLogRequest{Offset: -1})
	if err != nil || res.Data != "level=INFO msg=ready\n" {
		t.Fatalf("read: %+v %v", res, err)
	}
	if os.Geteuid() != 0 {
		if err = os.Chmod(missing, 0); err != nil {
			t.Fatal(err)
		}
		_, err = readDaemonLogFile(missing, kukeonv1.DaemonLogRequest{Offset: -1})
		if !errors.Is(err, fs.ErrPermission) || kukeonv1.KindFromError(err) != "PermissionDenied" {
			t.Fatalf("permission: %v", err)
		}
	}
}
