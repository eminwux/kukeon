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
	"errors"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/delete/healthreport"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

type deleteClient struct {
	kukeonv1.FakeClient

	deleted string
	err     error
}

func (c *deleteClient) Close() error { return nil }

func (c *deleteClient) DeleteHealthReport(_ context.Context, name string) error {
	c.deleted = name
	return c.err
}

func runDelete(client kukeonv1.Client, args ...string) (string, error) {
	cmd := healthreport.NewHealthReportCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SetContext(context.WithValue(context.Background(), healthreport.MockControllerKey{}, client))
	err := cmd.Execute()
	return out.String(), err
}

func TestDeleteHealthReport(t *testing.T) {
	client := &deleteClient{}
	out, err := runDelete(client, "20261122T101500.000000000Z")
	if err != nil || client.deleted != "20261122T101500.000000000Z" ||
		out != "Deleted health report \"20261122T101500.000000000Z\"\n" {
		t.Fatalf("out=%q deleted=%q err=%v", out, client.deleted, err)
	}

	missing := &deleteClient{err: errdefs.ErrHealthReportNotFound}
	if _, err = runDelete(missing, "nope"); !errors.Is(err, errdefs.ErrHealthReportNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err = runDelete(&deleteClient{}); err == nil {
		t.Fatal("a name is required")
	}
}
