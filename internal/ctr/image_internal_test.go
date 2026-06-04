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

package ctr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
)

// TestDeleteImagePassesSynchronousDelete is the regression guard for #1037 —
// `kuke image delete <ref>` removed only the image's metadata tag and left
// exclusive layers pinned by GC roots the deleted image was their last
// owner for, so operators saw no disk freed. The fix is to pass
// images.SynchronousDelete() so the containerd image-service handler runs
// the GC scheduler's ScheduleAndWait sweep after the metadata removal,
// reclaiming content/snapshots no surviving root references.
//
// The shared-layers preservation guarantee (AC item 2 — layers referenced
// by another tagged image or live cell survive the sweep) is containerd's
// GC refcount, not something this layer re-implements; this unit test
// asserts only the contract dev controls (the SynchronousDelete opt
// reaching the image store). Cross-image refcount behavior is exercised
// by an e2e/integration test against a real containerd, which this repo
// defers to a separate infra issue (no `kuke image` e2e suite exists
// today).
func TestDeleteImagePassesSynchronousDelete(t *testing.T) {
	srv := newFakeServices()
	srv.addImage("docker.io/library/alpine:latest", "sha256:manifest-alpine")

	c := &client{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := c.deleteImage(
		context.Background(),
		"test-ns",
		"docker.io/library/alpine:latest",
		srv.ImageService(),
	)
	if err != nil {
		t.Fatalf("deleteImage: unexpected error: %v", err)
	}

	if got := len(srv.imageList); got != 0 {
		t.Errorf("image remained after delete: got %d, want 0", got)
	}
	if got := len(srv.imageDeleteSync); got != 1 {
		t.Fatalf("expected one image-store Delete call, got %d (call log %v)", got, srv.callLog)
	}
	if !srv.imageDeleteSync[0] {
		t.Errorf(
			"DeleteImage did not pass images.SynchronousDelete(); GC sweep would not run, exclusive layers stay pinned (#1037)",
		)
	}
}

// TestDeleteImageNotFoundMapsToSentinel asserts that containerd's NotFound
// error continues to map to the kukeon ErrImageNotFound sentinel so upper
// layers (controller, CLI) keep surfacing a clean "image not found" message
// after the SynchronousDelete refactor (#1037). Belongs in the internal
// test file because it exercises the unexported deleteImage helper.
func TestDeleteImageNotFoundMapsToSentinel(t *testing.T) {
	srv := newFakeServices()
	// No image added — fakeImageStore.Delete returns containerd's
	// errdefs.ErrNotFound when the name is absent.

	c := &client{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := c.deleteImage(
		context.Background(),
		"test-ns",
		"docker.io/library/missing:latest",
		srv.ImageService(),
	)
	if err == nil {
		t.Fatal("deleteImage: expected error for missing image, got nil")
	}
	if !errors.Is(err, internalerrdefs.ErrImageNotFound) {
		t.Errorf("deleteImage: expected ErrImageNotFound, got %v", err)
	}
	if errors.Is(err, internalerrdefs.ErrDeleteImage) {
		t.Errorf("deleteImage: NotFound mistakenly wrapped as ErrDeleteImage: %v", err)
	}
	// Sanity-check the precondition: the fake's underlying error really
	// does satisfy containerd's IsNotFound (catches a stale-fake regression).
	if !cerrdefs.IsNotFound(errImageNotFoundProbe()) {
		t.Fatal("test precondition: containerd errdefs.IsNotFound rejected the fake's sentinel")
	}
}

// errImageNotFoundProbe re-materializes the error fakeImageStore.Delete
// returns for an absent name. Kept in this file so a future change to the
// fake's error shape surfaces the mismatch here rather than as an opaque
// failure in TestDeleteImageNotFoundMapsToSentinel.
func errImageNotFoundProbe() error {
	srv := newFakeServices()
	return srv.ImageService().Delete(context.Background(), "absent")
}
