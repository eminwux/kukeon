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
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// TestHealthDigestWindowAndReports drives the in-process client — what
// kukeond runs behind the socket — over an empty run path with no containerd.
func TestHealthDigestWindowAndReports(t *testing.T) {
	ctx := context.Background()
	runPath := t.TempDir()
	client := New(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), controller.Options{
		RunPath:          runPath,
		ContainerdSocket: filepath.Join(runPath, "no-containerd.sock"),
		KukeondSocket:    filepath.Join(runPath, "run", "kukeond.sock"),
	})
	defer func() { _ = client.Close() }()

	digest, err := client.ReadHealthDigest(ctx, kukeonv1.HealthDigestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := digest.Until.Sub(digest.Since); got != kukeonv1.DefaultHealthWindow {
		t.Fatalf("default window = %v", got)
	}
	if digest.SchemaVersion != kukeonv1.HealthSchemaVersion || len(digest.Checks) != 5 || digest.Clean ||
		digest.LogNote == "" || digest.Cells == nil || digest.LogErrors == nil {
		t.Fatalf("digest = %+v", digest)
	}
	if digest.Checks[0].Name != "containerd" || digest.Checks[0].Status != kukeonv1.HealthStatusFail {
		t.Fatalf("a missing containerd socket must fail the check: %+v", digest.Checks[0])
	}

	// With no report stored, last-report falls back to the default window.
	digest, err = client.ReadHealthDigest(ctx, kukeonv1.HealthDigestRequest{SinceLastReport: true})
	if err != nil || digest.Until.Sub(digest.Since) != kukeonv1.DefaultHealthWindow {
		t.Fatalf("fallback window = %v err=%v", digest.Until.Sub(digest.Since), err)
	}

	until := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	stored, err := client.SubmitHealthReport(ctx, kukeonv1.HealthReport{
		Since: until.Add(-time.Hour), Until: until,
		Cell:    kukeonv1.HealthCellRef{Realm: "default", Space: "default", Stack: "default", Name: "sweep"},
		Summary: "nothing to file", Findings: []kukeonv1.HealthFinding{},
	})
	if err != nil || stored.ID == "" {
		t.Fatalf("submit: %+v %v", stored, err)
	}
	digest, err = client.ReadHealthDigest(ctx, kukeonv1.HealthDigestRequest{SinceLastReport: true})
	if err != nil || !digest.Since.Equal(until) {
		t.Fatalf("last-report window starts at %v, want %v (err=%v)", digest.Since, until, err)
	}

	list, err := client.ListHealthReports(ctx)
	if err != nil || len(list) != 1 || list[0].ID != stored.ID {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if read, readErr := client.ReadHealthReport(ctx, kukeonv1.HealthReportLatest); readErr != nil ||
		read.ID != stored.ID {
		t.Fatalf("read latest = %+v err=%v", read, readErr)
	}
}
