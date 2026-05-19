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

package metadata_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
)

// discardLogger returns a slog logger that drops every record — keeps
// test output clean while exercising the production code paths that
// expect a non-nil *slog.Logger.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestExclusiveLockBlocksExclusiveAndShared locks the exclusive-blocks-
// every-other-acquisition invariant: while a goroutine holds the
// exclusive sidecar flock, both another exclusive acquisition and a
// shared acquisition must block until the original holder releases.
//
// We assert blocking by running the second acquisition with a short
// deadline against a timer-armed release. Acquisitions that complete
// before the release fires would falsify the invariant.
func TestExclusiveLockBlocksExclusiveAndShared(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	// Seed the file so the lock parent exists from the start.
	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"k": "v"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}

	cases := []struct {
		name     string
		acquire  func() error
		exclusiv bool
	}{
		{
			name: "exclusive blocks exclusive",
			acquire: func() error {
				return metadata.WithExclusiveLock(ctx, logger, file, func() error { return nil })
			},
		},
		{
			name: "exclusive blocks shared",
			acquire: func() error {
				return metadata.WithSharedLock(ctx, logger, file, func() error { return nil })
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			holdStarted := make(chan struct{})
			releaseAt := 200 * time.Millisecond
			holdDone := make(chan struct{})

			go func() {
				_ = metadata.WithExclusiveLock(ctx, logger, file, func() error {
					close(holdStarted)
					time.Sleep(releaseAt)
					return nil
				})
				close(holdDone)
			}()

			// Wait for the holder to have the lock in hand before
			// we race to acquire it.
			<-holdStarted

			acquireStart := time.Now()
			if err := tc.acquire(); err != nil {
				t.Fatalf("contended acquire returned error: %v", err)
			}
			elapsed := time.Since(acquireStart)

			// The contended acquisition must have waited at
			// least most of the hold duration. A generous lower
			// bound (60% of the hold) avoids flakes on busy
			// schedulers while still failing if the lock was
			// not respected at all.
			minWait := time.Duration(float64(releaseAt) * 0.6)
			if elapsed < minWait {
				t.Fatalf("contended acquire took %v, expected ≥ %v — exclusive lock was not respected", elapsed, minWait)
			}

			<-holdDone
		})
	}
}

// TestSharedLockAllowsSharedConcurrency asserts the read-side parallel
// admission contract: multiple shared holders must run concurrently. We
// gate two goroutines on a shared flock and check that the second one
// enters before the first releases. If the implementation degenerated
// shared to exclusive, the second would block on the first.
func TestSharedLockAllowsSharedConcurrency(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"k": "v"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}

	const holdDuration = 200 * time.Millisecond

	firstInside := make(chan struct{})
	secondInside := make(chan struct{})
	var firstReleased atomic.Bool

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = metadata.WithSharedLock(ctx, logger, file, func() error {
			close(firstInside)
			time.Sleep(holdDuration)
			firstReleased.Store(true)
			return nil
		})
	}()

	go func() {
		defer wg.Done()
		// Wait for the first holder to be inside its critical
		// section so we exercise the concurrent-shared path
		// rather than a sequential acquire-release-acquire.
		<-firstInside
		_ = metadata.WithSharedLock(ctx, logger, file, func() error {
			if firstReleased.Load() {
				t.Errorf("second shared holder entered after first released — that exercises sequential acquisition, not concurrent")
			}
			close(secondInside)
			return nil
		})
	}()

	select {
	case <-secondInside:
		// Concurrent admission confirmed.
	case <-time.After(holdDuration / 2):
		t.Fatalf("second shared holder did not enter within %v — shared lock did not admit concurrent reader", holdDuration/2)
	}

	wg.Wait()
}

// TestConcurrentWritersDoNotTearBytes hammers the same metadata file
// from many goroutines, each writing a different payload. With the
// exclusive flock holding the read-and-rename window, every final read
// observed during and after the storm must be one of the well-formed
// payloads any writer produced — never a partial or interleaved one.
func TestConcurrentWritersDoNotTearBytes(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	const (
		writers      = 8
		writesEach   = 20
		readerProbes = 200
	)

	// Pre-create the parent so the first lock acquisition does not
	// race the very first writer's MkdirAll. Cheap, deterministic.
	if err := os.MkdirAll(filepath.Dir(file), 0o0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}

	type payload struct {
		Writer int    `json:"writer"`
		Seq    int    `json:"seq"`
		Filler string `json:"filler"`
	}

	// A wide filler keeps each payload above 4 KiB so a torn write
	// (multiple sub-page writes interleaved at the kernel) would be
	// observable as an unmarshal failure or as a Filler whose length
	// does not match its expected size.
	const fillerLen = 8 * 1024
	filler := make([]byte, fillerLen)
	for i := range filler {
		filler[i] = 'x'
	}
	fillerStr := string(filler)

	var wg sync.WaitGroup

	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for s := 0; s < writesEach; s++ {
				doc := payload{Writer: id, Seq: s, Filler: fillerStr}
				if err := metadata.WriteMetadata(ctx, logger, doc, file); err != nil {
					t.Errorf("writer %d seq %d: %v", id, s, err)
					return
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < readerProbes; i++ {
			raw, err := metadata.ReadRaw(ctx, logger, file)
			if err != nil {
				if errors.Is(err, errdefs.ErrMissingMetadataFile) {
					// First reader may arrive before the
					// first writer renames; allow that.
					continue
				}
				t.Errorf("reader probe %d: %v", i, err)
				return
			}
			var got payload
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Errorf("reader probe %d: unmarshal failed on %d bytes — torn write surfaced: %v", i, len(raw), err)
				return
			}
			if len(got.Filler) != fillerLen {
				t.Errorf("reader probe %d: filler length %d, want %d — torn payload surfaced", i, len(got.Filler), fillerLen)
				return
			}
		}
	}()

	wg.Wait()

	// Final read must parse cleanly to one of the writers' payloads.
	raw, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("final ReadRaw: %v", err)
	}
	var final payload
	if err := json.Unmarshal(raw, &final); err != nil {
		t.Fatalf("final unmarshal: %v on %d bytes", err, len(raw))
	}
	if final.Writer < 0 || final.Writer >= writers || final.Seq < 0 || final.Seq >= writesEach {
		t.Fatalf("final payload out of range: %+v", final)
	}
	if len(final.Filler) != fillerLen {
		t.Fatalf("final filler length %d, want %d", len(final.Filler), fillerLen)
	}
}

// TestWriteMetadataCAS_FirstCreateAndSecondCreateRace covers the two
// boundary conditions of the CAS contract: a first writer creating the
// file from scratch (prior == nil, no file present) succeeds, and a
// second writer that also passed prior == nil after the file exists
// fails with ErrStaleResource.
func TestWriteMetadataCAS_FirstCreateAndSecondCreateRace(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	doc := map[string]string{"phase": "one"}
	if err := metadata.WriteMetadataCAS(ctx, logger, nil, doc, file); err != nil {
		t.Fatalf("first CAS create: %v", err)
	}

	// Same prior == nil now stale: file exists.
	err := metadata.WriteMetadataCAS(ctx, logger, nil, map[string]string{"phase": "two"}, file)
	if !errors.Is(err, errdefs.ErrStaleResource) {
		t.Fatalf("second CAS with prior==nil: err = %v, want ErrStaleResource", err)
	}
}

// TestWriteMetadataCAS_StaleAfterIntermediateWrite covers the canonical
// adoption pattern: a caller does ReadRaw + mutate + WriteMetadataCAS,
// but another writer slipped a write in between. The CAS must fail with
// ErrStaleResource so the caller knows to retry.
func TestWriteMetadataCAS_StaleAfterIntermediateWrite(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"v": "0"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}
	prior, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw prior: %v", err)
	}

	// Intervening writer — bytes on disk now diverge from `prior`.
	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"v": "intervening"}, file); err != nil {
		t.Fatalf("intervening WriteMetadata: %v", err)
	}

	err = metadata.WriteMetadataCAS(ctx, logger, prior, map[string]string{"v": "caller-update"}, file)
	if !errors.Is(err, errdefs.ErrStaleResource) {
		t.Fatalf("CAS after intervening write: err = %v, want ErrStaleResource", err)
	}

	// On-disk payload must still be the intervening writer's value —
	// CAS must not have written through on a stale check.
	after, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw after: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatalf("unmarshal after: %v", err)
	}
	if got["v"] != "intervening" {
		t.Fatalf("CAS over-wrote on stale check: got %v, want 'intervening'", got["v"])
	}
}

// TestWriteMetadataCAS_SucceedsWhenPriorMatches exercises the happy
// path: a caller reads, then writes back under the same observed bytes
// without anyone else racing. The new payload must land on disk.
func TestWriteMetadataCAS_SucceedsWhenPriorMatches(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"v": "0"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}
	prior, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw prior: %v", err)
	}

	if err := metadata.WriteMetadataCAS(ctx, logger, prior, map[string]string{"v": "1"}, file); err != nil {
		t.Fatalf("CAS happy path: %v", err)
	}

	raw, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw after: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["v"] != "1" {
		t.Fatalf("CAS did not persist: got %v, want '1'", got["v"])
	}
}

// TestLockFilePathIsSidecar locks the on-disk shape of the lock file:
// it sits next to the metadata file with a deterministic suffix so
// callers can filter it out of directory walks.
func TestLockFilePathIsSidecar(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/opt/kukeon/data/default/metadata.json", "/opt/kukeon/data/default/metadata.json.lock"},
		{"metadata.json", "metadata.json.lock"},
	}
	for _, tc := range cases {
		if got := metadata.LockFilePath(tc.in); got != tc.want {
			t.Errorf("LockFilePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWriteMetadataCreatesSidecarLockFile ensures the sidecar appears
// next to the data file after a successful write. The phase-3 adopters
// will rely on the sidecar being present after any write.
func TestWriteMetadataCreatesSidecarLockFile(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"k": "v"}, file); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	if _, err := os.Stat(metadata.LockFilePath(file)); err != nil {
		t.Fatalf("sidecar missing after WriteMetadata: %v", err)
	}
}

// TestDeleteMetadataRemovesSidecarAndEmptyParent verifies the parent-
// directory cleanup path still fires once the sidecar exists: without
// the sidecar removal in DeleteMetadata, the leftover .lock entry
// would keep the parent dir non-empty and break the cleanup.
func TestDeleteMetadataRemovesSidecarAndEmptyParent(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "cell")
	file := filepath.Join(parent, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"k": "v"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}
	if _, err := os.Stat(metadata.LockFilePath(file)); err != nil {
		t.Fatalf("seed sidecar missing: %v", err)
	}

	if err := metadata.DeleteMetadata(ctx, logger, file); err != nil {
		t.Fatalf("DeleteMetadata: %v", err)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("data file still present after delete: err = %v", err)
	}
	if _, err := os.Stat(metadata.LockFilePath(file)); !os.IsNotExist(err) {
		t.Errorf("sidecar still present after delete: err = %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("parent dir still present after delete (sidecar cleanup may have leaked): err = %v", err)
	}
}

// TestReadRawDoesNotMaterializeSidecarForMissingFile guards the "probe
// for an optional realm" use case: ReadRaw on a never-written path
// must short-circuit to ErrMissingMetadataFile without creating the
// sidecar lock file. Otherwise the daemon's list-walks would leak
// .lock files into directories that were never intended to host
// metadata.
func TestReadRawDoesNotMaterializeSidecarForMissingFile(t *testing.T) {
	tmp := t.TempDir()
	// Make the parent dir exist (mirrors a realm-dir-but-no-spaces
	// arrangement) so the only way a sidecar appears is via ReadRaw.
	parent := filepath.Join(tmp, "realm")
	if err := os.MkdirAll(parent, 0o0750); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	file := filepath.Join(parent, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	_, err := metadata.ReadRaw(ctx, logger, file)
	if !errors.Is(err, errdefs.ErrMissingMetadataFile) {
		t.Fatalf("ReadRaw on missing file: err = %v, want ErrMissingMetadataFile", err)
	}
	if _, err := os.Stat(metadata.LockFilePath(file)); !os.IsNotExist(err) {
		t.Errorf("sidecar leaked from probe-read of missing file: err = %v", err)
	}
}

// TestWriteMetadataCASRetryAfterStale documents the canonical
// retry-on-stale loop a phase-3 adopter will use: ReadRaw, mutate, CAS;
// on ErrStaleResource re-read and retry. The loop must terminate with
// the post-mutation payload on disk.
func TestWriteMetadataCASRetryAfterStale(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]int{"counter": 0}, file); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Caller A reads.
	priorA, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw A: %v", err)
	}

	// Caller B writes underneath A.
	if err := metadata.WriteMetadata(ctx, logger, map[string]int{"counter": 100}, file); err != nil {
		t.Fatalf("WriteMetadata B: %v", err)
	}

	// A's first CAS attempt fails.
	err = metadata.WriteMetadataCAS(ctx, logger, priorA, map[string]int{"counter": 1}, file)
	if !errors.Is(err, errdefs.ErrStaleResource) {
		t.Fatalf("first CAS: err = %v, want ErrStaleResource", err)
	}

	// Retry: re-read, then CAS with the freshly observed bytes.
	priorA, err = metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw A retry: %v", err)
	}
	var current map[string]int
	if err := json.Unmarshal(priorA, &current); err != nil {
		t.Fatalf("unmarshal current: %v", err)
	}
	current["counter"]++
	if err := metadata.WriteMetadataCAS(ctx, logger, priorA, current, file); err != nil {
		t.Fatalf("retry CAS: %v", err)
	}

	raw, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw final: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if got["counter"] != 101 {
		t.Fatalf("counter after retry = %d, want 101", got["counter"])
	}
}

// TestWithSharedLockDoesNotResurrectAfterDelete reproduces the TOCTOU
// race the reviewer flagged in PR #607: a sibling DeleteMetadata sweeps
// the parent dir + sidecar between a reader's existence pre-check and
// the shared-lock acquire. The acquire must surface
// ErrMissingMetadataFile WITHOUT recreating the parent dir or sidecar —
// otherwise both leak after the read returns ErrMissingMetadataFile
// anyway, and phase-3 adopters retrying on ErrStaleResource will widen
// the window.
//
// We drive the race deterministically: seed normal state, then call
// os.RemoveAll on the parent to simulate the deleter completing
// mid-flight, then invoke WithSharedLock directly. A racy two-goroutine
// arrangement would test the same property less reliably.
func TestWithSharedLockDoesNotResurrectAfterDelete(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "cell")
	file := filepath.Join(parent, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]string{"k": "v"}, file); err != nil {
		t.Fatalf("seed WriteMetadata: %v", err)
	}
	if err := os.RemoveAll(parent); err != nil {
		t.Fatalf("simulate concurrent DeleteMetadata: %v", err)
	}

	err := metadata.WithSharedLock(ctx, logger, file, func() error {
		t.Errorf("fn ran despite missing data file — shared-lock acquire should have failed")
		return nil
	})
	if !errors.Is(err, errdefs.ErrMissingMetadataFile) {
		t.Fatalf("WithSharedLock after parent rm: err = %v, want ErrMissingMetadataFile", err)
	}

	if _, statErr := os.Stat(parent); !os.IsNotExist(statErr) {
		t.Errorf("parent dir resurrected by shared-lock acquire: stat err = %v", statErr)
	}
	if _, statErr := os.Stat(metadata.LockFilePath(file)); !os.IsNotExist(statErr) {
		t.Errorf("sidecar resurrected by shared-lock acquire: stat err = %v", statErr)
	}
}

// TestWithSharedLockReadsLegacyMetadataWithoutSidecar guards the
// rollout path for #607: a host whose metadata files were written by a
// pre-sidecar binary still has the data file on disk but no `.lock`
// sidecar next to it. The shared-lock acquire must materialise the
// sidecar in that case and let the read proceed, instead of treating
// the missing sidecar as ErrMissingMetadataFile (which would surface
// upstream as ErrRealmNotFound and break `kuke get realms --no-daemon`
// + `kuke image load --realm …` on any host upgraded across the #607
// boundary). The TOCTOU branch — data file ALSO missing — stays
// covered by TestWithSharedLockDoesNotResurrectAfterDelete above.
func TestWithSharedLockReadsLegacyMetadataWithoutSidecar(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "realm")
	if err := os.MkdirAll(parent, 0o0750); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	file := filepath.Join(parent, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	// Stage the data file directly to simulate pre-#607 on-disk state:
	// the data is there, the sidecar is not.
	want := []byte(`{"k":"v"}`)
	if err := os.WriteFile(file, want, 0o0640); err != nil {
		t.Fatalf("seed legacy data file: %v", err)
	}
	if _, statErr := os.Stat(metadata.LockFilePath(file)); !os.IsNotExist(statErr) {
		t.Fatalf("precondition: sidecar should not exist, stat err = %v", statErr)
	}

	got, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("ReadRaw on legacy file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("ReadRaw payload = %q, want %q", got, want)
	}
	if _, statErr := os.Stat(metadata.LockFilePath(file)); statErr != nil {
		t.Errorf("sidecar not materialised after legacy ReadRaw: stat err = %v", statErr)
	}
}

// TestStressConcurrentWriteReadDelete hammers the same path with
// concurrent Write/Read/Delete goroutines. After every goroutine
// returns, the working dir must be in one of two well-defined terminal
// states:
//
//   - parent absent (last meaningful op was a successful DeleteMetadata
//     that rmdir'd the empty parent), or
//   - parent contains exactly {data file, sidecar} (last op was a
//     WriteMetadata that completed atomically).
//
// Any other state is the reviewer's bug shape — orphan parent dir,
// stray .lock without a data file, or data file without its sidecar.
// Both pre-fix race windows (ReadRaw resurrecting parent+sidecar after
// a sweep; DeleteMetadata racing a writer's atomicWriteFile rename to
// strand data without a sidecar) would manifest here.
func TestStressConcurrentWriteReadDelete(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "cell")
	file := filepath.Join(parent, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	const (
		writers  = 8
		readers  = 8
		deleters = 4
		iters    = 50
	)

	var wg sync.WaitGroup
	wg.Add(writers + readers + deleters)

	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = metadata.WriteMetadata(ctx, logger, map[string]int{"w": id, "i": i}, file)
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_, _ = metadata.ReadRaw(ctx, logger, file)
			}
		}()
	}
	for d := 0; d < deleters; d++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = metadata.DeleteMetadata(ctx, logger, file)
			}
		}()
	}
	wg.Wait()

	entries, err := os.ReadDir(parent)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("ReadDir parent: %v", err)
		}
		return // parent absent — clean terminal state
	}

	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)

	want := []string{
		filepath.Base(file),
		filepath.Base(file) + metadata.LockFileSuffix,
	}
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphan terminal state after stress: parent contents = %v; want %v or empty parent", got, want)
	}
}

// TestWriteMetadataNoLockUnderWithExclusiveLock asserts the
// caller-holds-the-lock helper pair works end-to-end: a caller can
// acquire the exclusive flock once, do an arbitrary read + write
// sequence under it, and observe a coherent payload — without the
// inner write deadlocking on its own attempted acquisition.
func TestWriteMetadataNoLockUnderWithExclusiveLock(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "metadata.json")
	logger := discardLogger()
	ctx := context.Background()

	if err := metadata.WriteMetadata(ctx, logger, map[string]int{"x": 0}, file); err != nil {
		t.Fatalf("seed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := metadata.WithExclusiveLock(ctx, logger, file, func() error {
			raw, readErr := metadata.ReadRawNoLock(ctx, logger, file)
			if readErr != nil {
				return fmt.Errorf("inner read: %w", readErr)
			}
			var doc map[string]int
			if jsonErr := json.Unmarshal(raw, &doc); jsonErr != nil {
				return fmt.Errorf("inner unmarshal: %w", jsonErr)
			}
			doc["x"]++
			return metadata.WriteMetadataNoLock(ctx, logger, doc, file)
		})
		if err != nil {
			t.Errorf("WithExclusiveLock fn: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("WithExclusiveLock + No-Lock helpers deadlocked")
	}

	raw, err := metadata.ReadRaw(ctx, logger, file)
	if err != nil {
		t.Fatalf("final ReadRaw: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["x"] != 1 {
		t.Fatalf("inner increment did not land: got %d, want 1", got["x"])
	}
}
