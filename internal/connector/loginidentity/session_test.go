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

// Tests of Start (issue #1685): one identity cell per login identity, which a
// second client joins — while it is still starting too — and which a failed
// start removes only when it is the one that start stored.

package loginidentity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/connector/loginidentity"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// sessionClient stands in for kukeond: Volumes and cells by scope and name,
// with each cell's state.
type sessionClient struct {
	volumes map[string]v1beta1.VolumeDoc
	cells   map[string]v1beta1.CellDoc
	running map[string]bool
	// create, when set, replaces the daemon's create: it may store a cell and
	// fail, as a start that cannot pull its image does.
	create  func(c *sessionClient, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error)
	created []v1beta1.CellDoc
	deleted []string
}

func cellKeyOf(doc v1beta1.CellDoc) string {
	return doc.Spec.RealmID + "/" + doc.Spec.SpaceID + "/" + doc.Spec.StackID + "/" + doc.Metadata.Name
}

func volumeKeyOf(md v1beta1.VolumeMetadata) string {
	return md.Realm + "/" + md.Space + "/" + md.Stack + "/" + md.Name
}

func (c *sessionClient) GetVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
	got, ok := c.volumes[volumeKeyOf(doc.Metadata)]
	if !ok {
		return kukeonv1.GetVolumeResult{}, errdefs.ErrVolumeNotFound
	}
	return kukeonv1.GetVolumeResult{Volume: got, MetadataExists: true}, nil
}

func (c *sessionClient) CreateVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
	c.volumes[volumeKeyOf(doc.Metadata)] = doc
	return kukeonv1.CreateVolumeResult{Volume: doc}, nil
}

func (c *sessionClient) ListVolumes(context.Context, string, string, string) ([]v1beta1.VolumeDoc, error) {
	return nil, nil
}

func (c *sessionClient) ListConfigs(context.Context, string, string, string) ([]v1beta1.CellConfigDoc, error) {
	return nil, nil
}

func (c *sessionClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) { return nil, nil }

func (c *sessionClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	key := cellKeyOf(doc)
	cell, ok := c.cells[key]
	if !ok {
		return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
	}
	return kukeonv1.GetCellResult{Cell: cell, MetadataExists: true, RootContainerTaskRunning: c.running[key]}, nil
}

func (c *sessionClient) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	c.created = append(c.created, doc)
	if c.create != nil {
		return c.create(c, doc)
	}
	key := cellKeyOf(doc)
	if stored, ok := c.cells[key]; ok {
		return kukeonv1.CreateCellResult{Cell: stored, MetadataExistsPre: true}, nil
	}
	c.cells[key] = doc
	c.running[key] = true
	return kukeonv1.CreateCellResult{Cell: doc}, nil
}

func (c *sessionClient) DeleteCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
	delete(c.cells, cellKeyOf(doc))
	c.deleted = append(c.deleted, doc.Metadata.Name)
	return kukeonv1.DeleteCellResult{}, nil
}

func newSessionClient() *sessionClient {
	return &sessionClient{
		volumes: map[string]v1beta1.VolumeDoc{}, cells: map[string]v1beta1.CellDoc{}, running: map[string]bool{},
	}
}

func startRequest(ref v1beta1.VolumeRef) loginidentity.Request {
	return loginidentity.Request{Connector: "agent", Method: "login", Volume: ref, Harnesses: harnessFS()}
}

// storeSession stores the identity cell of ref in state, as another client's
// start left it.
func storeSession(t *testing.T, c *sessionClient, ref v1beta1.VolumeRef, state v1beta1.CellState, running bool) {
	t.Helper()
	doc, err := loginidentity.CellDoc(loginidentity.CellRequest{
		Connector: "agent", Method: "login", Command: []string{"agent", "login"},
		Volume: ref, StatePath: "/home/user/.agent", Image: "example.com/harness/agent:v1", Session: "other",
	})
	if err != nil {
		t.Fatalf("CellDoc: %v", err)
	}
	doc.Status.State = state
	c.cells[cellKeyOf(doc)] = doc
	c.running[cellKeyOf(doc)] = running
}

func TestStart_Sessions(t *testing.T) {
	realm := v1beta1.VolumeRef{Name: "claude", Realm: "r"}
	stack := v1beta1.VolumeRef{Name: "claude", Realm: "r", Space: "default", Stack: "default"}

	t.Run("a session still starting is joined", func(t *testing.T) {
		c := newSessionClient()
		storeSession(t, c, realm, v1beta1.CellStatePending, false)
		session, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(realm))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if !session.Attached || len(c.created) != 0 || len(c.deleted) != 0 {
			t.Errorf("attached=%v created=%d deleted=%v; want the starting session joined, nothing replaced",
				session.Attached, len(c.created), c.deleted)
		}
	})

	t.Run("an ended session is replaced", func(t *testing.T) {
		c := newSessionClient()
		storeSession(t, c, realm, v1beta1.CellStateExited, false)
		session, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(realm))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if session.Attached || len(c.deleted) != 1 || len(c.created) != 1 {
			t.Errorf("attached=%v created=%d deleted=%v; want the ended cell cleared and a new one started",
				session.Attached, len(c.created), c.deleted)
		}
	})

	// A Volume of the same name at another scope has its own session: the
	// realm-scoped identity's running cell is not the stack-scoped one's.
	t.Run("another scope's session is not joined", func(t *testing.T) {
		c := newSessionClient()
		storeSession(t, c, realm, v1beta1.CellStateReady, true)
		session, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(stack))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if session.Attached || len(c.created) != 1 || session.Cell.Metadata.Name != "login-claude" {
			t.Errorf("attached=%v created=%d cell=%q; want a session of its own",
				session.Attached, len(c.created), session.Cell.Metadata.Name)
		}
		if len(c.cells) != 2 {
			t.Errorf("cells = %d, want both sessions", len(c.cells))
		}
	})

	t.Run("a failed start removes the cell it stored", func(t *testing.T) {
		c := newSessionClient()
		c.create = func(c *sessionClient, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			c.cells[cellKeyOf(doc)] = doc
			return kukeonv1.CreateCellResult{}, errors.New("pull failed")
		}
		if _, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(realm)); err == nil {
			t.Fatal("Start: want the create error")
		}
		if len(c.deleted) != 1 || len(c.cells) != 0 {
			t.Errorf("deleted=%v cells=%d; want its own cell removed", c.deleted, len(c.cells))
		}
	})

	// Another client stored the cell between this start's lookup and its
	// create; this create then fails: the other client's cell stays.
	t.Run("a failed start leaves another client's cell", func(t *testing.T) {
		c := newSessionClient()
		c.create = func(c *sessionClient, _ v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			storeSession(t, c, realm, v1beta1.CellStatePending, false)
			return kukeonv1.CreateCellResult{}, errors.New("pull failed")
		}
		if _, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(realm)); err == nil {
			t.Fatal("Start: want the create error")
		}
		if len(c.deleted) != 0 || len(c.cells) != 1 {
			t.Errorf("deleted=%v cells=%d; want the other client's cell kept", c.deleted, len(c.cells))
		}
	})

	t.Run("a create that finds another client's cell joins it", func(t *testing.T) {
		c := newSessionClient()
		c.create = func(c *sessionClient, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			storeSession(t, c, realm, v1beta1.CellStatePending, false)
			return kukeonv1.CreateCellResult{Cell: c.cells[cellKeyOf(doc)], MetadataExistsPre: true}, nil
		}
		session, err := loginidentity.Start(context.Background(), c, loginCatalog(t), startRequest(realm))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if !session.Attached || session.Cell.Metadata.Labels[v1beta1.LabelIdentitySession] != "other" {
			t.Errorf("attached=%v cell labels=%v; want the other client's session",
				session.Attached, session.Cell.Metadata.Labels)
		}
	})
}
