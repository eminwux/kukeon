//go:build !integration

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

package controller_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestListImages_Success(t *testing.T) {
	wantNS := consts.RealmNamespace("default")
	created := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

	var gotNS string
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", wantNS), nil
		},
		ListImagesFn: func(ns string) ([]ctr.ImageInfo, error) {
			gotNS = ns
			return []ctr.ImageInfo{
				{Name: "docker.io/library/alpine:3.20", Size: 4_500_000, CreatedAt: created, Digest: "sha256:abc"},
			}, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.ListImages("default")
	if err != nil {
		t.Fatalf("ListImages returned error: %v", err)
	}
	if gotNS != wantNS {
		t.Errorf("runner saw namespace %q, want %q", gotNS, wantNS)
	}
	if res.Realm != "default" {
		t.Errorf("Realm = %q, want default", res.Realm)
	}
	if len(res.Images) != 1 || res.Images[0].Name != "docker.io/library/alpine:3.20" {
		t.Errorf("unexpected images: %+v", res.Images)
	}
	if res.Images[0].CreatedAt != created {
		t.Errorf("CreatedAt = %v, want %v", res.Images[0].CreatedAt, created)
	}
}

func TestListImages_RealmNotFound(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
	}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.ListImages("ghost")
	if !errors.Is(err, errdefs.ErrRealmNotFound) {
		t.Fatalf("expected ErrRealmNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing realm; got %q", err.Error())
	}
}

func TestListImages_EmptyRealm(t *testing.T) {
	mock := &fakeRunner{}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.ListImages("   ")
	if !errors.Is(err, errdefs.ErrRealmNameRequired) {
		t.Fatalf("expected ErrRealmNameRequired, got %v", err)
	}
}

func TestListImages_RunnerErrorIsWrapped(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", ""), nil
		},
		ListImagesFn: func(string) ([]ctr.ImageInfo, error) {
			return nil, errors.New("containerd unreachable")
		},
	}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.ListImages("default")
	if !errors.Is(err, errdefs.ErrListImages) {
		t.Fatalf("expected ErrListImages wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "containerd unreachable") {
		t.Errorf("inner error should be preserved; got %q", err.Error())
	}
}

func TestGetImage_Success(t *testing.T) {
	wantNS := consts.RealmNamespace("default")

	var gotNS, gotRef string
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", wantNS), nil
		},
		GetImageFn: func(ns, ref string) (ctr.ImageInfo, error) {
			gotNS = ns
			gotRef = ref
			return ctr.ImageInfo{Name: ref, Size: 1234, Digest: "sha256:abc"}, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.GetImage("default", "docker.io/library/alpine:3.20")
	if err != nil {
		t.Fatalf("GetImage returned error: %v", err)
	}
	if gotNS != wantNS {
		t.Errorf("runner saw namespace %q, want %q", gotNS, wantNS)
	}
	if gotRef != "docker.io/library/alpine:3.20" {
		t.Errorf("runner saw ref %q, want docker.io/library/alpine:3.20", gotRef)
	}
	if res.Image.Name != "docker.io/library/alpine:3.20" {
		t.Errorf("Image.Name = %q, want docker.io/library/alpine:3.20", res.Image.Name)
	}
}

func TestGetImage_NotFoundIsPassedThrough(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", ""), nil
		},
		GetImageFn: func(string, string) (ctr.ImageInfo, error) {
			return ctr.ImageInfo{}, errdefs.ErrImageNotFound
		},
	}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.GetImage("default", "docker.io/library/missing:1")
	if !errors.Is(err, errdefs.ErrImageNotFound) {
		t.Fatalf("expected ErrImageNotFound, got %v", err)
	}
}

func TestGetImage_RealmNotFound(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
	}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.GetImage("ghost", "alpine")
	if !errors.Is(err, errdefs.ErrRealmNotFound) {
		t.Fatalf("expected ErrRealmNotFound, got %v", err)
	}
}

func TestGetImage_EmptyRealm(t *testing.T) {
	mock := &fakeRunner{}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.GetImage("   ", "alpine")
	if !errors.Is(err, errdefs.ErrRealmNameRequired) {
		t.Fatalf("expected ErrRealmNameRequired, got %v", err)
	}
}

func TestGetImage_EmptyRefIsNotFound(t *testing.T) {
	mock := &fakeRunner{}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.GetImage("default", "  ")
	if !errors.Is(err, errdefs.ErrImageNotFound) {
		t.Fatalf("expected ErrImageNotFound, got %v", err)
	}
}

func TestGetImage_RunnerErrorIsWrapped(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", ""), nil
		},
		GetImageFn: func(string, string) (ctr.ImageInfo, error) {
			return ctr.ImageInfo{}, errors.New("containerd unreachable")
		},
	}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.GetImage("default", "alpine")
	if !errors.Is(err, errdefs.ErrGetImage) {
		t.Fatalf("expected ErrGetImage wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "containerd unreachable") {
		t.Errorf("inner error should be preserved; got %q", err.Error())
	}
}
