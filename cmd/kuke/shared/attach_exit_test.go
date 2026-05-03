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
	"errors"
	"fmt"
	"testing"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	sbshattach "github.com/eminwux/sbsh/pkg/attach"
)

func TestClassifyAttachExit(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want kukeshared.AttachExit
	}{
		{
			name: "nil maps to detached",
			in:   nil,
			want: kukeshared.AttachExitDetached,
		},
		{
			name: "ErrDetached is detached",
			in:   sbshattach.ErrDetached,
			want: kukeshared.AttachExitDetached,
		},
		{
			name: "wrapped ErrDetached is detached",
			in:   fmt.Errorf("attach loop exited: %w", sbshattach.ErrDetached),
			want: kukeshared.AttachExitDetached,
		},
		{
			name: "ErrPeerClosed is peer-closed",
			in:   sbshattach.ErrPeerClosed,
			want: kukeshared.AttachExitPeerClosed,
		},
		{
			name: "wrapped ErrPeerClosed is peer-closed",
			in:   fmt.Errorf("attach loop exited: %w", sbshattach.ErrPeerClosed),
			want: kukeshared.AttachExitPeerClosed,
		},
		{
			name: "unrelated error is AttachExitError",
			in:   errors.New("control socket lost"),
			want: kukeshared.AttachExitError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kukeshared.ClassifyAttachExit(tc.in); got != tc.want {
				t.Errorf("ClassifyAttachExit(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsCleanAttachExit(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want bool
	}{
		{name: "nil is clean", in: nil, want: true},
		{name: "ErrDetached is clean", in: sbshattach.ErrDetached, want: true},
		{name: "ErrPeerClosed is clean", in: sbshattach.ErrPeerClosed, want: true},
		{
			name: "wrapped ErrDetached is clean",
			in:   fmt.Errorf("attach loop exited: %w", sbshattach.ErrDetached),
			want: true,
		},
		{
			name: "wrapped ErrPeerClosed is clean",
			in:   fmt.Errorf("attach loop exited: %w", sbshattach.ErrPeerClosed),
			want: true,
		},
		{
			name: "unrelated error is not clean",
			in:   errors.New("dial unix: connection refused"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kukeshared.IsCleanAttachExit(tc.in); got != tc.want {
				t.Errorf("IsCleanAttachExit(%v) = %t, want %t", tc.in, got, tc.want)
			}
		})
	}
}
