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
	"os"

	"github.com/spf13/viper"
)

type Var struct {
	Key        string // e.g. "KUKEON_RUN_PATH"
	ViperKey   string // optional, e.g. "global.runPath"
	CobraKey   string // optional, e.g. "run-path"
	Default    string // optional
	HasDefault bool
}

func DefineKV(envName, viperKey string, defaultVal ...string) Var {
	v := Var{Key: envName, ViperKey: viperKey}
	if len(defaultVal) > 0 {
		v.Default = defaultVal[0]
		v.HasDefault = true
	}
	return v
}

func Define(envName string, defaultVal ...string) Var {
	return DefineKV(envName, "", defaultVal...)
}

func (v *Var) EnvKey() string               { return v.Key }
func (v *Var) EnvVar() string               { return v.Key }
func (v *Var) DefaultValue() (string, bool) { return v.Default, v.HasDefault }

// ValueOrDefault defines precedence: viper (if ViperKey set and value present) → OS env → default → "".
func (v *Var) ValueOrDefault() string {
	if v.ViperKey != "" && viper.IsSet(v.ViperKey) {
		return viper.GetString(v.ViperKey)
	}
	if val, ok := os.LookupEnv(v.Key); ok {
		return val
	}
	if v.HasDefault {
		return v.Default
	}
	return ""
}

// BindEnv is safe if ViperKey is empty: does nothing.
func (v *Var) BindEnv() error {
	if v.ViperKey == "" {
		return nil
	}
	return viper.BindEnv(v.ViperKey, v.Key)
}

func (v *Var) Set(value string) error {
	return os.Setenv(v.Key, value)
}

func (v *Var) SetDefault(val string) {
	v.Default = val
	v.HasDefault = true
	if v.ViperKey != "" {
		viper.SetDefault(v.ViperKey, val)
	}
}

func KV(v Var, value string) string { return v.Key + "=" + value }

// ---- Declare statically (Viper key optional per var) ----.
var (
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKEON_ROOT_VERBOSE = DefineKV("KUKEON_VERBOSE", "kukeon/verbose")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKEON_ROOT_RUN_PATH = DefineKV("KUKEON_RUN_PATH", "kukeon/runPath")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKEON_ROOT_CONFIG_FILE = DefineKV("KUKEON_CONFIG_FILE", "kukeon/configFile")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKEON_ROOT_LOG_LEVEL = DefineKV("KUKEON_LOG_LEVEL", "kukeon/logLevel", "info")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKEON_ROOT_CONTAINERD_SOCKET = DefineKV("KUKEON_CONTAINERD_SOCKET", "kukeon/containerd.socket")

	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_INIT_REALM = DefineKV("KUKE_INIT_REALM", "kuke/init/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_INIT_SPACE = DefineKV("KUKE_INIT_SPACE", "kuke/init/space")

	// Create command variables
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_REALM_NAME = DefineKV("KUKE_CREATE_REALM_NAME", "kuke/create/realm/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_SPACE_NAME = DefineKV("KUKE_CREATE_SPACE_NAME", "kuke/create/space/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_SPACE_REALM = DefineKV("KUKE_CREATE_SPACE_REALM", "kuke/create/space/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_STACK_NAME = DefineKV("KUKE_CREATE_STACK_NAME", "kuke/create/stack/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_STACK_REALM = DefineKV("KUKE_CREATE_STACK_REALM", "kuke/create/stack/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_STACK_SPACE = DefineKV("KUKE_CREATE_STACK_SPACE", "kuke/create/stack/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CELL_NAME = DefineKV("KUKE_CREATE_CELL_NAME", "kuke/create/cell/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CELL_REALM = DefineKV("KUKE_CREATE_CELL_REALM", "kuke/create/cell/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CELL_SPACE = DefineKV("KUKE_CREATE_CELL_SPACE", "kuke/create/cell/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CELL_STACK = DefineKV("KUKE_CREATE_CELL_STACK", "kuke/create/cell/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_NAME = DefineKV("KUKE_CREATE_CONTAINER_NAME", "kuke/create/container/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_REALM = DefineKV("KUKE_CREATE_CONTAINER_REALM", "kuke/create/container/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_SPACE = DefineKV("KUKE_CREATE_CONTAINER_SPACE", "kuke/create/container/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_STACK = DefineKV("KUKE_CREATE_CONTAINER_STACK", "kuke/create/container/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_CELL = DefineKV("KUKE_CREATE_CONTAINER_CELL", "kuke/create/container/cell")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_CREATE_CONTAINER_IMAGE = DefineKV(
		"KUKE_CREATE_CONTAINER_IMAGE",
		"kuke/create/container/image",
		"docker.io/library/debian:latest",
	)

	// Get command variables
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_REALM_NAME = DefineKV("KUKE_GET_REALM_NAME", "kuke/get/realm/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_SPACE_NAME = DefineKV("KUKE_GET_SPACE_NAME", "kuke/get/space/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_SPACE_REALM = DefineKV("KUKE_GET_SPACE_REALM", "kuke/get/space/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_STACK_NAME = DefineKV("KUKE_GET_STACK_NAME", "kuke/get/stack/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_STACK_REALM = DefineKV("KUKE_GET_STACK_REALM", "kuke/get/stack/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_STACK_SPACE = DefineKV("KUKE_GET_STACK_SPACE", "kuke/get/stack/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CELL_NAME = DefineKV("KUKE_GET_CELL_NAME", "kuke/get/cell/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CELL_REALM = DefineKV("KUKE_GET_CELL_REALM", "kuke/get/cell/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CELL_SPACE = DefineKV("KUKE_GET_CELL_SPACE", "kuke/get/cell/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CELL_STACK = DefineKV("KUKE_GET_CELL_STACK", "kuke/get/cell/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CONTAINER_NAME = DefineKV("KUKE_GET_CONTAINER_NAME", "kuke/get/container/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CONTAINER_REALM = DefineKV("KUKE_GET_CONTAINER_REALM", "kuke/get/container/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CONTAINER_SPACE = DefineKV("KUKE_GET_CONTAINER_SPACE", "kuke/get/container/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CONTAINER_STACK = DefineKV("KUKE_GET_CONTAINER_STACK", "kuke/get/container/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_CONTAINER_CELL = DefineKV("KUKE_GET_CONTAINER_CELL", "kuke/get/container/cell")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_GET_OUTPUT = DefineKV("KUKE_GET_OUTPUT", "kuke/get/output")

	// Delete command variables
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_REALM_NAME = DefineKV("KUKE_DELETE_REALM_NAME", "kuke/delete/realm/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_SPACE_NAME = DefineKV("KUKE_DELETE_SPACE_NAME", "kuke/delete/space/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_SPACE_REALM = DefineKV("KUKE_DELETE_SPACE_REALM", "kuke/delete/space/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_STACK_NAME = DefineKV("KUKE_DELETE_STACK_NAME", "kuke/delete/stack/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_STACK_REALM = DefineKV("KUKE_DELETE_STACK_REALM", "kuke/delete/stack/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_STACK_SPACE = DefineKV("KUKE_DELETE_STACK_SPACE", "kuke/delete/stack/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CELL_NAME = DefineKV("KUKE_DELETE_CELL_NAME", "kuke/delete/cell/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CELL_REALM = DefineKV("KUKE_DELETE_CELL_REALM", "kuke/delete/cell/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CELL_SPACE = DefineKV("KUKE_DELETE_CELL_SPACE", "kuke/delete/cell/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CELL_STACK = DefineKV("KUKE_DELETE_CELL_STACK", "kuke/delete/cell/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CONTAINER_NAME = DefineKV("KUKE_DELETE_CONTAINER_NAME", "kuke/delete/container/name")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CONTAINER_REALM = DefineKV("KUKE_DELETE_CONTAINER_REALM", "kuke/delete/container/realm")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CONTAINER_SPACE = DefineKV("KUKE_DELETE_CONTAINER_SPACE", "kuke/delete/container/space")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CONTAINER_STACK = DefineKV("KUKE_DELETE_CONTAINER_STACK", "kuke/delete/container/stack")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CONTAINER_CELL = DefineKV("KUKE_DELETE_CONTAINER_CELL", "kuke/delete/container/cell")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_FORCE = DefineKV("KUKE_DELETE_FORCE", "kuke/delete/force")
	//nolint:revive,gochecknoglobals,staticcheck // ignore linter warning about this variable
	KUKE_DELETE_CASCADE = DefineKV("KUKE_DELETE_CASCADE", "kuke/delete/cascade")
)
