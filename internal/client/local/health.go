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
	"os"
	"path/filepath"
	"time"

	"github.com/eminwux/kukeon/internal/cgroupcheck"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ReadHealthDigest implements kukeonv1.Client (issue #1509). The checks run
// in this process — kukeond, when the call arrives over the socket — so they
// describe the host kukeond sees, not the caller's container.
func (c *Client) ReadHealthDigest(
	ctx context.Context, req kukeonv1.HealthDigestRequest,
) (kukeonv1.HealthDigest, error) {
	until := time.Now().UTC()
	since := req.Since.UTC()
	if req.SinceLastReport {
		last, ok, err := c.healthStore().LastUntil()
		if err != nil {
			return kukeonv1.HealthDigest{}, err
		}
		since = time.Time{}
		if ok {
			since = last.UTC()
		}
	}
	if since.IsZero() || since.After(until) {
		since = until.Add(-kukeonv1.DefaultHealthWindow)
	}
	digest := kukeonv1.HealthDigest{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindHealthDigest,
		Since: since, Until: until,
		LogErrors: []kukeonv1.HealthLogGroup{},
	}

	socketDir := ""
	if socket := c.ctrl.KukeondSocket(); socket != "" {
		socketDir = filepath.Dir(socket)
	}
	digest.Checks = health.Probe{
		ContainerdSocket: c.ctrl.ContainerdSocket(),
		CgroupRoot:       cgroupcheck.DefaultHostRoot(),
		CNIBinDir:        health.DefaultCNIBinDir,
		SocketDir:        socketDir,
		Namespaces: func() ([]string, error) {
			// The runner reports a missing socket as an empty list so
			// uninstall can degrade; for a health check it is a failure.
			if _, err := os.Stat(c.ctrl.ContainerdSocket()); err != nil {
				return nil, err
			}
			return c.ctrl.ListContainerdNamespaces()
		},
		RealmNamespaces: func() ([]string, error) {
			realms, err := c.ListRealms(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(realms))
			for i := range realms {
				ns := realms[i].Spec.Namespace
				if ns == "" {
					ns = consts.RealmNamespace(realms[i].Metadata.Name)
				}
				out = append(out, ns)
			}
			return out, nil
		},
	}.Checks()

	c.readHealthLog(ctx, &digest)

	cells, err := c.ListCells(ctx, "", "", "")
	if err != nil {
		return kukeonv1.HealthDigest{}, err
	}
	digest.Cells = health.FailedCells(cells)
	digest.Clean = health.Clean(&digest)
	return digest, nil
}

// readHealthLog fills the digest's log groups from kukeond's own log file. A
// kukeond that has none (run outside its cell) is not a failure of the
// digest: the reason is recorded and the other sections still apply.
func (c *Client) readHealthLog(ctx context.Context, digest *kukeonv1.HealthDigest) {
	located, err := c.LogContainer(ctx, kukeondContainerDoc())
	if err != nil {
		digest.LogNote = "kukeond log not read: " + daemonLogLocateError(err).Error()
		return
	}
	path := located.HostLogPath
	if path == "" {
		path = located.HostCapturePath
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			digest.LogNote = "kukeond log not read: " + path + " does not exist"
		} else {
			digest.LogNote = "kukeond log not read: " + err.Error()
		}
		return
	}
	defer func() { _ = file.Close() }()
	groups, err := health.GroupLogErrors(file, digest.Since, digest.Until)
	if err != nil {
		digest.LogNote = "kukeond log not read: " + err.Error()
		return
	}
	digest.LogSource, digest.LogErrors = path, groups
}

func (c *Client) healthStore() *health.Store { return health.NewStore(c.ctrl.RunPath()) }

// SubmitHealthReport implements kukeonv1.Client (issues #1509, #1619). It is
// the same create path `kuke apply` of a HealthReport document takes.
func (c *Client) SubmitHealthReport(
	_ context.Context, report kukeonv1.HealthReport,
) (kukeonv1.HealthReport, error) {
	return c.healthStore().Submit(report)
}

// ListHealthReports implements kukeonv1.Client (issue #1509).
func (c *Client) ListHealthReports(_ context.Context) ([]kukeonv1.HealthReportSummary, error) {
	return c.healthStore().List()
}

// ReadHealthReport implements kukeonv1.Client (issue #1509).
func (c *Client) ReadHealthReport(_ context.Context, id string) (kukeonv1.HealthReport, error) {
	return c.healthStore().Read(id)
}

// DeleteHealthReport implements kukeonv1.Client (issue #1619).
func (c *Client) DeleteHealthReport(_ context.Context, name string) error {
	return c.healthStore().Delete(name)
}
