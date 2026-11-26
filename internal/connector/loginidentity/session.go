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

package loginidentity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/team/harnesses"
)

// Client is the part of the daemon client a login session uses.
type Client interface {
	GetVolume(ctx context.Context, doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error)
	CreateVolume(ctx context.Context, doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error)
	ListVolumes(ctx context.Context, realm, space, stack string) ([]v1beta1.VolumeDoc, error)
	ListConfigs(ctx context.Context, realm, space, stack string) ([]v1beta1.CellConfigDoc, error)
	ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error)
	GetCell(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	CreateCell(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
	DeleteCell(ctx context.Context, doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error)
}

// Request names the login identity to create or log in to.
type Request struct {
	// Connector is the connector's name; Method its login method, empty for
	// the default one.
	Connector string
	Method    string
	// Volume is the identity's name and scope. An empty name is the
	// connector's own name; an empty realm is `default`.
	Volume v1beta1.VolumeRef
	// Image overrides the image the identity cell runs.
	Image string
	// Harnesses is the harness catalog; nil selects the one embedded in kuke.
	Harnesses fs.FS
}

// Session is a login identity and the identity cell logging in to it.
type Session struct {
	// Volume is the identity; VolumeCreated reports that this call created
	// it.
	Volume        v1beta1.VolumeDoc
	VolumeCreated bool
	// Cell is the identity cell and Container its one attachable container.
	// Attached reports that a session was already running for the Volume and
	// no second cell was started.
	Cell      v1beta1.CellDoc
	Container string
	Attached  bool
}

// resolve fills the request's defaults and finds its connector and method.
func resolve(catalog *connector.Catalog, req *Request) (*connector.Connector, connector.Method, error) {
	conn, ok := catalog.Find(strings.TrimSpace(req.Connector))
	if !ok {
		return nil, connector.Method{}, fmt.Errorf(
			"%w: unknown connector %q (connectors: %s)",
			errdefs.ErrConnectorUnknown, req.Connector, strings.Join(catalog.Names(), ", "))
	}
	method, err := Method(conn, req.Method)
	if err != nil {
		return nil, connector.Method{}, err
	}
	req.Volume = v1beta1.VolumeRef{
		Name:  strings.TrimSpace(req.Volume.Name),
		Realm: strings.TrimSpace(req.Volume.Realm),
		Space: strings.TrimSpace(req.Volume.Space),
		Stack: strings.TrimSpace(req.Volume.Stack),
	}
	if req.Volume.Name == "" {
		req.Volume.Name = conn.Metadata.Name
	}
	if req.Volume.Realm == "" {
		req.Volume.Realm = defaultScope
	}
	if req.Volume.Stack != "" && req.Volume.Space == "" {
		return nil, connector.Method{}, fmt.Errorf(
			"%w: a stack-scoped identity names its space too", errdefs.ErrVolumeScopeIncomplete)
	}
	return conn, method, nil
}

// Ensure creates the login identity a request names when it does not exist,
// and returns it. A Volume that exists is returned as it is when it is an
// identity of the connector, or the untagged state volume of a Config that
// logs in through the connector — a team-owned state volume that predates
// tagging; any other Volume is refused, so a login never lands in a volume
// that holds something else.
func Ensure(
	ctx context.Context, client Client, catalog *connector.Catalog, req Request,
) (v1beta1.VolumeDoc, bool, error) {
	conn, method, err := resolve(catalog, &req)
	if err != nil {
		return v1beta1.VolumeDoc{}, false, err
	}
	return ensure(ctx, client, conn, method, req)
}

func ensure(
	ctx context.Context, client Client, conn *connector.Connector, method connector.Method, req Request,
) (v1beta1.VolumeDoc, bool, error) {
	want := VolumeDoc(conn, method, req.Volume)
	got, err := client.GetVolume(ctx, want)
	if err != nil && !errors.Is(err, errdefs.ErrVolumeNotFound) {
		return v1beta1.VolumeDoc{}, false, err
	}
	if err != nil || !got.MetadataExists {
		created, createErr := client.CreateVolume(ctx, want)
		if createErr != nil {
			return v1beta1.VolumeDoc{}, false, createErr
		}
		return created.Volume, true, nil
	}
	where := req.Volume.Name + "@" + ScopePath(req.Volume)
	switch tag := got.Volume.Spec.Connector; {
	case tag == conn.Metadata.Name:
		return got.Volume, false, nil
	case tag != "":
		return v1beta1.VolumeDoc{}, false, fmt.Errorf(
			"%w: volume %s is a login identity of connector %q, not %q",
			errdefs.ErrVolumeIdentity, where, tag, conn.Metadata.Name)
	}
	configs, err := client.ListConfigs(ctx, req.Volume.Realm, "", "")
	if err != nil {
		return v1beta1.VolumeDoc{}, false, err
	}
	for _, id := range Collect([]v1beta1.VolumeDoc{got.Volume}, configs) {
		if id.Volume == req.Volume && id.Connector == conn.Metadata.Name {
			return got.Volume, false, nil
		}
	}
	return v1beta1.VolumeDoc{}, false, fmt.Errorf(
		"%w: volume %s exists and is not a login identity of connector %q; "+
			"tag it with `kuke create volume %s --connector %s`, or pick another name",
		errdefs.ErrVolumeIdentity, where, conn.Metadata.Name, req.Volume.Name, conn.Metadata.Name)
}

// Start makes sure the login identity exists and that an identity cell is
// running the harness login on it: it creates the Volume when missing, and
// starts the cell unless one is already running or starting for the Volume —
// then it returns that one, so two clients share a session instead of racing
// two logins on one state directory.
func Start(ctx context.Context, client Client, catalog *connector.Catalog, req Request) (Session, error) {
	conn, method, err := resolve(catalog, &req)
	if err != nil {
		return Session{}, err
	}
	source := req.Harnesses
	if source == nil {
		source = harnesses.FS
	}
	hn, err := LoadHarnessFrom(source, conn)
	if err != nil {
		return Session{}, err
	}
	var command []string
	if method.Login != nil {
		command = method.Login.Command
	}

	volume, created, err := ensure(ctx, client, conn, method, req)
	if err != nil {
		return Session{}, err
	}
	session := Session{Volume: volume, VolumeCreated: created, Container: containerID}

	image := strings.TrimSpace(req.Image)
	if image == "" {
		if image, err = roleImage(ctx, client, req.Volume); err != nil {
			return Session{}, err
		}
	}
	if image == "" {
		image = hn.Image
	}
	nonce, err := sessionNonce()
	if err != nil {
		return Session{}, err
	}
	doc, err := CellDoc(CellRequest{
		Connector: conn.Metadata.Name, Method: method.Name, Command: command,
		Volume: req.Volume, StatePath: hn.StatePath, Image: image, Session: nonce,
	})
	if err != nil {
		return Session{}, err
	}
	session.Cell = doc

	live, err := client.GetCell(ctx, doc)
	switch {
	case err != nil && !errors.Is(err, errdefs.ErrCellNotFound):
		return Session{}, err
	case err == nil && live.MetadataExists:
		if !IsIdentityCellOf(live.Cell, req.Volume) {
			return Session{}, fmt.Errorf(
				"%w: cell %q exists and is not the identity cell of %s; delete it or pick another identity name",
				errdefs.ErrCellAlreadyExists, doc.Metadata.Name, IdentityKey(req.Volume))
		}
		// Running, or still starting — the first client may be pulling
		// the image: join it.
		if SessionLive(live) {
			session.Cell, session.Attached = live.Cell, true
			return session, nil
		}
		// A session that ended and is not reaped yet: clear it first.
		if _, delErr := client.DeleteCell(ctx, doc); delErr != nil && !errors.Is(delErr, errdefs.ErrCellNotFound) {
			return Session{}, delErr
		}
	}
	return create(ctx, client, doc, nonce, session)
}

// create starts the identity cell doc for session. A cell that fails to start
// is not reaped by its autoDelete: it is removed, so the identity is free for
// the next attempt — but only the cell this call stored (its session label is
// nonce), never one another client started meanwhile. Best-effort.
func create(ctx context.Context, client Client, doc v1beta1.CellDoc, nonce string, session Session) (Session, error) {
	started, err := client.CreateCell(ctx, doc)
	if err != nil {
		if got, getErr := client.GetCell(ctx, doc); getErr == nil && got.MetadataExists &&
			got.Cell.Metadata.Labels[v1beta1.LabelIdentitySession] == nonce {
			_, _ = client.DeleteCell(ctx, doc)
		}
		return Session{}, err
	}
	// Another client stored the cell between the lookup and this create:
	// the daemon kept its record, so this is its session.
	if started.MetadataExistsPre {
		session.Attached = true
		if started.Cell.Metadata.Name != "" {
			session.Cell = started.Cell
		}
	}
	return session, nil
}

// sessionNonce is a value of this start alone, for LabelIdentitySession.
func sessionNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("identity cell session: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// roleImage returns the image of a Config that uses the identity, with a
// realm image handle resolved to its reference; empty when no Config does.
func roleImage(ctx context.Context, client Client, ref v1beta1.VolumeRef) (string, error) {
	configs, err := client.ListConfigs(ctx, ref.Realm, "", "")
	if err != nil {
		return "", err
	}
	var users []ConfigRef
	for _, cfg := range configs {
		if cfg.Status == nil {
			continue
		}
		for _, status := range cfg.Status.Connectors {
			if status.VolumeRef != nil && *status.VolumeRef == ref {
				users = append(users, ConfigRef{
					Name: cfg.Metadata.Name, Realm: cfg.Metadata.Realm,
					Space: cfg.Metadata.Space, Stack: cfg.Metadata.Stack,
				})
			}
		}
	}
	image := ImageFromConfigs(users, configs)
	if image == "" {
		return "", nil
	}
	realms, err := client.ListRealms(ctx)
	if err != nil {
		return "", err
	}
	for _, realm := range realms {
		if realm.Metadata.Name != ref.Realm {
			continue
		}
		if entry, ok := cellconfig.FindRealmImage(realm.Spec.Images, image); ok {
			return cellconfig.RealmImageRef(realm.Metadata.Name, entry), nil
		}
	}
	return image, nil
}
