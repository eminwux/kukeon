// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/daemonlog"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ReadDaemonLogs implements kukeonv1.Client (issue #1386, step 7). The
// authoritative source is the kukeond container's stdout/stderr stream —
// the runtime-shim log file `kuke daemon logs` reads — resolved through
// LogContainer so both readers agree on the path. A kukeond that is not
// running inside its kuke-system cell has no such file and reports
// ErrDaemonLogUnavailable.
func (c *Client) ReadDaemonLogs(
	ctx context.Context, req kukeonv1.DaemonLogRequest,
) (kukeonv1.DaemonLogResult, error) {
	located, err := c.LogContainer(ctx, kukeondContainerDoc())
	if err != nil {
		return kukeonv1.DaemonLogResult{}, daemonLogLocateError(err)
	}
	path := located.HostLogPath
	if path == "" {
		path = located.HostCapturePath
	}
	return readDaemonLogFile(path, req)
}

// daemonLogLocateError maps a missing kukeond container (or any missing
// parent scope) to ErrDaemonLogUnavailable.
func daemonLogLocateError(err error) error {
	if errors.Is(err, errdefs.ErrContainerNotFound) || errors.Is(err, errdefs.ErrCellNotFound) ||
		errors.Is(err, errdefs.ErrStackNotFound) || errors.Is(err, errdefs.ErrSpaceNotFound) ||
		errors.Is(err, errdefs.ErrRealmNotFound) {
		// The cause travels as text, not wrapped: the wire kind must resolve
		// to DaemonLogUnavailable alone, not race the not-found sentinel.
		return fmt.Errorf(
			"%w: the kukeond container (%s/%s/%s/%s/%s) is not provisioned: %s",
			errdefs.ErrDaemonLogUnavailable,
			consts.KukeSystemRealmName, consts.KukeSystemSpaceName, consts.KukeSystemStackName,
			consts.KukeSystemCellName, consts.KukeSystemContainerName, err.Error(),
		)
	}
	return fmt.Errorf("resolve kukeond log source: %w", err)
}

// readDaemonLogFile reads the resolved source, reporting a missing file as
// ErrDaemonLogUnavailable and keeping permission errors testable with
// errors.Is(err, fs.ErrPermission).
func readDaemonLogFile(path string, req kukeonv1.DaemonLogRequest) (kukeonv1.DaemonLogResult, error) {
	result, err := daemonlog.Read(path, req)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return kukeonv1.DaemonLogResult{Source: path}, fmt.Errorf(
			"%w: no log file at %s (kukeond is not running in its %s cell, or has not written output yet)",
			errdefs.ErrDaemonLogUnavailable, path, consts.KukeSystemRealmName,
		)
	case err != nil:
		return kukeonv1.DaemonLogResult{Source: path}, fmt.Errorf("read kukeond log %s: %w", path, err)
	}
	return result, nil
}

func kukeondContainerDoc() v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata:   v1beta1.ContainerMetadata{Name: consts.KukeSystemContainerName, Labels: map[string]string{}},
		Spec: v1beta1.ContainerSpec{
			ID:      consts.KukeSystemContainerName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
			CellID:  consts.KukeSystemCellName,
		},
	}
}
