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

package config

import (
	"testing"

	"github.com/spf13/viper"
)

// TestDefineKV_SetsViperDefault locks in that DefineKV registers the
// default with viper (viper.GetString returns it without any further
// wiring), so callers can rely on viper.GetString as a single read.
func TestDefineKV_SetsViperDefault(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	v := DefineKV("TEST_DEFINE_KV_VIPER", "test/defineKV/viper", "fallback-value")

	if got := viper.GetString(v.ViperKey); got != "fallback-value" {
		t.Errorf("viper.GetString: got %q, want %q (DefineKV must call viper.SetDefault)",
			got, "fallback-value")
	}
	if !viper.IsSet(v.ViperKey) {
		t.Error("viper.IsSet returned false after DefineKV — SetDefault should trip IsSet")
	}
	if v.Default != "fallback-value" || !v.HasDefault {
		t.Errorf("Var fields: Default=%q HasDefault=%v, want %q true",
			v.Default, v.HasDefault, "fallback-value")
	}
}

// TestDefineKVNoViperDefault_DoesNotSetViperDefault locks in the inverse
// of TestDefineKV_SetsViperDefault: DefineKVNoViperDefault must NOT call
// viper.SetDefault. The contract matters because applyRunPathImpliesKukeondSocket
// uses viper.IsSet to gate derivation — if SetDefault were called for
// KUKEOND_SOCKET, IsSet would always be true and `kuke --run-path X init`
// would silently keep the daemon on `/run/kukeon/kukeond.sock` regardless
// of X. If someone in the future "fixes" this constructor to call
// SetDefault, this test catches it before the next e2e cycle does.
func TestDefineKVNoViperDefault_DoesNotSetViperDefault(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	v := DefineKVNoViperDefault(
		"TEST_DEFINE_KV_NO_VIPER", "test/defineKV/noViper", "fallback-value",
	)

	if viper.IsSet(v.ViperKey) {
		t.Error("viper.IsSet returned true after DefineKVNoViperDefault — " +
			"SetDefault must NOT be called for this constructor")
	}
	if got := viper.GetString(v.ViperKey); got != "" {
		t.Errorf("viper.GetString: got %q, want %q (no viper.SetDefault expected)",
			got, "")
	}
	if v.Default != "fallback-value" || !v.HasDefault {
		t.Errorf("Var fields: Default=%q HasDefault=%v, want %q true (Go-side .Default still populated)",
			v.Default, v.HasDefault, "fallback-value")
	}
}
