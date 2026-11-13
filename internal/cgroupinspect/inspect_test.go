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

package cgroupinspect_test

import (
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/cgroupinspect"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestParseValue(t *testing.T) {
	if got := cgroupinspect.ParseValue("max\n"); got.State != kukeonv1.CgroupValueMax || got.Value != nil {
		t.Fatalf("max: got %+v", got)
	}
	got := cgroupinspect.ParseValue("0\n")
	if got.State != kukeonv1.CgroupValueSet || got.Value == nil || *got.Value != 0 {
		t.Fatalf("0: got %+v", got)
	}
	got = cgroupinspect.ParseValue("536870912")
	if got.Value == nil || *got.Value != 536870912 || got.Raw != "536870912" {
		t.Fatalf("number: got %+v", got)
	}
	got = cgroupinspect.ParseValue("weird")
	if got.State != kukeonv1.CgroupValueSet || got.Value != nil || got.Raw != "weird" {
		t.Fatalf("non-numeric: got %+v", got)
	}
}

func TestParseCPUMax(t *testing.T) {
	got := cgroupinspect.ParseCPUMax("max 100000\n")
	if got.State != kukeonv1.CgroupValueMax || got.QuotaMicros != nil || got.PeriodMicros == nil ||
		*got.PeriodMicros != 100000 {
		t.Fatalf("max quota: got %+v", got)
	}
	got = cgroupinspect.ParseCPUMax("50000 100000")
	if got.State != kukeonv1.CgroupValueSet || got.QuotaMicros == nil || *got.QuotaMicros != 50000 ||
		got.PeriodMicros == nil || *got.PeriodMicros != 100000 {
		t.Fatalf("quota/period: got %+v", got)
	}
}

func TestParseIOMax(t *testing.T) {
	got := cgroupinspect.ParseIOMax("8:0 rbps=1048576 wbps=max riops=max wiops=120\n")
	if got.State != kukeonv1.CgroupValueSet || len(got.Entries) != 1 {
		t.Fatalf("got %+v", got)
	}
	e := got.Entries[0]
	if e.Device != "8:0" || e.Limits["rbps"] != "1048576" || e.Limits["wbps"] != "max" || e.Limits["wiops"] != "120" {
		t.Fatalf("entry: got %+v", e)
	}
	if empty := cgroupinspect.ParseIOMax(""); empty.State != kukeonv1.CgroupValueSet || len(empty.Entries) != 0 {
		t.Fatalf("empty: got %+v", empty)
	}
}

func TestInspect_AbsentCgroup(t *testing.T) {
	exists, live, err := cgroupinspect.Inspect(fstest.MapFS{}, "/kukeon/default")
	if err != nil || exists || live != nil {
		t.Fatalf("got exists=%v live=%+v err=%v", exists, live, err)
	}
}

func TestFSPath_StaysUnderMountpoint(t *testing.T) {
	for in, want := range map[string]string{
		"/../../etc":      "etc",
		"/":               ".",
		"":                ".",
		"/kukeon/default": "kukeon/default",
	} {
		if got := cgroupinspect.FSPath(in); got != want {
			t.Fatalf("FSPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInspect_LiveValues(t *testing.T) {
	fsys := fstest.MapFS{
		"kukeon/default/cell/cgroup.controllers":     file("cpu memory pids\n"),
		"kukeon/default/cell/cgroup.subtree_control": file("\n"),
		"kukeon/default/cell/cgroup.events":          file("populated 1\nfrozen 0\n"),
		"kukeon/default/cell/memory.max":             file("max\n"),
		"kukeon/default/cell/memory.high":            file("268435456\n"),
		"kukeon/default/cell/memory.current":         file("4096\n"),
		"kukeon/default/cell/memory.events":          file("low 0\nhigh 0\nmax 0\noom 0\noom_kill 2\n"),
		"kukeon/default/cell/cpu.max":                file("max 100000\n"),
		"kukeon/default/cell/cpu.weight":             file("100\n"),
		"kukeon/default/cell/cpu.stat":               file("usage_usec 1234\nuser_usec 1000\nsystem_usec 234\n"),
		"kukeon/default/cell/pids.max":               file("max\n"),
		"kukeon/default/cell/pids.current":           file("0\n"),
	}
	exists, live, err := cgroupinspect.Inspect(fsys, "/kukeon/default/cell")
	if err != nil || !exists || live == nil {
		t.Fatalf("got exists=%v err=%v", exists, err)
	}
	if got := live.Controllers.Values; len(got) != 3 || got[1] != "memory" {
		t.Fatalf("controllers: %+v", live.Controllers)
	}
	if live.SubtreeControl.State != kukeonv1.CgroupValueSet || len(live.SubtreeControl.Values) != 0 {
		t.Fatalf("subtree_control: %+v", live.SubtreeControl)
	}
	if live.Populated.Value == nil || *live.Populated.Value != 1 {
		t.Fatalf("populated: %+v", live.Populated)
	}
	if live.Memory.Max.State != kukeonv1.CgroupValueMax {
		t.Fatalf("memory.max: %+v", live.Memory.Max)
	}
	// Files the kernel does not provide stay absent — never 0.
	for name, v := range map[string]kukeonv1.CgroupValue{
		"memory.swap.max": live.Memory.SwapMax,
		"memory.peak":     live.Memory.Peak,
	} {
		if v.State != kukeonv1.CgroupValueAbsent || v.Value != nil {
			t.Fatalf("%s: want absent, got %+v", name, v)
		}
	}
	if live.Memory.OOMKill.Value == nil || *live.Memory.OOMKill.Value != 2 {
		t.Fatalf("oom_kill: %+v", live.Memory.OOMKill)
	}
	if live.CPU.Max.State != kukeonv1.CgroupValueMax || live.CPU.UsageUsec.Value == nil ||
		*live.CPU.UsageUsec.Value != 1234 {
		t.Fatalf("cpu: %+v", live.CPU)
	}
	if live.Pids.Current.State != kukeonv1.CgroupValueSet || live.Pids.Current.Value == nil ||
		*live.Pids.Current.Value != 0 {
		t.Fatalf("pids.current: a real 0 must stay set, got %+v", live.Pids.Current)
	}
	if live.IO.State != kukeonv1.CgroupValueAbsent {
		t.Fatalf("io.max: want absent, got %+v", live.IO)
	}
}

func TestInspect_AbsentKeyedFile(t *testing.T) {
	fsys := fstest.MapFS{"kukeon/c/cgroup.controllers": file("")}
	_, live, err := cgroupinspect.Inspect(fsys, "/kukeon/c")
	if err != nil {
		t.Fatal(err)
	}
	if live.CPU.UsageUsec.State != kukeonv1.CgroupValueAbsent || live.Memory.OOM.State != kukeonv1.CgroupValueAbsent {
		t.Fatalf("keyed files absent: %+v %+v", live.CPU.UsageUsec, live.Memory.OOM)
	}
	if live.CPU.Max.State != kukeonv1.CgroupValueAbsent || live.Memory.Max.State != kukeonv1.CgroupValueAbsent {
		t.Fatalf("controller not enabled: %+v %+v", live.CPU.Max, live.Memory.Max)
	}
}
