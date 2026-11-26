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

package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	shared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/gorilla/websocket"
)

const (
	// execOutputLimit bounds each of stdout and stderr in a run-command
	// response; the rest is drained and dropped.
	execOutputLimit = 1 << 20
	// execRunTimeout bounds a run-command request. Past it the exec
	// connection is closed, which makes kukeond kill the process.
	execRunTimeout = 60 * time.Second
	maxExecBody    = 64 * 1024

	execIdentityContainer = "container"
	execIdentityRoot      = "root"
	// execExitUnknown is the exitCode of a run that was cut off before the
	// process reported one.
	execExitUnknown = -1

	execTerminalDefaultCols = 80
	execTerminalDefaultRows = 24
	// execTerminalShell is what the web terminal runs: bash when the image
	// has it, sh otherwise.
	execTerminalShell = "command -v bash >/dev/null 2>&1 && exec bash || exec sh"
)

// execLimits are the run-command bounds; tests shorten them.
type execLimits struct {
	output  int
	timeout time.Duration
}

// execRunRequest is the body of the run-command route.
type execRunRequest struct {
	// Command is one line, run as `sh -c <command>`.
	Command string `json:"command"`
	// Identity is "container" (the default) or "root".
	Identity string `json:"identity"`
	// ConfirmRoot must be true for identity "root".
	ConfirmRoot bool `json:"confirmRoot"`
}

// execRunResponse is what the run-command route answers with.
type execRunResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// ExitCode is the process's exit code, or -1 when the run timed out.
	ExitCode int `json:"exitCode"`
	// Truncated reports that stdout or stderr exceeded the output limit.
	Truncated bool `json:"truncated"`
	// TimedOut reports that the run exceeded the time limit and was killed.
	TimedOut bool `json:"timedOut"`
}

// execTarget is the container an exec route addresses.
type execTarget struct {
	realm, space, stack, cell, name string
}

func execTargetOf(r *http.Request) execTarget {
	return execTarget{
		realm: r.PathValue("realm"), space: r.PathValue("space"), stack: r.PathValue("stack"),
		cell: r.PathValue("cell"), name: r.PathValue("name"),
	}
}

func (t execTarget) doc() v1.ContainerDoc {
	return v1.ContainerDoc{
		Metadata: v1.ContainerMetadata{Name: t.name},
		Spec: v1.ContainerSpec{
			ID: t.name, RealmID: t.realm, SpaceID: t.space, StackID: t.stack, CellID: t.cell,
		},
	}
}

// cellNotLiveError carries the cell liveness guard's message verbatim (the
// one `kuke attach` and `kuke exec` print) and classifies as
// ErrExecTaskNotRunning so the API reports it with a stable code.
type cellNotLiveError struct{ err error }

func (e cellNotLiveError) Error() string { return e.err.Error() }

func (e cellNotLiveError) Is(target error) bool { return target == errdefs.ErrExecTaskNotRunning }

// registerExecRoutes exposes `kuke exec` to the browser (issue #1432): a
// run-command route that returns the output of one command, and an
// interactive terminal over a WebSocket. Both run as the container's own
// user unless root is asked for, and refuse root without the explicit
// confirmation the UI collects.
func registerExecRoutes(mux *http.ServeMux, dial Dial) {
	registerExecRoutesWith(mux, dial, execLimits{output: execOutputLimit, timeout: execRunTimeout})
}

func registerExecRoutesWith(mux *http.ServeMux, dial Dial, limits execLimits) {
	const base = "/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/exec"
	mux.HandleFunc("POST "+base, execRunHandler(dial, limits))
	mux.HandleFunc("GET "+base+"/terminal", execTerminalHandler(dial))
}

// execIdentity validates the identity selector and the root confirmation,
// and reports whether the process runs as root.
func execIdentity(identity string, confirmed bool) (bool, error) {
	switch identity {
	case "", execIdentityContainer:
		return false, nil
	case execIdentityRoot:
		if !confirmed {
			return false, errdefs.ErrExecRootNotConfirmed
		}
		return true, nil
	default:
		return false, fmt.Errorf("%w: identity must be %q or %q",
			errdefs.ErrExecInvalidRequest, execIdentityContainer, execIdentityRoot)
	}
}

// startExec runs the cell liveness guard, the same one the attach route
// applies, and then starts the exec.
func startExec(
	ctx context.Context,
	client kukeonv1.Client,
	target execTarget,
	req kukeonv1.ExecRequest,
) (*net.UnixConn, error) {
	result, err := client.GetCell(ctx, v1.CellDoc{
		Metadata: v1.CellMetadata{Name: target.cell},
		Spec:     v1.CellSpec{RealmID: target.realm, SpaceID: target.space, StackID: target.stack},
	})
	if err != nil {
		return nil, err
	}
	if !result.MetadataExists {
		return nil, fmt.Errorf("cell %q: %w", target.cell, errdefs.ErrCellNotFound)
	}
	if err = shared.GuardCellTaskLiveness(result, target.cell, target.realm, target.space, target.stack); err != nil {
		return nil, cellNotLiveError{err: err}
	}
	conn, err := client.ExecContainer(ctx, target.doc(), req)
	if err != nil {
		return nil, shared.WrapExecError(err, target.doc())
	}
	return conn, nil
}

func execRunHandler(dial Dial, limits execLimits) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Like every JSON mutation: a cross-site form post cannot declare
		// this media type without a preflight.
		if r.Header.Get("Content-Type") != jsonContentType {
			writeAPIError(w, fmt.Errorf("%w: JSON request required", errdefs.ErrExecInvalidRequest))
			return
		}
		var body execRunRequest
		decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxExecBody))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeAPIError(w, fmt.Errorf("%w: %w", errdefs.ErrExecInvalidRequest, err))
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeAPIError(w, fmt.Errorf("%w: expected one JSON document", errdefs.ErrExecInvalidRequest))
			return
		}
		// Refused before kukeond is dialed: a root request that is not
		// confirmed never reaches the daemon.
		root, err := execIdentity(body.Identity, body.ConfirmRoot)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if strings.TrimSpace(body.Command) == "" {
			writeAPIError(w, fmt.Errorf("%w: a command is required", errdefs.ErrExecInvalidRequest))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		conn, err := startExec(ctx, client, execTargetOf(r), kukeonv1.ExecRequest{
			Command: []string{"sh", "-c", body.Command},
			Root:    root,
		})
		if err != nil {
			writeAPIError(w, err)
			return
		}
		response, err := collectExec(r.Context(), conn, limits)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// collectExec reads one exec session to its end, keeping at most
// limits.output bytes of each stream. Closing conn is what ends a run that
// outlives limits.timeout or whose browser went away: kukeond kills the
// process of a session whose caller is gone.
func collectExec(ctx context.Context, conn *net.UnixConn, limits execLimits) (execRunResponse, error) {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(limits.timeout))

	stdout := &cappedBuffer{limit: limits.output}
	stderr := &cappedBuffer{limit: limits.output}
	exit, err := execstream.Receive(conn, stdout, stderr)
	response := execRunResponse{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  exit.Code,
		Truncated: stdout.truncated || stderr.truncated,
	}
	switch {
	case errors.Is(err, os.ErrDeadlineExceeded):
		response.ExitCode = execExitUnknown
		response.TimedOut = true
		return response, nil
	case err != nil:
		return execRunResponse{}, err
	case exit.Error != "":
		return execRunResponse{}, fmt.Errorf("%w: %s", errdefs.ErrExecFailed, exit.Error)
	}
	return response, nil
}

// cappedBuffer keeps the first limit bytes written to it and accepts the
// rest without storing it, so the process is never blocked on its output.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room < len(p) {
		b.truncated = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// execTerminalHandler bridges a WebSocket to an interactive exec session.
// The message protocol is the attach route's (binary frames are terminal
// bytes; text frames are ready, error and resize) plus an exit message sent
// before the server closes. The PTY lives in the container, so the bytes are
// bridged straight between the two connections.
func execTerminalHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The root rule is settled before the upgrade, as a plain API error.
		query := r.URL.Query()
		root, err := execIdentity(query.Get("identity"), query.Get("confirmRoot") == "1")
		if err != nil {
			writeAPIError(w, err)
			return
		}
		// The shared access policy has already checked Host/Origin.
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		const maxInputBytes = 64 * 1024
		const wsWriteTimeout = 10 * time.Second
		ws.SetReadLimit(maxInputBytes)
		var writeMu sync.Mutex
		write := func(kind int, data []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			return ws.WriteMessage(kind, data)
		}
		status := func(fields map[string]any) {
			payload, _ := json.Marshal(fields)
			_ = write(websocket.TextMessage, payload)
		}

		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		client, err := dial(ctx)
		if err != nil {
			cancel()
			status(map[string]any{"type": "error", "message": err.Error()})
			return
		}
		defer client.Close()
		conn, err := startExec(ctx, client, execTargetOf(r), kukeonv1.ExecRequest{
			Command: []string{"sh", "-c", execTerminalShell},
			Root:    root,
			Stdin:   true,
			TTY:     true,
			Cols:    queryDimension(query.Get("cols"), execTerminalDefaultCols),
			Rows:    queryDimension(query.Get("rows"), execTerminalDefaultRows),
		})
		cancel()
		if err != nil {
			status(map[string]any{"type": "error", "message": err.Error()})
			return
		}
		// Closing the exec connection is what kills the process when the
		// panel or the tab goes away.
		defer func() { _ = conn.Close() }()
		// The upgrade alone does not mean kukeond accepted the target.
		if err = write(websocket.TextMessage, []byte(`{"type":"ready"}`)); err != nil {
			return
		}

		finished := make(chan struct{})
		go func() {
			defer close(finished)
			output := wsBinaryWriter(write)
			exit, recvErr := execstream.Receive(conn, output, output)
			switch {
			case recvErr != nil:
				status(map[string]any{"type": "error", "message": recvErr.Error()})
			case exit.Error != "":
				status(map[string]any{"type": "error", "message": exit.Error})
			default:
				status(map[string]any{"type": "exit", "code": exit.Code})
			}
			// Ends the read loop below; the browser sees the close after the
			// last message.
			_ = ws.Close()
		}()

		frames := execstream.NewWriter(conn)
	bridge:
		for {
			kind, data, readErr := ws.ReadMessage()
			if readErr != nil {
				break
			}
			switch kind {
			case websocket.BinaryMessage:
				if frames.WriteData(execstream.TypeStdin, data) != nil {
					break bridge
				}
			case websocket.TextMessage:
				var msg struct {
					Type string `json:"type"`
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
				}
				if json.Unmarshal(data, &msg) != nil || msg.Type != "resize" || msg.Cols == 0 || msg.Rows == 0 {
					continue
				}
				_ = frames.Resize(msg.Cols, msg.Rows)
			}
		}
		_ = conn.Close()
		<-finished
	}
}

// wsBinaryWriter sends each write as one binary WebSocket message.
type wsBinaryWriter func(kind int, data []byte) error

func (w wsBinaryWriter) Write(p []byte) (int, error) {
	if err := w(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// queryDimension parses a terminal dimension from the query, falling back
// when it is absent or not a positive 16-bit number.
func queryDimension(raw string, fallback uint16) uint16 {
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || value == 0 {
		return fallback
	}
	return uint16(value)
}

// execErrorStatus maps the exec sentinels onto API statuses and codes.
func execErrorStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errdefs.ErrExecRootNotConfirmed):
		return http.StatusBadRequest, "root_not_confirmed", true
	case errors.Is(err, errdefs.ErrExecInvalidRequest):
		return http.StatusBadRequest, "invalid_request", true
	case errors.Is(err, errdefs.ErrExecTaskNotRunning):
		return http.StatusConflict, "not_running", true
	case errors.Is(err, errdefs.ErrExecSessionLost):
		return http.StatusBadGateway, "exec_session_lost", true
	case errors.Is(err, errdefs.ErrExecFailed):
		return http.StatusInternalServerError, "exec_failed", true
	}
	return 0, "", false
}
