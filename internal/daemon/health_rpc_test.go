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

package daemon_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type healthClientFake struct {
	kukeonv1.FakeClient

	request kukeonv1.HealthDigestRequest
	report  kukeonv1.HealthReport
	readID  string
	err     error
}

func (h *healthClientFake) ReadHealthDigest(
	_ context.Context, req kukeonv1.HealthDigestRequest,
) (kukeonv1.HealthDigest, error) {
	h.request = req
	return kukeonv1.HealthDigest{Clean: true, SchemaVersion: kukeonv1.HealthSchemaVersion}, h.err
}

func (h *healthClientFake) SubmitHealthReport(
	_ context.Context, report kukeonv1.HealthReport,
) (kukeonv1.HealthReport, error) {
	h.report = report
	report.ID = "stored"
	return report, h.err
}

func (h *healthClientFake) ListHealthReports(context.Context) ([]kukeonv1.HealthReportSummary, error) {
	return []kukeonv1.HealthReportSummary{{ID: "stored", Findings: 2}}, h.err
}

func (h *healthClientFake) ReadHealthReport(_ context.Context, id string) (kukeonv1.HealthReport, error) {
	h.readID = id
	return kukeonv1.HealthReport{ID: "stored"}, h.err
}

func TestHealthRPCsForwardRequestsAndResults(t *testing.T) {
	core := &healthClientFake{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	digestArgs := &kukeonv1.ReadHealthDigestArgs{Request: kukeonv1.HealthDigestRequest{SinceLastReport: true}}
	digestReply := &kukeonv1.ReadHealthDigestReply{}
	if err := svc.ReadHealthDigest(digestArgs, digestReply); err != nil || digestReply.Err != nil ||
		!digestReply.Result.Clean || !core.request.SinceLastReport {
		t.Fatalf("digest: reply=%+v err=%v", digestReply, err)
	}

	submitted := kukeonv1.HealthReport{Summary: "s", Until: time.Unix(10, 0).UTC()}
	submitReply := &kukeonv1.SubmitHealthReportReply{}
	if err := svc.SubmitHealthReport(&kukeonv1.SubmitHealthReportArgs{Report: submitted}, submitReply); err != nil ||
		submitReply.Err != nil || submitReply.Result.ID != "stored" || !reflect.DeepEqual(core.report, submitted) {
		t.Fatalf("submit: reply=%+v err=%v", submitReply, err)
	}

	listReply := &kukeonv1.ListHealthReportsReply{}
	if err := svc.ListHealthReports(&kukeonv1.ListHealthReportsArgs{}, listReply); err != nil ||
		len(listReply.Result) != 1 || listReply.Result[0].Findings != 2 {
		t.Fatalf("list: reply=%+v err=%v", listReply, err)
	}

	readReply := &kukeonv1.ReadHealthReportReply{}
	if err := svc.ReadHealthReport(&kukeonv1.ReadHealthReportArgs{ID: "latest"}, readReply); err != nil ||
		readReply.Result.ID != "stored" || core.readID != "latest" {
		t.Fatalf("read: reply=%+v err=%v", readReply, err)
	}
}

func TestHealthRPCsSurfaceSentinels(t *testing.T) {
	for _, sentinel := range []error{errdefs.ErrHealthReportNotFound, errdefs.ErrHealthReportInvalid} {
		svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), &healthClientFake{err: sentinel})
		readReply := &kukeonv1.ReadHealthReportReply{}
		if err := svc.ReadHealthReport(&kukeonv1.ReadHealthReportArgs{ID: "x"}, readReply); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(kukeonv1.FromAPIError(readReply.Err), sentinel) {
			t.Fatalf("wire error %+v does not unwrap to %v", readReply.Err, sentinel)
		}
	}
}
