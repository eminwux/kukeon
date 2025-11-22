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
func printRealmActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printSpaceActions github.com/eminwux/kukeon/cmd/kuke/init.printSpaceActions
func printSpaceActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printStackActions github.com/eminwux/kukeon/cmd/kuke/init.printStackActions
func printStackActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printCellActions github.com/eminwux/kukeon/cmd/kuke/init.printCellActions
func printCellActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printCNIActions github.com/eminwux/kukeon/cmd/kuke/init.printCNIActions
func printCNIActions(cmd *cobra.Command, report controller.BootstrapReport)

//go:linkname printDirAction github.com/eminwux/kukeon/cmd/kuke/init.printDirAction
func printDirAction(cmd *cobra.Command, label string, path string, created bool, existsPost bool)

//go:linkname printCgroupAction github.com/eminwux/kukeon/cmd/kuke/init.printCgroupAction
func printCgroupAction(cmd *cobra.Command, label string, existedPre bool, existsPost bool, created bool)

//go:linkname runInit github.com/eminwux/kukeon/cmd/kuke/init.runInit
func runInit(cmd *cobra.Command, args []string) error

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

func PrintRealmActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printRealmActions(cmd, report)
}

func PrintSpaceActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printSpaceActions(cmd, report)
}

func PrintStackActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printStackActions(cmd, report)
}

func PrintCellActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printCellActions(cmd, report)
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
			name: "initialized",
			report: controller.BootstrapReport{
				RealmCreated: true,
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
		RealmName:                "realm-a",
		RealmContainerdNamespace: "namespace-a",
		RunPath:                  "/tmp/run",
	}
	PrintOverview(cmd, report)
	got := buf.String()

	want := []string{
		"Realm: realm-a (namespace: namespace-a)",
		"Run path: /tmp/run",
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
		report   controller.BootstrapReport
		expected []string
	}{
		{
			name: "created",
			report: controller.BootstrapReport{
				RealmCreated:                       true,
				RealmContainerdNamespaceCreated:    true,
				RealmCgroupCreated:                 true,
				RealmCgroupExistsPost:              true,
				RealmContainerdNamespaceExistsPost: true,
			},
			expected: []string{
				"- realm: created",
				"- containerd namespace: created",
				"- realm cgroup: created",
			},
		},
		{
			name: "already-existed",
			report: controller.BootstrapReport{
				RealmCgroupExistsPre:               true,
				RealmCgroupExistsPost:              true,
				RealmContainerdNamespaceExistsPre:  true,
				RealmContainerdNamespaceExistsPost: true,
			},
			expected: []string{
				"- realm: already existed",
				"- containerd namespace: already existed",
				"- realm cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintRealmActions(cmd, tc.report)
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
		report   controller.BootstrapReport
		expected []string
	}{
		{
			name: "created",
			report: controller.BootstrapReport{
				SpaceCNINetworkName:    "net1",
				SpaceCreated:           true,
				SpaceCNINetworkCreated: true,
				SpaceCgroupCreated:     true,
			},
			expected: []string{
				"- space: created",
				"- network \"net1\": created",
				"- space cgroup: created",
			},
		},
		{
			name: "exists",
			report: controller.BootstrapReport{
				SpaceCNINetworkName:       "net2",
				SpaceMetadataExistsPre:    true,
				SpaceMetadataExistsPost:   true,
				SpaceCNINetworkExistsPre:  true,
				SpaceCNINetworkExistsPost: true,
				SpaceCgroupExistsPre:      true,
				SpaceCgroupExistsPost:     true,
			},
			expected: []string{
				"- space: already existed",
				"- network \"net2\": already existed",
				"- space cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintSpaceActions(cmd, tc.report)
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
		report   controller.BootstrapReport
		expected []string
	}{
		{
			name: "created",
			report: controller.BootstrapReport{
				StackCreated:          true,
				StackCgroupCreated:    true,
				StackCgroupExistsPost: true,
			},
			expected: []string{
				"- stack: created",
				"- stack cgroup: created",
			},
		},
		{
			name: "already",
			report: controller.BootstrapReport{
				StackMetadataExistsPre:  true,
				StackMetadataExistsPost: true,
				StackCgroupExistsPre:    true,
				StackCgroupExistsPost:   true,
			},
			expected: []string{
				"- stack: already existed",
				"- stack cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintStackActions(cmd, tc.report)
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
		report   controller.BootstrapReport
		expected []string
	}{
		{
			name: "created",
			report: controller.BootstrapReport{
				CellCreated:                 true,
				CellCgroupCreated:           true,
				CellRootContainerCreated:    true,
				CellStarted:                 true,
				CellCgroupExistsPost:        true,
				CellRootContainerExistsPost: true,
				CellStartedPost:             true,
			},
			expected: []string{
				"- cell: created",
				"- cell cgroup: created",
				"- cell root container cgroup: created",
				"- cell containers cgroup: created",
			},
		},
		{
			name: "already",
			report: controller.BootstrapReport{
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
				"- cell: already existed",
				"- cell cgroup: already existed",
				"- cell root container cgroup: already existed",
				"- cell containers cgroup: already existed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, buf := newOutputCommand()
			PrintCellActions(cmd, tc.report)
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

func newOutputCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	return cmd, buf
}
