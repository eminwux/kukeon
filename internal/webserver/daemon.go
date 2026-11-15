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
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// healthTimeout bounds one connectivity check so an unresponsive daemon is
// reported as unavailable instead of holding the health page open.
const healthTimeout = 5 * time.Second

// Daemon connectivity outcomes reported by GET /api/daemon/health.
const (
	healthReachable        = "reachable"
	healthUnavailable      = "unavailable"
	healthPermissionDenied = "permission_denied"
	healthOutdated         = "daemon_outdated"
	healthFailed           = "failed"
)

// daemonHealth is the connectivity check result (issue #1386, step 6). It
// reports only whether kukeond answered a Ping over the configured socket —
// not workload, cell or subsystem health.
type daemonHealth struct {
	Status        string          `json:"status"`
	Endpoint      string          `json:"endpoint"`
	DaemonVersion string          `json:"daemonVersion,omitempty"`
	ServerVersion string          `json:"serverVersion"`
	CheckedAt     time.Time       `json:"checkedAt"`
	LatencyMillis int64           `json:"latencyMs"`
	Error         *daemonAPIError `json:"error,omitempty"`
}

type daemonAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// daemonLogChunk is the JSON rendering of kukeonv1.DaemonLogResult.
type daemonLogChunk struct {
	Source    string `json:"source"`
	Data      string `json:"data"`
	Start     int64  `json:"start"`
	Next      int64  `json:"next"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	More      bool   `json:"more"`
	Reset     bool   `json:"reset"`
}

func registerDaemonRoutes(mux *http.ServeMux, endpoint string, dial Dial) {
	mux.HandleFunc("GET /api/daemon/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, checkDaemonHealth(r.Context(), endpoint, dial))
	})
	mux.HandleFunc("GET /api/daemon/logs", resourceRoute(dial,
		func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			req, err := daemonLogRequest(r)
			if err != nil {
				return nil, err
			}
			result, err := c.ReadDaemonLogs(ctx, req)
			if err != nil {
				return nil, err
			}
			return daemonLogChunk(result), nil
		}))
}

func checkDaemonHealth(ctx context.Context, endpoint string, dial Dial) daemonHealth {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	health := daemonHealth{Endpoint: endpoint, ServerVersion: config.Version, CheckedAt: time.Now().UTC()}
	began := time.Now()
	version, err := pingDaemon(ctx, dial)
	health.LatencyMillis = time.Since(began).Milliseconds()
	if err != nil {
		health.Status = classifyHealthError(err)
		health.Error = &daemonAPIError{Code: health.Status, Message: err.Error()}
		return health
	}
	health.Status = healthReachable
	health.DaemonVersion = version
	return health
}

func pingDaemon(ctx context.Context, dial Dial) (string, error) {
	client, err := dial(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	return client.PingVersion(ctx)
}

// classifyHealthError separates a socket the server may not open, a daemon
// that cannot be reached, and a daemon that answered but failed the check.
func classifyHealthError(err error) string {
	var opErr *net.OpError
	switch {
	case errors.Is(err, fs.ErrPermission):
		return healthPermissionDenied
	case errors.Is(err, errdefs.ErrDaemonMethodUnsupported):
		return healthOutdated
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, os.ErrNotExist),
		errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, rpc.ErrShutdown),
		errors.Is(err, kukeonv1.ErrUnsupportedScheme),
		errors.As(err, &opErr):
		return healthUnavailable
	}
	return healthFailed
}

func daemonLogRequest(r *http.Request) (kukeonv1.DaemonLogRequest, error) {
	req := kukeonv1.DaemonLogRequest{Offset: -1, MaxBytes: kukeonv1.DaemonLogDefaultMaxBytes}
	query := r.URL.Query()
	if raw := query.Get("offset"); raw != "" {
		offset, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || offset < -1 {
			return req, fmt.Errorf("%w: offset must be -1 (tail) or a byte offset", errdefs.ErrConfig)
		}
		req.Offset = offset
	}
	if raw := query.Get("maxBytes"); raw != "" {
		maxBytes, err := strconv.Atoi(raw)
		if err != nil || maxBytes < 1 || maxBytes > kukeonv1.DaemonLogMaxBytes {
			return req, fmt.Errorf(
				"%w: maxBytes must be between 1 and %d", errdefs.ErrConfig, kukeonv1.DaemonLogMaxBytes,
			)
		}
		req.MaxBytes = maxBytes
	}
	return req, nil
}
