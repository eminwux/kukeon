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

package runner

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/netpolicy"
	"github.com/eminwux/kukeon/internal/util/fs"
)

type recordingEnforcer struct{ removed []string }

func (e *recordingEnforcer) Apply(context.Context, *netpolicy.Policy) error { return nil }

func (e *recordingEnforcer) Remove(_ context.Context, realmName, spaceName string) error {
	e.removed = append(e.removed, realmName+"/"+spaceName)
	return nil
}

// A space whose conflist and bridge are already gone must still lose its
// egress chain: once the realm metadata is removed nothing reconciles it.
func TestTeardownRealmCNI_RemovesEgressWithoutBridge(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	r.bridgeRunner = &failingTeardownBridge{}
	enforcer := &recordingEnforcer{}
	r.netPolicy = enforcer
	if err := os.MkdirAll(fs.SpaceMetadataDir(runPath, "test-realm", "test-space"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := r.teardownRealmCNI("test-realm"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(enforcer.removed, []string{"test-realm/test-space"}) {
		t.Fatalf("removed egress = %v", enforcer.removed)
	}
}
