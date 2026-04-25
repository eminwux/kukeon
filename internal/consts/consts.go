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

package consts

const (
	KukeonCgroupRoot = "/kukeon"

	CgroupFilesystemPath = "/sys/fs/cgroup"

	KukeonMetadataFile = "metadata.json"

	// KukeonContainerTTYDir is the basename of the per-container directory
	// that owns the sbsh terminal socket plus its capture and log siblings.
	// kukeon bind-mounts this directory (not a single file) into the
	// container so sbsh's unlink-and-recreate of the socket inode stays
	// host-visible.
	KukeonContainerTTYDir = "tty"

	// KukeonContainerSocketFile is the basename of the per-container sbsh
	// terminal socket inside KukeonContainerTTYDir. The container sees the
	// same inode at /run/kukeon/tty/socket via the directory bind mount
	// injected by Attachable=true specs.
	KukeonContainerSocketFile = "socket"

	// Label keys shared across the user default hierarchy and the system hierarchy.
	KukeonRealmLabelKey     = "realm.kukeon.io"
	KukeonSpaceLabelKey     = "space.kukeon.io"
	KukeonStackLabelKey     = "stack.kukeon.io"
	KukeonCellLabelKey      = "cell.kukeon.io"
	KukeonContainerLabelKey = "container.kukeon.io"

	// Default user hierarchy created by `kuke init` for user workloads.
	KukeonDefaultRealmName      = "default"
	KukeonDefaultRealmNamespace = "kukeon.io"
	KukeonDefaultSpaceName      = "default"
	KukeonDefaultStackName      = "default"

	// System hierarchy created by `kuke init` for the kukeond daemon.
	KukeSystemRealmName      = "kuke-system"
	KukeSystemRealmNamespace = "kuke-system.kukeon.io"
	KukeSystemSpaceName      = "kukeon"
	KukeSystemStackName      = "kukeon"
	KukeSystemCellName       = "kukeond"
	KukeSystemContainerName  = "kukeond"
)
