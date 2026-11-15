// Copyright 2026 Emiliano Spinella (eminwux)
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

// Package renderers bundles the supported renderer with the executable, independently
// of the source supplying a Harness. External files cannot shadow these templates.
package renderers

import "embed"

// Templates contains only release-owned template files.
//
//go:embed kukeon/v1beta1/*.tmpl.yaml
var Templates embed.FS
