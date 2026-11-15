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

package initialize

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestKukeondIntegration requires an explicitly selected disposable kukeond with
// default realm/space/stack metadata. It applies only unique blueprint/config
// names, reads them back, repeats apply, and removes those documents on cleanup.
func TestKukeondIntegration(t *testing.T) {
	host := os.Getenv("KUKETEAMS_INTEGRATION_HOST")
	if host == "" {
		t.Skip("set KUKETEAMS_INTEGRATION_HOST to a disposable kukeond Unix endpoint")
	}
	client, err := kukeonv1.Dial(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	team := config.Root()
	team.Metadata.Name = fmt.Sprintf("init-check-%d", time.Now().UnixNano())
	result := compositionFor(t, "codex", nil, team)
	defer func() {
		for _, pair := range result.Pairs {
			if _, deleteErr := client.DeleteConfig(t.Context(), *pair.Config); deleteErr != nil {
				t.Errorf("cleanup config: %v", deleteErr)
			}
			if _, deleteErr := client.DeleteBlueprint(t.Context(), *pair.Blueprint); deleteErr != nil {
				t.Errorf("cleanup blueprint: %v", deleteErr)
			}
		}
	}()
	before, err := client.ListCells(t.Context(), "default", "default", "default")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = Apply(t.Context(), client, result); err != nil {
			t.Fatal(err)
		}
		for _, pair := range result.Pairs {
			bp, readErr := client.GetBlueprint(t.Context(), *pair.Blueprint)
			if readErr != nil || !bp.MetadataExists ||
				bp.Blueprint.Metadata.Labels[api.LabelTeam] != team.Metadata.Name {
				t.Fatalf("blueprint readback: exists=%v error=%v", bp.MetadataExists, readErr)
			}
			cc, readErr := client.GetConfig(t.Context(), *pair.Config)
			if readErr != nil || !cc.MetadataExists || cc.Config.Metadata.Labels[api.LabelTeam] != team.Metadata.Name ||
				cc.Config.Spec.Blueprint.Name != pair.Blueprint.Metadata.Name {
				t.Fatalf("config readback: exists=%v error=%v", cc.MetadataExists, readErr)
			}
		}
	}
	after, err := client.ListCells(t.Context(), "default", "default", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("initialization changed runtime cells")
	}
}
