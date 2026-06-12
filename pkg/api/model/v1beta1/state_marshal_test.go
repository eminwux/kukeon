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

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRealmState_MarshalJSON_EmitsString pins the bug-#474 fix for `kuke get
// realm -o json`. The on-disk integer (`2`) must surface as the human-readable
// label (`"Ready"`) so operators scripting against the JSON don't have to grep
// the source for the enum.
func TestRealmState_MarshalJSON_EmitsString(t *testing.T) {
	for _, tc := range []struct {
		in   RealmState
		want string
	}{
		{RealmStatePending, `"Pending"`},
		{RealmStateCreating, `"Creating"`},
		{RealmStateReady, `"Ready"`},
		{RealmStateDeleting, `"Deleting"`},
		{RealmStateFailed, `"Failed"`},
		{RealmStateUnknown, `"Unknown"`},
	} {
		got, err := json.Marshal(tc.in)
		if err != nil {
			t.Fatalf("MarshalJSON(%v) errored: %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Errorf("MarshalJSON(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestRealmState_MarshalYAML_EmitsString(t *testing.T) {
	doc := RealmDoc{Status: RealmStatus{State: RealmStateReady}}
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("yaml.Marshal errored: %v", err)
	}
	if !strings.Contains(string(out), "state: Ready") {
		t.Errorf("yaml output missing `state: Ready`:\n%s", out)
	}
	if strings.Contains(string(out), "state: 2") {
		t.Errorf("yaml output still has int form:\n%s", out)
	}
}

// TestRealmState_UnmarshalJSON_AcceptsBothForms verifies the back-compat aid:
// daemon-persisted metadata.json from before this fix carries int state and
// must still parse on a newer daemon. Issue #474 ACs.
func TestRealmState_UnmarshalJSON_AcceptsBothForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want RealmState
	}{
		{"string form", `"Ready"`, RealmStateReady},
		{"int form (legacy)", `2`, RealmStateReady},
		{"int form pending", `0`, RealmStatePending},
		{"int form unknown", `5`, RealmStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got RealmState
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("UnmarshalJSON(%s) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("UnmarshalJSON(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRealmState_UnmarshalJSON_Rejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"unknown label", `"PartyTime"`},
		{"int out of range high", `99`},
		{"int out of range negative", `-1`},
		{"not a scalar", `{"x":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got RealmState
			if err := json.Unmarshal([]byte(tc.in), &got); err == nil {
				t.Errorf("UnmarshalJSON(%s) succeeded with %v, want error", tc.in, got)
			}
		})
	}
}

func TestRealmState_UnmarshalYAML_AcceptsBothForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want RealmState
	}{
		{"string form", `Ready`, RealmStateReady},
		{"string form quoted", `"Ready"`, RealmStateReady},
		{"int form (legacy)", `2`, RealmStateReady},
		{"int form pending", `0`, RealmStatePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got RealmState
			if err := yaml.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("UnmarshalYAML(%s) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("UnmarshalYAML(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestRealmDoc_RoundTrip is the AC verifying a YAML output piped back through
// `kuke apply -f` succeeds: marshal a realm, unmarshal it, and confirm the
// state survives intact (no `state: 2` schema error).
func TestRealmDoc_RoundTrip(t *testing.T) {
	in := RealmDoc{
		APIVersion: APIVersionV1Beta1,
		Kind:       KindRealm,
		Metadata:   RealmMetadata{Name: "default"},
		Spec:       RealmSpec{Namespace: "default.kukeon.io"},
		Status:     RealmStatus{State: RealmStateReady, CgroupPath: "/kukeon/default"},
	}

	t.Run("yaml", func(t *testing.T) {
		out, marshalErr := yaml.Marshal(in)
		if marshalErr != nil {
			t.Fatalf("yaml.Marshal: %v", marshalErr)
		}
		var back RealmDoc
		if unmarshalErr := yaml.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("yaml.Unmarshal: %v\noutput:\n%s", unmarshalErr, out)
		}
		if back.Status.State != RealmStateReady {
			t.Errorf("round-trip state = %v, want Ready", back.Status.State)
		}
	})

	t.Run("json", func(t *testing.T) {
		out, marshalErr := json.Marshal(in)
		if marshalErr != nil {
			t.Fatalf("json.Marshal: %v", marshalErr)
		}
		var back RealmDoc
		if unmarshalErr := json.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal: %v\noutput: %s", unmarshalErr, out)
		}
		if back.Status.State != RealmStateReady {
			t.Errorf("round-trip state = %v, want Ready", back.Status.State)
		}
	})
}

// The remaining four state types share the same marshal pattern; a per-type
// "string label round-trips" check is enough to catch a misconfigured switch
// without re-enumerating every error path.

func TestSpaceState_StringRoundTrip(t *testing.T) {
	for _, s := range []SpaceState{SpaceStatePending, SpaceStateReady, SpaceStateFailed, SpaceStateUnknown} {
		out, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%v): %v", s, marshalErr)
		}
		var back SpaceState
		if unmarshalErr := json.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal(%s): %v", out, unmarshalErr)
		}
		if back != s {
			t.Errorf("round-trip space %v -> %s -> %v", s, out, back)
		}
	}
}

func TestStackState_StringRoundTrip(t *testing.T) {
	for _, s := range []StackState{StackStatePending, StackStateReady, StackStateFailed, StackStateUnknown} {
		out, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%v): %v", s, marshalErr)
		}
		var back StackState
		if unmarshalErr := json.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal(%s): %v", out, unmarshalErr)
		}
		if back != s {
			t.Errorf("round-trip stack %v -> %s -> %v", s, out, back)
		}
	}
}

func TestCellState_StringRoundTrip(t *testing.T) {
	for _, s := range []CellState{
		CellStatePending, CellStateReady, CellStateStopped, CellStateFailed, CellStateUnknown,
		CellStateExited, CellStateError, // #1267
	} {
		out, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%v): %v", s, marshalErr)
		}
		var back CellState
		if unmarshalErr := json.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal(%s): %v", out, unmarshalErr)
		}
		if back != s {
			t.Errorf("round-trip cell %v -> %s -> %v", s, out, back)
		}
	}
}

func TestContainerState_StringRoundTrip(t *testing.T) {
	for _, s := range []ContainerState{
		ContainerStatePending, ContainerStateReady, ContainerStateStopped,
		ContainerStatePaused, ContainerStatePausing, ContainerStateFailed, ContainerStateUnknown,
		ContainerStateNotCreated, ContainerStateExited, ContainerStateError, // #1267
	} {
		out, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%v): %v", s, marshalErr)
		}
		var back ContainerState
		if unmarshalErr := json.Unmarshal(out, &back); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal(%s): %v", out, unmarshalErr)
		}
		if back != s {
			t.Errorf("round-trip container %v -> %s -> %v", s, out, back)
		}
	}
}

// TestCellState_BackCompat_LegacyIntJSON ensures a daemon reading old
// metadata.json files (which serialized state as a raw int) still gets the
// correct enum value back. Critical for in-place upgrades.
func TestCellState_BackCompat_LegacyIntJSON(t *testing.T) {
	legacy := []byte(
		`{"name":"web","id":"abc","state":2,"restartCount":0,"restartTime":"0001-01-01T00:00:00Z","startTime":"0001-01-01T00:00:00Z","finishTime":"0001-01-01T00:00:00Z","exitCode":0,"exitSignal":""}`,
	)
	var status ContainerStatus
	if err := json.Unmarshal(legacy, &status); err != nil {
		t.Fatalf("Unmarshal legacy int form: %v", err)
	}
	if status.State != ContainerStateStopped {
		t.Errorf("legacy state=2 parsed as %v, want ContainerStateStopped", status.State)
	}
}
