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

package e2e_test

import (
	"encoding/json"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestKuke_NoContainers tests kuke get container when no containers exist.
func TestKuke_NoContainers(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	args := append(buildKukeDaemonArgs(host), "get", "container", "--output", "json")
	output := runReturningBinary(t, nil, kuke, args...)

	var containers []v1beta1.ContainerSpec
	if err := json.Unmarshal(output, &containers); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(containers) != 0 {
		t.Fatalf("expected empty container list, got %d containers", len(containers))
	}
}
