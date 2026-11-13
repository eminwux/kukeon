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

// Package cgroupinspect reads a cgroup v2 directory's interface files into
// the kukeonv1 cgroup-detail wire shape (issue #1410). It reads files
// directly rather than through cgroup2.Manager.Stat because Stat collapses a
// missing interface file to 0, and the detail view must keep "absent",
// "max/unlimited" and a real 0 distinct.
package cgroupinspect

import (
	"errors"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// FSPath converts an absolute cgroup group path (as recorded in a resource's
// status.cgroupPath) into an fs.FS path rooted at the cgroup mountpoint.
// Cleaning against "/" first means ".." segments cannot climb above the
// mountpoint.
func FSPath(group string) string {
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(group)), "/")
	if rel == "" {
		return "."
	}
	return rel
}

// Inspect reports whether the cgroup at group exists under fsys (rooted at the
// cgroup mountpoint) and, when it does, its live interface values.
func Inspect(fsys fs.FS, group string) (bool, *kukeonv1.CgroupLive, error) {
	dir := FSPath(group)
	info, err := fs.Stat(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil, nil
		}
		return false, nil, err
	}
	if !info.IsDir() {
		return false, nil, nil
	}
	r := reader{fsys: fsys, dir: dir}
	memEvents := r.keyed("memory.events")
	cpuStat := r.keyed("cpu.stat")
	live := &kukeonv1.CgroupLive{
		Controllers:    r.list("cgroup.controllers"),
		SubtreeControl: r.list("cgroup.subtree_control"),
		Populated:      r.keyed("cgroup.events").get("populated"),
		Memory: kukeonv1.CgroupMemory{
			Max:     r.value("memory.max"),
			High:    r.value("memory.high"),
			SwapMax: r.value("memory.swap.max"),
			Current: r.value("memory.current"),
			Peak:    r.value("memory.peak"),
			OOM:     memEvents.get("oom"),
			OOMKill: memEvents.get("oom_kill"),
		},
		CPU: kukeonv1.CgroupCPU{
			Max:        r.cpuMax("cpu.max"),
			Weight:     r.value("cpu.weight"),
			UsageUsec:  cpuStat.get("usage_usec"),
			UserUsec:   cpuStat.get("user_usec"),
			SystemUsec: cpuStat.get("system_usec"),
		},
		Pids: kukeonv1.CgroupPids{
			Max:     r.value("pids.max"),
			Current: r.value("pids.current"),
		},
		IO: r.ioMax("io.max"),
	}
	return true, live, nil
}

type reader struct {
	fsys fs.FS
	dir  string
}

// read returns the trimmed file content and the state describing the read.
func (r reader) read(name string) (string, string) {
	data, err := fs.ReadFile(r.fsys, path.Join(r.dir, name))
	switch {
	case err == nil:
		return strings.TrimSpace(string(data)), kukeonv1.CgroupValueSet
	case errors.Is(err, fs.ErrNotExist):
		return "", kukeonv1.CgroupValueAbsent
	default:
		return "", kukeonv1.CgroupValueUnreadable
	}
}

func (r reader) value(name string) kukeonv1.CgroupValue {
	raw, state := r.read(name)
	if state != kukeonv1.CgroupValueSet {
		return kukeonv1.CgroupValue{State: state}
	}
	return ParseValue(raw)
}

func (r reader) list(name string) kukeonv1.CgroupList {
	raw, state := r.read(name)
	if state != kukeonv1.CgroupValueSet {
		return kukeonv1.CgroupList{State: state}
	}
	return kukeonv1.CgroupList{State: state, Values: strings.Fields(raw)}
}

func (r reader) cpuMax(name string) kukeonv1.CgroupCPUMax {
	raw, state := r.read(name)
	if state != kukeonv1.CgroupValueSet {
		return kukeonv1.CgroupCPUMax{State: state}
	}
	return ParseCPUMax(raw)
}

func (r reader) ioMax(name string) kukeonv1.CgroupIOMax {
	raw, state := r.read(name)
	if state != kukeonv1.CgroupValueSet {
		return kukeonv1.CgroupIOMax{State: state}
	}
	return ParseIOMax(raw)
}

// keyedFile is a parsed flat-keyed file such as cpu.stat or memory.events.
type keyedFile struct {
	state  string
	values map[string]string
}

func (r reader) keyed(name string) keyedFile {
	raw, state := r.read(name)
	if state != kukeonv1.CgroupValueSet {
		return keyedFile{state: state}
	}
	return keyedFile{state: state, values: ParseKeyed(raw)}
}

// get returns key's value; a missing key in an existing file is absent.
func (k keyedFile) get(key string) kukeonv1.CgroupValue {
	if k.state != kukeonv1.CgroupValueSet {
		return kukeonv1.CgroupValue{State: k.state}
	}
	raw, ok := k.values[key]
	if !ok {
		return kukeonv1.CgroupValue{State: kukeonv1.CgroupValueAbsent}
	}
	return ParseValue(raw)
}

// ParseValue parses a single-value interface file. "max" is unlimited; an
// integer populates Value; anything else keeps only Raw.
func ParseValue(raw string) kukeonv1.CgroupValue {
	raw = strings.TrimSpace(raw)
	if raw == "max" {
		return kukeonv1.CgroupValue{State: kukeonv1.CgroupValueMax, Raw: raw}
	}
	v := kukeonv1.CgroupValue{State: kukeonv1.CgroupValueSet, Raw: raw}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		v.Value = &n
	}
	return v
}

// ParseCPUMax parses cpu.max ("$QUOTA $PERIOD", QUOTA may be "max"). A quota
// of "max" reports State CgroupValueMax with the period still populated.
func ParseCPUMax(raw string) kukeonv1.CgroupCPUMax {
	raw = strings.TrimSpace(raw)
	out := kukeonv1.CgroupCPUMax{State: kukeonv1.CgroupValueSet, Raw: raw}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return out
	}
	if len(fields) > 1 {
		if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			out.PeriodMicros = &n
		}
	}
	if fields[0] == "max" {
		out.State = kukeonv1.CgroupValueMax
		return out
	}
	if n, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
		out.QuotaMicros = &n
	}
	return out
}

// ParseKeyed parses a flat-keyed file ("key value" per line).
func ParseKeyed(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

// ParseIOMax parses io.max ("MAJ:MIN key=value ..." per line).
func ParseIOMax(raw string) kukeonv1.CgroupIOMax {
	out := kukeonv1.CgroupIOMax{State: kukeonv1.CgroupValueSet}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		entry := kukeonv1.CgroupIOMaxEntry{Device: fields[0], Limits: map[string]string{}}
		for _, kv := range fields[1:] {
			if k, v, ok := strings.Cut(kv, "="); ok {
				entry.Limits[k] = v
			}
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}
