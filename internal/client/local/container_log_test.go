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

	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	kfs "github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestReadContainerLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")

	res, err := readContainerLogFile(path, kukeonv1.ContainerLogRequest{}, containerlog.Read)
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
	res, err = readContainerLogFile(path, kukeonv1.ContainerLogRequest{}, containerlog.Read)
	if err != nil || !bytes.Equal(res.Data, content) || res.Source != path {
		t.Fatalf("read: %+v %v", res, err)
	}

	if os.Geteuid() != 0 {
		if err = os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		_, err = readContainerLogFile(path, kukeonv1.ContainerLogRequest{}, containerlog.Read)
		if !errors.Is(err, fs.ErrPermission) || kukeonv1.KindFromError(err) != "PermissionDenied" {
			t.Fatalf("permission: %v", err)
		}
	}
}

// TestPickContainerStream pins which file, and which format, `kuke log` and
// the web log view read for a container (issue #1783). The format follows the
// file chosen: a runtime log whose first line is a JSON object stays raw.
func TestPickContainerStream(t *testing.T) {
	runPath := t.TempDir()
	attachable := intmodel.ContainerSpec{
		ID: "work", RealmName: "r", SpaceName: "s", StackName: "st", CellName: "c", Attachable: true,
	}
	plain := attachable
	plain.Attachable = false
	cast := kfs.ContainerCapturePath(runPath, "r", "s", "st", "c", "work")
	raw := kfs.ContainerRawCapturePath(runPath, "r", "s", "st", "c", "work")
	runtimeLog := kfs.ContainerLogPath(runPath, "r", "s", "st", "c", "work")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, spec intmodel.ContainerSpec, wantPath, wantFormat string) {
		t.Helper()
		got := pickContainerStream(runPath, spec)
		if got.path != wantPath || got.format != wantFormat || got.attachable != spec.Attachable {
			t.Fatalf("%s: got %+v, want %s (%s)", name, got, wantPath, wantFormat)
		}
	}

	if filepath.Base(cast) != "capture.cast" || filepath.Base(raw) != "capture.raw" ||
		filepath.Dir(cast) != filepath.Dir(raw) {
		t.Fatalf("capture paths %s, %s", cast, raw)
	}
	check("nothing written yet", attachable, cast, containerlog.FormatAsciicast)
	write(raw, "$ ls\r\n")
	check("started before the change", attachable, raw, containerlog.FormatRaw)
	write(cast, `{"version":2,"width":80,"height":24}`+"\n")
	check("restarted since", attachable, cast, containerlog.FormatAsciicast)

	write(runtimeLog, `{"version":2,"width":80,"height":24}`+"\n"+`[0.1,"o","x"]`+"\n")
	check("runtime log starting with JSON", plain, runtimeLog, containerlog.FormatRaw)
	res, err := readContainerLogFile(runtimeLog, kukeonv1.ContainerLogRequest{}, containerlog.Read)
	if err != nil || !strings.HasPrefix(string(res.Data), `{"version":2`) {
		t.Fatalf("runtime log: %q, %v", res.Data, err)
	}
}

// TestSupervisorLogPath pins how `kuke log --supervisor` finds the supervisor
// log on the host (issue #1721).
func TestSupervisorLogPath(t *testing.T) {
	const runPath = "/opt/kukeon"
	base := intmodel.ContainerSpec{
		ID: "work", RealmName: "r", SpaceName: "s", StackName: "st", CellName: "c", Attachable: true,
	}
	ttyDir := kfs.ContainerTTYDir(runPath, "r", "s", "st", "c", "work")
	withLogFile := func(path string) intmodel.ContainerSpec {
		spec := base
		spec.Tty = &intmodel.ContainerTty{LogFile: path}
		return spec
	}
	nonAttachable := base
	nonAttachable.Attachable = false

	cases := []struct {
		name    string
		spec    intmodel.ContainerSpec
		want    string
		wantErr error
		errHas  string
	}{
		{name: "default", spec: base, want: ttyDir + "/supervisor.log"},
		{name: "empty tty", spec: withLogFile(""), want: ttyDir + "/supervisor.log"},
		{name: "override inside tty", spec: withLogFile("/run/kukeon/tty/logs/k.log"), want: ttyDir + "/logs/k.log"},
		{name: "override uncleaned", spec: withLogFile("/run/kukeon/tty/./x/../k.log"), want: ttyDir + "/k.log"},
		{
			name: "override outside tty", spec: withLogFile("/var/log/kuketty.log"),
			wantErr: errdefs.ErrSupervisorLogUnreadable, errHas: "/var/log/kuketty.log",
		},
		{
			name: "override escaping tty", spec: withLogFile("/run/kukeon/tty/../secret"),
			wantErr: errdefs.ErrSupervisorLogUnreadable, errHas: "/run/kukeon/tty/../secret",
		},
		{
			name: "override sibling prefix", spec: withLogFile("/run/kukeon/tty2/k.log"),
			wantErr: errdefs.ErrSupervisorLogUnreadable,
		},
		{
			name: "override tty dir itself", spec: withLogFile("/run/kukeon/tty"),
			wantErr: errdefs.ErrSupervisorLogUnreadable,
		},
		{
			name: "relative override", spec: withLogFile("tty/k.log"),
			wantErr: errdefs.ErrSupervisorLogUnreadable,
		},
		{name: "not attachable", spec: nonAttachable, wantErr: errdefs.ErrNoSupervisor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := supervisorLogPath(runPath, tc.spec)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("supervisorLogPath = %q, %v; want %v naming %q", got, err, tc.wantErr, tc.errHas)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("supervisorLogPath = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestReadContainerLog_StreamEcho pins that every result echoes the stream,
// errors included, so a client can tell a current kukeond from one that
// predates ContainerLogRequest.Stream.
func TestReadContainerLog_StreamEcho(t *testing.T) {
	c := &Client{}
	res, err := c.ReadContainerLog(t.Context(), v1beta1.ContainerDoc{}, kukeonv1.ContainerLogRequest{Stream: "bogus"})
	if !errors.Is(err, errdefs.ErrContainerLogStreamInvalid) || res.Stream != "bogus" {
		t.Fatalf("bogus stream: %+v, %v", res, err)
	}
}
