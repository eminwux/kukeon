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

package local

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestInspectCgroup_AbsentCgroupIsNotAnError(t *testing.T) {
	res, err := inspectCgroup(fstest.MapFS{}, "/sys/fs/cgroup", kukeonv1.GetCgroupDetailResult{
		CgroupPath: "/kukeon/default/s/st/c/default_s_st_c_app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Exists || res.Live != nil {
		t.Fatalf("want not present, got exists=%v live=%+v", res.Exists, res.Live)
	}
	if res.AbsolutePath != "/sys/fs/cgroup/kukeon/default/s/st/c/default_s_st_c_app" {
		t.Fatalf("absolute path: %q", res.AbsolutePath)
	}
}

func TestInspectCgroup_ReadsLiveValues(t *testing.T) {
	fsys := fstest.MapFS{"kukeon/default/memory.max": &fstest.MapFile{Data: []byte("max\n")}}
	res, err := inspectCgroup(fsys, "/sys/fs/cgroup", kukeonv1.GetCgroupDetailResult{CgroupPath: "/kukeon/default"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Exists || res.Live == nil || res.Live.Memory.Max.State != kukeonv1.CgroupValueMax {
		t.Fatalf("got %+v", res)
	}
}

func TestGetCgroupDetail_RejectsUnknownKind(t *testing.T) {
	_, err := (&Client{}).GetCgroupDetail(context.Background(), kukeonv1.CgroupScope{Kind: "volume"})
	if !errors.Is(err, errdefs.ErrCgroupScopeKind) {
		t.Fatalf("got %v", err)
	}
}
