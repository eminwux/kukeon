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

package config_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	configcmd "github.com/eminwux/kukeon/cmd/kuke/create/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestNewConfigCmd_RequiresFromBlueprint(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := configcmd.NewConfigCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(ctxWithFake(t, &fakeClient{}))

	cmd.SetArgs([]string{"myconfig"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "--from-blueprint is required") {
		t.Fatalf("expected --from-blueprint required error, got %v", err)
	}
}

func TestNewConfigCmd_BlueprintNotFound(t *testing.T) {
	t.Cleanup(viper.Reset)

	cases := []struct {
		name string
		fake *fakeClient
	}{
		{
			name: "ErrBlueprintNotFound sentinel",
			fake: &fakeClient{
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{}, errdefs.ErrBlueprintNotFound
				},
			},
		},
		{
			name: "MetadataExists=false",
			fake: &fakeClient{
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			cmd := configcmd.NewConfigCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetContext(ctxWithFake(t, tc.fake))
			_ = cmd.Flags().Set("from-blueprint", "missing")
			_ = cmd.Flags().Set("realm", "r1")
			_ = cmd.Flags().Set("space", "s1")
			_ = cmd.Flags().Set("stack", "st1")

			cmd.SetArgs([]string{"myconfig"})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, errdefs.ErrBlueprintNotFound) {
				t.Fatalf("expected ErrBlueprintNotFound, got %v", err)
			}
			for _, want := range []string{`"missing"`, `realm="r1"`, `space="s1"`, `stack="st1"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error message missing %q\nGot: %v", want, err)
				}
			}
		})
	}
}

func TestNewConfigCmd_LookupScope(t *testing.T) {
	t.Cleanup(viper.Reset)

	var captured v1beta1.CellBlueprintMetadata
	fake := &fakeClient{
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			captured = doc.Metadata
			return kukeonv1.GetBlueprintResult{
				Blueprint:      v1beta1.CellBlueprintDoc{Metadata: doc.Metadata},
				MetadataExists: true,
			}, nil
		},
	}

	cmd := configcmd.NewConfigCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(ctxWithFake(t, fake))

	_ = cmd.Flags().Set("from-blueprint", "bp")
	_ = cmd.Flags().Set("realm", "production")
	_ = cmd.Flags().Set("space", "team-a")
	_ = cmd.Flags().Set("stack", "web")

	cmd.SetArgs([]string{"myconfig"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Name != "bp" {
		t.Errorf("lookup name = %q, want %q", captured.Name, "bp")
	}
	if captured.Realm != "production" || captured.Space != "team-a" || captured.Stack != "web" {
		t.Errorf("lookup scope = %+v, want realm=production space=team-a stack=web", captured)
	}
}

func TestNewConfigCmd_EmitsParsableYAML(t *testing.T) {
	t.Cleanup(viper.Reset)

	cases := []struct {
		name             string
		blueprint        v1beta1.CellBlueprintDoc
		wantContains     []string
		wantNotContains  []string
		wantTODORequired []string // parameter names expected to render as "TODO (required, no default)"
		wantTODOOptional []string // parameter names expected to render as "optional (no default)"
		wantDefaults     map[string]string
		wantValuesBlock  string // exact text the values block must contain, or "" to skip
	}{
		{
			name: "no parameters emits empty values map",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "alpine"}},
					},
				},
			},
			wantContains: []string{"values: {}"},
		},
		{
			name: "required-no-default emits TODO marker",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Parameters: []v1beta1.CellProfileParameter{
						{Name: "TOKEN", Required: true},
					},
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "alpine"}},
					},
				},
			},
			wantTODORequired: []string{"TOKEN"},
		},
		{
			name: "all-optional with defaults pre-fills",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Parameters: []v1beta1.CellProfileParameter{
						{Name: "PORT", Default: ptr("5432")},
						{Name: "ENV", Default: ptr("production")},
						{Name: "TAGS", Default: ptr("a,b c")},
					},
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "alpine"}},
					},
				},
			},
			wantDefaults: map[string]string{"PORT": "5432", "ENV": "production", "TAGS": `"a,b c"`},
		},
		{
			name: "optional-no-default carries hint comment",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Parameters: []v1beta1.CellProfileParameter{
						{Name: "EXTRA"},
					},
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "alpine"}},
					},
				},
			},
			wantTODOOptional: []string{"EXTRA"},
		},
		{
			name: "repo slot emits TODO under spec.repos",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{
							{
								ID:    "main",
								Image: "alpine",
								Repos: []v1beta1.ContainerRepo{
									{Name: "project", Target: "/src/project", Required: true}, // url empty → slot
									{
										Name:   "vendored",
										Target: "/src/vendored",
										URL:    "git@x:y.git",
									}, // inline url, not a slot
								},
							},
						},
					},
				},
			},
			wantContains: []string{
				"repos:",
				"project: # required repo slot (container \"main\")",
				`url: ""`,
				"# TODO",
			},
			wantNotContains: []string{"vendored:"}, // inline-url repo is not a slot, must not appear
		},
		{
			name: "secret slot emits TODO under spec.secrets with consumption hint",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{
							{
								ID:    "main",
								Image: "alpine",
								Secrets: []v1beta1.BlueprintSecretSlot{
									{Name: "api-token", Mode: "env", EnvName: "API_TOKEN", Required: true},
									{Name: "tls-cert", Mode: "file", MountPath: "/etc/tls/cert.pem"},
								},
							},
						},
					},
				},
			},
			wantContains: []string{
				"secrets:",
				`api-token: # required secret slot (container "main", env "API_TOKEN")`,
				`tls-cert: # optional secret slot (container "main", file mount "/etc/tls/cert.pem")`,
				"secretRef:",
				`name: "" # TODO`,
				`realm: "" # TODO`,
			},
		},
		{
			name: "duplicate slot name across containers collapses to one entry (required wins)",
			blueprint: v1beta1.CellBlueprintDoc{
				Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "r1"},
				Spec: v1beta1.CellBlueprintSpec{
					Cell: v1beta1.BlueprintCellSpec{
						Containers: []v1beta1.BlueprintContainer{
							{
								ID:    "first",
								Image: "alpine",
								Secrets: []v1beta1.BlueprintSecretSlot{
									{Name: "shared", Mode: "env", EnvName: "SHARED"},
								},
							},
							{
								ID:    "second",
								Image: "alpine",
								Secrets: []v1beta1.BlueprintSecretSlot{
									{Name: "shared", Mode: "env", EnvName: "SHARED", Required: true},
								},
							},
						},
					},
				},
			},
			wantContains: []string{
				`shared: # required secret slot (container "first", "second", env "SHARED")`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			fake := &fakeClient{
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{Blueprint: tc.blueprint, MetadataExists: true}, nil
				},
			}

			cmd := configcmd.NewConfigCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetContext(ctxWithFake(t, fake))
			_ = cmd.Flags().Set("from-blueprint", tc.blueprint.Metadata.Name)
			_ = cmd.Flags().Set("realm", tc.blueprint.Metadata.Realm)

			cmd.SetArgs([]string{"myconfig"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			out := buf.String()

			for _, want := range tc.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
			for _, unwanted := range tc.wantNotContains {
				if strings.Contains(out, unwanted) {
					t.Errorf("output unexpectedly contains %q\nGot:\n%s", unwanted, out)
				}
			}
			for _, name := range tc.wantTODORequired {
				wantLine := name + ": \"\" # TODO (required, no default)"
				if !strings.Contains(out, wantLine) {
					t.Errorf("missing required-TODO marker for %q\nWanted line: %q\nGot:\n%s", name, wantLine, out)
				}
			}
			for _, name := range tc.wantTODOOptional {
				wantLine := name + ": \"\" # optional (no default)"
				if !strings.Contains(out, wantLine) {
					t.Errorf("missing optional hint for %q\nWanted line: %q\nGot:\n%s", name, wantLine, out)
				}
			}
			for key, val := range tc.wantDefaults {
				wantLine := "    " + key + ": " + val + "\n"
				if !strings.Contains(out, wantLine) {
					t.Errorf("missing default for %q\nWanted line: %q\nGot:\n%s", key, wantLine, out)
				}
			}

			// Round-trip: every emitted scaffold must parse cleanly through the
			// same loader `kuke apply -f` uses, as a CellConfig document.
			doc, err := parser.ParseDocument(0, buf.Bytes())
			if err != nil {
				t.Fatalf("ParseDocument failed on emitted scaffold: %v\nYAML:\n%s", err, out)
			}
			if doc.Kind != v1beta1.KindCellConfig {
				t.Fatalf("parsed kind = %q, want %q", doc.Kind, v1beta1.KindCellConfig)
			}
			if doc.CellConfigDoc == nil {
				t.Fatalf("expected CellConfigDoc, got nil")
			}
			if doc.CellConfigDoc.Metadata.Name != "myconfig" {
				t.Errorf("parsed metadata.name = %q, want %q", doc.CellConfigDoc.Metadata.Name, "myconfig")
			}
			if doc.CellConfigDoc.Spec.Blueprint.Name != tc.blueprint.Metadata.Name {
				t.Errorf("parsed spec.blueprint.name = %q, want %q",
					doc.CellConfigDoc.Spec.Blueprint.Name, tc.blueprint.Metadata.Name)
			}
			if doc.CellConfigDoc.Spec.Blueprint.Realm != tc.blueprint.Metadata.Realm {
				t.Errorf("parsed spec.blueprint.realm = %q, want %q",
					doc.CellConfigDoc.Spec.Blueprint.Realm, tc.blueprint.Metadata.Realm)
			}
		})
	}
}

func TestNewConfigCmd_BlueprintRefMatchesResolvedScope(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Cross-scope reference: operator asks --realm=consumer, but the Blueprint
	// returned by GetBlueprint lives in realm=shared. The emitted spec.blueprint
	// must record where the Blueprint actually lives (the resolved scope), not
	// the lookup-request scope — see CellConfigBlueprintRef's docstring about
	// cross-scope references.
	bp := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{Name: "shared-bp", Realm: "shared", Space: "platform"},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "alpine"}},
			},
		},
	}
	fake := &fakeClient{
		getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			// Daemon returns the canonical Blueprint with its own metadata scope.
			return kukeonv1.GetBlueprintResult{Blueprint: bp, MetadataExists: true}, nil
		},
	}

	cmd := configcmd.NewConfigCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(ctxWithFake(t, fake))
	_ = cmd.Flags().Set("from-blueprint", "shared-bp")
	_ = cmd.Flags().Set("realm", "consumer")

	cmd.SetArgs([]string{"myconfig"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	doc, err := parser.ParseDocument(0, buf.Bytes())
	if err != nil {
		t.Fatalf("ParseDocument failed: %v\n%s", err, buf.String())
	}

	// Config's own scope is the operator's request.
	if doc.CellConfigDoc.Metadata.Realm != "consumer" {
		t.Errorf("metadata.realm = %q, want %q", doc.CellConfigDoc.Metadata.Realm, "consumer")
	}
	// Blueprint ref records the Blueprint's actual scope, not the request's.
	if doc.CellConfigDoc.Spec.Blueprint.Realm != "shared" {
		t.Errorf("spec.blueprint.realm = %q, want %q", doc.CellConfigDoc.Spec.Blueprint.Realm, "shared")
	}
	if doc.CellConfigDoc.Spec.Blueprint.Space != "platform" {
		t.Errorf("spec.blueprint.space = %q, want %q", doc.CellConfigDoc.Spec.Blueprint.Space, "platform")
	}
}

func ctxWithFake(t *testing.T, fake *fakeClient) context.Context {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	return context.WithValue(ctx, configcmd.MockControllerKey{}, kukeonv1.Client(fake))
}

func ptr(s string) *string { return &s }

type fakeClient struct {
	kukeonv1.FakeClient

	getBlueprintFn func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context, doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}
