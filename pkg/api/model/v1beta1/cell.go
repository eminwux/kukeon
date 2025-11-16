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

type CellDoc struct {
	APIVersion Version      `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind         `json:"kind"       yaml:"kind"`
	Metadata   CellMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellSpec     `json:"spec"       yaml:"spec"`
	Status     CellStatus   `json:"status"     yaml:"status"`
}

type CellMetadata struct {
	Name   string            `json:"name"   yaml:"name"`
	Labels map[string]string `json:"labels" yaml:"labels"`
}

type CellSpec struct {
	ID string `json:"id" yaml:"id"`
}

type CellStatus struct {
	State CellState `json:"state" yaml:"state"`
}

type CellState int

const (
	CellStatePending CellState = iota
	CellStateReady
	CellStateFailed
	CellStateUnknown
)

func (c *CellState) String() string {
	switch *c {
	case CellStatePending:
		return StatePendingStr
	case CellStateReady:
		return StateReadyStr
	case CellStateFailed:
		return StateFailedStr
	case CellStateUnknown:
		return StateUnknownStr
	}
	return StateUnknownStr
}
