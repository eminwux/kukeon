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

package controller

import (
	"maps"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// rootContainerFieldPrefix qualifies a root-container field in DiffCell's
// cell-level ChangedFields / BreakingChanges.
const rootContainerFieldPrefix = "rootContainer."

// containerFieldOverlay copies one container field, by the name
// apply.DiffCell reports it under (diffContainerSpec), from src onto dst. The
// names are the same for the root container (reported as
// `rootContainer.<field>`) and a child (`containers[<id>].<field>`). A field
// DiffCell reports without an entry here would leave the right column of the
// diff wrong, which TestOverlayDrift_CoversEveryContainerField catches.
//
//nolint:gochecknoglobals // static lookup table, never written.
var containerFieldOverlay = map[string]func(dst, src *intmodel.ContainerSpec){
	"image":                  func(d, s *intmodel.ContainerSpec) { d.Image = s.Image },
	"command":                func(d, s *intmodel.ContainerSpec) { d.Command = s.Command },
	"args":                   func(d, s *intmodel.ContainerSpec) { d.Args = s.Args },
	"env":                    func(d, s *intmodel.ContainerSpec) { d.Env = s.Env },
	"volumes":                func(d, s *intmodel.ContainerSpec) { d.Volumes = s.Volumes },
	"privileged":             func(d, s *intmodel.ContainerSpec) { d.Privileged = s.Privileged },
	"user":                   func(d, s *intmodel.ContainerSpec) { d.User = s.User },
	"readOnlyRootFilesystem": func(d, s *intmodel.ContainerSpec) { d.ReadOnlyRootFilesystem = s.ReadOnlyRootFilesystem },
	"capabilities":           func(d, s *intmodel.ContainerSpec) { d.Capabilities = s.Capabilities },
	"securityOpts":           func(d, s *intmodel.ContainerSpec) { d.SecurityOpts = s.SecurityOpts },
	"devices":                func(d, s *intmodel.ContainerSpec) { d.Devices = s.Devices },
	"tmpfs":                  func(d, s *intmodel.ContainerSpec) { d.Tmpfs = s.Tmpfs },
	"resources":              func(d, s *intmodel.ContainerSpec) { d.Resources = s.Resources },
	"secrets":                func(d, s *intmodel.ContainerSpec) { d.Secrets = s.Secrets },
	"repos":                  func(d, s *intmodel.ContainerSpec) { d.Repos = s.Repos },
	"workingDir":             func(d, s *intmodel.ContainerSpec) { d.WorkingDir = s.WorkingDir },
	"hostNetwork":            func(d, s *intmodel.ContainerSpec) { d.HostNetwork = s.HostNetwork },
	"hostPID":                func(d, s *intmodel.ContainerSpec) { d.HostPID = s.HostPID },
	"hostCgroup":             func(d, s *intmodel.ContainerSpec) { d.HostCgroup = s.HostCgroup },
	"cniConfigPath":          func(d, s *intmodel.ContainerSpec) { d.CNIConfigPath = s.CNIConfigPath },
	"restartPolicy":          func(d, s *intmodel.ContainerSpec) { d.RestartPolicy = s.RestartPolicy },
	"attachable":             func(d, s *intmodel.ContainerSpec) { d.Attachable = s.Attachable },
	"tty":                    func(d, s *intmodel.ContainerSpec) { d.Tty = s.Tty },
	"git":                    func(d, s *intmodel.ContainerSpec) { d.Git = s.Git },
}

// cellFieldOverlay copies one cell-level field, by the name apply.DiffCell
// reports it under, from src onto dst. metadata.labels is handled apart
// (overlayLabels) because DiffCell compares only the labels kukeon does not
// manage.
//
//nolint:gochecknoglobals // static lookup table, never written.
var cellFieldOverlay = map[string]func(dst, src *intmodel.Cell){
	"metadata.name":            func(d, s *intmodel.Cell) { d.Metadata.Name = s.Metadata.Name },
	"spec.realmName":           func(d, s *intmodel.Cell) { d.Spec.RealmName = s.Spec.RealmName },
	"spec.spaceName":           func(d, s *intmodel.Cell) { d.Spec.SpaceName = s.Spec.SpaceName },
	"spec.stackName":           func(d, s *intmodel.Cell) { d.Spec.StackName = s.Spec.StackName },
	"spec.tty":                 func(d, s *intmodel.Cell) { d.Spec.Tty = s.Spec.Tty },
	"spec.autoDelete":          func(d, s *intmodel.Cell) { d.Spec.AutoDelete = s.Spec.AutoDelete },
	"spec.nestedCgroupRuntime": func(d, s *intmodel.Cell) { d.Spec.NestedCgroupRuntime = s.Spec.NestedCgroupRuntime },
	"metadata.labels":          overlayLabels,
	"spec.rootContainer":       overlayRootAdded,
}

// overlayDrift builds the cell the diff view shows on its "From Config" side
// (issue #1768): the live cell with only the fields apply.DiffCell reported,
// and the render annotations renderProvenanceDriftKeys reported, replaced by
// the fresh render's values. Everything DiffCell does not compare — runner
// stamps such as containerdId or kukeonGroupGID, the synthesized root
// container, the managed labels — keeps its live value, so a cell DiffCell
// calls in sync shows no difference at all, and the result is what a restart
// would apply.
//
// live is not modified: the result has its own label, annotation and
// container collections. Field values copied from desired are shared with it,
// read-only.
func overlayDrift(
	live, desired intmodel.Cell, diff apply.CellDiffResult, drift []provenanceDrift,
) intmodel.Cell {
	shown := live
	shown.Metadata.Labels = maps.Clone(live.Metadata.Labels)
	shown.Metadata.Annotations = maps.Clone(live.Metadata.Annotations)
	shown.Spec.Containers = slices.Clone(live.Spec.Containers)

	for _, field := range slices.Concat(diff.BreakingChanges, diff.ChangedFields) {
		overlayCellField(&shown, &desired, field)
	}
	overlayContainers(&shown, &desired, diff)

	for _, d := range drift {
		if shown.Metadata.Annotations == nil {
			shown.Metadata.Annotations = map[string]string{}
		}
		if value, ok := desired.Metadata.Annotations[d.Key]; ok {
			shown.Metadata.Annotations[d.Key] = value
		} else {
			delete(shown.Metadata.Annotations, d.Key)
		}
	}
	return shown
}

// overlayCellField applies one cell-level name from DiffCell's ChangedFields /
// BreakingChanges. Child-container names (`containers[<id>].<field>`, which
// DiffCell repeats there for breaking changes) are left to overlayContainers.
// It reports whether the name was one it knows.
func overlayCellField(shown, desired *intmodel.Cell, field string) bool {
	if copyField, ok := cellFieldOverlay[field]; ok {
		copyField(shown, desired)
		return true
	}
	if name, ok := strings.CutPrefix(field, rootContainerFieldPrefix); ok {
		dst, src := rootContainerSpec(shown), rootContainerSpec(desired)
		copyField, known := containerFieldOverlay[name]
		if known && dst != nil && src != nil {
			copyField(dst, src)
		}
		return known
	}
	return strings.HasPrefix(field, "containers[")
}

// overlayContainers applies DiffCell's per-container verdicts: added
// containers are appended in the render's order, updated ones get each
// reported field from the render's container with the same ID, and orphans
// (live containers the render no longer has) are removed.
func overlayContainers(shown, desired *intmodel.Cell, diff apply.CellDiffResult) {
	added := make(map[string]bool)
	for _, cd := range diff.Containers {
		switch cd.Action {
		case "add":
			added[cd.Name] = true
		case "update":
			dst, src := childContainerSpec(shown, cd.Name), childContainerSpec(desired, cd.Name)
			if dst == nil || src == nil {
				continue
			}
			for _, field := range slices.Concat(cd.BreakingChanges, cd.ChangedFields) {
				if copyField, ok := containerFieldOverlay[field]; ok {
					copyField(dst, src)
				}
			}
		}
	}
	for _, c := range desired.Spec.Containers {
		if !c.Root && added[c.ID] {
			shown.Spec.Containers = append(shown.Spec.Containers, c)
		}
	}
	if len(diff.Orphans) > 0 {
		shown.Spec.Containers = slices.DeleteFunc(shown.Spec.Containers, func(c intmodel.ContainerSpec) bool {
			return !c.Root && slices.Contains(diff.Orphans, c.ID)
		})
	}
}

// overlayLabels replaces the labels kukeon does not manage with the render's,
// keeping the managed ones (apply.IsManagedLabel), the same split DiffCell's
// metadata.labels comparison makes.
func overlayLabels(shown, desired *intmodel.Cell) {
	labels := make(map[string]string, len(shown.Metadata.Labels)+len(desired.Metadata.Labels))
	for k, v := range shown.Metadata.Labels {
		if apply.IsManagedLabel(k) {
			labels[k] = v
		}
	}
	for k, v := range desired.Metadata.Labels {
		if !apply.IsManagedLabel(k) {
			labels[k] = v
		}
	}
	shown.Metadata.Labels = labels
}

// overlayRootAdded inserts the render's root container when the live cell has
// none (DiffCell's spec.rootContainer).
func overlayRootAdded(shown, desired *intmodel.Cell) {
	root := rootContainerSpec(desired)
	if root == nil || rootContainerSpec(shown) != nil {
		return
	}
	shown.Spec.Containers = slices.Insert(shown.Spec.Containers, 0, *root)
}

// rootContainerSpec returns the cell's Root container, as DiffCell finds it.
func rootContainerSpec(cell *intmodel.Cell) *intmodel.ContainerSpec {
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].Root {
			return &cell.Spec.Containers[i]
		}
	}
	return nil
}

// childContainerSpec returns the cell's non-root container with the ID, as
// DiffCell pairs them.
func childContainerSpec(cell *intmodel.Cell, id string) *intmodel.ContainerSpec {
	for i := range cell.Spec.Containers {
		if !cell.Spec.Containers[i].Root && cell.Spec.Containers[i].ID == id {
			return &cell.Spec.Containers[i]
		}
	}
	return nil
}
