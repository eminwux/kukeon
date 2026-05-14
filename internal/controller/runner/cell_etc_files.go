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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	utilfs "github.com/eminwux/kukeon/internal/util/fs"
)

// etcHostsLocalhostBlock is the static localhost block kukeond emits at the
// top of every per-cell /etc/hosts. Mirrors the block Docker, Kubernetes
// kubelet, and containerd's nerdctl render — the IPv4 loopback first, then
// the IPv6 loopback aliases tools like `getent hosts localhost` rely on.
const etcHostsLocalhostBlock = `127.0.0.1	localhost
::1	localhost ip6-localhost ip6-loopback
fe00::	ip6-localnet
ff00::	ip6-mcastprefix
ff02::1	ip6-allnodes
ff02::2	ip6-allrouters
`

// renderCellEtcHostname writes the cell name plus a trailing newline to the
// given path, atomically replacing whatever was there. The bind-mount in the
// container's OCI spec resolves to the destination path's inode at mount
// time, so an in-place rewrite (truncate + write) is what propagates an
// updated cell-name to running containers.
func renderCellEtcHostname(path, cellName string) error {
	cellName = strings.TrimSpace(cellName)
	if cellName == "" {
		return fmt.Errorf("cell name is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cell metadata dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(cellName+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// renderCellEtcHosts writes the localhost block plus an optional
// "<cellIP>\t<cellName>" line. cellIP may be nil — used at cell-create time
// before CNI ADD has assigned an address; the post-CNI render replaces the
// file with the IP populated. Truncate-on-write so the inode the container's
// bind-mount resolves to keeps reflecting the latest content.
func renderCellEtcHosts(path, cellName string, cellIP net.IP) error {
	cellName = strings.TrimSpace(cellName)
	if cellName == "" {
		return fmt.Errorf("cell name is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cell metadata dir: %w", err)
	}
	var b strings.Builder
	b.WriteString(etcHostsLocalhostBlock)
	if cellIP != nil {
		b.WriteString(cellIP.String())
		b.WriteByte('\t')
		b.WriteString(cellName)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// cellEtcFilePaths returns the per-cell /etc/hostname and /etc/hosts host
// source paths and a suppressHosts flag set when the cell's root container
// runs with HostNetwork=true (the kukeond carve-out). Returns zero values
// when the cell's identity fields are incomplete so callers can no-op
// safely.
func (r *Exec) cellEtcFilePaths(cell *intmodel.Cell) (hostnamePath, hostsPath string, suppressHosts bool) {
	if cell == nil {
		return "", "", false
	}
	cellName := strings.TrimSpace(cell.Metadata.Name)
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if cellName == "" || realmName == "" || spaceName == "" || stackName == "" {
		return "", "", false
	}
	hostnamePath = utilfs.CellEtcHostnamePath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	hostsPath = utilfs.CellEtcHostsPath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	suppressHosts = cellRootHostNetwork(cell)
	return hostnamePath, hostsPath, suppressHosts
}

// stampCellEtcFilePathsOnContainers fills EtcHostnamePath and EtcHostsPath
// on every container spec in the cell so BuildContainerSpec /
// BuildRootContainerSpec emit the bind-mounts. /etc/hosts is suppressed for
// host-network cells (root container with HostNetwork=true) — the host's
// /etc/hosts is the authoritative view in that mode and overriding it would
// hide host-side aliases the daemon depends on. /etc/hostname is set
// unconditionally so `cat /etc/hostname` agrees with the UTS hostname for
// every container in the cell.
func (r *Exec) stampCellEtcFilePathsOnContainers(cell *intmodel.Cell) {
	hostnamePath, hostsPath, suppressHosts := r.cellEtcFilePaths(cell)
	if hostnamePath == "" {
		return
	}
	for i := range cell.Spec.Containers {
		stampEtcFilePathsOnContainerSpec(&cell.Spec.Containers[i], hostnamePath, hostsPath, suppressHosts)
	}
}

// stampEtcFilePathsOnContainerSpec is the per-container counterpart to
// stampCellEtcFilePathsOnContainers — used by call sites that hold a local
// container spec value (e.g. a root spec built fresh by
// ensureCellRootContainerSpec) and need it to carry the same bind-mount
// paths the cell-wide stamp would apply.
func stampEtcFilePathsOnContainerSpec(spec *intmodel.ContainerSpec, hostnamePath, hostsPath string, suppressHosts bool) {
	if spec == nil || hostnamePath == "" {
		return
	}
	spec.EtcHostnamePath = hostnamePath
	if suppressHosts {
		spec.EtcHostsPath = ""
	} else {
		spec.EtcHostsPath = hostsPath
	}
}

// stampContainerRecreateRuntimeFields applies the runtime-only field stamps
// that the single-container StartContainer recreate path needs before
// invoking BuildContainerSpec via CreateContainerFromSpec. Mirrors the
// cell-wide stampCellEtcFilePathsOnContainers + stampCellProfileNameOnContainers
// pair the StartCell / ensureCellContainers paths run, but per-spec since
// the recreate path holds a local container-spec pointer. Without this,
// `kuke start container` drops the recreated container's /etc/hosts +
// /etc/hostname bind-mounts and KUKEON_CELL_PROFILE_NAME env entry. Issue
// #354.
func (r *Exec) stampContainerRecreateRuntimeFields(spec *intmodel.ContainerSpec, cell *intmodel.Cell) {
	hostnamePath, hostsPath, suppressHosts := r.cellEtcFilePaths(cell)
	stampEtcFilePathsOnContainerSpec(spec, hostnamePath, hostsPath, suppressHosts)
	stampCellProfileNameOnContainerSpec(spec, cell)
}

// renderCellEtcFilesPreCNI writes the per-cell /etc/hostname and an initial
// /etc/hosts (localhost block only, no cell IP) so the bind-mount sources
// exist before runc creates any container in the cell. Idempotent — every
// CreateCell / StartCell / EnsureCellContainers tick calls this so a
// rebooted host or a daemon restart finds the files in place.
func (r *Exec) renderCellEtcFilesPreCNI(cell *intmodel.Cell) error {
	if cell == nil {
		return nil
	}
	cellName := strings.TrimSpace(cell.Metadata.Name)
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if cellName == "" || realmName == "" || spaceName == "" || stackName == "" {
		return nil
	}
	hostnamePath := utilfs.CellEtcHostnamePath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	hostsPath := utilfs.CellEtcHostsPath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	if err := renderCellEtcHostname(hostnamePath, cellName); err != nil {
		return err
	}
	if cellRootHostNetwork(cell) {
		// Host-network cells use the host's /etc/hosts; nothing to render.
		return nil
	}
	return renderCellEtcHosts(hostsPath, cellName, nil)
}

// ensureCellEtcFilesExistPreCNI guarantees the per-cell /etc/hostname and
// /etc/hosts source files exist without rewriting them when they already
// do. Use this on call paths where a prior StartCell may have rendered
// /etc/hosts with the cell IP — an unconditional pre-CNI re-render would
// truncate the IP line and regress the DNS-lookup fix issue #345 ships.
// First-creation paths still call renderCellEtcFilesPreCNI directly.
func (r *Exec) ensureCellEtcFilesExistPreCNI(cell *intmodel.Cell) error {
	if cell == nil {
		return nil
	}
	hostnamePath, hostsPath, suppressHosts := r.cellEtcFilePaths(cell)
	if hostnamePath == "" {
		return nil
	}
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if _, err := os.Stat(hostnamePath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", hostnamePath, err)
		}
		if rerr := renderCellEtcHostname(hostnamePath, cellName); rerr != nil {
			return rerr
		}
	}
	if suppressHosts {
		return nil
	}
	if _, err := os.Stat(hostsPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", hostsPath, err)
		}
		return renderCellEtcHosts(hostsPath, cellName, nil)
	}
	return nil
}

// renderCellEtcHostsWithIP rewrites the cell's /etc/hosts to include the
// CNI-assigned cell IP. Called from StartCell after AddContainerToNetwork
// succeeds (or returns the cached IP on the idempotent-veth-exists path).
// No-op for host-network cells.
func (r *Exec) renderCellEtcHostsWithIP(cell *intmodel.Cell, cellIP net.IP) error {
	if cell == nil || cellIP == nil {
		return nil
	}
	cellName := strings.TrimSpace(cell.Metadata.Name)
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if cellName == "" || realmName == "" || spaceName == "" || stackName == "" {
		return nil
	}
	if cellRootHostNetwork(cell) {
		return nil
	}
	hostsPath := utilfs.CellEtcHostsPath(r.opts.RunPath, realmName, spaceName, stackName, cellName)
	return renderCellEtcHosts(hostsPath, cellName, cellIP)
}

// cellRootHostNetwork reports whether the cell's root container runs with
// HostNetwork=true — the kukeond carve-out where /etc/hosts injection is
// inappropriate. Looks up the root via Spec.RootContainerID and falls back
// to scanning Containers for a Root=true entry when the field is unset.
func cellRootHostNetwork(cell *intmodel.Cell) bool {
	if cell == nil {
		return false
	}
	rootID := strings.TrimSpace(cell.Spec.RootContainerID)
	for _, c := range cell.Spec.Containers {
		if rootID != "" && c.ID == rootID {
			return c.HostNetwork
		}
		if rootID == "" && c.Root {
			return c.HostNetwork
		}
	}
	return false
}
