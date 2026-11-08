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

// Package webserver serves embedded assets and a read-only daemon-backed resource API.
package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/webui"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	requestTimeout = 10 * time.Second
	headerTimeout  = 5 * time.Second
	writeTimeout   = 15 * time.Second
)

// Dial creates a request-owned client, closed after the RPC completes.
type Dial func(context.Context) (kukeonv1.Client, error)

// Run binds only loopback and prints the URL after a successful bind.
func Run(ctx context.Context, host string, port int, endpoint string, dial Dial, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	address, err := listenAddress(host, port)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s (choose another --port if in use): %w", address, err)
	}
	defer func() { _ = listener.Close() }()
	_, actualPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return fmt.Errorf("read listening address: %w", err)
	}
	authority := net.JoinHostPort(host, actualPort)
	handler, err := newHandler(authority, endpoint, dial)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: headerTimeout, IdleTimeout: time.Minute,
		WriteTimeout: writeTimeout,
		BaseContext:  func(net.Listener) context.Context { return ctx },
	}
	if _, err = fmt.Fprintf(out, "Listening on http://%s\n", authority); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), requestTimeout)
		defer shutdownCancel()
		if server.Shutdown(shutdownCtx) != nil {
			_ = server.Close()
		}
	}()
	err = server.Serve(listener)
	// Ensure the shutdown goroutine also exits on an unexpected Serve failure.
	if !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	<-done
	return nil
}

func listenAddress(host string, port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("%w: --port must be between 0 and 65535", errdefs.ErrConfig)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("%w: --listen must be localhost or a loopback IP address", errdefs.ErrConfig)
	}
	// Pin localhost to loopback, never rely on DNS for the binding boundary.
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func newHandler(authority, endpoint string, dial Dial) (http.Handler, error) {
	assets, err := webui.Assets()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(assets))
	mux.HandleFunc("GET /api/connection", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"endpoint": endpoint})
	})
	mux.HandleFunc("GET /api/realms", realmHandler(dial, false))
	mux.HandleFunc("GET /api/realms/{name}", realmHandler(dial, true))
	registerResourceRoutes(mux, dial)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		origins := r.Header.Values("Origin")
		if r.Host != authority || len(origins) > 1 || (len(origins) == 1 && origins[0] != "http://"+authority) {
			http.Error(w, "unexpected Host or Origin", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func realmHandler(dial Dial, detail bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		if detail {
			result, getErr := client.GetRealm(
				ctx,
				v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: r.PathValue("name")}},
			)
			if getErr != nil {
				writeAPIError(w, getErr)
				return
			}
			if !result.MetadataExists {
				writeAPIError(w, errdefs.ErrRealmNotFound)
				return
			}
			writeJSON(w, http.StatusOK, result.Realm)
			return
		}
		realms, err := client.ListRealms(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if realms == nil {
			realms = []v1beta1.RealmDoc{}
		}
		writeJSON(w, http.StatusOK, realms)
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, fs.ErrPermission):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, errdefs.ErrRealmNotFound), errors.Is(err, errdefs.ErrSpaceNotFound),
		errors.Is(err, errdefs.ErrStackNotFound), errors.Is(err, errdefs.ErrCellNotFound), errors.Is(err, errdefs.ErrContainerNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, errdefs.ErrInvalidRealmName), errors.Is(err, errdefs.ErrRealmNameRequired),
		errors.Is(err, errdefs.ErrInvalidName), errors.Is(err, errdefs.ErrSpaceNameRequired),
		errors.Is(err, errdefs.ErrStackNameRequired), errors.Is(err, errdefs.ErrCellNameRequired),
		errors.Is(err, errdefs.ErrContainerNameRequired):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
