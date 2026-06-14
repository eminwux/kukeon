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

package cell_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/cell"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestSynthesizeFromImage_DefaultsAttachableShell(t *testing.T) {
	doc, err := cell.SynthesizeFromImage("docker.io/library/alpine:3", "")
	if err != nil {
		t.Fatalf("SynthesizeFromImage: %v", err)
	}
	if doc.APIVersion != v1beta1.APIVersionV1Beta1 || doc.Kind != v1beta1.KindCell {
		t.Errorf("apiVersion/kind=%q/%q want %q/%q", doc.APIVersion, doc.Kind, v1beta1.APIVersionV1Beta1, v1beta1.KindCell)
	}
	if len(doc.Spec.Containers) != 1 {
		t.Fatalf("containers=%d want 1 (single user container; root synthesized by runner)", len(doc.Spec.Containers))
	}
	c := doc.Spec.Containers[0]
	if c.ID != cell.ImageContainerID {
		t.Errorf("container id=%q want %q", c.ID, cell.ImageContainerID)
	}
	if c.Image != "docker.io/library/alpine:3" {
		t.Errorf("image=%q want docker.io/library/alpine:3", c.Image)
	}
	if c.Command != cell.ImageDefaultCommand {
		t.Errorf("command=%q want default %q", c.Command, cell.ImageDefaultCommand)
	}
	if !c.Attachable {
		t.Error("synthesized container must be attachable so the default attach mode has a target")
	}
	// Name + scope are left for the caller to finalize.
	if doc.Metadata.Name != "" || doc.Spec.ID != "" {
		t.Errorf("synthesis must not name the cell; got name=%q id=%q", doc.Metadata.Name, doc.Spec.ID)
	}
}

func TestSynthesizeFromImage_CommandOverride(t *testing.T) {
	doc, err := cell.SynthesizeFromImage("nginx", "/usr/sbin/nginx")
	if err != nil {
		t.Fatalf("SynthesizeFromImage: %v", err)
	}
	if got := doc.Spec.Containers[0].Command; got != "/usr/sbin/nginx" {
		t.Errorf("command=%q want /usr/sbin/nginx (override)", got)
	}
}

func TestSynthesizeFromImage_EmptyImage_Errors(t *testing.T) {
	_, err := cell.SynthesizeFromImage("   ", "")
	if !errors.Is(err, errdefs.ErrImageRequired) {
		t.Fatalf("err=%v want ErrImageRequired", err)
	}
}

func TestImageNamePrefix(t *testing.T) {
	for _, tc := range []struct {
		image string
		want  string
	}{
		{"docker.io/library/alpine:3", "alpine"},
		{"nginx", "nginx"},
		{"nginx:1.27", "nginx"},
		{"localhost:5000/myapp:dev", "myapp"},
		{"ghcr.io/foo/bar@sha256:deadbeef", "bar"},
		{"registry.example.com/team/My_App:latest", "my-app"},
		{"/////", cell.ImageNameFallbackPrefix},
		{"", cell.ImageNameFallbackPrefix},
	} {
		t.Run(tc.image, func(t *testing.T) {
			if got := cell.ImageNamePrefix(tc.image); got != tc.want {
				t.Errorf("ImageNamePrefix(%q)=%q want %q", tc.image, got, tc.want)
			}
		})
	}
}
