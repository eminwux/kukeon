// Copyright 2026 Emiliano Spinella (eminwux)
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

//nolint:testpackage // exercises *Exec.ExecContainer against an in-package ctr.Client fake
package runner

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

type execTaskCall struct {
	namespace, containerdID string
	spec                    ctr.ExecSpec
	stdio                   ctr.ExecStdio
}

// execCell is a cell with a root container and a workload container; only
// the root carries a persisted containerd ID, so the workload exercises the
// rebuilt-from-naming fallback.
func execCell() intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "web"},
		Spec: intmodel.CellSpec{
			ID: "web", RealmName: "default", SpaceName: "sp", StackName: "st",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, ContainerdID: "persisted_root_id"},
				{ID: "work"},
			},
		},
	}
}

// TestExecContainer_ResolvesLikeGetContainerObservation pins that exec
// targets the same containerd namespace and ID the state query does, for a
// persisted containerd ID and for the rebuilt one.
func TestExecContainer_ResolvesLikeGetContainerObservation(t *testing.T) {
	for _, name := range []string{"root", "work"} {
		t.Run(name, func(t *testing.T) {
			var observed [2]string
			var got execTaskCall
			fake := &deleteCellFakeClient{
				existsContainerFn: func(namespace, id string) (bool, error) {
					observed = [2]string{namespace, id}
					return true, nil
				},
				taskStatusFn: func(_, _ string) (containerd.Status, error) {
					return containerd.Status{Status: containerd.Running}, nil
				},
				execTaskFn: func(
					namespace, id string, spec ctr.ExecSpec, stdin io.Reader, stdout, stderr io.Writer,
				) (ctr.ExecProcess, error) {
					got = execTaskCall{namespace, id, spec, ctr.ExecStdio{Stdin: stdin, Stdout: stdout, Stderr: stderr}}
					return nil, nil //nolint:nilnil // the test only inspects the call
				},
			}
			r := newDeleteCellTestExec(t, fake)
			seedDeleteCellRealm(t, r, "default")

			if _, err := r.GetContainerObservation(execCell(), name); err != nil {
				t.Fatalf("GetContainerObservation: %v", err)
			}
			stdio := ctr.ExecStdio{Stdout: io.Discard, Stderr: io.Discard}
			spec := ctr.ExecSpec{ID: "kuke-exec-0123456789abcdef", Command: []string{"id"}}
			if _, err := r.ExecContainer(execCell(), name, spec, stdio); err != nil {
				t.Fatalf("ExecContainer: %v", err)
			}

			if observed[0] == "" || observed[1] == "" {
				t.Fatalf("state query did not reach containerd: %v", observed)
			}
			if got.namespace != observed[0] || got.containerdID != observed[1] {
				t.Errorf("exec targets %s/%s, the state query %s/%s",
					got.namespace, got.containerdID, observed[0], observed[1])
			}
			if want := filepath.Join(r.opts.RunPath, "exec-fifo"); got.spec.FIFODir != want {
				t.Errorf("FIFODir = %q, want %q (under the run path)", got.spec.FIFODir, want)
			}
			if got.spec.ID != spec.ID || got.stdio.Stdin != nil || got.stdio.Stdout != io.Discard {
				t.Errorf("spec or stdio not forwarded: %+v", got)
			}
		})
	}
}

func TestExecContainer_UnknownContainer(t *testing.T) {
	fake := &deleteCellFakeClient{
		execTaskFn: func(string, string, ctr.ExecSpec, io.Reader, io.Writer, io.Writer) (ctr.ExecProcess, error) {
			t.Error("ExecTask called for a container the cell does not have")
			return nil, nil //nolint:nilnil // unreachable in a passing test
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, "default")

	_, err := r.ExecContainer(execCell(), "nope", ctr.ExecSpec{ID: "x", Command: []string{"id"}}, ctr.ExecStdio{})
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("ExecContainer = %v, want ErrContainerNotFound", err)
	}
}
