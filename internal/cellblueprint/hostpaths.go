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

package cellblueprint

import (
	"path/filepath"
	"strings"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// The reserved host-path template variables (issue #1710). A blueprint embeds
// them in bind-mount sources and env values that name the rendering daemon's
// own host paths — team blueprints' host mounts (`kukeond-socket`,
// `kuke-binary`, `kukeon-install`) and the in-cell KUKEON_HOST — so a stored
// blueprint follows the daemon that renders it, whatever its run path or
// socket. Like ${CELL_NAME} they are not blueprint parameters: they are
// resolved after the scalar ${KEY} substitution, by ExpandHostPaths, and a
// blueprint or Config should not declare or set values with these names,
// since the scalar substitution would consume the token first.
const (
	// RunPathVar is the daemon's run path (`kukeond serve --run-path`).
	RunPathVar = "${KUKEON_RUN_PATH}"
	// SocketDirVar is the directory of the daemon's socket, which holds the
	// socket share directory (consts.KukeondSocketShareSubdir, #1483).
	SocketDirVar = "${KUKEOND_SOCKET_DIR}"
	// SocketNameVar is the base name of the daemon's socket, the name of its
	// hard link in the share directory.
	SocketNameVar = "${KUKEOND_SOCKET_NAME}"
)

// The standard host layout, used for a HostPaths field left empty.
const (
	DefaultHostRunPath = "/opt/kukeon"
	DefaultHostSocket  = "/run/kukeon/kukeond.sock"
)

// HostPaths are the rendering daemon's host paths the reserved variables
// expand to. An empty field means the standard layout.
type HostPaths struct {
	RunPath string
	Socket  string
}

func (p HostPaths) replacer() *strings.Replacer {
	runPath := strings.TrimSpace(p.RunPath)
	if runPath == "" {
		runPath = DefaultHostRunPath
	}
	socket := strings.TrimSpace(p.Socket)
	if socket == "" {
		socket = DefaultHostSocket
	}
	return strings.NewReplacer(
		RunPathVar, filepath.Clean(runPath),
		SocketDirVar, filepath.Dir(filepath.Clean(socket)),
		SocketNameVar, filepath.Base(socket),
	)
}

// ExpandHostPaths rewrites the reserved host-path variables in every bind
// mount source and env value of every container. On the standard layout the
// result is the literal paths team blueprints carried before #1710, so cells
// rendered there are unchanged.
//
// Volumes and Env are cloned before they are rewritten, for the reason
// ExpandPerCellVolumes gives: a blueprint document's slices may back several
// materializations.
func ExpandHostPaths(paths HostPaths, containers []v1beta1.ContainerSpec) {
	r := paths.replacer()
	for ci := range containers {
		c := &containers[ci]
		if hasHostPathVar(c) {
			vols := make([]v1beta1.VolumeMount, len(c.Volumes))
			copy(vols, c.Volumes)
			for vi := range vols {
				if vols[vi].Kind == "" || vols[vi].Kind == v1beta1.VolumeKindBind {
					vols[vi].Source = r.Replace(vols[vi].Source)
				}
			}
			c.Volumes = vols
			env := make([]string, len(c.Env))
			for ei, entry := range c.Env {
				env[ei] = r.Replace(entry)
			}
			c.Env = env
		}
	}
}

func hasHostPathVar(c *v1beta1.ContainerSpec) bool {
	contains := func(s string) bool {
		return strings.Contains(s, RunPathVar) || strings.Contains(s, SocketDirVar) ||
			strings.Contains(s, SocketNameVar)
	}
	for _, v := range c.Volumes {
		if contains(v.Source) {
			return true
		}
	}
	for _, e := range c.Env {
		if contains(e) {
			return true
		}
	}
	return false
}
