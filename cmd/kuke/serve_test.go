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

package kuke

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

type serveOutput chan string

func (w serveOutput) Write(p []byte) (int, error) { w <- string(p); return len(p), nil }

func TestServeConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, env, flag, want string }{
		{name: "file", want: "unix:///tmp/from-file.sock"},
		{name: "env", env: "unix:///tmp/from-env.sock", want: "unix:///tmp/from-env.sock"},
		{name: "flag", env: "unix:///tmp/from-env.sock", flag: "unix:///tmp/from-flag.sock", want: "unix:///tmp/from-flag.sock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			restoreRuntimeGlobals(t)
			t.Setenv("KUKEON_HOST", tc.env)
			if tc.env == "" {
				if err := os.Unsetenv("KUKEON_HOST"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("KUKEON_NO_DAEMON", "true") // serve must remain daemon-only.
			path := filepath.Join(t.TempDir(), "client.yaml")
			if err := os.WriteFile(path, []byte("apiVersion: kukeon.io/v1beta1\nkind: ClientConfiguration\nspec:\n  host: unix:///tmp/from-file.sock\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd, err := NewKukeCmd()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--configuration", path, "serve", "--port", "0"}
			if tc.flag != "" {
				args = append(args, "--host", tc.flag)
			}
			cmd.SetArgs(args)
			output := make(serveOutput, 4)
			cmd.SetOut(output)
			cmd.SetErr(io.Discard)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			var url string
			select {
			case line := <-output:
				url = strings.TrimSpace(strings.TrimPrefix(line, "Listening on "))
			case runErr := <-done:
				t.Fatalf("early exit: %v", runErr)
			case <-time.After(5 * time.Second):
				t.Fatal("startup timeout")
			}
			resp, err := http.Get(url + "/api/connection")
			if err != nil {
				t.Fatal(err)
			}
			var info map[string]string
			err = json.NewDecoder(resp.Body).Decode(&info)
			_ = resp.Body.Close()
			if err != nil || info["endpoint"] != tc.want {
				t.Fatalf("info=%v err=%v", info, err)
			}
			cancel()
			select {
			case runErr := <-done:
				if runErr != nil {
					t.Fatal(runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown timeout")
			}
		})
	}
}
