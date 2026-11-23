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
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
	if digest.APIVersion != v1beta1.APIVersionV1Beta1 || digest.Kind != v1beta1.KindHealthDigest ||
		len(digest.Checks) != 5 || digest.Clean ||
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
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindHealthReport,
		Spec: v1beta1.HealthReportSpec{
			Since: until.Add(-time.Hour), Until: until,
			Cell:    kukeonv1.HealthCellRef{Realm: "default", Space: "default", Stack: "default", Name: "sweep"},
			Summary: "nothing to file", Findings: []kukeonv1.HealthFinding{},
		},
	})
	if err != nil || stored.Metadata.Name == "" || stored.Kind != v1beta1.KindHealthReport {
		t.Fatalf("submit: %+v %v", stored, err)
	}
	digest, err = client.ReadHealthDigest(ctx, kukeonv1.HealthDigestRequest{SinceLastReport: true})
	if err != nil || !digest.Since.Equal(until) {
		t.Fatalf("last-report window starts at %v, want %v (err=%v)", digest.Since, until, err)
	}

	list, err := client.ListHealthReports(ctx)
	if err != nil || len(list) != 1 || list[0].ID != stored.Metadata.Name {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if read, readErr := client.ReadHealthReport(ctx, kukeonv1.HealthReportLatest); readErr != nil ||
		read.Metadata.Name != stored.Metadata.Name {
		t.Fatalf("read latest = %+v err=%v", read, readErr)
	}
	if err = client.DeleteHealthReport(ctx, stored.Metadata.Name); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ReadHealthReport(ctx, stored.Metadata.Name); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("read after delete: %v", err)
	}
}

// TestApplyHealthReport drives `kuke apply -f` of a HealthReport document: it
// takes the submit path (assigned createdAt, recurring) and a stored name
// cannot be applied again (issue #1619).
func TestApplyHealthReport(t *testing.T) {
	ctx := context.Background()
	runPath := t.TempDir()
	client := New(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), controller.Options{
		RunPath:          runPath,
		ContainerdSocket: filepath.Join(runPath, "no-containerd.sock"),
	})
	defer func() { _ = client.Close() }()

	doc := func(name string) []byte {
		return []byte(`apiVersion: v1beta1
kind: HealthReport
metadata:
  name: ` + name + `
  realm: kuke-system
spec:
  since: 2026-11-22T09:15:00Z
  until: 2026-11-22T10:15:00Z
  cell: {realm: default, space: default, stack: default, name: sweep}
  summary: one kukeon finding
  findings:
    - {signature: "start cell failed", classification: kukeon, severity: error, evidence: ["log line"]}
`)
	}
	res, err := client.ApplyDocuments(ctx, doc("first"))
	if err != nil || len(res.Resources) != 1 || res.Resources[0].Action != "created" ||
		res.Resources[0].Name != "first" || res.Resources[0].Kind != string(v1beta1.KindHealthReport) {
		t.Fatalf("apply = %+v err=%v", res, err)
	}
	stored, err := client.ReadHealthReport(ctx, "first")
	if err != nil || stored.Spec.CreatedAt.IsZero() || stored.Spec.Findings[0].Recurring {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}

	res, err = client.ApplyDocuments(ctx, doc("first"))
	if err != nil || len(res.Resources) != 1 || res.Resources[0].Action != "failed" ||
		!strings.Contains(res.Resources[0].Error, "already stored") {
		t.Fatalf("re-apply of a stored name = %+v err=%v", res, err)
	}

	time.Sleep(time.Millisecond) // the second report is created after the first
	if res, err = client.ApplyDocuments(ctx, doc("second")); err != nil || res.Resources[0].Action != "created" {
		t.Fatalf("apply second = %+v err=%v", res, err)
	}
	second, err := client.ReadHealthReport(ctx, kukeonv1.HealthReportLatest)
	if err != nil || second.Metadata.Name != "second" || !second.Spec.Findings[0].Recurring {
		t.Fatalf("latest = %+v err=%v", second, err)
	}

	if _, err = client.ApplyDocuments(ctx, []byte("apiVersion: v1beta1\nkind: HealthReport\nmetadata: {name: bad}\n"+
		"spec: {summary: s}\n")); err == nil {
		t.Fatal("an invalid HealthReport must be rejected")
	}
	if _, err = client.ReadHealthReport(ctx, "bad"); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("an invalid report must write nothing: %v", err)
	}

	// The daemon's apply path requires apiVersion and rejects a HealthDigest
	// (#1633).
	unversioned := strings.Replace(string(doc("third")), "apiVersion: v1beta1\n", "", 1)
	if _, err = client.ApplyDocuments(ctx, []byte(unversioned)); err == nil ||
		!strings.Contains(err.Error(), "apiVersion is required (expected v1beta1)") {
		t.Fatalf("a document without apiVersion: %v", err)
	}
	if _, err = client.ReadHealthReport(ctx, "third"); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("a document without apiVersion must write nothing: %v", err)
	}
	digestDoc := []byte("apiVersion: v1beta1\nkind: HealthDigest\nclean: true\n")
	if _, err = client.ApplyDocuments(ctx, digestDoc); err == nil ||
		!strings.Contains(err.Error(), errdefs.ErrUnknownKind.Error()) {
		t.Fatalf("a HealthDigest document: %v", err)
	}
}
