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

package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const healthReportDoc = `apiVersion: v1beta1
kind: HealthReport
metadata: { name: 20261122T101500.000000000Z, realm: kuke-system }
spec:
  createdAt: 2026-11-22T10:15:00Z
  since: 2026-11-22T09:15:00Z
  until: 2026-11-22T10:15:00Z
  cell: { realm: default, space: default, stack: default, name: kuke-majordomo-claude-health-sweep-1a2b3c }
  summary: one kukeon finding, one workload finding
  findings:
    - { signature: "start cell failed", classification: kukeon, severity: error, recurring: false, evidence: ["log"] }
  body: |
    markdown
`

func TestParseHealthReport(t *testing.T) {
	doc, err := parser.ParseDocument(0, []byte(healthReportDoc))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != v1beta1.KindHealthReport || doc.HealthReportDoc == nil ||
		doc.HealthReportDoc.Metadata.Name != "20261122T101500.000000000Z" ||
		doc.HealthReportDoc.Spec.Cell.Stack != "default" || len(doc.HealthReportDoc.Spec.Findings) != 1 ||
		doc.HealthReportDoc.Spec.Body != "markdown\n" {
		t.Fatalf("parsed = %+v", doc.HealthReportDoc)
	}
	if verr := parser.ValidateDocument(doc); verr != nil {
		t.Fatalf("valid report rejected: %v", verr)
	}
}

func TestParseHealthReportLeavesDaemonFieldsOut(t *testing.T) {
	// name, realm and createdAt are filled by kukeond when left out.
	raw := strings.Replace(healthReportDoc,
		"metadata: { name: 20261122T101500.000000000Z, realm: kuke-system }\n", "", 1)
	raw = strings.Replace(raw, "  createdAt: 2026-11-22T10:15:00Z\n", "", 1)
	doc, err := parser.ParseDocument(0, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if verr := parser.ValidateDocument(doc); verr != nil {
		t.Fatalf("report without daemon-assigned fields rejected: %v", verr)
	}
}

func TestParseHealthReportRejects(t *testing.T) {
	for name, tc := range map[string]struct{ old, new string }{
		"other realm":   {"realm: kuke-system }", "realm: default }"},
		"no summary":    {"  summary: one kukeon finding, one workload finding\n", ""},
		"reserved name": {"name: 20261122T101500.000000000Z,", "name: latest,"},
		"unsafe name":   {"name: 20261122T101500.000000000Z,", "name: ../x,"},
		"bad severity":  {"severity: error", "severity: fatal"},
	} {
		doc, err := parser.ParseDocument(0, []byte(strings.Replace(healthReportDoc, tc.old, tc.new, 1)))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		verr := parser.ValidateDocument(doc)
		if verr == nil || !errors.Is(verr.Err, errdefs.ErrHealthReportInvalid) {
			t.Errorf("%s: %v", name, verr)
		}
	}
	// Decoding is strict: a misspelled key is an error, not a dropped field.
	if _, err := parser.ParseDocument(0, []byte(healthReportDoc+"  sumary: typo\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

// A HealthDigest is computed by kukeond and never stored: `kuke apply` rejects
// it as an unknown kind (#1633).
func TestParseHealthDigestRejected(t *testing.T) {
	_, err := parser.ParseDocument(0, []byte("apiVersion: v1beta1\nkind: HealthDigest\nclean: true\n"))
	if !errors.Is(err, errdefs.ErrUnknownKind) {
		t.Fatalf("HealthDigest: got %v, want ErrUnknownKind", err)
	}
}

// A TerminalSupervisor document is written by kuketty inside a container and
// copied into ContainerStatus by kukeond; it is never stored, so `kuke apply`
// rejects it as an unknown kind (#1711).
func TestParseTerminalSupervisorRejected(t *testing.T) {
	_, err := parser.ParseDocument(
		0,
		[]byte("apiVersion: v1beta1\nkind: TerminalSupervisor\nmetadata:\n  name: work\n"),
	)
	if !errors.Is(err, errdefs.ErrUnknownKind) {
		t.Fatalf("TerminalSupervisor: got %v, want ErrUnknownKind", err)
	}
}
