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

package uninstall_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/uninstall"
)

// fakeSystemctl records every systemctl invocation as one space-joined
// string, so tests can assert the verbs and their order.
type fakeSystemctl struct {
	calls []string
}

func (f *fakeSystemctl) run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	return nil
}

func foundSystemctl(string) (string, error) { return "/usr/bin/systemctl", nil }

func missingSystemctl(string) (string, error) { return "", exec.ErrNotFound }

// tempUnits returns the production units with their paths moved under a
// temp dir; present names the units whose file is written there.
func tempUnits(t *testing.T, present ...string) []uninstall.SystemdUnit {
	t.Helper()
	dir := t.TempDir()
	units := uninstall.DefaultSystemdUnits()
	for i := range units {
		units[i].Path = filepath.Join(dir, units[i].Name)
		for _, name := range present {
			if name == units[i].Name {
				if err := os.WriteFile(units[i].Path, []byte("[Unit]\n"), 0o600); err != nil {
					t.Fatalf("write unit: %v", err)
				}
			}
		}
	}
	return units
}

func assertFileGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected %s to be removed; stat err = %v", path, err)
	}
}

func TestDefaultSystemdUnits_WebUIFirst(t *testing.T) {
	want := []uninstall.SystemdUnit{
		{Name: "kuke-serve.service", Path: "/etc/systemd/system/kuke-serve.service"},
		{Name: "kukeond.service", Path: "/etc/systemd/system/kukeond.service"},
	}
	if got := uninstall.DefaultSystemdUnits(); !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultSystemdUnits() = %+v; want %+v", got, want)
	}
}

func TestRemoveSystemdUnits_BothPresent(t *testing.T) {
	units := tempUnits(t, uninstall.WebUISystemdUnitName, uninstall.SystemdUnitName)
	fake := &fakeSystemctl{}

	reports := uninstall.RemoveSystemdUnits(context.Background(), foundSystemctl, fake.run, units)

	wantCalls := []string{
		"stop kuke-serve.service",
		"disable kuke-serve.service",
		"stop kukeond.service",
		"disable kukeond.service",
		"daemon-reload",
	}
	if !reflect.DeepEqual(fake.calls, wantCalls) {
		t.Errorf("systemctl calls = %q; want %q", fake.calls, wantCalls)
	}
	if len(reports) != len(units) {
		t.Fatalf("got %d reports; want %d", len(reports), len(units))
	}
	for i, r := range reports {
		want := uninstall.SystemdRemovalReport{
			UnitPath:       units[i].Path,
			Stopped:        true,
			Disabled:       true,
			FileRemoved:    true,
			DaemonReloaded: true,
		}
		if r != want {
			t.Errorf("report[%d] = %+v; want %+v", i, r, want)
		}
		assertFileGone(t, units[i].Path)
	}
}

func TestRemoveSystemdUnits_OnlyKukeondPresent(t *testing.T) {
	units := tempUnits(t, uninstall.SystemdUnitName)
	fake := &fakeSystemctl{}

	reports := uninstall.RemoveSystemdUnits(context.Background(), foundSystemctl, fake.run, units)

	wantCalls := []string{
		"stop kukeond.service",
		"disable kukeond.service",
		"daemon-reload",
	}
	if !reflect.DeepEqual(fake.calls, wantCalls) {
		t.Errorf("systemctl calls = %q; want %q", fake.calls, wantCalls)
	}
	want := []uninstall.SystemdRemovalReport{
		{UnitPath: units[0].Path, Skipped: true, SkipReason: "unit file absent"},
		{
			UnitPath:       units[1].Path,
			Stopped:        true,
			Disabled:       true,
			FileRemoved:    true,
			DaemonReloaded: true,
		},
	}
	if len(reports) != len(want) {
		t.Fatalf("got %d reports; want %d", len(reports), len(want))
	}
	for i := range want {
		if reports[i] != want[i] {
			t.Errorf("report[%d] = %+v; want %+v", i, reports[i], want[i])
		}
	}
	assertFileGone(t, units[1].Path)
}

func TestRemoveSystemdUnits_NonePresentSkipsDaemonReload(t *testing.T) {
	units := tempUnits(t)
	fake := &fakeSystemctl{}

	reports := uninstall.RemoveSystemdUnits(context.Background(), foundSystemctl, fake.run, units)

	if len(fake.calls) != 0 {
		t.Errorf("expected no systemctl calls; got %q", fake.calls)
	}
	for i, r := range reports {
		if !r.Skipped || r.SkipReason != "unit file absent" {
			t.Errorf("report[%d] = %+v; want skipped with %q", i, r, "unit file absent")
		}
	}
}

func TestRemoveSystemdUnits_NoSystemctl(t *testing.T) {
	units := tempUnits(t, uninstall.WebUISystemdUnitName, uninstall.SystemdUnitName)
	fake := &fakeSystemctl{}

	reports := uninstall.RemoveSystemdUnits(context.Background(), missingSystemctl, fake.run, units)

	if len(fake.calls) != 0 {
		t.Errorf("expected no systemctl calls; got %q", fake.calls)
	}
	if len(reports) != len(units) {
		t.Fatalf("got %d reports; want %d", len(reports), len(units))
	}
	for i, r := range reports {
		want := uninstall.SystemdRemovalReport{
			UnitPath:   units[i].Path,
			Skipped:    true,
			SkipReason: "systemctl not found in PATH",
		}
		if r != want {
			t.Errorf("report[%d] = %+v; want %+v", i, r, want)
		}
		// The unit files must be left alone: without systemctl there is no
		// way to stop the units first.
		if _, err := os.Stat(units[i].Path); err != nil {
			t.Errorf("unit file %s should be untouched; stat err = %v", units[i].Path, err)
		}
	}
}
