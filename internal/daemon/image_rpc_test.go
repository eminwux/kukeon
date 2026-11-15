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

package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// imageClientFake stubs the read-only image inventory pair (#1406). Every
// other Client method stays at the FakeClient default (ErrUnexpectedCall).
type imageClientFake struct {
	kukeonv1.FakeClient

	gotRealm string
	gotRef   string
	err      error
}

func (f *imageClientFake) ListImages(_ context.Context, realm string) (kukeonv1.ListImagesResult, error) {
	f.gotRealm = realm
	if f.err != nil {
		return kukeonv1.ListImagesResult{}, f.err
	}
	return kukeonv1.ListImagesResult{
		Realm: realm, Namespace: realm + ".kukeon.io",
		Images: []kukeonv1.ImageInfo{{Name: "docker.io/library/alpine:3.19", Size: -1, Digest: "sha256:abc"}},
	}, nil
}

func (f *imageClientFake) GetImage(_ context.Context, realm, ref string) (kukeonv1.GetImageResult, error) {
	f.gotRealm, f.gotRef = realm, ref
	if f.err != nil {
		return kukeonv1.GetImageResult{}, f.err
	}
	return kukeonv1.GetImageResult{
		Realm: realm, Namespace: realm + ".kukeon.io",
		Image: kukeonv1.ImageInfo{Name: ref, Size: 42, Digest: "sha256:abc"},
	}, nil
}

func TestListImages_RPCRoundTrip(t *testing.T) {
	core := &imageClientFake{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)
	reply := &kukeonv1.ListImagesReply{}
	if err := svc.ListImages(&kukeonv1.ListImagesArgs{Realm: "dev"}, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if reply.Err != nil || core.gotRealm != "dev" {
		t.Fatalf("err=%v realm=%q", kukeonv1.FromAPIError(reply.Err), core.gotRealm)
	}
	if reply.Result.Namespace != "dev.kukeon.io" || len(reply.Result.Images) != 1 ||
		reply.Result.Images[0].Size != -1 {
		t.Fatalf("result=%+v", reply.Result)
	}
}

func TestGetImage_RPCRoundTripAndSentinel(t *testing.T) {
	core := &imageClientFake{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)
	reply := &kukeonv1.GetImageReply{}
	args := &kukeonv1.GetImageArgs{Realm: "dev", Ref: "docker.io/library/alpine:3.19"}
	if err := svc.GetImage(args, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if reply.Err != nil || core.gotRef != args.Ref || reply.Result.Image.Size != 42 {
		t.Fatalf("err=%v ref=%q result=%+v", kukeonv1.FromAPIError(reply.Err), core.gotRef, reply.Result)
	}

	core.err = fmt.Errorf("%w: missing:1", errdefs.ErrImageNotFound)
	reply = &kukeonv1.GetImageReply{}
	if err := svc.GetImage(args, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if reply.Err == nil || !errors.Is(kukeonv1.FromAPIError(reply.Err), errdefs.ErrImageNotFound) {
		t.Fatalf("expected ErrImageNotFound on the wire, got %v", reply.Err)
	}
}
