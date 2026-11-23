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

package healthreport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/kuke/get/healthreport"
	healthcmd "github.com/eminwux/kukeon/cmd/kuke/health"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// storeClient serves the report RPCs from a real health store.
type storeClient struct {
	kukeonv1.FakeClient

	store *health.Store
}

func (c *storeClient) Close() error { return nil }

func (c *storeClient) ListHealthReports(context.Context) ([]kukeonv1.HealthReportSummary, error) {
	return c.store.List()
}

func (c *storeClient) ReadHealthReport(_ context.Context, name string) (kukeonv1.HealthReport, error) {
	return c.store.Read(name)
}

func newStoreClient(t *testing.T, names ...string) *storeClient {
	t.Helper()
	store := health.NewStore(t.TempDir())
	at := time.Date(2026, 11, 22, 10, 15, 0, 0, time.UTC)
	for i, name := range names {
		_, err := store.Submit(kukeonv1.HealthReport{
			APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindHealthReport,
			Metadata: v1beta1.HealthReportMetadata{Name: name},
			Spec: v1beta1.HealthReportSpec{
				CreatedAt: at.Add(time.Duration(i) * time.Hour),
				Since:     at.Add(-time.Hour), Until: at.Add(time.Duration(i) * time.Hour),
				Cell:    kukeonv1.HealthCellRef{Realm: "default", Space: "default", Stack: "default", Name: "sweep"},
				Summary: "summary of " + name,
				Findings: []kukeonv1.HealthFinding{{
					Signature: "start cell failed", Classification: kukeonv1.HealthClassKukeon,
					Severity: kukeonv1.HealthSeverityError, Evidence: []string{"log"},
				}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return &storeClient{store: store}
}

func runGet(t *testing.T, client kukeonv1.Client, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := healthreport.NewHealthReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SetContext(context.WithValue(context.Background(), healthreport.MockControllerKey{}, client))
	err := cmd.Execute()
	return out.String(), err
}

func runHealthReports(t *testing.T, client kukeonv1.Client) string {
	t.Helper()
	cmd := healthcmd.NewHealthCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"reports"})
	cmd.SetContext(context.WithValue(context.Background(), healthcmd.MockClientKey{}, client))
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestGetHealthReportsPrintsWhatKukeHealthReportsPrints(t *testing.T) {
	for _, names := range [][]string{nil, {"first", "second"}} {
		client := newStoreClient(t, names...)
		got, err := runGet(t, client)
		if err != nil {
			t.Fatal(err)
		}
		if want := runHealthReports(t, client); got != want {
			t.Fatalf("kuke get healthreports:\n%s\nkuke health reports:\n%s", got, want)
		}
	}
}

func TestGetHealthReportOne(t *testing.T) {
	client := newStoreClient(t, "first", "second")

	out, err := runGet(t, client, "latest")
	if err != nil || !strings.Contains(out, "second") || strings.Contains(out, "first") {
		t.Fatalf("latest table: %q %v", out, err)
	}

	out, err = runGet(t, client, "first", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc v1beta1.HealthReportDoc
	if err = yaml.Unmarshal([]byte(out), &doc); err != nil || doc.Kind != v1beta1.KindHealthReport ||
		doc.APIVersion != v1beta1.APIVersionV1Beta1 || doc.Metadata.Name != "first" ||
		doc.Metadata.Realm != "kuke-system" || doc.Spec.Summary != "summary of first" {
		t.Fatalf("yaml document:\n%s (%v)", out, err)
	}
	if strings.Contains(out, "schemaVersion") {
		t.Fatalf("a report carries no schemaVersion:\n%s", out)
	}

	out, err = runGet(t, client, "second", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	doc = v1beta1.HealthReportDoc{}
	if err = json.Unmarshal([]byte(out), &doc); err != nil || doc.Metadata.Name != "second" ||
		len(doc.Spec.Findings) != 1 || !doc.Spec.Findings[0].Recurring {
		t.Fatalf("json document:\n%s (%v)", out, err)
	}

	if _, err = runGet(t, client, "missing"); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("missing report: %v", err)
	}
}
