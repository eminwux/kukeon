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

package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// sourceCore stubs the Source methods of the in-process client.
type sourceCore struct {
	kukeonv1.FakeClient

	gotRef   kukeonv1.SourceRef
	gotRealm string
	err      error
}

func agentsSource() kukeonv1.GetSourceResult {
	return kukeonv1.GetSourceResult{
		Exists:   true,
		Document: []byte("apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: agents\n"),
		Summary: kukeonv1.SourceSummary{
			Name: "agents", Realm: "default", URL: "github.com/eminwux/agents", Ref: "branch main", Identity: "git-ssh",
		},
	}
}

func (c *sourceCore) GetSource(_ context.Context, ref kukeonv1.SourceRef) (kukeonv1.GetSourceResult, error) {
	c.gotRef = ref
	if c.err != nil {
		return kukeonv1.GetSourceResult{}, c.err
	}
	return agentsSource(), nil
}

func (c *sourceCore) ListSources(_ context.Context, realm string) ([]kukeonv1.GetSourceResult, error) {
	c.gotRealm = realm
	if c.err != nil {
		return nil, c.err
	}
	return []kukeonv1.GetSourceResult{agentsSource()}, nil
}

func (c *sourceCore) DeleteSource(_ context.Context, ref kukeonv1.SourceRef) (kukeonv1.DeleteSourceResult, error) {
	c.gotRef = ref
	if c.err != nil {
		return kukeonv1.DeleteSourceResult{}, c.err
	}
	return kukeonv1.DeleteSourceResult{Deleted: true}, nil
}

// TestSourceRPCRoundTrip drives the three Source methods over a real kukeond
// socket (issue #1643), and their sentinels survive the wire.
func TestSourceRPCRoundTrip(t *testing.T) {
	core := &sourceCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, shortTempDir(t), core))
	t.Cleanup(func() { _ = client.Close() })
	ctx := testCtx(t)

	got, err := client.GetSource(ctx, kukeonv1.SourceRef{Name: "agents", Realm: "default"})
	if err != nil || core.gotRef.Name != "agents" || !got.Exists || got.Summary != agentsSource().Summary {
		t.Fatalf("GetSource = %+v, %v (ref %+v)", got, err, core.gotRef)
	}
	listed, err := client.ListSources(ctx, "other")
	if err != nil || core.gotRealm != "other" || len(listed) != 1 {
		t.Fatalf("ListSources = %+v, %v", listed, err)
	}
	deleted, err := client.DeleteSource(ctx, kukeonv1.SourceRef{Name: "agents"})
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteSource = %+v, %v", deleted, err)
	}
	for _, sentinel := range []error{
		errdefs.ErrSourceNotFound, errdefs.ErrSourceInUse, errdefs.ErrSourceExists, errdefs.ErrInvalidSource,
	} {
		core.err = fmt.Errorf("%w: agents", sentinel)
		_, getErr := client.GetSource(ctx, kukeonv1.SourceRef{Name: "agents"})
		_, deleteErr := client.DeleteSource(ctx, kukeonv1.SourceRef{Name: "agents"})
		if !errors.Is(getErr, sentinel) || !errors.Is(deleteErr, sentinel) {
			t.Errorf("%v: get %v, delete %v", sentinel, getErr, deleteErr)
		}
	}
}

// gitFetchCore records the caller uid the daemon hands StageGitFetch and
// ReleaseGitFetch.
type gitFetchCore struct {
	kukeonv1.FakeClient

	stageUID, releaseUID int
	stageOK, releaseOK   bool
	req                  kukeonv1.GitFetchRequest
	id                   string
}

func (c *gitFetchCore) StageGitFetch(
	ctx context.Context, req kukeonv1.GitFetchRequest,
) (kukeonv1.StageGitFetchResult, error) {
	c.stageUID, c.stageOK = kukeonv1.PeerUIDFrom(ctx)
	c.req = req
	return kukeonv1.StageGitFetchResult{Staged: true, ID: "0123456789abcdef", Dir: "/run/kukeon/gitfetch/x"}, nil
}

func (c *gitFetchCore) ReleaseGitFetch(ctx context.Context, id string) error {
	c.releaseUID, c.releaseOK = kukeonv1.PeerUIDFrom(ctx)
	c.id = id
	return nil
}

// TestGitFetchRPCPeerUID is #1643 D10: the uid a staging is owned to is the
// socket peer's, read by the server codec, not anything the request carries.
func TestGitFetchRPCPeerUID(t *testing.T) {
	core := &gitFetchCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, shortTempDir(t), core))
	t.Cleanup(func() { _ = client.Close() })
	ctx := testCtx(t)

	req := kukeonv1.GitFetchRequest{Realm: "default", Address: "github.com/acme/app"}
	got, err := client.StageGitFetch(ctx, req)
	if err != nil || !got.Staged || got.ID != "0123456789abcdef" {
		t.Fatalf("StageGitFetch = %+v, %v", got, err)
	}
	if !core.stageOK || core.stageUID != os.Getuid() || core.req != req {
		t.Errorf("core saw uid %d (known %v), request %+v; want uid %d",
			core.stageUID, core.stageOK, core.req, os.Getuid())
	}
	if err = client.ReleaseGitFetch(ctx, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if !core.releaseOK || core.releaseUID != os.Getuid() || core.id != "0123456789abcdef" {
		t.Errorf("release saw uid %d (known %v), id %q", core.releaseUID, core.releaseOK, core.id)
	}
}

// TestGitFetchArgsCarryNoUID: the uid field never travels on the wire, so a
// client cannot name the owner of a staging.
func TestGitFetchArgsCarryNoUID(t *testing.T) {
	uid := 0
	for _, args := range []any{
		kukeonv1.StageGitFetchArgs{PeerUID: &uid}, kukeonv1.ReleaseGitFetchArgs{ID: "x", PeerUID: &uid},
	} {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "uid") {
			t.Errorf("%T encodes its uid: %s", args, raw)
		}
	}
	var decoded kukeonv1.StageGitFetchArgs
	//nolint:musttag // the field is tagged json:"-"; the test checks that a client cannot set it
	if err := json.Unmarshal([]byte(`{"PeerUID":0,"peerUID":0}`), &decoded); err != nil || decoded.PeerUID != nil {
		t.Errorf("a uid in the request was decoded: %v, %v", decoded.PeerUID, err)
	}
}
