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
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestLoadImage_Success(t *testing.T) {
	tarball := []byte("fake-tarball-bytes")
	wantNS := consts.RealmNamespace("kuke-system")

	var gotNS string
	var gotBytes []byte
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("kuke-system", wantNS), nil
		},
		LoadImageFn: func(ns string, r io.Reader) ([]string, error) {
			gotNS = ns
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			gotBytes = b
			return []string{"docker.io/library/kukeon-local:dev"}, nil
		},
	}

	ctrl := setupTestController(t, mock)
	res, err := ctrl.LoadImage("kuke-system", bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("LoadImage returned error: %v", err)
	}

	if res.Realm != "kuke-system" {
		t.Errorf("Realm = %q, want %q", res.Realm, "kuke-system")
	}
	if res.Namespace != wantNS {
		t.Errorf("Namespace = %q, want %q", res.Namespace, wantNS)
	}
	if len(res.Images) != 1 || res.Images[0] != "docker.io/library/kukeon-local:dev" {
		t.Errorf("Images = %v, want [docker.io/library/kukeon-local:dev]", res.Images)
	}
	if gotNS != wantNS {
		t.Errorf("runner received namespace %q, want %q", gotNS, wantNS)
	}
	if !bytes.Equal(gotBytes, tarball) {
		t.Errorf("runner received %d bytes, want %d", len(gotBytes), len(tarball))
	}
}

func TestLoadImage_DefaultRealmMappedToDefaultNamespace(t *testing.T) {
	wantNS := consts.RealmNamespace(consts.KukeonDefaultRealmName)

	var gotNS string
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm(consts.KukeonDefaultRealmName, wantNS), nil
		},
		LoadImageFn: func(ns string, _ io.Reader) ([]string, error) {
			gotNS = ns
			return []string{"foo"}, nil
		},
	}

	ctrl := setupTestController(t, mock)
	if _, err := ctrl.LoadImage(consts.KukeonDefaultRealmName, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("LoadImage returned error: %v", err)
	}
	if gotNS != wantNS {
		t.Errorf("runner saw namespace %q, want %q", gotNS, wantNS)
	}
}

func TestLoadImage_RealmNotFound(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return intmodel.Realm{}, errdefs.ErrRealmNotFound
		},
	}

	ctrl := setupTestController(t, mock)
	_, err := ctrl.LoadImage("ghost", bytes.NewReader([]byte("x")))
	if !errors.Is(err, errdefs.ErrRealmNotFound) {
		t.Fatalf("expected ErrRealmNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error message should name the missing realm; got %q", err.Error())
	}
}

func TestLoadImage_EmptyRealm(t *testing.T) {
	mock := &fakeRunner{}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.LoadImage("   ", bytes.NewReader([]byte("x")))
	if !errors.Is(err, errdefs.ErrRealmNameRequired) {
		t.Fatalf("expected ErrRealmNameRequired, got %v", err)
	}
}

func TestLoadImage_NilReader(t *testing.T) {
	mock := &fakeRunner{}
	ctrl := setupTestController(t, mock)
	_, err := ctrl.LoadImage("default", nil)
	if !errors.Is(err, errdefs.ErrTarballRequired) {
		t.Fatalf("expected ErrTarballRequired, got %v", err)
	}
}

func TestLoadImage_RunnerErrorIsWrapped(t *testing.T) {
	mock := &fakeRunner{
		GetRealmFn: func(_ intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("default", ""), nil
		},
		LoadImageFn: func(_ string, _ io.Reader) ([]string, error) {
			return nil, errors.New("containerd unreachable")
		},
	}

	ctrl := setupTestController(t, mock)
	_, err := ctrl.LoadImage("default", bytes.NewReader([]byte("x")))
	if !errors.Is(err, errdefs.ErrLoadImage) {
		t.Fatalf("expected ErrLoadImage wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "containerd unreachable") {
		t.Errorf("inner error should be preserved; got %q", err.Error())
	}
}
