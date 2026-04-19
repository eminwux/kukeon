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

package init_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/eminwux/kukeon/cmd/config"
	initpkg "github.com/eminwux/kukeon/cmd/kuke/init"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	_ = initpkg.NewInitCmd // ensure init package is linked in
	_ unsafe.Pointer       // mark unsafe as used
)

// Link names to access unexported symbols from cmd/kuke/init.
//
//go:linkname setFlags github.com/eminwux/kukeon/cmd/kuke/init.setFlags
func setFlags(cmd *cobra.Command) error

//go:linkname printHeader github.com/eminwux/kukeon/cmd/kuke/init.printHeader
func printHeader(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printOverview github.com/eminwux/kukeon/cmd/kuke/init.printOverview
func printOverview(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printRealmActions github.com/eminwux/kukeon/cmd/kuke/init.printRealmActions
func printRealmActions(cmd *cobra.Command, section controller.RealmSection)

//go:linkname printSpaceActions github.com/eminwux/kukeon/cmd/kuke/init.printSpaceActions
func printSpaceActions(cmd *cobra.Command, section controller.SpaceSection)

//go:linkname printStackActions github.com/eminwux/kukeon/cmd/kuke/init.printStackActions
func printStackActions(cmd *cobra.Command, section controller.StackSection)

//go:linkname printCellActions github.com/eminwux/kukeon/cmd/kuke/init.printCellActions
func printCellActions(cmd *cobra.Command, section controller.CellSection, image string)

//go:linkname printCNIActions github.com/eminwux/kukeon/cmd/kuke/init.printCNIActions
func printCNIActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printDirAction github.com/eminwux/kukeon/cmd/kuke/init.printDirAction
func printDirAction(cmd *cobra.Command, label string, path string, created bool, existsPost bool)

//go:linkname printCgroupAction github.com/eminwux/kukeon/cmd/kuke/init.printCgroupAction
func printCgroupAction(cmd *cobra.Command, label string, existedPre bool, existsPost bool, created bool)

//go:linkname runInit github.com/eminwux/kukeon/cmd/kuke/init.runInit
func runInit(cmd *cobra.Command, args []string) error

//go:linkname resolveKukeondImage github.com/eminwux/kukeon/cmd/kuke/init.resolveKukeondImage
func resolveKukeondImage() string

// ResolveKukeondImage exposes the unexported helper for tests.
func ResolveKukeondImage() string {
	return resolveKukeondImage()
}

// Exported helpers

func SetFlags(cmd *cobra.Command) error {
	return setFlags(cmd)
}

func PrintHeader(cmd *cobra.Command, report controller.BootstrapReport) {
	printHeader(cmd, report)
}

func PrintOverview(cmd *cobra.Command, report controller.BootstrapReport) {
	printOverview(cmd, report)
}

func PrintRealmActions(cmd *cobra.Command, section controller.RealmSection) {
	printRealmActions(cmd, section)
}

func PrintSpaceActions(cmd *cobra.Command, section controller.SpaceSection) {
	printSpaceActions(cmd, section)
}

func PrintStackActions(cmd *cobra.Command, section controller.StackSection) {
	printStackActions(cmd, section)
}

func PrintCellActions(cmd *cobra.Command, section controller.CellSection, image string) {
	printCellActions(cmd, section, image)
}

func PrintCNIActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printCNIActions(cmd, report)
}

func PrintDirAction(cmd *cobra.Command, label, path string, created, existsPost bool) {
	printDirAction(cmd, label, path, created, existsPost)
}

func PrintCgroupAction(cmd *cobra.Command, label string, existedPre, existsPost, created bool) {
	printCgroupAction(cmd, label, existedPre, existsPost, created)
}

func RunInit(cmd *cobra.Command) error {
	return runInit(cmd, nil)
}

func TestSetFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	testCases := []struct {
		name      string
		args      []string
		wantRealm string
		wantSpace string
	}{
		{
			name:      "defaults",
			args:      nil,
			wantRealm: "main",
			wantSpace: "default",
		},
		{
			name:      "overrides",
			args:      []string{"--realm=dev", "--space=blue"},
			wantRealm: "dev",
			wantSpace: "blue",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			cmd := &cobra.Command{Use: "init"}
			if err := SetFlags(cmd); err != nil {
				t.Fatalf("setFlags failed: %v", err)
			}
			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			if got := viper.GetString(config.KUKE_INIT_REALM.ViperKey); got != tc.wantRealm {
				t.Errorf("realm mismatch: got %q want %q", got, tc.wantRealm)
			}
			if got := viper.GetString(config.KUKE_INIT_SPACE.ViperKey); got != tc.wantSpace {
				t.Errorf("space mismatch: got %q want %q", got, tc.wantSpace)
			}
		})
	}
}

func TestPrintHeader(t *testing.T) {
	testCases := []struct {
		name     string
		report   controller.BootstrapReport
		expected []string
	}{
		{
			name: "initialized-default",
			report: controller.BootstrapReport{
				DefaultRealm: controller.RealmSection{RealmCreated: true},
			},
			expected: []string{"Initialized Kukeon runtime"},
		},
		{
			name: "initialized-system",
			report: controller.BootstrapReport{
				SystemCell: controller.CellSection{CellCreated: true},
			},
			expected: []string{"Initialized Kukeon runtime"},
		},
		{
			name:     "already",
			report:   controller.BootstrapReport{},
			expected: []string{"Kukeon runtime already initialized"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintHeader(cmd, tc.report)
			got := buf.String()
			for _, want := range tc.expected {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestPrintOverview(t *testing.T) {
	cmd, buf := newOutputCommand()
	report := controller.BootstrapReport{
		DefaultRealm: controller.RealmSection{
			RealmName:                "realm-a",
			RealmContainerdNamespace: "namespace-a",
		},
		SystemRealm: controller.RealmSection{
			RealmName:                "kuke-system",
			RealmContainerdNamespace: "kuke-system.kukeon.io",
		},
		RunPath:      "/tmp/run",
		KukeondImage: "ghcr.io/eminwux/kukeon:v1.2.3",
	}
	PrintOverview(cmd, report)
	got := buf.String()

	want := []string{
		"Realm: realm-a (namespace: namespace-a)",
		"System realm: kuke-system (namespace: kuke-system.kukeon.io)",
		"Run path: /tmp/run",
		"Kukeond image: ghcr.io/eminwux/kukeon:v1.2.3",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("output %q missing %q", got, w)
		}
	}
}

func TestPrintRealmActions(t *testing.T) {
	testCases := []struct {
		name     string
		section  controller.RealmSection
		expected []string
	}{
		{
			name: "created",
			section: controller.RealmSection{
				RealmName:                          "default",
				RealmContainerdNamespace:           "kukeon.io",
				RealmCreated:                       true,
				RealmContainerdNamespaceCreated:    true,
				RealmCgroupCreated:                 true,
				RealmCgroupExistsPost:              true,
				RealmContainerdNamespaceExistsPost: true,
			},
			expected: []string{
				"- realm \"default\": created",
				"- containerd namespace \"kukeon.io\": created",
				"- realm cgroup: created",
			},
		},
		{
			name: "already-existed",
			section: controller.RealmSection{
				RealmName:                          "default",
				RealmContainerdNamespace:           "kukeon.io",
				RealmCgroupExistsPre:               true,
				RealmCgroupExistsPost:              true,
				RealmContainerdNamespaceExistsPre:  true,
				RealmContainerdNamespaceExistsPost: true,
				RealmMetadataExistsPre:             true,
				RealmMetadataExistsPost:            true,
			},
			expected: []string{
				"- realm \"default\": already existed",
				"- containerd namespace \"kukeon.io\": already existed",
				"- realm cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintRealmActions(cmd, tc.section)
			got := buf.String()
			for _, want := range tc.expected {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestPrintSpaceActions(t *testing.T) {
	testCases := []struct {
		name     string
		section  controller.SpaceSection
		expected []string
	}{
		{
			name: "created",
			section: controller.SpaceSection{
				SpaceName:              "default",
				SpaceCNINetworkName:    "net1",
				SpaceCreated:           true,
				SpaceCNINetworkCreated: true,
				SpaceCgroupCreated:     true,
			},
			expected: []string{
				"- space \"default\": created",
				"- network \"net1\": created",
				"- space cgroup: created",
			},
		},
		{
			name: "exists",
			section: controller.SpaceSection{
				SpaceName:                 "default",
				SpaceCNINetworkName:       "net2",
				SpaceMetadataExistsPre:    true,
				SpaceMetadataExistsPost:   true,
				SpaceCNINetworkExistsPre:  true,
				SpaceCNINetworkExistsPost: true,
				SpaceCgroupExistsPre:      true,
				SpaceCgroupExistsPost:     true,
			},
			expected: []string{
				"- space \"default\": already existed",
				"- network \"net2\": already existed",
				"- space cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintSpaceActions(cmd, tc.section)
			got := buf.String()
			for _, want := range tc.expected {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestPrintStackActions(t *testing.T) {
	testCases := []struct {
		name     string
		section  controller.StackSection
		expected []string
	}{
		{
			name: "created",
			section: controller.StackSection{
				StackName:             "default",
				StackCreated:          true,
				StackCgroupCreated:    true,
				StackCgroupExistsPost: true,
			},
			expected: []string{
				"- stack \"default\": created",
				"- stack cgroup: created",
			},
		},
		{
			name: "already",
			section: controller.StackSection{
				StackName:               "default",
				StackMetadataExistsPre:  true,
				StackMetadataExistsPost: true,
				StackCgroupExistsPre:    true,
				StackCgroupExistsPost:   true,
			},
			expected: []string{
				"- stack \"default\": already existed",
				"- stack cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintStackActions(cmd, tc.section)
			got := buf.String()
			for _, want := range tc.expected {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestPrintCellActions(t *testing.T) {
	testCases := []struct {
		name     string
		section  controller.CellSection
		image    string
		expected []string
	}{
		{
			name: "created",
			section: controller.CellSection{
				CellName:                    "kukeond",
				CellCreated:                 true,
				CellCgroupCreated:           true,
				CellRootContainerCreated:    true,
				CellStarted:                 true,
				CellCgroupExistsPost:        true,
				CellRootContainerExistsPost: true,
				CellStartedPost:             true,
			},
			image: "ghcr.io/eminwux/kukeon:v1.0.0",
			expected: []string{
				"- cell \"kukeond\": created (image ghcr.io/eminwux/kukeon:v1.0.0)",
				"- cell cgroup: created",
				"- cell root container cgroup: created",
				"- cell containers cgroup: created",
			},
		},
		{
			name: "already",
			section: controller.CellSection{
				CellName:                    "kukeond",
				CellMetadataExistsPre:       true,
				CellMetadataExistsPost:      true,
				CellCgroupExistsPre:         true,
				CellCgroupExistsPost:        true,
				CellRootContainerExistsPre:  true,
				CellRootContainerExistsPost: true,
				CellStartedPre:              true,
				CellStartedPost:             true,
			},
			expected: []string{
				"- cell \"kukeond\": already existed",
				"- cell cgroup: already existed",
				"- cell root container cgroup: already existed",
				"- cell containers cgroup: already existed",
			},
		},
		{
			name:     "not-provisioned",
			section:  controller.CellSection{},
			expected: []string{"- cell: not provisioned"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintCellActions(cmd, tc.section, tc.image)
			got := buf.String()
			for _, want := range tc.expected {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestPrintCNIActions(t *testing.T) {
	report := controller.BootstrapReport{
		CniConfigDir:          "/tmp/cni/config",
		CniCacheDir:           "/tmp/cni/cache",
		CniBinDir:             "/tmp/cni/bin",
		CniConfigDirCreated:   true,
		CniCacheDirCreated:    false,
		CniCacheDirExistsPost: true,
		CniBinDirExistsPost:   true,
	}

	cmd, buf := newOutputCommand()
	PrintCNIActions(cmd, report)
	got := buf.String()

	expect := []string{
		"- CNI config dir \"/tmp/cni/config\": created",
		"- CNI cache dir \"/tmp/cni/cache\": already existed",
		"- CNI bin dir \"/tmp/cni/bin\": already existed",
	}

	for _, e := range expect {
		if !strings.Contains(got, e) {
			t.Fatalf("output %q missing %q", got, e)
		}
	}
}

func TestPrintDirAction(t *testing.T) {
	testCases := []struct {
		name       string
		label      string
		path       string
		created    bool
		existsPost bool
		want       string
	}{
		{
			name:    "created",
			label:   "item",
			path:    "/tmp/item",
			created: true,
			want:    "- item \"/tmp/item\": created",
		},
		{
			name:       "existing",
			label:      "item",
			path:       "/tmp/item",
			existsPost: true,
			want:       "- item \"/tmp/item\": already existed",
		},
		{
			name:  "missing",
			label: "item",
			path:  "/tmp/item",
			want:  "- item \"/tmp/item\": not created",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintDirAction(cmd, tc.label, tc.path, tc.created, tc.existsPost)
			got := buf.String()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("output %q missing %q", got, tc.want)
			}
		})
	}
}

func TestPrintCgroupAction(t *testing.T) {
	testCases := []struct {
		name       string
		label      string
		existedPre bool
		existsPost bool
		created    bool
		want       string
	}{
		{
			name:    "created",
			label:   "realm",
			created: true,
			want:    "- realm cgroup: created",
		},
		{
			name:       "exists",
			label:      "realm",
			existsPost: true,
			want:       "- realm cgroup: already existed",
		},
		{
			name:       "missing-with-history",
			label:      "realm",
			existedPre: true,
			want:       "- realm cgroup: missing (was previously present)",
		},
		{
			name:  "missing",
			label: "realm",
			want:  "- realm cgroup: missing",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintCgroupAction(cmd, tc.label, tc.existedPre, tc.existsPost, tc.created)
			got := buf.String()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("output %q missing %q", got, tc.want)
			}
		})
	}
}

func TestRunInitErrors(t *testing.T) {
	testCases := []struct {
		name        string
		setup       func(*testing.T, *cobra.Command)
		wantErr     error
		expectError bool
	}{
		{
			name: "missing logger",
			setup: func(_ *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			wantErr:     errdefs.ErrLoggerNotFound,
			expectError: true,
		},
		{
			name: "controller failure",
			setup: func(t *testing.T, cmd *cobra.Command) {
				t.Cleanup(viper.Reset)
				tmp := t.TempDir()
				viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, tmp)
				viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(tmp, "missing.sock"))
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "init"}
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			tc.setup(t, cmd)

			err := RunInit(cmd)
			if tc.expectError && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if tc.name == "controller failure" && (err == nil || errors.Is(err, errdefs.ErrLoggerNotFound)) {
				t.Fatalf("controller failure case should propagate controller error, got %v", err)
			}
		})
	}
}

func TestResolveKukeondImage(t *testing.T) {
	origVersion := config.Version
	origRepo := config.KukeondImageRepo
	t.Cleanup(func() {
		config.Version = origVersion
		config.KukeondImageRepo = origRepo
		viper.Reset()
	})

	testCases := []struct {
		name     string
		version  string
		repo     string
		override string
		want     string
	}{
		{
			name:    "release-tag",
			version: "v1.2.3",
			repo:    "ghcr.io/eminwux/kukeon",
			want:    "ghcr.io/eminwux/kukeon:v1.2.3",
		},
		{
			name:    "dev-version-falls-back-to-latest",
			version: "0.1.0",
			repo:    "ghcr.io/eminwux/kukeon",
			want:    "ghcr.io/eminwux/kukeon:latest",
		},
		{
			name:    "fork-repo-injected-via-ldflags",
			version: "v2.0.0",
			repo:    "ghcr.io/forked-org/kukeon",
			want:    "ghcr.io/forked-org/kukeon:v2.0.0",
		},
		{
			name:     "flag-override-wins",
			version:  "v1.2.3",
			repo:     "ghcr.io/eminwux/kukeon",
			override: "my.registry/custom/kukeond:dev",
			want:     "my.registry/custom/kukeond:dev",
		},
		{
			name: "empty-repo-falls-back-to-default",
			// empty string in repo should revert to compiled-in default
			version: "v1.0.0",
			repo:    "",
			want:    "ghcr.io/eminwux/kukeon:v1.0.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			config.Version = tc.version
			config.KukeondImageRepo = tc.repo
			if tc.override != "" {
				viper.Set(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey, tc.override)
			}
			if got := ResolveKukeondImage(); got != tc.want {
				t.Fatalf("ResolveKukeondImage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func newOutputCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}
