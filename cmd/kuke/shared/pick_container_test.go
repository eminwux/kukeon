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

package shared_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type fakeListClient struct {
	kukeonv1.FakeClient

	specs []v1beta1.ContainerSpec
	err   error
}

func (f *fakeListClient) ListContainers(
	_ context.Context,
	_, _, _, _ string,
) ([]v1beta1.ContainerSpec, error) {
	return f.specs, f.err
}

// nonRootAttachable mirrors the `kuke attach` filter.
func nonRootAttachable(spec v1beta1.ContainerSpec) bool {
	return !spec.Root && spec.Attachable
}

// nonRoot mirrors the `kuke log` filter (Attachable accepted too).
func nonRoot(spec v1beta1.ContainerSpec) bool {
	return !spec.Root
}

func TestPickContainer_SingleAttachable(t *testing.T) {
	fc := &fakeListClient{specs: []v1beta1.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "shell", Attachable: true},
	}}
	got, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRootAttachable)
	if err != nil {
		t.Fatalf("PickContainer: %v", err)
	}
	if got != "shell" {
		t.Errorf("got %q, want %q", got, "shell")
	}
}

func TestPickContainer_NoCandidate_WrapsSentinel(t *testing.T) {
	fc := &fakeListClient{specs: []v1beta1.ContainerSpec{
		{ID: "root", Root: true},
	}}
	_, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRootAttachable)
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("err=%v, want wraps ErrAttachNoCandidate", err)
	}
	if !strings.Contains(err.Error(), `"c1"`) {
		t.Errorf("err=%q missing cell name", err)
	}
}

func TestPickContainer_Ambiguous_WrapsSentinelWithSortedList(t *testing.T) {
	fc := &fakeListClient{specs: []v1beta1.ContainerSpec{
		{ID: "shell", Attachable: true},
		{ID: "claude", Attachable: true},
	}}
	_, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRootAttachable)
	if !errors.Is(err, errdefs.ErrAttachAmbiguous) {
		t.Fatalf("err=%v, want wraps ErrAttachAmbiguous", err)
	}
	if got := err.Error(); !strings.Contains(got, "claude, shell") {
		t.Errorf("err=%q missing sorted candidate list", got)
	}
}

// TestPickContainer_AttachableFilterRejectsNonAttachable locks in the
// `kuke attach` filter shape: even though the cell has a single non-root
// container, it is non-Attachable and must surface as ErrAttachNoCandidate
// rather than being auto-picked.
func TestPickContainer_AttachableFilterRejectsNonAttachable(t *testing.T) {
	fc := &fakeListClient{specs: []v1beta1.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "daemon"},
	}}
	_, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRootAttachable)
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("err=%v, want ErrAttachNoCandidate (non-Attachable must be filtered out for attach)", err)
	}
}

// TestPickContainer_LogFilterAcceptsNonAttachable locks in the `kuke log`
// filter shape: a lone non-Attachable container is auto-picked because
// `kuke log` is meaningful for non-sbsh IO too (e.g. kukeond's cio.LogFile).
func TestPickContainer_LogFilterAcceptsNonAttachable(t *testing.T) {
	fc := &fakeListClient{specs: []v1beta1.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "daemon"},
	}}
	got, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRoot)
	if err != nil {
		t.Fatalf("PickContainer: %v", err)
	}
	if got != "daemon" {
		t.Errorf("got %q, want %q", got, "daemon")
	}
}

func TestPickContainer_ListContainersError_PropagatesUnwrapped(t *testing.T) {
	sentinel := errors.New("rpc dropped")
	fc := &fakeListClient{err: sentinel}
	_, err := kukeshared.PickContainer(context.Background(), fc, "r", "s", "st", "c1", nonRoot)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err=%v, want wraps sentinel %v", err, sentinel)
	}
}
