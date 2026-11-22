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

	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ReadContainerLog implements kukeonv1.Client (issue #1537). It resolves the
// stream through LogContainer — so the not-found errors and the
// capture-vs-shim-log choice are LogContainer's — and reads one bounded
// chunk of it. kukeond serves the RPC through this method, and the
// KUKEON_NO_DAEMON path calls it directly, so `kuke log` has one code path.
func (c *Client) ReadContainerLog(
	ctx context.Context, doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	located, err := c.LogContainer(ctx, doc)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	path := located.HostCapturePath
	if path == "" {
		path = located.HostLogPath
	}
	if path == "" {
		return kukeonv1.ContainerLogResult{}, fmt.Errorf(
			"%w: no stream path resolved for container %q", errdefs.ErrContainerLogUnavailable, doc.Metadata.Name,
		)
	}
	return readContainerLogFile(path, req)
}

// readContainerLogFile reads the resolved stream, reporting a missing file as
// ErrContainerLogUnavailable (with Source set so callers can name the path)
// and keeping permission errors testable with errors.Is(err, fs.ErrPermission).
func readContainerLogFile(
	path string, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	result, err := containerlog.Read(path, req)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return kukeonv1.ContainerLogResult{Source: path}, fmt.Errorf(
			"%w: no log file at %s", errdefs.ErrContainerLogUnavailable, path,
		)
	case err != nil:
		return kukeonv1.ContainerLogResult{Source: path}, fmt.Errorf("read container log %s: %w", path, err)
	}
	return result, nil
}
