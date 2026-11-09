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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/creack/pty"
	shared "github.com/eminwux/kukeon/cmd/kuke/shared"
	v1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/api"
	"github.com/eminwux/sbsh/pkg/attach"
	terminal "github.com/eminwux/sbsh/pkg/rpcclient/terminal"
	"github.com/gorilla/websocket"
)

func registerAttachRoute(mux *http.ServeMux, dial Dial) {
	registerAttachRouteWith(mux, dial, attach.Run, resizeTerminal)
}

func resizeTerminal(ctx context.Context, socket string, cols, rows int) error {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctrl := terminal.NewUnix(socket, logger)
	defer ctrl.Close()
	return ctrl.Resize(ctx, &api.ResizeArgs{Cols: cols, Rows: rows})
}

func forwardAttachOutput(output io.Reader, write func(int, []byte) error, onWriteFailure func()) {
	const outputBufferSize = 16 * 1024
	buf := make([]byte, outputBufferSize)
	for {
		n, err := output.Read(buf)
		if n > 0 && write(websocket.BinaryMessage, buf[:n]) != nil {
			onWriteFailure()
			return
		}
		if err != nil {
			return
		}
	}
}

//nolint:funlen // Keep the connection lifecycle and its cleanup in one handler.
func registerAttachRouteWith(
	mux *http.ServeMux,
	dial Dial,
	run func(context.Context, attach.Options) error,
	resize func(context.Context, string, int, int) error,
) {
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/attach",
		func(w http.ResponseWriter, r *http.Request) {
			// A WebSocket can carry terminal input, so require an explicit same-origin browser.
			if r.Header.Get("Origin") != "http://"+r.Host {
				http.Error(w, "same-origin request required", http.StatusForbidden)
				return
			}
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer ws.Close()
			const maxInputBytes = 64 * 1024
			const writeTimeout = 10 * time.Second
			const detachTimeout = 3 * time.Second
			const initialRows, initialCols = 24, 80
			ws.SetReadLimit(maxInputBytes)
			var writeMu sync.Mutex
			write := func(kind int, data []byte) error {
				writeMu.Lock()
				defer writeMu.Unlock()
				_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
				return ws.WriteMessage(kind, data)
			}
			fail := func(err error) {
				payload, _ := json.Marshal(map[string]string{"type": "error", "message": err.Error()})
				_ = write(websocket.TextMessage, payload)
			}
			ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
			client, err := dial(ctx)
			if err != nil {
				cancel()
				fail(err)
				return
			}
			defer client.Close()
			realm, space, stack, cell, name := r.PathValue(
				"realm",
			), r.PathValue(
				"space",
			), r.PathValue(
				"stack",
			), r.PathValue(
				"cell",
			), r.PathValue(
				"name",
			)
			result, err := client.GetCell(
				ctx,
				v1.CellDoc{
					Metadata: v1.CellMetadata{Name: cell},
					Spec:     v1.CellSpec{RealmID: realm, SpaceID: space, StackID: stack},
				},
			)
			if err == nil {
				err = shared.GuardCellTaskLiveness(result, cell)
			}
			var socket string
			if err == nil {
				attached, attachErr := client.AttachContainer(
					ctx,
					v1.ContainerDoc{
						Metadata: v1.ContainerMetadata{Name: name},
						Spec: v1.ContainerSpec{
							ID:      name,
							RealmID: realm,
							SpaceID: space,
							StackID: stack,
							CellID:  cell,
						},
					},
				)
				err, socket = attachErr, attached.HostSocketPath
			}
			cancel()
			if err != nil {
				fail(err)
				return
			}
			if socket == "" {
				fail(fmt.Errorf("daemon returned an empty attach socket for container %q", name))
				return
			}
			master, slave, err := pty.Open()
			if err != nil {
				fail(err)
				return
			}
			defer master.Close()
			defer slave.Close()
			_ = pty.Setsize(master, &pty.Winsize{Rows: initialRows, Cols: initialCols})
			session, stop := context.WithCancel(r.Context())
			defer stop()
			// The WebSocket upgrade alone does not mean the daemon accepted the
			// target. Signal readiness only after validation and PTY setup.
			if err = write(websocket.TextMessage, []byte(`{"type":"ready"}`)); err != nil {
				return
			}
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				done <- run(session, attach.Options{SocketPath: socket, Stdin: slave, Stdout: slave, Stderr: slave})
			}()
			outputDone := make(chan struct{})
			go func() {
				defer close(outputDone)
				forwardAttachOutput(master, write, func() {
					stop()
					_ = ws.Close()
				})
			}()
			go func() {
				defer close(finished)
				runErr := <-done
				if session.Err() == nil && runErr != nil && !errors.Is(runErr, attach.ErrDetached) {
					fail(fmt.Errorf("terminal connection lost: %w", runErr))
				}
				_ = ws.Close()
			}()
		readLoop:
			for {
				kind, data, readErr := ws.ReadMessage()
				if readErr != nil {
					break
				}
				switch kind {
				case websocket.BinaryMessage:
					if _, err = master.Write(data); err != nil {
						fail(err)
						break readLoop
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
					_ = pty.Setsize(master, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols})
					resizeCtx, resizeCancel := context.WithTimeout(session, time.Second)
					resizeErr := resize(resizeCtx, socket, int(msg.Cols), int(msg.Rows))
					resizeCancel()
					if resizeErr != nil {
						fail(fmt.Errorf("terminal resize failed: %w", resizeErr))
					}
				}
			}
			// The normal detach sequence asks sbsh to release its client registration.
			_, _ = master.Write([]byte{0x1d, 0x1d})
			select {
			case <-finished:
			case <-time.After(detachTimeout):
			}
			stop()
			<-finished
			_ = master.Close()
			<-outputDone
		},
	)
}
