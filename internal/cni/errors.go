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
	"encoding/json"
	"fmt"
	"strings"

	libcni "github.com/containernetworking/cni/libcni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// translateCNIError inspects an error returned by libcni when adding or
// deleting a container on a network and, when possible, wraps it with an
// actionable errdefs sentinel. Unclassified errors are returned unchanged so
// this function is strictly additive — callers can continue to use errors.Is
// against the sentinels they already know about.
//
// bridge is the bridge device name from the loaded conflist (may be empty if
// the caller could not determine it); it is used to disambiguate ERANGE
// (IFNAMSIZ) from other out-of-range conditions.
func translateCNIError(err error, networkName, bridge string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// IFNAMSIZ: libcni surfaces netlink ERANGE as "numerical result out of
	// range". This is only meaningful if the bridge name itself is too long;
	// other ERANGE causes (e.g. route table overflow) are left untranslated.
	if strings.Contains(msg, "numerical result out of range") && len(bridge) > maxBridgeNameLen {
		return fmt.Errorf(
			"%w: bridge %q is %d chars, max %d (IFNAMSIZ-1); SafeBridgeName was expected to hash — file a bug: %w",
			errdefs.ErrBridgeNameTooLong, bridge, len(bridge), maxBridgeNameLen, err,
		)
	}

	// Missing CNI plugin binary. libcni reports this as either "failed to
	// find plugin" or an exec error like `exec: "bridge": executable file not
	// found in $PATH`. The bridge plugin is the common offender on fresh hosts.
	if strings.Contains(msg, "failed to find plugin") ||
		strings.Contains(msg, "executable file not found") {
		return fmt.Errorf(
			"%w: required CNI plugin missing on host — install the standard CNI plugins (e.g. apt install containernetworking-plugins) so %q resolves: %w",
			errdefs.ErrCNIPluginNotFound, networkName, err,
		)
	}

	return err
}

// bridgeNameFromNetConf extracts the bridge device name from the first bridge
// plugin in a loaded CNI conflist. Returns "" when the conflist is nil, has no
// bridge plugin, or is unparseable — callers should treat "" as "unknown".
func bridgeNameFromNetConf(netConf *libcni.NetworkConfigList) string {
	if netConf == nil {
		return ""
	}
	for _, p := range netConf.Plugins {
		if p == nil || len(p.Bytes) == 0 {
			continue
		}
		var raw struct {
			Type   string `json:"type"`
			Bridge string `json:"bridge"`
		}
		if err := json.Unmarshal(p.Bytes, &raw); err != nil {
			continue
		}
		if raw.Type == "bridge" && raw.Bridge != "" {
			return raw.Bridge
		}
	}
	return ""
}
