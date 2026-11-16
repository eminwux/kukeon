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

// Package webserver serves embedded assets and a daemon-backed resource API.
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
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/celllifecycle"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/webui"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	requestTimeout   = 10 * time.Second
	versionTimeout   = 3 * time.Second
	headerTimeout    = 5 * time.Second
	creationTimeout  = 2 * time.Minute
	lifecycleTimeout = 5 * time.Minute
	writeTimeout     = lifecycleTimeout + headerTimeout
)

// Dial creates a request-owned client, closed after the RPC completes.
type Dial func(context.Context) (kukeonv1.Client, error)

// Run binds the --listen address, applies the --url access policy, and
// prints the bound URL only after a successful bind.
func Run(
	ctx context.Context, listen, browserURL, endpoint string, dial Dial, out, errOut io.Writer,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	target, err := parseListen(listen)
	if err != nil {
		return err
	}
	policy, err := parseAccessPolicy(browserURL)
	if err != nil {
		return err
	}
	handler, err := newHandlerWithPolicy(policy, endpoint, dial)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", target.bind)
	if err != nil {
		return fmt.Errorf("listen on %s (choose another --listen port if in use): %w", target.bind, err)
	}
	defer func() { _ = listener.Close() }()
	_, actualPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return fmt.Errorf("read listening address: %w", err)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: headerTimeout, IdleTimeout: time.Minute,
		WriteTimeout: writeTimeout,
		BaseContext:  func(net.Listener) context.Context { return ctx },
	}
	if _, err = fmt.Fprintf(out, "Listening on http://%s\n", net.JoinHostPort(target.host, actualPort)); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(out, "Accepted browser origin: %s\n", policy); err != nil {
		return err
	}
	if !target.loopback {
		_, _ = fmt.Fprintln(errOut, "WARNING: the web UI has no authentication. Anyone who can reach "+
			"this address can open cell terminals and run daemon actions.")
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

// newHandler builds the handler for a raw --url value ("" accepts any
// Host and Origin).
func newHandler(browserURL, endpoint string, dial Dial) (http.Handler, error) {
	policy, err := parseAccessPolicy(browserURL)
	if err != nil {
		return nil, err
	}
	return newHandlerWithPolicy(policy, endpoint, dial)
}

func newHandlerWithPolicy(policy accessPolicy, endpoint string, dial Dial) (http.Handler, error) {
	assets, err := webui.Assets()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(assets))
	mux.HandleFunc("GET /api/connection", connectionHandler(endpoint, dial))
	mux.HandleFunc("GET /api/realms", realmHandler(dial, false))
	mux.HandleFunc("GET /api/realms/{name}", realmHandler(dial, true))
	mux.HandleFunc("GET /api/realms/{name}/images", realmImagesHandler(dial))
	registerResourceRoutes(mux, dial)
	registerDaemonRoutes(mux, endpoint, dial)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().
			Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		if !policy.allows(r) {
			http.Error(w, "unexpected Host or Origin", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

// connectionInfo reports the web server build and, when kukeond answers,
// the daemon build so the UI can flag a version mismatch (#1470).
type connectionInfo struct {
	Endpoint      string `json:"endpoint"`
	Version       string `json:"version"`
	DaemonVersion string `json:"daemonVersion,omitempty"`
}

// connectionHandler always succeeds: an unreachable daemon leaves
// daemonVersion absent, and the inventory requests report the outage.
func connectionHandler(endpoint string, dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := connectionInfo{Endpoint: endpoint, Version: config.Version}
		ctx, cancel := context.WithTimeout(r.Context(), versionTimeout)
		defer cancel()
		if client, err := dial(ctx); err == nil {
			if version, pingErr := client.PingVersion(ctx); pingErr == nil {
				info.DaemonVersion = version
			}
			_ = client.Close()
		}
		writeJSON(w, http.StatusOK, info)
	}
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
			writeJSON(w, http.StatusOK, redactRealm(result.Realm))
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
		for i := range realms {
			realms[i] = redactRealm(realms[i])
		}
		writeJSON(w, http.StatusOK, realms)
	}
}

// realmImagesHandler lists a realm's spec.images with the reference each
// resolves to and its digest in the realm's containerd namespace (#1418).
func realmImagesHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()
		result, err := client.GetRealm(
			ctx,
			v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: r.PathValue("name")}},
		)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if !result.MetadataExists {
			writeAPIError(w, errdefs.ErrRealmNotFound)
			return
		}
		images := result.Images
		if images == nil {
			images = []v1beta1.RealmImageStatus{}
		}
		writeJSON(w, http.StatusOK, images)
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	status, code := lifecycleErrorStatus(err)
	fields := map[string]any{"code": code, "message": err.Error()}
	var phaseErr *celllifecycle.PhaseError
	if errors.As(err, &phaseErr) {
		fields["phase"] = string(phaseErr.Phase)
		if phaseErr.StoppedFirst {
			fields["stoppedFirst"] = true
		}
	}
	writeJSON(w, status, map[string]any{"error": fields})
}

func apiErrorStatus(err error) (int, string) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, errdefs.ErrDaemonMethodUnsupported):
		status, code = http.StatusNotImplemented, "daemon_outdated"
	case errors.Is(err, errdefs.ErrDaemonLogUnavailable):
		status, code = http.StatusNotFound, "log_unavailable"
	case errors.Is(err, errdefs.ErrCellAlreadyExists):
		status, code = http.StatusConflict, "already_exists"
	case errors.Is(err, fs.ErrPermission):
		status, code = http.StatusForbidden, "permission_denied"
	case errors.Is(err, errdefs.ErrBlueprintNotFound),
		errors.Is(err, errdefs.ErrConfigNotFound),
		errors.Is(err, errdefs.ErrVolumeNotFound),
		errors.Is(err, errdefs.ErrSecretNotFound),
		errors.Is(err, errdefs.ErrBlueprintScopeNotFound),
		errors.Is(err, errdefs.ErrConfigScopeNotFound),
		errors.Is(err, errdefs.ErrVolumeScopeNotFound),
		errors.Is(err, errdefs.ErrSecretScopeNotFound),
		errors.Is(err, errdefs.ErrRealmNotFound),
		errors.Is(err, errdefs.ErrSpaceNotFound),
		errors.Is(
			err,
			errdefs.ErrStackNotFound,
		),
		errors.Is(err, errdefs.ErrCellNotFound),
		errors.Is(err, errdefs.ErrContainerNotFound),
		errors.Is(err, errdefs.ErrImageNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, errdefs.ErrConfig),
		errors.Is(err, errdefs.ErrBlueprintNameRequired),
		errors.Is(err, errdefs.ErrConfigNameRequired),
		errors.Is(err, errdefs.ErrVolumeNameRequired),
		errors.Is(err, errdefs.ErrSecretNameRequired),
		errors.Is(err, errdefs.ErrInvalidRealmName),
		errors.Is(err, errdefs.ErrRealmNameRequired),
		errors.Is(err, errdefs.ErrInvalidName),
		errors.Is(err, errdefs.ErrSpaceNameRequired),
		errors.Is(err, errdefs.ErrStackNameRequired),
		errors.Is(err, errdefs.ErrCellNameRequired),
		errors.Is(err, errdefs.ErrContainerNameRequired):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	return status, code
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
