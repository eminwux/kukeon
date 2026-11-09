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

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/sbsh/pkg/attach"
)

const (
	maxCreateCellRequestBytes = 16 << 10
	terminalDefaultColumns    = 80
	terminalDefaultRows       = 24
	terminalMaxMessageBytes   = 1 << 20
	terminalBufferBytes       = 32 << 10
)

// ConfigSummary deliberately omits config values and secret/repository fills.
// Those inputs can contain private data and are not needed to choose a config
// or understand its lineage in the UI.
type ConfigSummary struct {
	Metadata    v1beta1.CellConfigMetadata      `json:"metadata"`
	Blueprint   *v1beta1.CellConfigBlueprintRef `json:"blueprint,omitempty"`
	ValueKeys   []string                        `json:"valueKeys,omitempty"`
	RepoSlots   []string                        `json:"repoSlots,omitempty"`
	SecretSlots []string                        `json:"secretSlots,omitempty"`
}

func registerCellActionRoutes(mux *http.ServeMux, dial Dial, authority string) {
	mux.HandleFunc("GET /api/configs", configListHandler(dial))
	mux.HandleFunc("GET /api/configs/{name}", configDetailHandler(dial))
	mux.HandleFunc("POST /api/cells/from-config", createCellFromConfigHandler(dial))
	mux.HandleFunc(
		"POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/start",
		lifecycleCellHandler(dial, true),
	)
	mux.HandleFunc(
		"POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/stop",
		lifecycleCellHandler(dial, false),
	)
	mux.HandleFunc(
		"GET /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/terminal",
		terminalHandler(dial, authority),
	)
}

func configListHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		configs, err := client.ListConfigs(
			ctx,
			r.URL.Query().Get("realm"),
			r.URL.Query().Get("space"),
			r.URL.Query().Get("stack"),
		)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		out := make([]ConfigSummary, 0, len(configs))
		for _, cfg := range configs {
			out = append(out, summarizeConfig(cfg))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func configDetailHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		lookup := v1beta1.CellConfigDoc{Metadata: v1beta1.CellConfigMetadata{
			Name: r.PathValue("name"), Realm: r.URL.Query().Get("realm"),
			Space: r.URL.Query().Get("space"), Stack: r.URL.Query().Get("stack"),
		}}
		result, err := client.GetConfig(ctx, lookup)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if !result.MetadataExists {
			writeAPIError(w, errdefs.ErrConfigNotFound)
			return
		}
		writeJSON(w, http.StatusOK, summarizeConfig(result.Config))
	}
}

func summarizeConfig(doc v1beta1.CellConfigDoc) ConfigSummary {
	result := ConfigSummary{Metadata: doc.Metadata}
	if doc.Spec.Blueprint.Name != "" {
		ref := doc.Spec.Blueprint
		result.Blueprint = &ref
	}
	for key := range doc.Spec.Values {
		result.ValueKeys = append(result.ValueKeys, key)
	}
	for key := range doc.Spec.Repos {
		result.RepoSlots = append(result.RepoSlots, key)
	}
	for key := range doc.Spec.Secrets {
		result.SecretSlots = append(result.SecretSlots, key)
	}
	sort.Strings(result.ValueKeys)
	sort.Strings(result.RepoSlots)
	sort.Strings(result.SecretSlots)
	return result
}

type createFromConfigRequest struct {
	Config v1beta1.CellConfigMetadata `json:"config"`
	Scope  struct {
		Realm string `json:"realm"`
		Space string `json:"space"`
		Stack string `json:"stack"`
	} `json:"scope"`
	Name  string `json:"name"`
	Start bool   `json:"start"`
}

func createCellFromConfigHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createFromConfigRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateCellRequestBytes)).Decode(&req); err != nil {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]any{
					"error": map[string]string{"code": "invalid_request", "message": "Invalid create-cell request."},
				},
			)
			return
		}
		if strings.TrimSpace(req.Config.Name) == "" || strings.TrimSpace(req.Config.Realm) == "" ||
			strings.TrimSpace(req.Scope.Realm) == "" ||
			strings.TrimSpace(req.Scope.Space) == "" ||
			strings.TrimSpace(req.Scope.Stack) == "" {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]any{
					"error": map[string]string{
						"code":    "invalid_request",
						"message": "A config and target realm, space, and stack are required.",
					},
				},
			)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), actionTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		cfgResult, err := client.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: req.Config})
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if !cfgResult.MetadataExists {
			writeAPIError(w, errdefs.ErrConfigNotFound)
			return
		}
		ref := cfgResult.Config.Spec.Blueprint
		bpResult, err := client.GetBlueprint(
			ctx,
			v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{
					Name:  ref.Name,
					Realm: ref.Realm,
					Space: ref.Space,
					Stack: ref.Stack,
				},
			},
		)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if !bpResult.MetadataExists {
			writeAPIError(w, errdefs.ErrBlueprintNotFound)
			return
		}
		cell, err := cellconfig.MaterializeWithName(cfgResult.Config, bpResult.Blueprint, req.Name)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		cell.Spec.RealmID, cell.Spec.SpaceID, cell.Spec.StackID = req.Scope.Realm, req.Scope.Space, req.Scope.Stack
		cell.Metadata.Name, err = allocateCellName(ctx, client, cfgResult.Config, req)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		cell.Spec.ID = cell.Metadata.Name
		var result kukeonv1.CreateCellResult
		if req.Start {
			result, err = client.CreateCell(ctx, cell)
		} else {
			result, err = client.MaterializeCell(ctx, cell)
		}
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"cell": redactCell(result.Cell), "started": result.Started})
	}
}

func allocateCellName(
	ctx context.Context,
	client kukeonv1.Client,
	config v1beta1.CellConfigDoc,
	req createFromConfigRequest,
) (string, error) {
	return naming.AllocCellName(req.Name, cellconfig.Prefix(config), func(name string) (bool, error) {
		result, err := client.GetCell(ctx, v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: name},
			Spec: v1beta1.CellSpec{
				RealmID: req.Scope.Realm,
				SpaceID: req.Scope.Space,
				StackID: req.Scope.Stack,
			},
		})
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return result.MetadataExists, nil
	})
}

func lifecycleCellHandler(dial Dial, start bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), actionTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		doc := v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: r.PathValue("cell")},
			Spec: v1beta1.CellSpec{
				RealmID: r.PathValue("realm"),
				SpaceID: r.PathValue("space"),
				StackID: r.PathValue("stack"),
			},
		}
		if start {
			result, startErr := client.StartCell(ctx, doc)
			if startErr != nil {
				writeAPIError(w, startErr)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"cell": redactCell(result.Cell), "started": result.Started})
			return
		}
		result, stopErr := client.StopCell(ctx, doc)
		if stopErr != nil {
			writeAPIError(w, stopErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cell": redactCell(result.Cell), "stopped": result.Stopped})
	}
}

type terminalResize struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func terminalHandler(dial Dial, authority string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+authority {
			http.Error(w, "terminal requires a same-origin browser connection", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		client, err := dial(ctx)
		if err != nil {
			cancel()
			writeAPIError(w, err)
			return
		}
		result, err := client.AttachContainer(ctx, v1beta1.ContainerDoc{
			Metadata: v1beta1.ContainerMetadata{Name: r.PathValue("name")},
			Spec: v1beta1.ContainerSpec{
				ID:      r.PathValue("name"),
				RealmID: r.PathValue("realm"),
				SpaceID: r.PathValue("space"),
				StackID: r.PathValue("stack"),
				CellID:  r.PathValue("cell"),
			},
		})
		_ = client.Close()
		cancel()
		if err != nil {
			writeAPIError(w, err)
			return
		}
		serveTerminal(w, r, result.HostSocketPath)
	}
}

func serveTerminal(w http.ResponseWriter, r *http.Request, socketPath string) {
	master, slave, err := pty.Open()
	if err != nil {
		http.Error(w, "could not open terminal", http.StatusInternalServerError)
		return
	}
	defer master.Close()
	defer slave.Close()
	cols, rows := uint16(terminalDefaultColumns), uint16(terminalDefaultRows)
	_ = pty.Setsize(master, &pty.Winsize{Cols: cols, Rows: rows})
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(terminalMaxMessageBytes)
	sessionCtx, stop := context.WithCancel(context.Background())
	defer stop()
	attachDone := make(chan error, 1)
	go func() {
		attachDone <- attach.Run(sessionCtx, attach.Options{SocketPath: socketPath, Stdin: slave, Stdout: slave, Stderr: slave, DisableDetachKeystroke: true})
	}()
	outputDone := streamTerminalOutput(sessionCtx, conn, master)
	input := readTerminalInput(sessionCtx, conn)
	for {
		select {
		case <-r.Context().Done():
			stop()
			return
		case <-attachDone:
			stop()
			return
		case <-outputDone:
			stop()
			return
		case message := <-input:
			if message.err != nil {
				stop()
				return
			}
			if inputErr := applyTerminalInput(master, message); inputErr != nil {
				return
			}
		}
	}
}

type terminalMessage struct {
	kind websocket.MessageType
	data []byte
	err  error
}

func streamTerminalOutput(ctx context.Context, conn *websocket.Conn, master *os.File) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, terminalBufferBytes)
		for {
			n, readErr := master.Read(buf)
			if n > 0 {
				copyBuf := append([]byte(nil), buf[:n]...)
				if writeErr := conn.Write(ctx, websocket.MessageBinary, copyBuf); writeErr != nil {
					done <- writeErr
					return
				}
			}
			if readErr != nil {
				done <- readErr
				return
			}
		}
	}()
	return done
}

func readTerminalInput(ctx context.Context, conn *websocket.Conn) <-chan terminalMessage {
	input := make(chan terminalMessage, 1)
	go func() {
		for {
			kind, data, readErr := conn.Read(ctx)
			select {
			case input <- terminalMessage{kind: kind, data: data, err: readErr}:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()
	return input
}

func applyTerminalInput(master *os.File, message terminalMessage) error {
	if message.kind == websocket.MessageBinary {
		_, err := master.Write(message.data)
		return err
	}
	var resize terminalResize
	_ = json.Unmarshal(message.data, &resize)
	if resize.Type != "resize" || resize.Cols == 0 || resize.Rows == 0 {
		return nil
	}
	return pty.Setsize(master, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows})
}
