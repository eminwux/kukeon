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

//nolint:testpackage // Tests inner workings of the cgroups manager cache.
package ctr

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func setupTestClientForCgroupsCache(t *testing.T) *client {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewClient(ctx, logger, "/test/socket").(*client)
}

func TestStoreManagerInitializesMap(t *testing.T) {
	c := setupTestClientForCgroupsCache(t)
	c.cgroups = nil // Simulate uninitialized map

	c.storeManager("/kukeon/test", nil)

	if c.cgroups == nil {
		t.Fatal("cgroups map should be initialized")
	}
	if _, ok := c.cgroups["/kukeon/test"]; !ok {
		t.Error("manager should be stored in cache")
	}
}

func TestDropManagerNilMap(t *testing.T) {
	c := setupTestClientForCgroupsCache(t)
	c.cgroups = nil // Should not panic

	c.dropManager("/kukeon/test")
}

func TestDropManager(t *testing.T) {
	c := setupTestClientForCgroupsCache(t)
	c.storeManager("/kukeon/test", nil)

	c.dropManager("/kukeon/test")

	if _, ok := c.cgroups["/kukeon/test"]; ok {
		t.Error("manager should be removed from cache")
	}
}

// TestCgroupsCacheConcurrency drives concurrent store/drop/load of the cgroups
// manager cache so `go test -race` flags any unsynchronized map access. The
// load path (managerFor) is exercised against a pre-populated, never-dropped
// group so it stays a cache hit and never falls through to cgroup2.LoadManager
// (which would touch the filesystem); the store/drop churn runs on a disjoint
// set of groups concurrently. Without cgroupsMu this races with a fatal
// "concurrent map writes" / detector report.
func TestCgroupsCacheConcurrency(t *testing.T) {
	c := setupTestClientForCgroupsCache(t)

	const stable = "/kukeon/stable"
	c.storeManager(stable, nil) // cache hit target for the load path

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers * 3)

	for w := 0; w < workers; w++ {
		group := fmt.Sprintf("/kukeon/churn-%d", w)
		go func() {
			defer wg.Done()
			c.storeManager(group, nil)
		}()
		go func() {
			defer wg.Done()
			c.dropManager(group)
		}()
		go func() {
			defer wg.Done()
			if _, err := c.managerFor(stable, "/sys/fs/cgroup"); err != nil {
				t.Errorf("managerFor(%q) cache hit returned error: %v", stable, err)
			}
		}()
	}

	wg.Wait()
}
