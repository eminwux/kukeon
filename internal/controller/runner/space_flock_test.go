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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

const (
	flockTestRealm = "test-realm"
	flockTestSpace = "test-space"
	flockBlocked   = 100 * time.Millisecond
	flockDeadline  = 5 * time.Second
)

// acquireAsync takes the space lock on r in a goroutine and returns a channel
// that delivers the release func once the lock is held.
func acquireAsync(r *Exec) <-chan func() {
	held := make(chan func(), 1)
	go func() { held <- r.lockSpace(flockTestRealm, flockTestSpace) }()
	return held
}

func expectBlocked(t *testing.T, held <-chan func(), what string) {
	t.Helper()
	select {
	case release := <-held:
		release()
		t.Fatalf("%s acquired the space lock while another process held it", what)
	case <-time.After(flockBlocked):
	}
}

func expectAcquired(t *testing.T, held <-chan func(), what string) func() {
	t.Helper()
	select {
	case release := <-held:
		return release
	case <-time.After(flockDeadline):
		t.Fatalf("%s never acquired the space lock", what)
		return nil
	}
}

// Two runners sharing a RunPath stand in for the daemon and a `--no-daemon`
// CLI: each has its own in-process lock manager, so only the file lock can
// serialize them (issue #1469).
func TestLockSpace_SerializesAcrossRunnersSharingRunPath(t *testing.T) {
	runPath := t.TempDir()
	daemon := newProvisionTestExec(t, runPath, false)
	cli := newProvisionTestExec(t, runPath, false)

	release := daemon.lockSpace(flockTestRealm, flockTestSpace)
	held := acquireAsync(cli)
	expectBlocked(t, held, "second runner")
	release()
	expectAcquired(t, held, "second runner")()

	other := newProvisionTestExec(t, t.TempDir(), false)
	release = daemon.lockSpace(flockTestRealm, flockTestSpace)
	defer release()
	expectAcquired(t, acquireAsync(other), "runner on a different RunPath")()
}

// A waiter blocked on a lock file the holder unlinks must not end up holding
// the orphaned inode: a later acquirer would then lock a fresh file and both
// would run at once.
func TestLockSpace_RemovedLockFileStillExcludes(t *testing.T) {
	runPath := t.TempDir()
	first := newProvisionTestExec(t, runPath, false)
	second := newProvisionTestExec(t, runPath, false)
	third := newProvisionTestExec(t, runPath, false)

	releaseFirst := first.lockSpaceRemovable(flockTestRealm, flockTestSpace)
	secondHeld := acquireAsync(second)
	expectBlocked(t, secondHeld, "second runner")
	releaseFirst(true)
	releaseSecond := expectAcquired(t, secondHeld, "second runner")

	thirdHeld := acquireAsync(third)
	expectBlocked(t, thirdHeld, "third runner")
	releaseSecond()
	expectAcquired(t, thirdHeld, "third runner")()
}

// The race #1469 leaked a chain through: another process's reconcile tick
// read the space before DeleteSpace removed it, then re-installed the egress
// chain afterwards. Under the shared lock the tick waits, re-reads, and finds
// the space gone.
func TestEnsureSpace_WaitsForCrossProcessDelete(t *testing.T) {
	runPath := t.TempDir()
	deleter := newProvisionTestExec(t, runPath, false)
	reconciler := newProvisionTestExec(t, runPath, false)
	enforcer := &recordingEnforcer{}
	reconciler.netPolicy = enforcer
	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: flockTestSpace},
		Spec:     intmodel.SpaceSpec{RealmName: flockTestRealm},
		Status:   intmodel.SpaceStatus{State: intmodel.SpaceStateReady},
	}
	if err := deleter.UpdateSpaceMetadata(space); err != nil {
		t.Fatalf("seed space: %v", err)
	}

	release := deleter.lockSpace(flockTestRealm, flockTestSpace)
	done := make(chan error, 1)
	go func() {
		_, err := reconciler.EnsureSpace(space)
		done <- err
	}()
	select {
	case err := <-done:
		release()
		t.Fatalf("EnsureSpace ran while another process held the space lock (err=%v)", err)
	case <-time.After(flockBlocked):
	}
	if err := os.RemoveAll(fs.SpaceMetadataDir(runPath, flockTestRealm, flockTestSpace)); err != nil {
		t.Fatal(err)
	}
	release()

	select {
	case err := <-done:
		if !errors.Is(err, errdefs.ErrSpaceNotFound) {
			t.Fatalf("EnsureSpace error = %v, want ErrSpaceNotFound", err)
		}
	case <-time.After(flockDeadline):
		t.Fatal("EnsureSpace never returned")
	}
	if len(enforcer.applied) != 0 {
		t.Fatalf("applied egress for a deleted space: %v", enforcer.applied)
	}
}

// DeleteSpace drops the lock file once the space is gone, so deleted spaces
// leave nothing behind under <RunPath>/locks.
func TestDeleteSpace_RemovesSpaceLockFile(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	r.lockSpace(flockTestRealm, flockTestSpace)()
	path := fs.SpaceLockPath(runPath, flockTestRealm, flockTestSpace)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	err := r.DeleteSpace(intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: flockTestSpace},
		Spec:     intmodel.SpaceSpec{RealmName: flockTestRealm},
	})
	if err != nil {
		t.Fatalf("DeleteSpace: %v", err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file still present after DeleteSpace: %v", err)
	}
}
