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

	imagecmd "github.com/eminwux/kukeon/cmd/kuke/get/image"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestImageCmd_ListCrossRealmDefault(t *testing.T) {
	fake := &fakeImageClient{
		listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
			return []v1beta1.RealmDoc{
				{Metadata: v1beta1.RealmMetadata{Name: "default"}},
				{Metadata: v1beta1.RealmMetadata{Name: "kuke-system"}},
			}, nil
		},
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			switch realm {
			case "default":
				return kukeonv1.ListImagesResult{
					Realm:     "default",
					Namespace: "default.kukeon.io",
					Images: []kukeonv1.ImageInfo{
						{
							Name:      "docker.io/library/alpine:3.20",
							Size:      7_500_000,
							CreatedAt: time.Now().Add(-2 * time.Hour),
						},
					},
				}, nil
			case "kuke-system":
				return kukeonv1.ListImagesResult{
					Realm:     "kuke-system",
					Namespace: "kuke-system.kukeon.io",
					Images: []kukeonv1.ImageInfo{
						{
							Name:      "docker.io/library/kukeon-local:dev",
							Size:      75_000_000,
							CreatedAt: time.Now().Add(-30 * time.Minute),
						},
					},
				}, nil
			}
			t.Fatalf("unexpected realm %q in cross-realm list", realm)
			return kukeonv1.ListImagesResult{}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantHeaders := []string{"NAME", "REALM", "SIZE", "AGE"}
	for _, h := range wantHeaders {
		if !strings.Contains(out, h) {
			t.Errorf("expected header %q in table output, got:\n%s", h, out)
		}
	}
	for _, want := range []string{
		"docker.io/library/alpine:3.20", "default",
		"docker.io/library/kukeon-local:dev", "kuke-system",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in table output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CREATED") || strings.Contains(out, "DIGEST") {
		t.Errorf("default table must not include CREATED/DIGEST columns, got:\n%s", out)
	}
}

func TestImageCmd_ListSingleRealmNarrowing(t *testing.T) {
	fake := &fakeImageClient{
		listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
			t.Fatal("ListRealms should not be called when --realm is supplied")
			return nil, nil
		},
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			if realm != "kuke-system" {
				t.Fatalf("expected realm %q, got %q", "kuke-system", realm)
			}
			return kukeonv1.ListImagesResult{
				Realm:     "kuke-system",
				Namespace: "kuke-system.kukeon.io",
				Images: []kukeonv1.ImageInfo{
					{
						Name:      "docker.io/library/kukeon-local:dev",
						Size:      1_024 * 1_024 * 10,
						CreatedAt: time.Now().Add(-time.Minute),
					},
				},
			}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{"--realm", "kuke-system"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "REALM") {
		t.Errorf("--realm narrowing must still emit the REALM column for grep-ability, got:\n%s", out)
	}
	if !strings.Contains(out, "kuke-system") {
		t.Errorf("expected %q in narrowed output, got:\n%s", "kuke-system", out)
	}
}

func TestImageCmd_DescribeSingleImageYAMLDefault(t *testing.T) {
	want := kukeonv1.ImageInfo{
		Name:      "docker.io/library/alpine:3.20",
		Size:      7_500_000,
		Digest:    "sha256:cafef00d",
		MediaType: "application/vnd.oci.image.manifest.v1+json",
	}
	fake := &fakeImageClient{
		getImageFn: func(realm, ref string) (kukeonv1.GetImageResult, error) {
			if realm != "default" {
				t.Fatalf("default describe realm: want %q, got %q", "default", realm)
			}
			if ref != want.Name {
				t.Fatalf("describe ref: want %q, got %q", want.Name, ref)
			}
			return kukeonv1.GetImageResult{
				Realm:     "default",
				Namespace: "default.kukeon.io",
				Image:     want,
			}, nil
		},
		listImagesFn: func(string) (kukeonv1.ListImagesResult, error) {
			t.Fatal("ListImages should not be called when a positional ref is supplied")
			return kukeonv1.ListImagesResult{}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{want.Name})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "name: "+want.Name) {
		t.Errorf("expected yaml describe to carry %q, got:\n%s", want.Name, out)
	}
	if !strings.Contains(out, "digest: "+want.Digest) {
		t.Errorf("expected yaml describe to carry digest, got:\n%s", out)
	}
}

func TestImageCmd_DescribeSingleImageJSON(t *testing.T) {
	want := kukeonv1.ImageInfo{Name: "docker.io/library/alpine:3.20", Size: 7_500_000}
	fake := &fakeImageClient{
		getImageFn: func(realm, _ string) (kukeonv1.GetImageResult, error) {
			return kukeonv1.GetImageResult{Realm: realm, Image: want}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{want.Name, "-o", "json"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "\"Name\":") || !strings.Contains(out, want.Name) {
		t.Errorf("expected json describe to carry Name field, got:\n%s", out)
	}
}

func TestImageCmd_EmptyResultMessage(t *testing.T) {
	fake := &fakeImageClient{
		listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
			return []v1beta1.RealmDoc{{Metadata: v1beta1.RealmMetadata{Name: "default"}}}, nil
		},
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			return kukeonv1.ListImagesResult{Realm: realm}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "No images found.") {
		t.Errorf("expected empty-result message, got:\n%s", out)
	}
}

func TestImageCmd_WideOutputAddsCreatedAndDigest(t *testing.T) {
	digest := "sha256:abcd1234"
	fake := &fakeImageClient{
		listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
			return []v1beta1.RealmDoc{{Metadata: v1beta1.RealmMetadata{Name: "default"}}}, nil
		},
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			return kukeonv1.ListImagesResult{
				Realm: realm,
				Images: []kukeonv1.ImageInfo{
					{
						Name:      "alpine:3.20",
						Size:      2_000_000,
						CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
						Digest:    digest,
					},
				},
			}, nil
		},
	}

	out, err := runImageGet(t, fake, []string{"-o", "wide"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"NAME", "REALM", "SIZE", "AGE", "CREATED", "DIGEST", "2026-01-02T03:04:05Z", digest} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in wide output, got:\n%s", want, out)
		}
	}
}

func TestImageCmd_DescribeNotFound(t *testing.T) {
	fake := &fakeImageClient{
		getImageFn: func(string, string) (kukeonv1.GetImageResult, error) {
			return kukeonv1.GetImageResult{}, errdefs.ErrImageNotFound
		},
	}

	_, err := runImageGet(t, fake, []string{"ghost:latest"})
	if !errors.Is(err, errdefs.ErrImageNotFound) {
		t.Errorf("expected ErrImageNotFound, got %v", err)
	}
}

func TestImageCmd_PerRealmListErrorIsFatal(t *testing.T) {
	fake := &fakeImageClient{
		listRealmsFn: func() ([]v1beta1.RealmDoc, error) {
			return []v1beta1.RealmDoc{
				{Metadata: v1beta1.RealmMetadata{Name: "default"}},
				{Metadata: v1beta1.RealmMetadata{Name: "kuke-system"}},
			}, nil
		},
		listImagesFn: func(realm string) (kukeonv1.ListImagesResult, error) {
			if realm == "kuke-system" {
				return kukeonv1.ListImagesResult{}, errors.New("containerd unreachable")
			}
			return kukeonv1.ListImagesResult{Realm: realm}, nil
		},
	}

	_, err := runImageGet(t, fake, []string{})
	if err == nil || !strings.Contains(err.Error(), "kuke-system") {
		t.Errorf("expected per-realm error surfaced with realm name, got %v", err)
	}
}

func runImageGet(t *testing.T, fake imagecmd.Client, args []string) (string, error) {
	t.Helper()
	cmd := imagecmd.NewImageCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, imagecmd.MockControllerKey{}, fake)
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

type fakeImageClient struct {
	listRealmsFn func() ([]v1beta1.RealmDoc, error)
	listImagesFn func(realm string) (kukeonv1.ListImagesResult, error)
	getImageFn   func(realm, ref string) (kukeonv1.GetImageResult, error)
}

func (f *fakeImageClient) Close() error { return nil }

func (f *fakeImageClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	if f.listRealmsFn == nil {
		return nil, errors.New("unexpected ListRealms call")
	}
	return f.listRealmsFn()
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
