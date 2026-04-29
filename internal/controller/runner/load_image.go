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

package runner

import (
	"fmt"
	"io"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// LoadImage imports an OCI/docker image tarball into the given containerd
// namespace and returns the names of the imported images. The caller is
// responsible for ensuring the namespace exists; the controller layer
// validates the realm before invoking this method.
func (r *Exec) LoadImage(namespace string, reader io.Reader) ([]string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, errdefs.ErrCheckNamespaceExists
	}
	if err := r.ensureClientConnected(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	r.ctrClient.SetNamespace(namespace)
	return r.ctrClient.LoadImage(reader)
}
