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

package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
)

// MockRunKey is used to inject a mock runFn via context in tests, so the
// real pkg/attach.Run (which would open a TTY and connect to a real
// control socket) is bypassed.
type MockRunKey struct{}

// runFn drives the in-process sbsh attach loop. Returns nil on a clean
// detach / context cancel and any unrecoverable controller error otherwise.
type runFn func(ctx context.Context, opts attach.Options) error

// pickAttachTarget resolves the container `kuke run -a` should attach to,
// applying the documented precedence:
//
//  1. explicit --container flag, when set
//  2. cell.tty.default, when the spec sets it
//  3. the first container in declaration order with attachable=true
//
// Errors when no candidate exists or the explicit flag names a non-attachable
// or unknown container; the error message names the attachable containers so
// the operator can re-run with a valid --container.
func pickAttachTarget(spec v1beta1.CellSpec, cellName, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		for _, c := range spec.Containers {
			if c.ID == explicit {
				if !c.Attachable {
					return "", fmt.Errorf(
						"container %q in cell %q is not attachable: %w; available attachable containers: %s",
						explicit, cellName, errdefs.ErrAttachNotSupported,
						formatAttachableList(spec),
					)
				}
				return c.ID, nil
			}
		}
		return "", fmt.Errorf(
			"container %q not found in cell %q: %w; available attachable containers: %s",
			explicit, cellName, errdefs.ErrContainerNotFound,
			formatAttachableList(spec),
		)
	}

	if spec.Tty != nil {
		if name := strings.TrimSpace(spec.Tty.Default); name != "" {
			return name, nil
		}
	}

	for _, c := range spec.Containers {
		if c.Attachable {
			return c.ID, nil
		}
	}

	return "", fmt.Errorf(
		"%w (cell %q); declare 'attachable: true' on one container or omit -a",
		errdefs.ErrAttachNoCandidate, cellName,
	)
}

// formatAttachableList returns a comma-separated list of attachable container
// IDs in the cell, or "<none>" when there are zero. Used to point operators
// at a valid --container value when their explicit choice is rejected.
func formatAttachableList(spec v1beta1.CellSpec) string {
	names := make([]string, 0, len(spec.Containers))
	for _, c := range spec.Containers {
		if c.Attachable {
			names = append(names, c.ID)
		}
	}
	if len(names) == 0 {
		return "<none>"
	}
	return strings.Join(names, ", ")
}

// runAttachLoop resolves the per-container sbsh control socket via the
// daemon's AttachContainer RPC and drives the in-process attach loop.
// Returns the tri-state used by the -a --rm cleanup decision in
// attachAndMaybeAutoDelete:
//
//   - detached=true,  err=nil — operator pressed ^]^] (or peer issued
//     a Detach RPC). Cell must stay alive.
//   - detached=false, err=nil — peer dropped the connection (workload
//     exited / hung up). Surface exit 0; -a --rm fires KillCell so a
//     long-lived root does not pin the cell.
//   - detached=false, err≠nil — pre-attach setup error or unrecoverable
//     controller error. Surface to the user; -a --rm still fires
//     KillCell because a half-detached session would otherwise leak
//     the cell.
func runAttachLoop(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	container string,
) (bool, error) {
	containerDoc := v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   container,
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      container,
			RealmID: doc.Spec.RealmID,
			SpaceID: doc.Spec.SpaceID,
			StackID: doc.Spec.StackID,
			CellID:  doc.Metadata.Name,
		},
	}

	result, err := client.AttachContainer(cmd.Context(), containerDoc)
	if err != nil {
		if errors.Is(err, errdefs.ErrAttachNotSupported) {
			return false, fmt.Errorf("container %q in cell %q is not attachable: %w",
				container, doc.Metadata.Name, err)
		}
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return false, fmt.Errorf("container %q not found in cell %q",
				container, doc.Metadata.Name)
		}
		return false, err
	}
	if result.HostSocketPath == "" {
		return false, fmt.Errorf("daemon returned empty HostSocketPath for container %q", container)
	}

	run := resolveRun(cmd)
	runErr := run(cmd.Context(), attach.Options{
		SocketPath: result.HostSocketPath,
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	})
	switch kukeshared.ClassifyAttachExit(runErr) {
	case kukeshared.AttachExitDetached:
		return true, nil
	case kukeshared.AttachExitPeerClosed:
		return false, nil
	case kukeshared.AttachExitError:
		return false, runErr
	default:
		return false, runErr
	}
}

func resolveRun(cmd *cobra.Command) runFn {
	if mock, ok := cmd.Context().Value(MockRunKey{}).(runFn); ok {
		return mock
	}
	return attach.Run
}
