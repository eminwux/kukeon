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

//nolint:testpackage // exercises *Exec.detachRootContainerFromNetwork and its netns helper (unexported)
package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// deadTaskContainer is a near-empty containerd.Container whose Task() reports
// the post-reboot / OOM signature: the container record survives but its task
// is gone. It models the exact dead-task path issue #715 cares about — distinct
// from an absent container record (GetContainer error), which deleteCellFakeClient
// already produces. Only Task() carries behavior; every other method returns a
// zero value because rootContainerNetnsPath never calls them.
type deadTaskContainer struct {
	containerd.Container
}

func (c deadTaskContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	return nil, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errArtificialDeadTask)
}

var errArtificialDeadTask = fmt.Errorf("task: not found")

// deadTaskClient returns a live container record whose task is gone, driving
// rootContainerNetnsPath down its taskErr branch.
type deadTaskClient struct {
	deleteCellFakeClient
}

func (c *deadTaskClient) GetContainer(string, string) (containerd.Container, error) {
	return deadTaskContainer{}, nil
}

// TestRootContainerNetnsPath_EmptyOnDeadOrAbsentTask pins the precondition the
// #715 fix relies on: when the root task is dead (record present, task gone) or
// the container record is absent, the resolved netns is "" — the input
// detachRootContainerFromNetwork now issues a best-effort CNI DEL with, rather
// than skipping the release entirely.
func TestRootContainerNetnsPath_EmptyOnDeadOrAbsentTask(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("absent container record", func(t *testing.T) {
		r := &Exec{ctx: context.Background(), logger: logger, ctrClient: &deleteCellFakeClient{}}
		if got := r.rootContainerNetnsPath("default.kukeon.io", "root"); got != "" {
			t.Errorf("rootContainerNetnsPath = %q, want \"\" when GetContainer fails", got)
		}
	})

	t.Run("dead task on a surviving record", func(t *testing.T) {
		r := &Exec{ctx: context.Background(), logger: logger, ctrClient: &deadTaskClient{}}
		if got := r.rootContainerNetnsPath("default.kukeon.io", "root"); got != "" {
			t.Errorf("rootContainerNetnsPath = %q, want \"\" when Task() reports the task is gone", got)
		}
	})
}

// writeFakeCNIPlugin drops an executable CNI plugin under binDir named
// pluginType. On a DEL invocation it removes <storeDir>/<CNI_CONTAINERID>,
// emulating a host-local IPAM release so a test can assert the reservation was
// freed; VERSION returns the supported set libcni probes for; every other
// command exits 0 with a minimal valid result. Using a self-provided fake
// keeps the test independent of real /opt/cni/bin plugins (CI-safe) — the
// same reason internal/cni/container_test.go avoids loaded-config plugin runs.
func writeFakeCNIPlugin(t *testing.T, binDir, pluginType, storeDir string) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
case "$CNI_COMMAND" in
  DEL) rm -f "%s/$CNI_CONTAINERID"; exit 0 ;;
  VERSION) echo '{"cniVersion":"1.0.0","supportedVersions":["0.1.0","0.2.0","0.3.0","0.3.1","0.4.0","1.0.0"]}'; exit 0 ;;
  *) echo '{"cniVersion":"0.4.0"}'; exit 0 ;;
esac
`, storeDir)
	path := filepath.Join(binDir, pluginType)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test-only executable fake plugin
		t.Fatalf("write fake CNI plugin: %v", err)
	}
}

// TestDetachRootContainerFromNetwork_DeadTaskReleasesReservation is the headline
// #715 guard: a cell whose root task has already exited must still have its
// CNI/IPAM reservation released on stop/kill. The old nested live-task / PID>0
// guard made the dead-task path a silent no-op, so the clean plugin-chain DEL
// never ran and only the post-delete file-purge safety net released the IPAM
// file. Here the root task is absent (netns resolves to ""), and the fake
// host-local-style plugin releases the reservation from a temp store on DEL —
// asserting the release proves detach now issues CNI DEL even with an empty
// netns.
func TestDetachRootContainerFromNetwork_DeadTaskReleasesReservation(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	confDir := filepath.Join(tmp, "conf")
	cacheDir := filepath.Join(tmp, "cache")
	storeDir := filepath.Join(tmp, "store") // stand-in for the host-local store
	for _, d := range []string{binDir, confDir, cacheDir, storeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	const (
		containerID = "kukeon_kukeon_web_root"
		pluginType  = "kukeon-test-cni"
	)

	// Seed the reservation as the start-time CNI ADD would have left it.
	reservation := filepath.Join(storeDir, containerID)
	if err := os.WriteFile(reservation, []byte("10.88.0.5"), 0o600); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	writeFakeCNIPlugin(t, binDir, pluginType, storeDir)

	confPath := filepath.Join(confDir, "kukeon-test.conflist")
	conflist := fmt.Sprintf(`{"cniVersion":"0.4.0","name":"kukeon-test","plugins":[{"type":"%s"}]}`, pluginType)
	if err := os.WriteFile(confPath, []byte(conflist), 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}

	// Dead/absent root task: GetContainer fails, so the resolved netns is "".
	r := &Exec{
		ctx:       context.Background(),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctrClient: &deleteCellFakeClient{},
		cniConf:   &cni.Conf{CniBinDir: binDir, CniConfigDir: confDir, CniCacheDir: cacheDir},
	}

	r.detachRootContainerFromNetwork(containerID, confPath, "default.kukeon.io", "web", "web", "kukeon", "default")

	if _, err := os.Stat(reservation); !os.IsNotExist(err) {
		t.Fatalf(
			"reservation %s not released on the dead-task path (stat err=%v); CNI DEL was not issued with an empty netns",
			reservation, err,
		)
	}
}
