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

// Package version declares the apiVersion kukeon documents carry and the one
// check every reader uses to accept it (issue #1633). It is a leaf package: it
// imports nothing from this module, so every API, team and configuration
// package can depend on it without an import cycle.
package version

import "fmt"

// V1Beta1 is the only apiVersion kukeon documents carry.
const V1Beta1 = "v1beta1"

// Check reports whether v is a supported apiVersion. An empty value is not.
func Check(v string) error {
	switch v {
	case V1Beta1:
		return nil
	case "":
		return fmt.Errorf("apiVersion is required (expected %s)", V1Beta1)
	default:
		return fmt.Errorf("unsupported apiVersion %q (expected %s)", v, V1Beta1)
	}
}
