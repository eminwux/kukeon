//go:build !integration

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

package controller

import (
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestKukeondCellDocVolumes(t *testing.T) {
	tests := []struct {
		name             string
		image            string
		socketPath       string
		runPath          string
		containerdSocket string
		want             []v1beta1.VolumeMount
	}{
		{
			name:             "all-distinct-dirs",
			image:            "docker.io/library/kukeon:dev",
			socketPath:       "/run/kukeon/kukeond.sock",
			runPath:          "/opt/kukeon",
			containerdSocket: "/run/containerd/containerd.sock",
			want: []v1beta1.VolumeMount{
				{Source: "/run/kukeon", Target: "/run/kukeon"},
				{Source: "/sys/fs/cgroup", Target: "/sys/fs/cgroup"},
				{Source: "/var/lib/containerd", Target: "/var/lib/containerd"},
				{Source: "/opt/cni/net.d", Target: "/opt/cni/net.d"},
				{Source: "/var/lib/cni", Target: "/var/lib/cni"},
				{Source: "/opt/cni/cache", Target: "/opt/cni/cache"},
				{Source: "/opt/kukeon", Target: "/opt/kukeon"},
				{Source: "/run/containerd", Target: "/run/containerd"},
			},
		},
		{
			name:       "no-runpath-no-containerd",
			image:      "docker.io/library/kukeon:dev",
			socketPath: "/run/kukeon/kukeond.sock",
			want: []v1beta1.VolumeMount{
				{Source: "/run/kukeon", Target: "/run/kukeon"},
				{Source: "/sys/fs/cgroup", Target: "/sys/fs/cgroup"},
				{Source: "/var/lib/containerd", Target: "/var/lib/containerd"},
				{Source: "/opt/cni/net.d", Target: "/opt/cni/net.d"},
				{Source: "/var/lib/cni", Target: "/var/lib/cni"},
				{Source: "/opt/cni/cache", Target: "/opt/cni/cache"},
			},
		},
		{
			name:             "containerd-socket-collides-with-sockdir",
			image:            "docker.io/library/kukeon:dev",
			socketPath:       "/run/kukeon/kukeond.sock",
			runPath:          "/opt/kukeon",
			containerdSocket: "/run/kukeon/containerd.sock",
			want: []v1beta1.VolumeMount{
				{Source: "/run/kukeon", Target: "/run/kukeon"},
				{Source: "/sys/fs/cgroup", Target: "/sys/fs/cgroup"},
				{Source: "/var/lib/containerd", Target: "/var/lib/containerd"},
				{Source: "/opt/cni/net.d", Target: "/opt/cni/net.d"},
				{Source: "/var/lib/cni", Target: "/var/lib/cni"},
				{Source: "/opt/cni/cache", Target: "/opt/cni/cache"},
				{Source: "/opt/kukeon", Target: "/opt/kukeon"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := kukeondCellDoc(tc.image, tc.socketPath, tc.runPath, tc.containerdSocket)
			if len(doc.Spec.Containers) != 1 {
				t.Fatalf("expected 1 container, got %d", len(doc.Spec.Containers))
			}
			got := doc.Spec.Containers[0].Volumes
			if len(got) != len(tc.want) {
				t.Fatalf("volumes length: got %d, want %d (got=%+v)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("volumes[%d]: got %+v, want %+v", i, got[i], w)
				}
			}

			// Cgroup hierarchy mount must always be present so kukeond can
			// create realm/space/stack/cell cgroups for user workloads.
			var hasCgroup bool
			for _, v := range got {
				if v.Source == "/sys/fs/cgroup" && v.Target == "/sys/fs/cgroup" {
					hasCgroup = true
					break
				}
			}
			if !hasCgroup {
				t.Errorf("missing /sys/fs/cgroup bind mount in volumes: %+v", got)
			}

			// Containerd data root must always be present so kukeond's in-process
			// overlay mounts during cell creation (image unpack, image-config
			// resolution) can resolve snapshot lowerdirs that live on the host.
			var hasContainerdData bool
			for _, v := range got {
				if v.Source == "/var/lib/containerd" && v.Target == "/var/lib/containerd" {
					hasContainerdData = true
					break
				}
			}
			if !hasContainerdData {
				t.Errorf("missing /var/lib/containerd bind mount in volumes: %+v", got)
			}

			// CNI conflist directory must be shared so the daemon reads the
			// same network definitions kuke init wrote in-process on the host.
			var hasCniNetD bool
			for _, v := range got {
				if v.Source == "/opt/cni/net.d" && v.Target == "/opt/cni/net.d" {
					hasCniNetD = true
					break
				}
			}
			if !hasCniNetD {
				t.Errorf("missing /opt/cni/net.d bind mount in volumes: %+v", got)
			}

			// host-local IPAM state must be shared so daemon and --no-daemon
			// don't allocate the same IP from the same subnet independently.
			var hasCniState bool
			for _, v := range got {
				if v.Source == "/var/lib/cni" && v.Target == "/var/lib/cni" {
					hasCniState = true
					break
				}
			}
			if !hasCniState {
				t.Errorf("missing /var/lib/cni bind mount in volumes: %+v", got)
			}

			// CNI invocation cache must be shared so DEL sees the same ADD
			// arguments and stops leaking veths and IPs across daemon restarts.
			var hasCniCache bool
			for _, v := range got {
				if v.Source == "/opt/cni/cache" && v.Target == "/opt/cni/cache" {
					hasCniCache = true
					break
				}
			}
			if !hasCniCache {
				t.Errorf("missing /opt/cni/cache bind mount in volumes: %+v", got)
			}

			// kukeond must run in the host's network namespace so the bridges,
			// veths, and iptables rules it installs land in host scope. The
			// runner reads HostNetwork to omit the netns entry from the OCI
			// spec and skip CNI attach for the cell's root container.
			if !doc.Spec.Containers[0].HostNetwork {
				t.Errorf("kukeond container must have HostNetwork=true")
			}
		})
	}
}
