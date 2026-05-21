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

package apischeme_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func gitContainerDoc(git *ext.ContainerGit) ext.ContainerDoc {
	return ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "c"},
		Spec: ext.ContainerSpec{
			ID:      "c",
			RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
			Image: "alpine:latest",
			Git:   git,
		},
	}
}

// TestContainerGitRoundTripV1Beta1 covers that a populated git block survives
// ConvertContainerDocToInternal + BuildContainerExternalFromInternal with no
// fields dropped. Issue #618.
func TestContainerGitRoundTripV1Beta1(t *testing.T) {
	input := gitContainerDoc(&ext.ContainerGit{
		Author:         &ext.GitIdentity{Name: "Dev", Email: "dev@eminwux.com"},
		Committer:      &ext.GitIdentity{Name: "Dev", Email: "dev@eminwux.com"},
		SigningKey:     "/home/claude/.ssh/id_ed25519.pub",
		Sign:           []string{ext.GitSignCommits, ext.GitSignTags},
		AllowedSigners: "/home/claude/.ssh/allowed_signers",
	})

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if internal.Spec.Git == nil {
		t.Fatalf("internal.Spec.Git = nil, want populated")
	}
	if internal.Spec.Git.Author == nil || internal.Spec.Git.Author.Email != "dev@eminwux.com" {
		t.Errorf("internal author = %+v, want dev@eminwux.com", internal.Spec.Git.Author)
	}
	if internal.Spec.Git.SigningKey != input.Spec.Git.SigningKey {
		t.Errorf("internal signingKey = %q, want %q", internal.Spec.Git.SigningKey, input.Spec.Git.SigningKey)
	}
	if len(internal.Spec.Git.Sign) != 2 {
		t.Errorf("internal sign = %v, want 2 entries", internal.Spec.Git.Sign)
	}
	if internal.Spec.Git.AllowedSigners != input.Spec.Git.AllowedSigners {
		t.Errorf("internal allowedSigners = %q, want %q", internal.Spec.Git.AllowedSigners, input.Spec.Git.AllowedSigners)
	}

	out, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if out.Spec.Git == nil {
		t.Fatalf("round-trip dropped git block: %+v", out.Spec)
	}
	if out.Spec.Git.Committer == nil || out.Spec.Git.Committer.Name != "Dev" {
		t.Errorf("round-trip committer = %+v, want Dev", out.Spec.Git.Committer)
	}
	if out.Spec.Git.AllowedSigners != input.Spec.Git.AllowedSigners {
		t.Errorf("round-trip allowedSigners = %q, want %q", out.Spec.Git.AllowedSigners, input.Spec.Git.AllowedSigners)
	}
}

// TestContainerGitDeepCopy asserts gitToInternal deep-copies the Author
// pointer and Sign slice, so mutating the source after conversion can't bleed
// into the internal model.
func TestContainerGitDeepCopy(t *testing.T) {
	src := &ext.ContainerGit{
		Author:     &ext.GitIdentity{Name: "Dev", Email: "dev@eminwux.com"},
		SigningKey: "/k.pub",
		Sign:       []string{ext.GitSignCommits},
	}
	internal, _, err := apischeme.NormalizeContainer(gitContainerDoc(src))
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	src.Author.Email = "mutated@example.com"
	src.Sign[0] = "mutated"
	if internal.Spec.Git.Author.Email != "dev@eminwux.com" {
		t.Errorf("internal author email mutated through shared pointer: %q", internal.Spec.Git.Author.Email)
	}
	if internal.Spec.Git.Sign[0] != ext.GitSignCommits {
		t.Errorf("internal sign mutated through shared slice: %q", internal.Spec.Git.Sign[0])
	}
}

func TestContainerGitValidation(t *testing.T) {
	tests := []struct {
		name    string
		git     *ext.ContainerGit
		wantErr string // substring; "" means expect success
	}{
		{
			name: "full block valid",
			git: &ext.ContainerGit{
				Author:         &ext.GitIdentity{Name: "Dev", Email: "dev@eminwux.com"},
				Committer:      &ext.GitIdentity{Name: "Dev", Email: "dev@eminwux.com"},
				SigningKey:     "/k.pub",
				Sign:           []string{ext.GitSignCommits, ext.GitSignTags},
				AllowedSigners: "/allowed",
			},
		},
		{name: "nil git valid", git: nil},
		{
			name:    "author missing email",
			git:     &ext.ContainerGit{Author: &ext.GitIdentity{Name: "Dev"}},
			wantErr: "git.author requires both name and email",
		},
		{
			name:    "committer missing name",
			git:     &ext.ContainerGit{Committer: &ext.GitIdentity{Email: "dev@eminwux.com"}},
			wantErr: "git.committer requires both name and email",
		},
		{
			name:    "sign without signingKey",
			git:     &ext.ContainerGit{Sign: []string{ext.GitSignCommits}},
			wantErr: "git.sign requires git.signingKey",
		},
		{
			name:    "unknown sign target",
			git:     &ext.ContainerGit{SigningKey: "/k.pub", Sign: []string{"branches"}},
			wantErr: "must be one of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := apischeme.ConvertContainerDocToInternal(gitContainerDoc(tt.git))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
