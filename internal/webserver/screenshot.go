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

package webserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	shared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
)

// errScreenUnavailable means the cell has no terminal screen to read right
// now: it is not live, it has no attachable container, or the container's
// terminal refused the connection. apiErrorStatus maps it to 404
// screen_unavailable. It stays package-private on purpose (#1635): the
// ErrAttach* sentinels keep their global mapping, which lifecycle code in the
// web UI branches on.
var errScreenUnavailable = errors.New("terminal screen unavailable")

// cellScreen is the JSON rendering of one cell terminal screenshot. The ANSI
// rendering of sbsh's ScreenshotResult is left out: the web UI shows plain
// text only.
type cellScreen struct {
	Container     string `json:"container"`
	Cols          int    `json:"cols"`
	Rows          int    `json:"rows"`
	CursorX       int    `json:"cursorX"`
	CursorY       int    `json:"cursorY"`
	CursorVisible bool   `json:"cursorVisible"`
	AltScreen     bool   `json:"altScreen"`
	Text          string `json:"text"`
}

// screenshooter makes one sbsh Screenshot RPC against a container's terminal.
type screenshooter func(context.Context, kukeonv1.Client, v1beta1.ContainerDoc) (api.ScreenshotResult, error)

// registerScreenshotRoute exposes a cell's rendered terminal screen as text
// (issue #1635), the web counterpart of `kuke screenshot`, so the Workbench
// grid can show one thumbnail per cell with one request per tile.
func registerScreenshotRoute(mux *http.ServeMux, dial Dial) {
	registerScreenshotRouteWith(mux, dial, sbshScreenshot)
}

func registerScreenshotRouteWith(mux *http.ServeMux, dial Dial, shoot screenshooter) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/screenshot",
		resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			realm, space, stack, cell := r.PathValue("realm"), r.PathValue("space"),
				r.PathValue("stack"), r.PathValue("cell")
			result, err := c.GetCell(ctx, v1beta1.CellDoc{
				Metadata: v1beta1.CellMetadata{Name: cell},
				Spec:     v1beta1.CellSpec{RealmID: realm, SpaceID: space, StackID: stack},
			})
			if err != nil {
				return nil, err
			}
			if !result.MetadataExists {
				return nil, errdefs.ErrCellNotFound
			}
			if err = shared.GuardCellTaskLiveness(result, cell, realm, space, stack); err != nil {
				return nil, fmt.Errorf("%w: %w", errScreenUnavailable, err)
			}
			container, err := screenContainer(ctx, c, r, result.Cell.Spec.Tty)
			if err != nil {
				return nil, err
			}
			shot, err := shoot(ctx, c, v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{Name: container},
				Spec: v1beta1.ContainerSpec{
					ID: container, RealmID: realm, SpaceID: space, StackID: stack, CellID: cell,
				},
			})
			if errors.Is(err, errdefs.ErrAttachNotSupported) ||
				errors.Is(err, errdefs.ErrAttachTaskNotRunning) ||
				errors.Is(err, errdefs.ErrAttachStaleSocket) {
				return nil, fmt.Errorf("%w: %w", errScreenUnavailable, err)
			}
			if err != nil {
				return nil, err
			}
			return cellScreen{
				Container:     container,
				Cols:          shot.Cols,
				Rows:          shot.Rows,
				CursorX:       shot.CursorX,
				CursorY:       shot.CursorY,
				CursorVisible: shot.CursorVisible,
				AltScreen:     shot.AltScreen,
				Text:          shot.Text,
			}, nil
		}),
	)
}

// screenContainer picks the container whose screen is read: ?container= when
// given, else the cell's spec.tty.default when that container is attachable
// and not root, else the first non-root attachable container in list order.
// Unlike `kuke screenshot`, more than one attachable container is not an
// error: the browser's terminal makes the same first-attachable choice.
func screenContainer(ctx context.Context, c kukeonv1.Client, r *http.Request, tty *v1beta1.CellTty) (string, error) {
	if name := r.URL.Query().Get("container"); name != "" {
		if !safePathSegment(name) {
			return "", fmt.Errorf("%w: invalid container name %q", errdefs.ErrConfig, name)
		}
		return name, nil
	}
	specs, err := c.ListContainers(
		ctx, r.PathValue("realm"), r.PathValue("space"), r.PathValue("stack"), r.PathValue("cell"),
	)
	if err != nil {
		return "", err
	}
	var attachable []string
	for _, spec := range specs {
		if spec.Attachable && !spec.Root {
			attachable = append(attachable, spec.ID)
		}
	}
	if len(attachable) == 0 {
		return "", fmt.Errorf("%w: cell %q has no attachable container", errScreenUnavailable, r.PathValue("cell"))
	}
	if tty != nil {
		for _, id := range attachable {
			if id == tty.Default {
				return id, nil
			}
		}
	}
	return attachable[0], nil
}

// sbshScreenshot is the body of shared.ConnectTerminalClient without cobra:
// the dialer's preflight runs the daemon's Attachable and task-Running gates,
// then one Screenshot RPC reads the rendered screen. Terminal bytes never
// traverse kukeond.
func sbshScreenshot(ctx context.Context, c kukeonv1.Client, doc v1beta1.ContainerDoc) (api.ScreenshotResult, error) {
	dialer := shared.NewTerminalDialer(c, doc)
	defer dialer.Close()
	rc, err := dialer.TerminalClient(ctx, logging.NewNoopLogger())
	if err != nil {
		return api.ScreenshotResult{}, err
	}
	defer func() { _ = rc.Close() }()
	var res api.ScreenshotResult
	err = rc.Screenshot(ctx, &api.ScreenshotArgs{}, &res)
	return res, err
}
