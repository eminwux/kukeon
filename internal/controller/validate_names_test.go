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

package controller_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// invalidStoredNames fail the shared name rule (issue #1719); each would
// resolve outside, or shadow part of, its kind's directory.
func invalidStoredNames() []string {
	return []string{".", "..", "../metadata.json", "../../../x", ".x", ".types", "A", "a_b", "a b"}
}

// touchedRunner fails the test if a lookup reaches the runner: a rejected name
// must never be joined into a path.
func touchedRunner(t *testing.T) *fakeRunner {
	t.Helper()
	fail := func(kind string) { t.Errorf("runner %s reached with an invalid name", kind) }
	return &fakeRunner{
		GetBlueprintFn: func(bp intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			fail("GetBlueprint")
			return bp, nil
		},
		DeleteBlueprintFn: func(intmodel.CellBlueprint) error { fail("DeleteBlueprint"); return nil },
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			fail("GetConfig")
			return c, nil
		},
		WriteConfigFn:  func(intmodel.CellConfig) (bool, error) { fail("WriteConfig"); return true, nil },
		DeleteConfigFn: func(intmodel.CellConfig) error { fail("DeleteConfig"); return nil },
		GetSecretFn: func(s intmodel.Secret) (intmodel.Secret, error) {
			fail("GetSecret")
			return s, nil
		},
		DeleteSecretFn: func(intmodel.Secret) error { fail("DeleteSecret"); return nil },
		GetVolumeFn: func(v intmodel.Volume) (intmodel.Volume, error) {
			fail("GetVolume")
			return v, nil
		},
		DeleteVolumeFn: func(intmodel.Volume) error { fail("DeleteVolume"); return nil },
		GetTeamFn: func(tm intmodel.Team) (intmodel.Team, error) {
			fail("GetTeam")
			return tm, nil
		},
		DeleteTeamFn: func(intmodel.Team) error { fail("DeleteTeam"); return nil },
		GetSourceFn: func(s intmodel.Source) (intmodel.Source, error) {
			fail("GetSource")
			return s, nil
		},
		DeleteSourceFn: func(intmodel.Source) error { fail("DeleteSource"); return nil },
	}
}

// TestStoredKinds_RejectInvalidNames is #1719's AC that CellBlueprint and
// CellConfig names and scope coordinates are validated on create, get and
// delete like Secret and Volume, and that Secret, Volume, Team and Source
// names use the same shared rule: "../metadata.json" and the rest are
// refused with the kind's sentinel and never reach the runner.
func TestStoredKinds_RejectInvalidNames(t *testing.T) {
	type call struct {
		name string
		fn   func(t *testing.T, bad string) error
		want error
	}
	bp := func(name, realm, space string) intmodel.CellBlueprint {
		return intmodel.CellBlueprint{Metadata: intmodel.CellBlueprintMetadata{Name: name, Realm: realm, Space: space}}
	}
	cfg := func(name, realm, space string) intmodel.CellConfig {
		return intmodel.CellConfig{Metadata: intmodel.CellConfigMetadata{Name: name, Realm: realm, Space: space}}
	}
	sec := func(name, realm, space string) intmodel.Secret {
		return intmodel.Secret{Metadata: intmodel.SecretMetadata{Name: name, Realm: realm, Space: space}}
	}
	vol := func(name, realm, space string) intmodel.Volume {
		return intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: name, Realm: realm, Space: space}}
	}
	calls := []call{
		{"GetBlueprint name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetBlueprint(bp(b, "alpha", ""))
			return err
		}, errdefs.ErrBlueprintCoordUnsafe},
		{"GetBlueprint space", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetBlueprint(bp("x", "alpha", b))
			return err
		}, errdefs.ErrBlueprintCoordUnsafe},
		{"DeleteBlueprint name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteBlueprint(bp(b, "alpha", ""))
			return err
		}, errdefs.ErrBlueprintCoordUnsafe},
		{"DeleteBlueprint realm", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteBlueprint(bp("x", b, ""))
			return err
		}, errdefs.ErrBlueprintCoordUnsafe},
		{"ListBlueprints realm", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).ListBlueprints(b, "", "")
			return err
		}, errdefs.ErrBlueprintCoordUnsafe},
		{"CreateConfig name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).CreateConfig(cfg(b, "alpha", ""))
			return err
		}, errdefs.ErrConfigCoordUnsafe},
		{"GetConfig name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetConfig(cfg(b, "alpha", ""))
			return err
		}, errdefs.ErrConfigCoordUnsafe},
		{"GetConfig space", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetConfig(cfg("x", "alpha", b))
			return err
		}, errdefs.ErrConfigCoordUnsafe},
		{"DeleteConfig name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteConfig(cfg(b, "alpha", ""))
			return err
		}, errdefs.ErrConfigCoordUnsafe},
		{"ListConfigs realm", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).ListConfigs(b, "", "")
			return err
		}, errdefs.ErrConfigCoordUnsafe},
		{"GetSecret name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetSecret(sec(b, "alpha", ""))
			return err
		}, errdefs.ErrSecretCoordUnsafe},
		{"DeleteSecret space", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteSecret(sec("x", "alpha", b))
			return err
		}, errdefs.ErrSecretCoordUnsafe},
		{"GetVolume name", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).GetVolume(vol(b, "alpha", ""))
			return err
		}, errdefs.ErrVolumeCoordUnsafe},
		{"DeleteVolume space", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteVolume(vol("x", "alpha", b))
			return err
		}, errdefs.ErrVolumeCoordUnsafe},
		{"GetTeam name", func(t *testing.T, b string) error {
			_, _, err := setupTestController(t, touchedRunner(t)).GetTeam(b, "alpha")
			return err
		}, errdefs.ErrInvalidTeam},
		{"DeleteTeam realm", func(t *testing.T, b string) error {
			_, err := setupTestController(t, touchedRunner(t)).DeleteTeam("x", b)
			return err
		}, errdefs.ErrInvalidTeam},
		{"GetSource name", func(t *testing.T, b string) error {
			_, _, err := setupTestController(t, touchedRunner(t)).GetSource(b, "alpha")
			return err
		}, errdefs.ErrInvalidSource},
		{"DeleteSource name", func(t *testing.T, b string) error {
			return setupTestController(t, touchedRunner(t)).DeleteSource(b, "alpha")
		}, errdefs.ErrInvalidSource},
	}
	for _, c := range calls {
		for _, bad := range invalidStoredNames() {
			t.Run(c.name+"/"+bad, func(t *testing.T) {
				if err := c.fn(t, bad); !errors.Is(err, c.want) {
					t.Fatalf("%s(%q) = %v, want errors.Is(_, %v)", c.name, bad, err, c.want)
				}
			})
		}
	}
}
