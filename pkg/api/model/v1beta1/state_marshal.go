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

// JSON/YAML marshaling for the *State enum types. The serializers emit the
// human-readable label (`Ready`, `Pending`, …) instead of the raw int form
// that the encoding/json and gopkg.in/yaml.v3 defaults would produce.
// Unmarshal accepts both forms — string is canonical going forward, int is
// retained for one release so older on-disk metadata.json files persisted by
// pre-fix daemons still round-trip through `kuke get -o yaml | kuke apply`.
// Issue #474.

import (
	"encoding/json"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// yamlIntTag is the auto-resolved gopkg.in/yaml.v3 tag for an unquoted
// integer scalar — the legacy on-disk encoding for state fields the
// UnmarshalYAML methods accept for back-compat.
const yamlIntTag = "!!int"

// --- RealmState ---

func (s RealmState) MarshalJSON() ([]byte, error) {
	return json.Marshal((&s).String())
}

func (s RealmState) MarshalYAML() (interface{}, error) {
	return (&s).String(), nil
}

func (s *RealmState) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return parseRealmState(str, s)
	}
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("realm state: expected string or int, got %s", string(data))
	}
	return assignRealmStateInt(i, s)
}

func (s *RealmState) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("realm state: expected scalar, got %v", node.Kind)
	}
	if node.Tag == yamlIntTag {
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("realm state: %w", err)
		}
		return assignRealmStateInt(i, s)
	}
	return parseRealmState(node.Value, s)
}

func parseRealmState(in string, out *RealmState) error {
	switch in {
	case StatePendingStr:
		*out = RealmStatePending
	case StateCreatingStr:
		*out = RealmStateCreating
	case StateReadyStr:
		*out = RealmStateReady
	case StateDeletingStr:
		*out = RealmStateDeleting
	case StateFailedStr:
		*out = RealmStateFailed
	case StateUnknownStr:
		*out = RealmStateUnknown
	default:
		return fmt.Errorf("realm state: unknown label %q", in)
	}
	return nil
}

func assignRealmStateInt(i int, out *RealmState) error {
	v := RealmState(i)
	if v < RealmStatePending || v > RealmStateUnknown {
		return fmt.Errorf("realm state: int %d out of range", i)
	}
	*out = v
	return nil
}

// --- SpaceState ---

func (s SpaceState) MarshalJSON() ([]byte, error) {
	return json.Marshal((&s).String())
}

func (s SpaceState) MarshalYAML() (interface{}, error) {
	return (&s).String(), nil
}

func (s *SpaceState) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return parseSpaceState(str, s)
	}
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("space state: expected string or int, got %s", string(data))
	}
	return assignSpaceStateInt(i, s)
}

func (s *SpaceState) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("space state: expected scalar, got %v", node.Kind)
	}
	if node.Tag == yamlIntTag {
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("space state: %w", err)
		}
		return assignSpaceStateInt(i, s)
	}
	return parseSpaceState(node.Value, s)
}

func parseSpaceState(in string, out *SpaceState) error {
	switch in {
	case StatePendingStr:
		*out = SpaceStatePending
	case StateReadyStr:
		*out = SpaceStateReady
	case StateFailedStr:
		*out = SpaceStateFailed
	case StateUnknownStr:
		*out = SpaceStateUnknown
	default:
		return fmt.Errorf("space state: unknown label %q", in)
	}
	return nil
}

func assignSpaceStateInt(i int, out *SpaceState) error {
	v := SpaceState(i)
	if v < SpaceStatePending || v > SpaceStateUnknown {
		return fmt.Errorf("space state: int %d out of range", i)
	}
	*out = v
	return nil
}

// --- StackState ---

func (s StackState) MarshalJSON() ([]byte, error) {
	return json.Marshal((&s).String())
}

func (s StackState) MarshalYAML() (interface{}, error) {
	return (&s).String(), nil
}

func (s *StackState) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return parseStackState(str, s)
	}
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("stack state: expected string or int, got %s", string(data))
	}
	return assignStackStateInt(i, s)
}

func (s *StackState) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("stack state: expected scalar, got %v", node.Kind)
	}
	if node.Tag == yamlIntTag {
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("stack state: %w", err)
		}
		return assignStackStateInt(i, s)
	}
	return parseStackState(node.Value, s)
}

func parseStackState(in string, out *StackState) error {
	switch in {
	case StatePendingStr:
		*out = StackStatePending
	case StateReadyStr:
		*out = StackStateReady
	case StateFailedStr:
		*out = StackStateFailed
	case StateUnknownStr:
		*out = StackStateUnknown
	default:
		return fmt.Errorf("stack state: unknown label %q", in)
	}
	return nil
}

func assignStackStateInt(i int, out *StackState) error {
	v := StackState(i)
	if v < StackStatePending || v > StackStateUnknown {
		return fmt.Errorf("stack state: int %d out of range", i)
	}
	*out = v
	return nil
}

// --- CellState ---

func (s CellState) MarshalJSON() ([]byte, error) {
	return json.Marshal((&s).String())
}

func (s CellState) MarshalYAML() (interface{}, error) {
	return (&s).String(), nil
}

func (s *CellState) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return parseCellState(str, s)
	}
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("cell state: expected string or int, got %s", string(data))
	}
	return assignCellStateInt(i, s)
}

func (s *CellState) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("cell state: expected scalar, got %v", node.Kind)
	}
	if node.Tag == yamlIntTag {
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("cell state: %w", err)
		}
		return assignCellStateInt(i, s)
	}
	return parseCellState(node.Value, s)
}

func parseCellState(in string, out *CellState) error {
	switch in {
	case StatePendingStr:
		*out = CellStatePending
	case StateReadyStr:
		*out = CellStateReady
	case StateStoppedStr:
		*out = CellStateStopped
	case StateFailedStr:
		*out = CellStateFailed
	case StateUnknownStr:
		*out = CellStateUnknown
	default:
		return fmt.Errorf("cell state: unknown label %q", in)
	}
	return nil
}

func assignCellStateInt(i int, out *CellState) error {
	v := CellState(i)
	if v < CellStatePending || v > CellStateUnknown {
		return fmt.Errorf("cell state: int %d out of range", i)
	}
	*out = v
	return nil
}

// --- ContainerState ---

func (s ContainerState) MarshalJSON() ([]byte, error) {
	return json.Marshal((&s).String())
}

func (s ContainerState) MarshalYAML() (interface{}, error) {
	return (&s).String(), nil
}

func (s *ContainerState) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		return parseContainerState(str, s)
	}
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("container state: expected string or int, got %s", string(data))
	}
	return assignContainerStateInt(i, s)
}

func (s *ContainerState) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("container state: expected scalar, got %v", node.Kind)
	}
	if node.Tag == yamlIntTag {
		i, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("container state: %w", err)
		}
		return assignContainerStateInt(i, s)
	}
	return parseContainerState(node.Value, s)
}

func parseContainerState(in string, out *ContainerState) error {
	switch in {
	case StatePendingStr:
		*out = ContainerStatePending
	case StateReadyStr:
		*out = ContainerStateReady
	case StateStoppedStr:
		*out = ContainerStateStopped
	case StatePausedStr:
		*out = ContainerStatePaused
	case StatePausingStr:
		*out = ContainerStatePausing
	case StateFailedStr:
		*out = ContainerStateFailed
	case StateUnknownStr:
		*out = ContainerStateUnknown
	default:
		return fmt.Errorf("container state: unknown label %q", in)
	}
	return nil
}

func assignContainerStateInt(i int, out *ContainerState) error {
	v := ContainerState(i)
	if v < ContainerStatePending || v > ContainerStateUnknown {
		return fmt.Errorf("container state: int %d out of range", i)
	}
	*out = v
	return nil
}
