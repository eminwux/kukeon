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

package run_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

// TestRun_ModeRequiresConfigSource: --mode picks one of the modes a CellConfig
// enables, so `kuke run` rejects it on every source that has no Config rather
// than silently dropping it (#1472, step 11).
func TestRun_ModeRequiresConfigSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "--image",
			args: []string{"--image", "docker.io/library/alpine:3", "--mode", "headless", "-d"},
			want: "--mode is only valid with --from-config or --clone",
		},
		{
			name: "-f",
			args: []string{"-f", writeTempYAML(t, validCellYAML), "--mode", "headless", "-d"},
			want: "--mode is only valid with --from-config or --clone",
		},
		{
			name: "<cell> positional",
			args: []string{"existing", "--mode", "headless", "-d"},
			want: "--mode is only valid with --from-config or --clone",
		},
		{
			name: "--from-blueprint",
			args: []string{"--from-blueprint", "web", "--mode", "headless", "-d"},
			want: "--mode is only valid with --from-config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			lastConfigRequest = kukeonv1.CellFromConfigRequest{}
			fc := &fakeClient{}
			cmd, _ := newCmd(t, fc)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if fc.createCalls != 0 || lastConfigRequest.Config.Name != "" {
				t.Errorf("a cell was created despite the rejection (creates=%d, render=%+v)",
					fc.createCalls, lastConfigRequest)
			}
		})
	}
}
