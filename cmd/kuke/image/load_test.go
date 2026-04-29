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
	"os"
	"path/filepath"
	"strings"
	"testing"

	image "github.com/eminwux/kukeon/cmd/kuke/image"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestLoadCmd_FromFileDefaultRealm(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "img.tar")
	want := []byte("oci-tarball-bytes")
	if err := os.WriteFile(tarPath, want, 0o600); err != nil {
		t.Fatalf("write tarball: %v", err)
	}

	var gotRealm string
	var gotBytes []byte
	fake := &fakeClient{
		loadImageFn: func(realm string, tarball []byte) (kukeonv1.LoadImageResult, error) {
			gotRealm = realm
			gotBytes = tarball
			return kukeonv1.LoadImageResult{
				Realm:     realm,
				Namespace: realm + ".kukeon.io",
				Images:    []string{"docker.io/library/foo:bar"},
			}, nil
		},
	}

	out, err := runLoad(t, fake, []string{tarPath})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "default" {
		t.Errorf("realm = %q, want default", gotRealm)
	}
	if !bytes.Equal(gotBytes, want) {
		t.Errorf("client received %d bytes, want %d", len(gotBytes), len(want))
	}
	if !strings.Contains(out, "docker.io/library/foo:bar") {
		t.Errorf("output missing image name; got: %s", out)
	}
}

func TestLoadCmd_FromStdinExplicitRealm(t *testing.T) {
	want := []byte("stdin-tarball")

	var gotRealm string
	var gotBytes []byte
	fake := &fakeClient{
		loadImageFn: func(realm string, tarball []byte) (kukeonv1.LoadImageResult, error) {
			gotRealm = realm
			gotBytes = tarball
			return kukeonv1.LoadImageResult{Realm: realm, Namespace: "kuke-system.kukeon.io"}, nil
		},
	}

	if _, err := runLoadWithStdin(t, fake, []string{"-", "--realm", "kuke-system"}, bytes.NewReader(want)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "kuke-system" {
		t.Errorf("realm = %q, want kuke-system", gotRealm)
	}
	if !bytes.Equal(gotBytes, want) {
		t.Errorf("client received %d bytes, want %d", len(gotBytes), len(want))
	}
}

func TestLoadCmd_FromDockerAndPositionalAreMutuallyExclusive(t *testing.T) {
	fake := &fakeClient{
		loadImageFn: func(string, []byte) (kukeonv1.LoadImageResult, error) {
			t.Fatal("client should not be invoked")
			return kukeonv1.LoadImageResult{}, nil
		},
	}

	_, err := runLoad(t, fake, []string{"foo.tar", "--from-docker", "kukeon-local:dev"})
	if err == nil {
		t.Fatal("expected mutually-exclusive error, got nil")
	}
	if !strings.Contains(err.Error(), "either a tarball path or --from-docker") {
		t.Errorf("error = %q, want mutually-exclusive message", err.Error())
	}
}

func TestLoadCmd_NoSourceIsAnError(t *testing.T) {
	fake := &fakeClient{}
	_, err := runLoad(t, fake, nil)
	if err == nil {
		t.Fatal("expected missing-source error, got nil")
	}
	if !strings.Contains(err.Error(), "is required") {
		t.Errorf("error = %q, want missing-source message", err.Error())
	}
}

func TestLoadCmd_ClientErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "img.tar")
	if err := os.WriteFile(tarPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write tarball: %v", err)
	}

	fake := &fakeClient{
		loadImageFn: func(string, []byte) (kukeonv1.LoadImageResult, error) {
			return kukeonv1.LoadImageResult{}, errors.New("boom")
		},
	}

	if _, err := runLoad(t, fake, []string{tarPath}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected client error to propagate, got %v", err)
	}
}

// --- helpers ---

func runLoad(t *testing.T, fake *fakeClient, args []string) (string, error) {
	t.Helper()
	return runLoadWithStdin(t, fake, args, nil)
}

func runLoadWithStdin(t *testing.T, fake *fakeClient, args []string, stdin io.Reader) (string, error) {
	t.Helper()
	cmd := image.NewLoadCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	if stdin != nil {
		cmd.SetIn(stdin)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, image.MockControllerKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

type fakeClient struct {
	kukeonv1.FakeClient

	loadImageFn func(realm string, tarball []byte) (kukeonv1.LoadImageResult, error)
}

func (f *fakeClient) LoadImage(_ context.Context, realm string, tarball []byte) (kukeonv1.LoadImageResult, error) {
	if f.loadImageFn == nil {
		return kukeonv1.LoadImageResult{}, errors.New("unexpected LoadImage call")
	}
	return f.loadImageFn(realm, tarball)
}
