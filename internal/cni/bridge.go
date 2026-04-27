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

package cni

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// BridgeRunner deletes a kernel bridge link by name. Implementations must be
// idempotent: deleting an already-absent bridge is success, not an error.
type BridgeRunner interface {
	DeleteBridge(ctx context.Context, name string) error
}

// IPBridgeRunner shells out to the host `ip` binary to delete bridge links.
// It mirrors the iproute2 invocation used by the standard CNI bridge plugin
// at create time, so the create/teardown contract stays symmetric.
type IPBridgeRunner struct{}

// DeleteBridge runs `ip link delete <name> type bridge`. It treats the
// "Cannot find device" error as success, so callers can invoke it without
// first probing for the bridge.
func (IPBridgeRunner) DeleteBridge(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "ip", "link", "delete", name, "type", "bridge")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(strings.ToLower(msg), "cannot find device") {
		return nil
	}
	return fmt.Errorf("ip link delete %s: %w: %s", name, err, msg)
}

// TeardownNetwork removes the CNI network for networkName end-to-end: it reads
// the bridge name from the conflist, removes the conflist file, and deletes
// the kernel bridge link. The bridge name is read **before** the conflist is
// removed so that a transient failure between steps still leaves enough state
// to retry from. If configPath is empty the manager's default location is used.
//
// Each step is idempotent — a missing conflist, a conflist with no bridge
// plugin, an empty `bridge` field, or an already-absent bridge are all treated
// as success — so callers can invoke it on partially-cleaned-up state without
// special-casing.
func (m *Manager) TeardownNetwork(
	ctx context.Context, runner BridgeRunner, networkName, configPath string,
) error {
	if networkName == "" {
		return errors.New("network name is required")
	}
	if runner == nil {
		runner = IPBridgeRunner{}
	}
	if configPath == "" {
		configPath = filepath.Join(m.conf.CniConfigDir, networkName+".conflist")
	}

	bridge, readErr := m.ReadBridgeName(configPath)
	if readErr != nil &&
		!errors.Is(readErr, errdefs.ErrNetworkNotFound) &&
		!errors.Is(readErr, errdefs.ErrBridgePluginMissing) {
		return fmt.Errorf("read bridge name: %w", readErr)
	}

	if delErr := m.DeleteNetwork(networkName, configPath); delErr != nil {
		return delErr
	}

	if bridge == "" {
		return nil
	}
	return runner.DeleteBridge(ctx, bridge)
}
