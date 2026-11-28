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
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ReadContainerLog implements kukeonv1.Client (issue #1537). It resolves the
// stream through LogContainer — so the not-found errors and the
// capture-vs-shim-log choice are LogContainer's — and reads one bounded
// chunk of it. kukeond serves the RPC through this method, and the
// KUKEON_NO_DAEMON path calls it directly, so `kuke log` has one code path.
//
// req.Stream "supervisor" reads the terminal supervisor's log instead (issue
// #1721, see supervisorLogPath). The result's Stream echoes the stream on
// every return, errors included, so a client can tell this kukeond from one
// that predates the field.
func (c *Client) ReadContainerLog(
	ctx context.Context, doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	stream := req.Stream
	if stream == "" {
		stream = kukeonv1.ContainerLogStreamTerminal
	}
	result, err := c.readContainerLogStream(ctx, doc, req, stream)
	result.Stream = stream
	return result, err
}

func (c *Client) readContainerLogStream(
	ctx context.Context, doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest, stream string,
) (kukeonv1.ContainerLogResult, error) {
	switch stream {
	case kukeonv1.ContainerLogStreamTerminal:
	case kukeonv1.ContainerLogStreamSupervisor:
		return c.readSupervisorLog(doc, req)
	default:
		return kukeonv1.ContainerLogResult{}, fmt.Errorf(
			"%w: %q (want %q or %q)", errdefs.ErrContainerLogStreamInvalid, stream,
			kukeonv1.ContainerLogStreamTerminal, kukeonv1.ContainerLogStreamSupervisor,
		)
	}
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

// readSupervisorLog reads the terminal supervisor's log of the container doc
// names, running or stopped.
func (c *Client) readSupervisorLog(
	doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	internal, _, err := apischeme.NormalizeContainer(doc)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	res, err := c.ctrl.GetContainer(internal)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	if !res.ContainerExists {
		return kukeonv1.ContainerLogResult{}, errdefs.ErrContainerNotFound
	}
	path, err := supervisorLogPath(c.ctrl.RunPath(), res.Container.Spec)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	return readContainerLogFile(path, req)
}

// supervisorLogPath resolves the host path of an Attachable container's
// supervisor log. With no spec.containers[].tty.logFile it is
// tty/supervisor.log. An override under the in-container tty directory
// (/run/kukeon/tty/) maps onto the host tty directory, which is that
// directory's bind-mount source. Any other override lives somewhere kukeond
// cannot see, so it is reported with the in-container path rather than
// guessed at. Issue #1721.
func supervisorLogPath(runPath string, spec intmodel.ContainerSpec) (string, error) {
	if !spec.Attachable {
		return "", fmt.Errorf("%w: container %q is not attachable", errdefs.ErrNoSupervisor, spec.ID)
	}
	if spec.Tty == nil || spec.Tty.LogFile == "" {
		return fs.ContainerSupervisorLogPath(
			runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
		), nil
	}
	override := spec.Tty.LogFile
	rel, err := filepath.Rel(ctr.AttachableTTYDir, filepath.Clean(override))
	if !filepath.IsAbs(override) || err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"%w: container %q writes it to %s (tty.logFile), outside %s",
			errdefs.ErrSupervisorLogUnreadable, spec.ID, override, ctr.AttachableTTYDir,
		)
	}
	return filepath.Join(
		fs.ContainerTTYDir(runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID),
		rel,
	), nil
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
