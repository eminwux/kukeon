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

// Version is the kuke/kukeond build version.
//
//nolint:gochecknoglobals // needs to be global for ldflags injection
var Version = "0.1.0"

// KukeondImageRepo is the OCI image reference (without tag) that `kuke init`
// will provision for the kukeond system cell when the user does not pass
// --kukeond-image. The release pipeline injects the ghcr.io path that the
// matching kukeond image is published to.
//
//nolint:gochecknoglobals // needs to be global for ldflags injection
var KukeondImageRepo = "ghcr.io/eminwux/kukeon"
