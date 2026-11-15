//go:build !integration

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

//nolint:testpackage // exercises *Exec.InspectCellNetwork against an in-package ctr.Client fake
package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	netInspectRealm  = "default"
	netInspectSpace  = "web"
	netInspectStack  = "app"
	netInspectCell   = "api"
	netInspectRootID = "web_app_api_root"
	netInspectNet    = "default-web"
)

func netInspectCellModel() intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: netInspectCell},
		Spec: intmodel.CellSpec{
			ID:        netInspectCell,
			RealmName: netInspectRealm,
			SpaceName: netInspectSpace,
			StackName: netInspectStack,
		},
	}
}

// seedNetInspectSpace writes a space whose conflist is a real bridge conflist
// at confPath, so InspectCellNetwork can load it and read libcni's cache.
func seedNetInspectSpace(t *testing.T, r *Exec, confPath string) {
	t.Helper()
	conflist := fmt.Sprintf(`{
  "cniVersion": "1.0.0",
  "name": %q,
  "plugins": [{"type": "bridge", "bridge": "kuke-test0"}]
}`, netInspectNet)
	if err := os.WriteFile(confPath, []byte(conflist), 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}
	doc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: netInspectSpace},
		Spec:       v1beta1.SpaceSpec{RealmID: netInspectRealm, CNIConfigPath: confPath},
	}
	path := fs.SpaceMetadataPath(r.opts.RunPath, netInspectRealm, netInspectSpace)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir space metadata dir: %v", err)
	}
	if err := metadata.WriteMetadata(r.ctx, r.logger, doc, path); err != nil {
		t.Fatalf("write space metadata: %v", err)
	}
}

// seedNetInspectCNICache writes the libcni result-cache entry CNI ADD leaves
// for the root container's eth0 attachment.
func seedNetInspectCNICache(t *testing.T, cacheDir, ipCIDR string) {
	t.Helper()
	entry := map[string]any{
		"kind":        "cniCacheV1",
		"containerId": netInspectRootID,
		"ifName":      "eth0",
		"networkName": netInspectNet,
		"result": map[string]any{
			"cniVersion": "1.0.0",
			"ips":        []map[string]any{{"address": ipCIDR}},
		},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal cache entry: %v", err)
	}
	dir := filepath.Join(cacheDir, "results")
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cni cache: %v", err)
	}
	name := fmt.Sprintf("%s-%s-eth0", netInspectNet, netInspectRootID)
	if err = os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write cni cache: %v", err)
	}
}

// TestInspectCellNetwork_RunningBridgedCellReportsCachedIPv4 locks down the
// #1421 review fix: the root task status must be read through
// ctrClient.TaskStatus with the realm's namespace. The earlier direct
// container.Task(r.ctx) call ran without a namespace, containerd rejected it,
// and every running cell read back as not running.
func TestInspectCellNetwork_RunningBridgedCellReportsCachedIPv4(t *testing.T) {
	var gotNamespace, gotID string
	fake := &deleteCellFakeClient{
		taskStatusFn: func(namespace, id string) (containerd.Status, error) {
			gotNamespace, gotID = namespace, id
			return containerd.Status{Status: containerd.Running}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, netInspectRealm)
	seedNetInspectSpace(t, r, filepath.Join(t.TempDir(), "web.conflist"))
	seedNetInspectCNICache(t, r.cniConf.CniCacheDir, "10.88.4.7/24")

	running, ipv4, err := r.InspectCellNetwork(netInspectCellModel(), false)
	if err != nil {
		t.Fatalf("InspectCellNetwork: unexpected error: %v", err)
	}
	if !running {
		t.Fatalf("running = false, want true for a Running root task")
	}
	if ipv4 != "10.88.4.7" {
		t.Errorf("ipv4 = %q, want 10.88.4.7 from the libcni cache", ipv4)
	}
	if gotNamespace != netInspectRealm+".kukeon.io" || gotID != netInspectRootID {
		t.Errorf("TaskStatus(%q, %q), want (%q, %q)",
			gotNamespace, gotID, netInspectRealm+".kukeon.io", netInspectRootID)
	}
}

func TestInspectCellNetwork_RunningHostNetworkCellHasNoIPv4(t *testing.T) {
	fake := &deleteCellFakeClient{
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Running}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, netInspectRealm)

	running, ipv4, err := r.InspectCellNetwork(netInspectCellModel(), true)
	if err != nil {
		t.Fatalf("InspectCellNetwork: unexpected error: %v", err)
	}
	if !running || ipv4 != "" {
		t.Errorf("got (running=%v, ipv4=%q), want (true, \"\")", running, ipv4)
	}
}

func TestInspectCellNetwork_StoppedOrMissingTaskIsNotRunning(t *testing.T) {
	cases := map[string]struct {
		status containerd.Status
		err    error
	}{
		"stopped task": {status: containerd.Status{Status: containerd.Stopped}},
		"task not found": {
			err: fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("no running task")),
		},
		"container not found": {
			err: fmt.Errorf("%w: %w", errdefs.ErrContainerNotFound, errors.New("not found")),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fn := func(_, _ string) (containerd.Status, error) { return tc.status, tc.err }
			r := newDeleteCellTestExec(t, &deleteCellFakeClient{taskStatusFn: fn})
			seedDeleteCellRealm(t, r, netInspectRealm)

			running, ipv4, err := r.InspectCellNetwork(netInspectCellModel(), false)
			if err != nil {
				t.Fatalf("InspectCellNetwork: unexpected error: %v", err)
			}
			if running || ipv4 != "" {
				t.Errorf("got (running=%v, ipv4=%q), want (false, \"\")", running, ipv4)
			}
		})
	}
}

// TestInspectCellNetwork_TaskStatusErrorIsReturned pins that a containerd
// failure other than not-found is surfaced instead of reading as not running.
func TestInspectCellNetwork_TaskStatusErrorIsReturned(t *testing.T) {
	fake := &deleteCellFakeClient{
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{}, errors.New("containerd: rpc transport closed")
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, netInspectRealm)

	_, _, err := r.InspectCellNetwork(netInspectCellModel(), false)
	if !errors.Is(err, errdefs.ErrCellTaskStatus) {
		t.Fatalf("err = %v, want ErrCellTaskStatus", err)
	}
}
