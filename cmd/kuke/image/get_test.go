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

package image_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	image "github.com/eminwux/kukeon/cmd/kuke/image"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestGetCmd_ListDefaultRealmTable(t *testing.T) {
	created := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	var gotRealm string
	fake := &fakeImageClient{
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			gotRealm = realm
			return kukeonv1.ListImagesResult{
				Realm:     realm,
				Namespace: realm + ".kukeon.io",
				Images: []kukeonv1.ImageInfo{
					{Name: "docker.io/library/alpine:3.20", Size: 4_500_000, CreatedAt: created},
					{Name: "registry.eminwux.com/kukeon-local:dev", Size: -1},
				},
			}, nil
		},
	}

	out, err := runGet(t, fake, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "default" {
		t.Errorf("realm = %q, want default", gotRealm)
	}
	if !strings.Contains(out, "docker.io/library/alpine:3.20") {
		t.Errorf("table missing first image; got: %s", out)
	}
	if !strings.Contains(out, "4.3 MiB") {
		t.Errorf("table missing humanized size; got: %s", out)
	}
	if !strings.Contains(out, "2026-04-29T12:00:00Z") {
		t.Errorf("table missing created timestamp; got: %s", out)
	}
	// Image with Size=-1 should render as "-" rather than "0 B".
	if strings.Contains(out, "0 B") {
		t.Errorf("missing-size image rendered as 0 B; got: %s", out)
	}
	if !strings.Contains(out, "-\n") && !strings.Contains(out, "- ") {
		t.Errorf("missing-size image did not render as '-'; got: %s", out)
	}
}

func TestGetCmd_ListEmptyRealm(t *testing.T) {
	fake := &fakeImageClient{
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			return kukeonv1.ListImagesResult{Realm: realm, Namespace: realm + ".kukeon.io"}, nil
		},
	}
	out, err := runGet(t, fake, []string{"--realm", "kuke-system"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `No images found in realm "kuke-system"`) {
		t.Errorf("missing empty-realm message; got: %s", out)
	}
}

func TestGetCmd_DescribeSingleImageRoutesToGetImage(t *testing.T) {
	var gotRealm, gotRef string
	fake := &fakeImageClient{
		getImageFn: func(realm, ref string) (kukeonv1.GetImageResult, error) {
			gotRealm = realm
			gotRef = ref
			return kukeonv1.GetImageResult{
				Realm:     realm,
				Namespace: realm + ".kukeon.io",
				Image:     kukeonv1.ImageInfo{Name: ref, Size: 4_500_000, Digest: "sha256:abcd"},
			}, nil
		},
		listImagesFn: func(string) (kukeonv1.ListImagesResult, error) {
			t.Fatal("ListImages should not be called when a positional ref is supplied")
			return kukeonv1.ListImagesResult{}, nil
		},
	}

	if _, err := runGet(t, fake, []string{"docker.io/library/alpine:3.20"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "default" {
		t.Errorf("realm = %q, want default", gotRealm)
	}
	if gotRef != "docker.io/library/alpine:3.20" {
		t.Errorf("ref = %q, want docker.io/library/alpine:3.20", gotRef)
	}
}

func TestGetCmd_NotFoundIsFriendly(t *testing.T) {
	fake := &fakeImageClient{
		getImageFn: func(string, string) (kukeonv1.GetImageResult, error) {
			return kukeonv1.GetImageResult{}, errdefs.ErrImageNotFound
		},
	}

	_, err := runGet(t, fake, []string{"docker.io/library/missing:1"})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !errors.Is(err, errdefs.ErrImageNotFound) {
		t.Errorf("error not wrapping ErrImageNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), `image "docker.io/library/missing:1" not found in realm "default"`) {
		t.Errorf("error = %q, want friendly not-found message", err.Error())
	}
}

func TestGetCmd_ClientErrorPropagates(t *testing.T) {
	fake := &fakeImageClient{
		listImagesFn: func(string) (kukeonv1.ListImagesResult, error) {
			return kukeonv1.ListImagesResult{}, errors.New("boom")
		},
	}
	if _, err := runGet(t, fake, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected client error to propagate, got %v", err)
	}
}

// --- helpers ---

func runGet(t *testing.T, fake *fakeImageClient, args []string) (string, error) {
	t.Helper()
	cmd := image.NewGetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, image.MockControllerKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

type fakeImageClient struct {
	kukeonv1.FakeClient

	listImagesFn func(realm string) (kukeonv1.ListImagesResult, error)
	getImageFn   func(realm, ref string) (kukeonv1.GetImageResult, error)
}

func (f *fakeImageClient) ListImages(_ context.Context, realm string) (kukeonv1.ListImagesResult, error) {
	if f.listImagesFn == nil {
		return kukeonv1.ListImagesResult{}, errors.New("unexpected ListImages call")
	}
	return f.listImagesFn(realm)
}

func (f *fakeImageClient) GetImage(_ context.Context, realm, ref string) (kukeonv1.GetImageResult, error) {
	if f.getImageFn == nil {
		return kukeonv1.GetImageResult{}, errors.New("unexpected GetImage call")
	}
	return f.getImageFn(realm, ref)
}
