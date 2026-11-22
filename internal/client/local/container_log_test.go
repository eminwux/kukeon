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
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestReadContainerLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")

	res, err := readContainerLogFile(path, kukeonv1.ContainerLogRequest{})
	if !errors.Is(err, errdefs.ErrContainerLogUnavailable) || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing: %v", err)
	}
	if res.Source != path {
		t.Fatalf("missing: Source %q, want %q so callers can name the path", res.Source, path)
	}
	if kind := kukeonv1.KindFromError(err); kind != "ContainerLogUnavailable" {
		t.Fatalf("missing: wire kind %q", kind)
	}

	content := []byte("$ \x1b[0mprompt\xff")
	if err = os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = readContainerLogFile(path, kukeonv1.ContainerLogRequest{})
	if err != nil || !bytes.Equal(res.Data, content) || res.Source != path {
		t.Fatalf("read: %+v %v", res, err)
	}

	if os.Geteuid() != 0 {
		if err = os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		_, err = readContainerLogFile(path, kukeonv1.ContainerLogRequest{})
		if !errors.Is(err, fs.ErrPermission) || kukeonv1.KindFromError(err) != "PermissionDenied" {
			t.Fatalf("permission: %v", err)
		}
	}
}
