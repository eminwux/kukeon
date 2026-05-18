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

package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// deleteDocsClientFake stubs DeleteDocuments on the in-process Client the
// daemon binds to. Every other Client method stays at the FakeClient default
// (ErrUnexpectedCall) so the test would fail loudly if the RPC handler
// accidentally invoked a different method.
type deleteDocsClientFake struct {
	kukeonv1.FakeClient

	gotRaw     []byte
	gotCascade bool
	gotForce   bool

	result kukeonv1.DeleteDocumentsResult
	err    error
}

func (d *deleteDocsClientFake) DeleteDocuments(
	_ context.Context,
	raw []byte,
	cascade, force bool,
) (kukeonv1.DeleteDocumentsResult, error) {
	d.gotRaw = raw
	d.gotCascade = cascade
	d.gotForce = force
	return d.result, d.err
}

// TestDeleteDocuments_RPCRoundTrip pins the daemon RPC layer's pass-through of
// the cascade/force flags and the wire-level Result for `kuke delete -f` —
// the symmetry restored by #574 (previously the CLI bypassed the daemon
// entirely so this RPC path was effectively dead).
func TestDeleteDocuments_RPCRoundTrip(t *testing.T) {
	const wantRaw = "apiVersion: v1beta1\nkind: Realm\n"

	core := &deleteDocsClientFake{
		result: kukeonv1.DeleteDocumentsResult{
			Resources: []kukeonv1.DeleteResourceResult{
				{Index: 0, Kind: "Realm", Name: "r1", Action: "deleted", Cascaded: []string{"default"}},
			},
		},
	}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.DeleteDocumentsArgs{
		RawYAML: []byte(wantRaw),
		Cascade: true,
		Force:   true,
	}
	reply := &kukeonv1.DeleteDocumentsReply{}
	if err := svc.DeleteDocuments(args, reply); err != nil {
		t.Fatalf("DeleteDocuments returned transport error: %v", err)
	}
	if reply.Err != nil {
		t.Fatalf("expected wire-level Err to be nil, got %v", kukeonv1.FromAPIError(reply.Err))
	}

	if !bytes.Equal(core.gotRaw, []byte(wantRaw)) {
		t.Errorf("RawYAML passthrough mismatch: got %q want %q", string(core.gotRaw), wantRaw)
	}
	if !core.gotCascade {
		t.Errorf("Cascade not forwarded to core client")
	}
	if !core.gotForce {
		t.Errorf("Force not forwarded to core client")
	}

	if got := len(reply.Result.Resources); got != 1 {
		t.Fatalf("expected 1 wire resource, got %d", got)
	}
	r := reply.Result.Resources[0]
	if r.Kind != "Realm" || r.Name != "r1" || r.Action != "deleted" {
		t.Errorf("wire resource mismatch: %+v", r)
	}
	if len(r.Cascaded) != 1 || r.Cascaded[0] != "default" {
		t.Errorf("Cascaded list lost on the wire: %+v", r.Cascaded)
	}
}

// TestDeleteDocuments_RPCSurfacesSentinel pins the structured-error
// pass-through: an errdefs sentinel returned by the in-process Client must
// survive the wire as a typed APIError so `kuke delete -f` can render
// idempotent "not found" semantics even when routed through the daemon.
func TestDeleteDocuments_RPCSurfacesSentinel(t *testing.T) {
	core := &deleteDocsClientFake{err: errdefs.ErrRealmNotFound}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.DeleteDocumentsArgs{RawYAML: []byte("apiVersion: v1beta1\n")}
	reply := &kukeonv1.DeleteDocumentsReply{}
	if err := svc.DeleteDocuments(args, reply); err != nil {
		t.Fatalf("DeleteDocuments returned transport error: %v", err)
	}
	if reply.Err == nil {
		t.Fatalf("expected wire-level Err to be populated, got nil")
	}
	wireErr := kukeonv1.FromAPIError(reply.Err)
	if !errors.Is(wireErr, errdefs.ErrRealmNotFound) {
		t.Errorf("wire error %q does not unwrap to ErrRealmNotFound", wireErr)
	}
}
