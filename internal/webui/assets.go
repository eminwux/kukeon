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

// Package webui provides the production frontend assets for the local web UI.
package webui

import (
	"embed"
	"fmt"
	"io/fs"
)

// dist is regenerated with make web-ui and committed with its sources.
//
//go:embed dist
var dist embed.FS

// Assets returns an immutable filesystem rooted at the built index.html.
// Consumers can serve it with http.FileServerFS; listener and access policy
// belong to the web server, not this asset package.
func Assets() (fs.FS, error) {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded web assets: %w", err)
	}
	return assets, nil
}
