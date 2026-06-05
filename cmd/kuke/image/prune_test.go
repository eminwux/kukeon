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
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestPruneCmd_DefaultRealmSuccess(t *testing.T) {
	var gotRealm string
	fake := &fakePruneClient{
		pruneImagesFn: func(realm string) (kukeonv1.PruneImagesResult, error) {
			gotRealm = realm
			return kukeonv1.PruneImagesResult{
				Realm:          realm,
				Namespace:      realm + ".kukeon.io",
				LeasesDeleted:  264,
				LeasesRetained: 2,
			}, nil
		},
	}

	out, err := runPrune(t, fake, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "default" {
		t.Errorf("realm = %q, want default", gotRealm)
	}
	if !strings.Contains(out, "released 264 lease(s), retained 2") {
		t.Errorf("output missing prune summary; got: %s", out)
	}
	if !strings.Contains(out, "default.kukeon.io") {
		t.Errorf("output missing namespace; got: %s", out)
	}
}

func TestPruneCmd_ExplicitRealm(t *testing.T) {
	var gotRealm string
	fake := &fakePruneClient{
		pruneImagesFn: func(realm string) (kukeonv1.PruneImagesResult, error) {
			gotRealm = realm
			return kukeonv1.PruneImagesResult{Realm: realm, Namespace: realm + ".kukeon.io"}, nil
		},
	}

	if _, err := runPrune(t, fake, []string{"--realm", "kuke-system"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotRealm != "kuke-system" {
		t.Errorf("realm = %q, want kuke-system", gotRealm)
	}
}

func TestPruneCmd_ClientErrorPropagates(t *testing.T) {
	fake := &fakePruneClient{
		pruneImagesFn: func(string) (kukeonv1.PruneImagesResult, error) {
			return kukeonv1.PruneImagesResult{}, errors.New("boom")
		},
	}
	if _, err := runPrune(t, fake, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected client error to propagate, got %v", err)
	}
}

func TestPruneCmd_RejectsPositionalArgs(t *testing.T) {
	fake := &fakePruneClient{
		pruneImagesFn: func(string) (kukeonv1.PruneImagesResult, error) {
			t.Fatal("client should not be invoked when positional args are rejected")
			return kukeonv1.PruneImagesResult{}, nil
		},
	}
	if _, err := runPrune(t, fake, []string{"unexpected"}); err == nil {
		t.Fatal("expected args error, got nil")
	}
}

// --- helpers ---

func runPrune(t *testing.T, fake *fakePruneClient, args []string) (string, error) {
	t.Helper()
	cmd := image.NewPruneCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, image.MockControllerKey{}, image.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

type fakePruneClient struct {
	pruneImagesFn func(realm string) (kukeonv1.PruneImagesResult, error)
}

func (f *fakePruneClient) Close() error { return nil }

func (f *fakePruneClient) LoadImage(context.Context, string, []byte) (kukeonv1.LoadImageResult, error) {
	return kukeonv1.LoadImageResult{}, errors.New("unexpected LoadImage call")
}

func (f *fakePruneClient) DeleteImage(context.Context, string, string) (kukeonv1.DeleteImageResult, error) {
	return kukeonv1.DeleteImageResult{}, errors.New("unexpected DeleteImage call")
}

func (f *fakePruneClient) PruneImages(_ context.Context, realm string) (kukeonv1.PruneImagesResult, error) {
	if f.pruneImagesFn == nil {
		return kukeonv1.PruneImagesResult{}, errors.New("unexpected PruneImages call")
	}
	return f.pruneImagesFn(realm)
}
