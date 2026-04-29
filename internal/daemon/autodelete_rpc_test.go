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

package daemon_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// createCellClientFake stubs CreateCell so the auto-delete tests can drive
// the result path independently of the real controller. All other methods
// fall through to FakeClient's ErrUnexpectedCall — the launcher should
// never call into them in the cases under test, since cleanup happens
// inside the watcher goroutine which uses the real *local.Client in prod.
type createCellClientFake struct {
	kukeonv1.FakeClient

	result kukeonv1.CreateCellResult
	err    error
}

func (f *createCellClientFake) CreateCell(
	context.Context,
	v1beta1.CellDoc,
) (kukeonv1.CreateCellResult, error) {
	return f.result, f.err
}

// recordingLauncher captures every call site the service routes into the
// auto-delete path. The test compares Calls against the expected count for
// each scenario.
type recordingLauncher struct {
	calls atomic.Int32
	docs  []v1beta1.CellDoc
}

func (r *recordingLauncher) launch(_ context.Context, doc v1beta1.CellDoc) error {
	r.calls.Add(1)
	r.docs = append(r.docs, doc)
	return nil
}

func TestCreateCell_AutoDelete_LaunchesWatcherWhenStarted(t *testing.T) {
	core := &createCellClientFake{
		result: kukeonv1.CreateCellResult{
			Cell:    v1beta1.CellDoc{Metadata: v1beta1.CellMetadata{Name: "my-cell"}},
			Started: true,
			Created: true,
		},
	}
	launcher := &recordingLauncher{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, launcher.launch)

	args := &kukeonv1.CreateCellArgs{
		Doc: v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: "my-cell"},
			Spec:     v1beta1.CellSpec{AutoDelete: true},
		},
	}
	reply := &kukeonv1.CreateCellReply{}
	if err := svc.CreateCell(args, reply); err != nil {
		t.Fatalf("CreateCell transport error: %v", err)
	}
	if got := launcher.calls.Load(); got != 1 {
		t.Errorf("launcher calls=%d want 1", got)
	}
	if len(launcher.docs) != 1 || launcher.docs[0].Metadata.Name != "my-cell" {
		t.Errorf("launcher received unexpected doc: %+v", launcher.docs)
	}
}

func TestCreateCell_AutoDelete_NoLaunchWhenAutoDeleteFalse(t *testing.T) {
	// Without AutoDelete on the spec, the launcher must not fire even
	// when the cell was successfully started — the bit is opt-in.
	core := &createCellClientFake{
		result: kukeonv1.CreateCellResult{Started: true},
	}
	launcher := &recordingLauncher{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, launcher.launch)

	args := &kukeonv1.CreateCellArgs{
		Doc: v1beta1.CellDoc{Spec: v1beta1.CellSpec{AutoDelete: false}},
	}
	reply := &kukeonv1.CreateCellReply{}
	if err := svc.CreateCell(args, reply); err != nil {
		t.Fatalf("CreateCell transport error: %v", err)
	}
	if got := launcher.calls.Load(); got != 0 {
		t.Errorf("launcher calls=%d want 0 when AutoDelete=false", got)
	}
}

func TestCreateCell_AutoDelete_NoLaunchWhenNotStarted(t *testing.T) {
	// An idempotent CreateCell that returns Started=false (cell already
	// running, no transition) must not install a fresh watcher — that
	// would race against a watcher installed on the original CreateCell.
	core := &createCellClientFake{
		result: kukeonv1.CreateCellResult{Started: false},
	}
	launcher := &recordingLauncher{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, launcher.launch)

	args := &kukeonv1.CreateCellArgs{
		Doc: v1beta1.CellDoc{Spec: v1beta1.CellSpec{AutoDelete: true}},
	}
	reply := &kukeonv1.CreateCellReply{}
	if err := svc.CreateCell(args, reply); err != nil {
		t.Fatalf("CreateCell transport error: %v", err)
	}
	if got := launcher.calls.Load(); got != 0 {
		t.Errorf("launcher calls=%d want 0 when Started=false", got)
	}
}

func TestCreateCell_AutoDelete_NoLaunchOnError(t *testing.T) {
	// When CreateCell itself fails, the launcher must not run — there's
	// nothing to watch and the error path already returns.
	core := &createCellClientFake{err: errors.New("boom")}
	launcher := &recordingLauncher{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, launcher.launch)

	args := &kukeonv1.CreateCellArgs{
		Doc: v1beta1.CellDoc{Spec: v1beta1.CellSpec{AutoDelete: true}},
	}
	reply := &kukeonv1.CreateCellReply{}
	if err := svc.CreateCell(args, reply); err != nil {
		t.Fatalf("CreateCell transport error: %v", err)
	}
	if reply.Err == nil {
		t.Errorf("expected reply.Err set on failure, got nil")
	}
	if got := launcher.calls.Load(); got != 0 {
		t.Errorf("launcher calls=%d want 0 on CreateCell failure", got)
	}
}

func TestCreateCell_AutoDelete_NilLauncherTolerated(t *testing.T) {
	// Constructed with a nil launcher (older callers, or tests without
	// the wiring), CreateCell must still complete successfully even
	// when AutoDelete is set — the watcher is genuinely optional.
	core := &createCellClientFake{
		result: kukeonv1.CreateCellResult{Started: true},
	}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, nil)

	args := &kukeonv1.CreateCellArgs{
		Doc: v1beta1.CellDoc{Spec: v1beta1.CellSpec{AutoDelete: true}},
	}
	reply := &kukeonv1.CreateCellReply{}
	if err := svc.CreateCell(args, reply); err != nil {
		t.Fatalf("CreateCell transport error: %v", err)
	}
	if reply.Err != nil {
		t.Errorf("reply.Err=%v want nil with nil launcher", reply.Err)
	}
}
