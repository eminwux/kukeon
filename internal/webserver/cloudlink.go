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
	"crypto/ed25519"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/cloudhost"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/cloudlink"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// CloudLink configures `kuke serve --cloud-link` (issue #1755).
type CloudLink struct {
	// URL is the relay's wss:// link URL.
	URL string
	// StatusFile, when set, is where the link status is kept for `kuke
	// cloud status`.
	StatusFile string
}

// cloudUserKey carries the relay-verified user of a link request.
type cloudUserKey struct{}

// newLinkHandler serves requests arriving over the cloud link: the /api
// routes the local server has, minus the login routes. The link itself
// authenticates the relay, so there is no local sign-in; instead every
// request must carry a user identity signed by the relay's key, which is
// checked and recorded in the audit log before the request is served.
func newLinkHandler(
	endpoint string, dial Dial, catalog *connector.Catalog,
	hostID string, relayKey ed25519.PublicKey, audit *slog.Logger, now func() time.Time,
) http.Handler {
	mux := http.NewServeMux()
	registerAPIRoutes(mux, endpoint, dial, catalog)
	// The console asks who is signed in; over the link it is the relay's user.
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(cloudUserKey{}).(string)
		writeJSON(w, http.StatusOK, sessionInfo{User: user})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		user, err := cloudlink.VerifyRequest(r, relayKey, hostID, now())
		if err != nil {
			audit.WarnContext(r.Context(), "cloud link request rejected",
				"method", r.Method, "path", r.URL.Path, "reason", err.Error())
			writeAPIError(w, fmt.Errorf("%w: %w", errdefs.ErrUnauthenticated, err))
			return
		}
		audit.InfoContext(r.Context(), "cloud link request",
			"user", user, "method", r.Method, "path", r.URL.Path)
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), cloudUserKey{}, user)))
	})
}

// preparedLink is a cloud link whose credential loaded.
type preparedLink struct {
	client     *cloudlink.Client
	hostID     string
	url        string
	statusFile string
	pid        int
}

// prepareCloudLink loads the enrolled credential and builds the link
// client, so a host that is not enrolled fails before anything binds.
func prepareCloudLink(
	ctx context.Context, link *CloudLink, endpoint string, dial Dial, catalog *connector.Catalog,
	errOut io.Writer,
) (*preparedLink, error) {
	if err := cloudlink.ValidateLinkURL(link.URL); err != nil {
		return nil, fmt.Errorf("%w: --cloud-link: %w", errdefs.ErrConfig, err)
	}
	loadCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client, err := dial(loadCtx)
	if err != nil {
		return nil, fmt.Errorf("--cloud-link: %w", err)
	}
	cred, err := cloudhost.LoadCredential(loadCtx, client)
	_ = client.Close()
	if err != nil {
		return nil, fmt.Errorf("--cloud-link: %w", err)
	}
	tlsConfig, err := cred.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("--cloud-link: %w", err)
	}
	relayKey, err := cred.IdentityPublicKey()
	if err != nil {
		return nil, fmt.Errorf("--cloud-link: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(errOut, nil))
	pid := os.Getpid()
	onState := func(state cloudlink.State) {
		if link.StatusFile == "" {
			return
		}
		status := cloudhost.Status{PID: pid, URL: link.URL, HostID: cred.HostID, State: state}
		if writeErr := cloudhost.WriteStatus(link.StatusFile, status); writeErr != nil {
			logger.WarnContext(ctx, "cloud link status not recorded", "file", link.StatusFile, "error", writeErr)
		}
	}
	linkClient := &cloudlink.Client{
		URL:       link.URL,
		TLSConfig: tlsConfig,
		Handler:   newLinkHandler(endpoint, dial, catalog, cred.HostID, relayKey, logger, time.Now),
		OnState:   onState,
		Logger:    logger,
	}
	return &preparedLink{
		client: linkClient, hostID: cred.HostID, url: link.URL, statusFile: link.StatusFile, pid: pid,
	}, nil
}

// start runs the link until ctx is done. The returned function waits for
// it to stop.
func (p *preparedLink) start(ctx context.Context, out io.Writer) (func(), error) {
	if _, err := fmt.Fprintf(out, "Cloud link: %s (host %s)\n", p.url, p.hostID); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.client.Run(ctx)
		if p.statusFile != "" {
			cloudhost.RemoveStatus(p.statusFile, p.pid)
		}
	}()
	return func() { <-done }, nil
}
