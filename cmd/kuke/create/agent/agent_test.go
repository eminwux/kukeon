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

package agent_test

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	agentcmd "github.com/eminwux/kukeon/cmd/kuke/create/agent"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

type fakeClient struct {
	kukeonv1.FakeClient

	created  []v1beta1.CellConfigDoc
	started  int
	startErr error
}

func (f *fakeClient) Close() error { return nil }

func (f *fakeClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	if doc.Metadata.Name != "default-agent-claude" {
		return kukeonv1.GetConfigResult{}, nil
	}
	return kukeonv1.GetConfigResult{MetadataExists: true, Config: v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{
			Name: "default-agent-claude", Realm: "default", Space: "default", Stack: "default",
			Labels: map[string]string{
				v1beta1.LabelTeam: "default", v1beta1.LabelTemplate: "true",
				v1beta1.LabelRole: "agent", v1beta1.LabelHarness: "claude",
			},
		},
	}}, nil
}

func (f *fakeClient) CreateConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.CreateConfigResult, error) {
	f.created = append(f.created, doc)
	return kukeonv1.CreateConfigResult{Config: doc, Created: true}, nil
}

func (f *fakeClient) CreateCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	return kukeonv1.CreateCellFromConfigResult{CreateCellResult: kukeonv1.CreateCellResult{Cell: v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: req.Config.Name + "-4d5e6f"},
		Spec:     v1beta1.CellSpec{RealmID: req.Config.Realm, SpaceID: req.Config.Space, StackID: req.Config.Stack},
	}}}, nil
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	f.started++
	return kukeonv1.StartCellResult{Cell: doc, Started: true}, f.startErr
}

func runCmd(t *testing.T, f *fakeClient, attach agentcmd.AttachFunc, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(t.Context(), agentcmd.MockControllerKey{}, kukeonv1.Client(f))
	if attach != nil {
		ctx = context.WithValue(ctx, agentcmd.MockAttachKey{}, attach)
	}
	cmd := agentcmd.NewAgentCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCreateAgent(t *testing.T) {
	f := &fakeClient{}
	out, err := runCmd(t, f, nil, "--harness", "claude", "--name", "Reviewer")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.created) != 1 || f.started != 1 {
		t.Fatalf("created %d configs, started %d cells", len(f.created), f.started)
	}
	cfg := f.created[0]
	if !regexp.MustCompile(`^default-agent-claude-[0-9a-f]{6}$`).MatchString(cfg.Metadata.Name) ||
		cfg.Metadata.Annotations[v1beta1.AnnotationDisplayName] != "Reviewer" {
		t.Fatalf("config = %+v", cfg.Metadata)
	}
	for _, want := range []string{
		`Config "` + cfg.Metadata.Name + `" (Reviewer) stored in default/default/default, from template default-agent-claude`,
		`Cell "` + cfg.Metadata.Name + `-4d5e6f" created and started`,
		"kuke attach " + cfg.Metadata.Name + "-4d5e6f --realm default --space default --stack default",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCreateAgentAttach(t *testing.T) {
	f := &fakeClient{}
	var attached []string
	attach := func(_ *cobra.Command, _ kukeonv1.Client, doc v1beta1.CellDoc) error {
		attached = append(attached, doc.Metadata.Name)
		return nil
	}
	out, err := runCmd(t, f, attach, "--harness", "claude", "-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(attached) != 1 || attached[0] != f.created[0].Metadata.Name+"-4d5e6f" ||
		strings.Contains(out, "kuke attach") {
		t.Fatalf("attached %v, output:\n%s", attached, out)
	}
	if _, set := f.created[0].Metadata.Annotations[v1beta1.AnnotationDisplayName]; set {
		t.Fatal("a display name was stored without --name")
	}
}

func TestCreateAgentFlagsAndFailures(t *testing.T) {
	if _, err := runCmd(t, &fakeClient{}, nil); err == nil || !strings.Contains(err.Error(), "--harness is required") {
		t.Fatalf("no harness: %v", err)
	}
	if _, err := runCmd(t, &fakeClient{}, nil, "--harness", "opencode"); !errors.Is(err, errdefs.ErrConfig) {
		t.Fatalf("unknown harness: %v", err)
	}
	if _, err := runCmd(t, &fakeClient{}, nil, "--harness", "codex"); !errors.Is(
		err,
		errdefs.ErrAgentTemplateNotFound,
	) {
		t.Fatalf("missing template: %v", err)
	}
	// A start failure keeps and names the stored Config.
	f := &fakeClient{startErr: errors.New("no room")}
	out, err := runCmd(t, f, nil, "--harness", "claude")
	if err == nil || len(f.created) != 1 || !strings.Contains(out, `Config "`+f.created[0].Metadata.Name+`"`) ||
		!strings.Contains(err.Error(), "starting it failed") {
		t.Fatalf("start failure: %v\n%s", err, out)
	}
}
