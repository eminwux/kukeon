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

package v1beta1

import "testing"

// TestContainerSpec_HostNetworkDefault pins the zero-value behavior of the
// HostNetwork field so existing manifests (which don't set it) keep their
// per-container netns and CNI attach. Issue #96.
func TestContainerSpec_HostNetworkDefault(t *testing.T) {
	t.Run("zero value is false", func(t *testing.T) {
		var spec ContainerSpec
		if spec.HostNetwork {
			t.Errorf("zero-value ContainerSpec.HostNetwork = true, want false")
		}
	})

	t.Run("NewContainerDoc default is false", func(t *testing.T) {
		doc := NewContainerDoc(nil)
		if doc.Spec.HostNetwork {
			t.Errorf("NewContainerDoc(nil).Spec.HostNetwork = true, want false")
		}
	})

	t.Run("NewContainerDoc preserves explicit true", func(t *testing.T) {
		in := &ContainerDoc{Spec: ContainerSpec{HostNetwork: true}}
		out := NewContainerDoc(in)
		if !out.Spec.HostNetwork {
			t.Errorf("NewContainerDoc copy lost HostNetwork=true")
		}
	})
}

// TestContainerSpec_HostPIDDefault pins the zero-value behavior of the
// HostPID field so existing manifests (which don't set it) keep their
// per-container PID namespace. Issue #105.
func TestContainerSpec_HostPIDDefault(t *testing.T) {
	t.Run("zero value is false", func(t *testing.T) {
		var spec ContainerSpec
		if spec.HostPID {
			t.Errorf("zero-value ContainerSpec.HostPID = true, want false")
		}
	})

	t.Run("NewContainerDoc default is false", func(t *testing.T) {
		doc := NewContainerDoc(nil)
		if doc.Spec.HostPID {
			t.Errorf("NewContainerDoc(nil).Spec.HostPID = true, want false")
		}
	})

	t.Run("NewContainerDoc preserves explicit true", func(t *testing.T) {
		in := &ContainerDoc{Spec: ContainerSpec{HostPID: true}}
		out := NewContainerDoc(in)
		if !out.Spec.HostPID {
			t.Errorf("NewContainerDoc copy lost HostPID=true")
		}
	})
}
