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

package version_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/version"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		wantErr string
	}{
		{in: "v1beta1"},
		{in: "", wantErr: "apiVersion is required (expected v1beta1)"},
		{in: "v1", wantErr: `unsupported apiVersion "v1" (expected v1beta1)`},
		{in: "v1alpha1", wantErr: `unsupported apiVersion "v1alpha1"`},
		{in: "kuketeams.io/v1", wantErr: `unsupported apiVersion "kuketeams.io/v1"`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			err := version.Check(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want nil", tc.in, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Check(%q) = %v, want error containing %q", tc.in, err, tc.wantErr)
			}
		})
	}
}
