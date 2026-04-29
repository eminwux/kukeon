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

	image "github.com/eminwux/kukeon/cmd/kuke/image"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestDeleteCmd_DefaultRealmSuccess(t *testing.T) {
	var gotRealm, gotRef string
	fake := &fakeDeleteClient{
		deleteImageFn: func(realm, ref string) (kukeonv1.DeleteImageResult, error) {
			gotRealm = realm
			gotRef = ref
			return kukeonv1.DeleteImageResult{
				Realm:     realm,
				Namespace: realm + ".kukeon.io",
				Ref:       ref,
			}, nil
		},
	}

	out, err := runDelete(t, fake, []string{"docker.io/library/alpine:3.20"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "default" {
		t.Errorf("realm = %q, want default", gotRealm)
	}
	if gotRef != "docker.io/library/alpine:3.20" {
		t.Errorf("ref = %q, want docker.io/library/alpine:3.20", gotRef)
	}
	if !strings.Contains(out, "deleted image") || !strings.Contains(out, "docker.io/library/alpine:3.20") {
		t.Errorf("output missing confirmation; got: %s", out)
	}
	if !strings.Contains(out, "default.kukeon.io") {
		t.Errorf("output missing namespace; got: %s", out)
	}
}

func TestDeleteCmd_ExplicitRealm(t *testing.T) {
	var gotRealm string
	fake := &fakeDeleteClient{
		deleteImageFn: func(realm, ref string) (kukeonv1.DeleteImageResult, error) {
			gotRealm = realm
			return kukeonv1.DeleteImageResult{Realm: realm, Namespace: realm + ".kukeon.io", Ref: ref}, nil
		},
	}

	if _, err := runDelete(t, fake, []string{"foo", "--realm", "kuke-system"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "kuke-system" {
		t.Errorf("realm = %q, want kuke-system", gotRealm)
	}
}

func TestDeleteCmd_NotFoundIsFriendly(t *testing.T) {
	fake := &fakeDeleteClient{
		deleteImageFn: func(string, string) (kukeonv1.DeleteImageResult, error) {
			return kukeonv1.DeleteImageResult{}, errdefs.ErrImageNotFound
		},
	}

	_, err := runDelete(t, fake, []string{"docker.io/library/missing:1"})
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

func TestDeleteCmd_ClientErrorPropagates(t *testing.T) {
	fake := &fakeDeleteClient{
		deleteImageFn: func(string, string) (kukeonv1.DeleteImageResult, error) {
			return kukeonv1.DeleteImageResult{}, errors.New("boom")
		},
	}
	if _, err := runDelete(t, fake, []string{"alpine"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected client error to propagate, got %v", err)
	}
}

func TestDeleteCmd_RequiresRef(t *testing.T) {
	fake := &fakeDeleteClient{
		deleteImageFn: func(string, string) (kukeonv1.DeleteImageResult, error) {
			t.Fatal("client should not be invoked without a positional ref")
			return kukeonv1.DeleteImageResult{}, nil
		},
	}
	_, err := runDelete(t, fake, nil)
	if err == nil {
		t.Fatal("expected missing-arg error, got nil")
	}
}

// --- helpers ---

func runDelete(t *testing.T, fake *fakeDeleteClient, args []string) (string, error) {
	t.Helper()
	cmd := image.NewDeleteCmd()
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

type fakeDeleteClient struct {
	kukeonv1.FakeClient

	deleteImageFn func(realm, ref string) (kukeonv1.DeleteImageResult, error)
}

func (f *fakeDeleteClient) DeleteImage(_ context.Context, realm, ref string) (kukeonv1.DeleteImageResult, error) {
	if f.deleteImageFn == nil {
		return kukeonv1.DeleteImageResult{}, errors.New("unexpected DeleteImage call")
	}
	return f.deleteImageFn(realm, ref)
}
