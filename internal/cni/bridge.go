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
	"path/filepath"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/jsimonetti/rtnetlink/v2"
)

// BridgeRunner deletes a kernel bridge link by name. Implementations must be
// idempotent: deleting an already-absent bridge is success, not an error.
type BridgeRunner interface {
	DeleteBridge(ctx context.Context, name string) error
}

// NetlinkBridgeRunner deletes bridge links through the kernel API. No iproute2
// executable is needed, including in host-side daemons inside development cells.
type NetlinkBridgeRunner struct{}

func (NetlinkBridgeRunner) DeleteBridge(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := rtnetlink.Dial(nil)
	if err != nil {
		return fmt.Errorf("open route netlink: %w", err)
	}
	defer conn.Close()
	// Bound kernel IO even when the caller supplied no deadline.
	const netlinkTimeout = 5 * time.Second
	deadline := time.Now().Add(netlinkTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	links, err := conn.Link.List()
	if err != nil {
		return fmt.Errorf("list links: %w", err)
	}
	for _, link := range links {
		if link.Attributes == nil || link.Attributes.Name != name {
			continue
		}
		if link.Attributes.Info == nil || link.Attributes.Info.Kind != "bridge" {
			return fmt.Errorf("%w: interface %s is not a bridge", errdefs.ErrBridgePluginMissing, name)
		}
		err = conn.Link.Delete(link.Index)
		if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENOENT) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("delete bridge %s: %w", name, err)
		}
		return nil
	}
	return nil // Already absent.
}

// TeardownNetwork deletes the bridge before removing its conflist so a failed
// deletion remains retryable. For legacy partial teardown with no conflist,
// recover the deterministic bridge name from the network name. A present
// conflist remains authoritative (including custom names and non-bridge networks).
func (m *Manager) TeardownNetwork(
	ctx context.Context, runner BridgeRunner, networkName, configPath string,
) error {
	if networkName == "" {
		return errors.New("network name is required")
	}
	if runner == nil {
		runner = NetlinkBridgeRunner{}
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

	if errors.Is(readErr, errdefs.ErrNetworkNotFound) {
		bridge = SafeBridgeName(networkName)
	}
	if bridge != "" {
		if err := runner.DeleteBridge(ctx, bridge); err != nil {
			return fmt.Errorf("delete bridge %s: %w", bridge, err)
		}
	}
	return m.DeleteNetwork(networkName, configPath)
}
