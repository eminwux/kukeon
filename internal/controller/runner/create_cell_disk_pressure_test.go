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
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/diskpressure"
)

func diskPressureTestExec(blockPct int, sample func(string) (diskpressure.Usage, error)) *Exec {
	return &Exec{
		ctx:         context.Background(),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:        Options{RunPath: "/run/kukeon-test", DiskPressureBlockPercent: blockPct},
		diskSampler: sample,
	}
}

func cellForGuard(ignore bool) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "c1"},
		Spec:     intmodel.CellSpec{RealmName: "default", IgnoreDiskPressure: ignore},
	}
}

func TestGuardDiskPressure_BlocksAtOrAboveThreshold(t *testing.T) {
	r := diskPressureTestExec(95, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 96, TotalBytes: 100, AvailBytes: 4}, nil
	})
	err := r.guardDiskPressure(cellForGuard(false))
	if !errors.Is(err, errdefs.ErrDiskPressure) {
		t.Fatalf("guardDiskPressure at 96%% (block 95%%): got %v, want ErrDiskPressure", err)
	}
}

func TestGuardDiskPressure_AllowsBelowThreshold(t *testing.T) {
	r := diskPressureTestExec(95, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 80}, nil
	})
	if err := r.guardDiskPressure(cellForGuard(false)); err != nil {
		t.Fatalf("guardDiskPressure at 80%% (block 95%%): got %v, want nil", err)
	}
}

func TestGuardDiskPressure_OverrideBypasses(t *testing.T) {
	r := diskPressureTestExec(95, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 99}, nil
	})
	if err := r.guardDiskPressure(cellForGuard(true)); err != nil {
		t.Fatalf("guardDiskPressure with IgnoreDiskPressure at 99%%: got %v, want nil", err)
	}
}

func TestGuardDiskPressure_DisabledWhenThresholdZero(t *testing.T) {
	called := false
	r := diskPressureTestExec(0, func(string) (diskpressure.Usage, error) {
		called = true
		return diskpressure.Usage{UsedPercent: 99}, nil
	})
	if err := r.guardDiskPressure(cellForGuard(false)); err != nil {
		t.Fatalf("guardDiskPressure with block threshold 0: got %v, want nil", err)
	}
	if called {
		t.Error("disabled guard sampled the volume; expected an early return before statfs")
	}
}

func TestGuardDiskPressure_SampleErrorAllowsCreation(t *testing.T) {
	r := diskPressureTestExec(95, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{}, errors.New("statfs boom")
	})
	if err := r.guardDiskPressure(cellForGuard(false)); err != nil {
		t.Fatalf("guardDiskPressure on sample error: got %v, want nil (fail-open)", err)
	}
}
