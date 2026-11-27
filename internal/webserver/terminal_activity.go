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
	"net/rpc"
	"strings"
	"sync"
	"time"

	shared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	rpcterminal "github.com/eminwux/sbsh/pkg/rpcclient/terminal"
)

const (
	// maxActivityCells caps the cell parameters of one activity request:
	// the sidebar pages cells by 100.
	maxActivityCells = 100
	// activityConcurrency is how many cells one request reads at a time.
	activityConcurrency = 8
	// activityCellTimeout bounds the reads of one cell, the terminal RPC
	// included; a cell that runs past it is reported unavailable "error".
	activityCellTimeout = 2 * time.Second
	// scopeSegments is realm/space/stack/cell.
	scopeSegments = 4
)

// Values of cellActivity.Unavailable.
const (
	activityNotFound    = "not_found"
	activityNotLive     = "not_live"
	activityUnsupported = "unsupported"
	activityError       = "error"
)

// cellActivity is one cell of a terminal-activity response: the counters of
// the cell's default terminal, or Unavailable and no counters.
type cellActivity struct {
	Scope       []string `json:"scope"`
	Container   string   `json:"container,omitempty"`
	StartedAt   string   `json:"startedAt,omitempty"`
	BytesOut    *uint64  `json:"bytesOut,omitempty"`
	BytesIn     *uint64  `json:"bytesIn,omitempty"`
	IdleMs      *int64   `json:"idleMs,omitempty"`
	Unavailable string   `json:"unavailable,omitempty"`
}

type terminalActivity struct {
	Cells []cellActivity `json:"cells"`
}

// activityReader makes one sbsh Activity RPC against a container's terminal.
type activityReader func(context.Context, kukeonv1.Client, v1beta1.ContainerDoc) (api.ActivityResult, error)

// registerTerminalActivityRoute reports the output counters of many cells'
// terminals in one request (issue #1696), so the web UI can mark cells
// whose terminal produced output the operator has not seen. Like the
// screenshot route it never attaches: it does not use a terminal
// connection and leaves the terminal's attachers alone.
func registerTerminalActivityRoute(mux *http.ServeMux, dial Dial) {
	registerTerminalActivityRouteWith(mux, dial, sbshActivity)
}

func registerTerminalActivityRouteWith(mux *http.ServeMux, dial Dial, read activityReader) {
	mux.HandleFunc("GET /api/terminal-activity", func(w http.ResponseWriter, r *http.Request) {
		scopes, err := activityScopes(r.URL.Query()["cell"])
		if err != nil {
			writeAPIError(w, err)
			return
		}
		cells := make([]cellActivity, len(scopes))
		sem := make(chan struct{}, activityConcurrency)
		var wg sync.WaitGroup
		for i, scope := range scopes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-r.Context().Done():
					cells[i] = cellActivity{Scope: scope, Unavailable: activityError}
					return
				}
				defer func() { <-sem }()
				cells[i] = readCellActivity(r.Context(), dial, read, scope)
			}()
		}
		wg.Wait()
		writeJSON(w, http.StatusOK, terminalActivity{Cells: cells})
	})
}

// activityScopes parses the cell parameters: realm/space/stack/cell, each
// segment non-empty and safe to use in a path.
func activityScopes(values []string) ([][]string, error) {
	if len(values) > maxActivityCells {
		return nil, fmt.Errorf(
			"%w: at most %d cell parameters, got %d",
			errdefs.ErrConfig,
			maxActivityCells,
			len(values),
		)
	}
	scopes := make([][]string, 0, len(values))
	for _, value := range values {
		scope := strings.Split(value, "/")
		if len(scope) != scopeSegments {
			return nil, fmt.Errorf("%w: cell %q is not realm/space/stack/cell", errdefs.ErrConfig, value)
		}
		for _, segment := range scope {
			if segment == "" || !safePathSegment(segment) {
				return nil, fmt.Errorf("%w: cell %q has an invalid segment %q", errdefs.ErrConfig, value, segment)
			}
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

// readCellActivity reads one cell under its own deadline and kukeond
// client: kukeonv1's client drops its connection when a call's context is
// cancelled, so a shared client would let one slow cell fail the others.
func readCellActivity(ctx context.Context, dial Dial, read activityReader, scope []string) cellActivity {
	out := cellActivity{Scope: scope}
	ctx, cancel := context.WithTimeout(ctx, activityCellTimeout)
	defer cancel()
	c, err := dial(ctx)
	if err != nil {
		out.Unavailable = activityError
		return out
	}
	defer func() { _ = c.Close() }()
	realm, space, stack, cell := scope[0], scope[1], scope[2], scope[3]
	result, err := c.GetCell(ctx, v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: cell},
		Spec:     v1beta1.CellSpec{RealmID: realm, SpaceID: space, StackID: stack},
	})
	if err != nil {
		out.Unavailable = activityUnavailable(err)
		return out
	}
	if !result.MetadataExists {
		out.Unavailable = activityNotFound
		return out
	}
	if err = shared.GuardCellTaskLiveness(result, cell, realm, space, stack); err != nil {
		out.Unavailable = activityNotLive
		return out
	}
	name, err := screenContainer(ctx, c, realm, space, stack, cell, "", result.Cell.Spec.Tty)
	if err != nil {
		out.Unavailable = activityUnavailable(err)
		return out
	}
	activity, err := read(ctx, c, v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{Name: name},
		Spec: v1beta1.ContainerSpec{
			ID: name, RealmID: realm, SpaceID: space, StackID: stack, CellID: cell,
		},
	})
	if err != nil {
		out.Unavailable = activityUnavailable(err)
		return out
	}
	bytesOut, bytesIn, idleMs := activity.BytesOut, activity.BytesIn, activity.IdleFor.Milliseconds()
	out.Container = name
	out.StartedAt = activity.StartedAt.UTC().Format(time.RFC3339Nano)
	out.BytesOut, out.BytesIn, out.IdleMs = &bytesOut, &bytesIn, &idleMs
	return out
}

// activityUnavailable maps a failed cell read to its unavailable value:
// the cases the screenshot route reports as screen_unavailable are
// not_live, and a terminal whose sbsh predates the Activity RPC (net/rpc's
// "can't find method") is unsupported.
func activityUnavailable(err error) string {
	var serverErr rpc.ServerError
	switch {
	case errors.Is(err, errdefs.ErrCellNotFound):
		return activityNotFound
	case errors.Is(err, errScreenUnavailable),
		errors.Is(err, errdefs.ErrAttachNotSupported),
		errors.Is(err, errdefs.ErrAttachTaskNotRunning),
		errors.Is(err, errdefs.ErrAttachStaleSocket):
		return activityNotLive
	case errors.As(err, &serverErr) && strings.HasPrefix(string(serverErr), "rpc: can't find "):
		return activityUnsupported
	default:
		return activityError
	}
}

// sbshActivity mirrors sbshScreenshot: the dialer's preflight runs the
// daemon's Attachable and task-Running gates, then one Activity RPC reads
// the terminal's counters.
func sbshActivity(ctx context.Context, c kukeonv1.Client, doc v1beta1.ContainerDoc) (api.ActivityResult, error) {
	dialer := shared.NewTerminalDialer(c, doc)
	defer dialer.Close()
	rc, err := dialer.TerminalClient(ctx, logging.NewNoopLogger())
	if err != nil {
		return api.ActivityResult{}, err
	}
	defer func() { _ = rc.Close() }()
	return terminalActivityRPC(ctx, rc)
}

func terminalActivityRPC(ctx context.Context, rc rpcterminal.Client) (api.ActivityResult, error) {
	var res api.ActivityResult
	err := rc.Activity(ctx, &api.ActivityArgs{}, &res)
	return res, err
}
