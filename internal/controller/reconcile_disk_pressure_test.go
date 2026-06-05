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
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/diskpressure"
)

func diskPressureControllerExec(
	buf *bytes.Buffer,
	warnPct int,
	sample func(string) (diskpressure.Usage, error),
) *Exec {
	return &Exec{
		ctx:         context.Background(),
		logger:      slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
		opts:        Options{RunPath: "/run/kukeon-test", DiskPressureWarnPercent: warnPct},
		diskWarner:  diskpressure.NewWarner(5 * time.Minute),
		diskSampler: sample,
	}
}

func twoRealms() []intmodel.Realm {
	return []intmodel.Realm{
		{Metadata: intmodel.RealmMetadata{Name: "default"}},
		{Metadata: intmodel.RealmMetadata{Name: "kuke-system"}},
	}
}

func TestCheckDiskPressure_WarnsOverThreshold(t *testing.T) {
	var buf bytes.Buffer
	r := diskPressureControllerExec(&buf, 85, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 90, TotalBytes: 1000, AvailBytes: 100}, nil
	})

	r.checkDiskPressure(twoRealms())

	out := buf.String()
	if !strings.Contains(out, "data volume under disk pressure") {
		t.Fatalf("expected disk-pressure WARN, got: %q", out)
	}
	if !strings.Contains(out, "default") || !strings.Contains(out, "kuke-system") {
		t.Errorf("expected both realms named in WARNs, got: %q", out)
	}
	if !strings.Contains(out, "usedPercent=90.0") {
		t.Errorf("expected usedPercent in WARN, got: %q", out)
	}
}

func TestCheckDiskPressure_SilentBelowThreshold(t *testing.T) {
	var buf bytes.Buffer
	r := diskPressureControllerExec(&buf, 85, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 50}, nil
	})

	r.checkDiskPressure(twoRealms())

	if buf.Len() != 0 {
		t.Errorf("expected no WARN below threshold, got: %q", buf.String())
	}
}

func TestCheckDiskPressure_DisabledWhenThresholdZero(t *testing.T) {
	var buf bytes.Buffer
	called := false
	r := diskPressureControllerExec(&buf, 0, func(string) (diskpressure.Usage, error) {
		called = true
		return diskpressure.Usage{UsedPercent: 99}, nil
	})

	r.checkDiskPressure(twoRealms())

	if called {
		t.Error("disabled warn sampled the volume; expected an early return")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no WARN when disabled, got: %q", buf.String())
	}
}

func TestCheckDiskPressure_RateLimitedAcrossPasses(t *testing.T) {
	var buf bytes.Buffer
	r := diskPressureControllerExec(&buf, 85, func(string) (diskpressure.Usage, error) {
		return diskpressure.Usage{UsedPercent: 99}, nil
	})

	r.checkDiskPressure(twoRealms())
	first := strings.Count(buf.String(), "data volume under disk pressure")
	if first != 2 {
		t.Fatalf("first pass: got %d WARNs, want 2 (one per realm)", first)
	}

	// A second pass inside the rate-limit window must not re-emit.
	r.checkDiskPressure(twoRealms())
	total := strings.Count(buf.String(), "data volume under disk pressure")
	if total != 2 {
		t.Errorf("second pass within window re-warned: got %d total WARNs, want 2", total)
	}
}
