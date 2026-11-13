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

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestManualCellPreviewAndCreateLeaveDefaultRootToDaemon(t *testing.T) {
	client := &creationClient{
		materializeCreated: true,
		materializedCell: v1beta1.CellDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindCell,
			Metadata:   v1beta1.CellMetadata{Name: "manual-cell"},
			Spec: v1beta1.CellSpec{
				ID: "manual-cell", RealmID: "realm", SpaceID: "space", StackID: "stack", RootContainerID: "root",
				Containers: []v1beta1.ContainerSpec{{ID: "root", Image: "kukeon.internal/kukepause:dev", Root: true}},
			},
		},
	}
	handler := testCreateHandler(t, client)
	input := manualCellRequest{
		Name: "manual-cell", Realm: "realm", Space: "space", Stack: "stack",
		Labels: map[string]string{"team": "platform"},
		Spec:   manualCellSpec{NestedCgroupRuntime: true, AutoDelete: true},
	}
	preview := postCreate(t, handler, "/api/cells/manual/preview", input)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var response struct {
		Preview  string          `json:"preview"`
		Document v1beta1.CellDoc `json:"document"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Document.Spec.RealmID != "realm" || response.Document.Spec.NestedCgroupRuntime != true {
		t.Fatalf("preview document omitted requested spec: %+v", response.Document.Spec)
	}
	if len(response.Document.Spec.Containers) != 0 || response.Document.Spec.RootContainerID != "" {
		t.Fatalf("web server injected a root container: %+v", response.Document.Spec)
	}

	input.Preview = response.Preview
	created := postCreate(t, handler, "/api/cells/manual/create", input)
	if created.Code != http.StatusOK || client.created != 1 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if client.doc.Metadata.Name != "manual-cell" || len(client.doc.Spec.Containers) != 0 ||
		client.doc.Spec.RootContainerID != "" {
		t.Fatalf("manual request was changed before MaterializeCell: %+v", client.doc)
	}
	var returned v1beta1.CellDoc
	if err := json.Unmarshal(created.Body.Bytes(), &returned); err != nil {
		t.Fatal(err)
	}
	if returned.Spec.RootContainerID != "root" || len(returned.Spec.Containers) != 1 ||
		!returned.Spec.Containers[0].Root {
		t.Fatalf("response omitted the root container returned by Kukeon: %+v", returned.Spec)
	}
}

func TestManualCellAcceptsCompleteContainerSpecification(t *testing.T) {
	client := &creationClient{}
	backoff, retries, memory, cpuShares, pids := int64(4), int64(2), int64(1<<30), int64(512), int64(256)
	input := manualCellRequest{
		Name: "complex-cell", Realm: "realm", Space: "space", Stack: "stack",
		Labels:      map[string]string{"team": "platform"},
		Annotations: map[string]string{"owner": "ops"},
		Spec: manualCellSpec{
			RootContainerID: "app",
			Tty:             &v1beta1.CellTty{Default: "app"},
			Containers: []v1beta1.ContainerSpec{{
				ID: "app", Root: true, Image: "example/app:latest", Command: "/entrypoint",
				Args: []string{
					"serve",
					"--http",
				}, WorkingDir: "/srv/app", Env: []string{"MODE=prod", "TOKEN=secret-value"},
				Ports: []string{"8080/tcp"},
				Volumes: []v1beta1.VolumeMount{
					{Kind: v1beta1.VolumeKindBind, Source: "/srv/data", Target: "/data", ReadOnly: true},
				},
				Networks: []string{"frontend"}, NetworksAliases: []string{"app-alias"},
				Privileged: false, HostNetwork: false, HostPID: true, HostCgroup: true,
				User: "1000:1000", ReadOnlyRootFilesystem: true,
				Capabilities: &v1beta1.ContainerCapabilities{Drop: []string{"ALL"}, Add: []string{"NET_BIND_SERVICE"}},
				SecurityOpts: []string{"no-new-privileges:true"}, Devices: []string{"/dev/kvm"},
				Tmpfs: []v1beta1.ContainerTmpfsMount{{Path: "/run", SizeBytes: 4096, Options: []string{"noexec"}}},
				Resources: &v1beta1.ContainerResources{
					MemoryLimitBytes: &memory,
					CPUShares:        &cpuShares,
					PidsLimit:        &pids,
				},
				Secrets: []v1beta1.ContainerSecret{
					{Name: "token", SecretRef: &v1beta1.ContainerSecretRef{Name: "app-token", Realm: "realm"}},
				},
				Repos: []v1beta1.ContainerRepo{
					{Name: "source", Target: "/srv/source", URL: "https://example.test/repo.git", Required: true},
				},
				Git: &v1beta1.ContainerGit{
					Author:     &v1beta1.GitIdentity{Name: "Operator", Email: "operator@example.test"},
					Committer:  &v1beta1.GitIdentity{Name: "Operator", Email: "operator@example.test"},
					SigningKey: "/home/operator/.ssh/id_ed25519", Sign: []string{v1beta1.GitSignCommits},
					AllowedSigners: "/home/operator/.ssh/allowed_signers",
				},
				CNIConfigPath: "/etc/cni/net.d/custom.conflist", RestartPolicy: "on-failure",
				RestartBackoffSeconds: &backoff, RestartMaxRetries: &retries,
				Attachable: true, Tty: &v1beta1.ContainerTty{Prompt: "app> ", LogLevel: "info", OnInit: []v1beta1.TtyStage{{Script: "echo ready", RunOn: v1beta1.RunOnStart}}},
			}},
			AutoDelete: true, NestedCgroupRuntime: true, IgnoreDiskPressure: true,
		},
	}
	doc, err := manualCell(context.Background(), client, input)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Spec.RootContainerID != "app" || doc.Spec.Containers[0].ID != "app" || !doc.Spec.Containers[0].Root {
		t.Fatalf("explicit root was not preserved: %+v", doc.Spec)
	}
	if doc.Spec.Containers[0].Resources.MemoryLimitBytes == nil ||
		*doc.Spec.Containers[0].Resources.MemoryLimitBytes != memory {
		t.Fatalf("resource limits were not preserved: %+v", doc.Spec.Containers[0].Resources)
	}
	if !doc.Spec.AutoDelete || !doc.Spec.NestedCgroupRuntime || !doc.Spec.IgnoreDiskPressure {
		t.Fatalf("cell-level settings were not preserved: %+v", doc.Spec)
	}
}

func TestManualCellRejectsDaemonFieldsAndConflictingRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input manualCellRequest
	}{
		{name: "containerd id", input: manualCellRequest{Name: "c", Realm: "r", Space: "s", Stack: "t", Spec: manualCellSpec{Containers: []v1beta1.ContainerSpec{{ID: "app", Image: "busybox", ContainerdID: "forged"}}}}},
		{name: "conflicting root", input: manualCellRequest{Name: "c", Realm: "r", Space: "s", Stack: "t", Spec: manualCellSpec{RootContainerID: "app", Containers: []v1beta1.ContainerSpec{{ID: "app", Image: "busybox", Root: false}, {ID: "other", Image: "busybox", Root: true}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &creationClient{}
			if _, err := manualCell(context.Background(), client, tc.input); err == nil {
				t.Fatal("invalid manual specification was accepted")
			}
			if client.created != 0 {
				t.Fatal("invalid specification reached MaterializeCell")
			}
		})
	}
}

func TestManualCellAPIRejectsRuntimeFieldsAndRedactsSecrets(t *testing.T) {
	client := &creationClient{materializeCreated: true, materializeErr: errors.New("failed with secret-value")}
	handler := testCreateHandler(t, client)
	input := manualCellRequest{
		Name: "safe-cell", Realm: "realm", Space: "space", Stack: "stack",
		Spec: manualCellSpec{
			Containers: []v1beta1.ContainerSpec{{ID: "app", Image: "example/app", Env: []string{"TOKEN=secret-value"}}},
		},
	}
	preview := postCreate(t, handler, "/api/cells/manual/preview", input)
	if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), "secret-value") {
		t.Fatalf("preview exposed an environment value: %d %s", preview.Code, preview.Body.String())
	}
	var previewResult struct {
		Preview string `json:"preview"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewResult); err != nil {
		t.Fatal(err)
	}
	input.Preview = previewResult.Preview
	created := postCreate(t, handler, "/api/cells/manual/create", input)
	if created.Code != http.StatusServiceUnavailable || strings.Contains(created.Body.String(), "secret-value") ||
		!strings.Contains(created.Body.String(), "[redacted]") {
		t.Fatalf(
			"creation error did not safely redact the environment value: %d %s",
			created.Code,
			created.Body.String(),
		)
	}

	bad := `{"name":"safe-cell","realm":"realm","space":"space","stack":"stack","status":{"state":1},"spec":{}}`
	response := postCreateRaw(t, handler, "/api/cells/manual/preview", bad, "http://localhost:5129")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("runtime status was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestManualCellConflictUsesConflictStatus(t *testing.T) {
	client := &creationClient{existing: true}
	handler := testCreateHandler(t, client)
	input := manualCellRequest{Name: "occupied", Realm: "realm", Space: "space", Stack: "stack"}
	response := postCreate(t, handler, "/api/cells/manual/preview", input)
	if response.Code != http.StatusConflict {
		t.Fatalf("existing target status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
}

func testCreateHandler(t *testing.T, client *creationClient) http.Handler {
	t.Helper()
	handler, err := newHandler(
		"localhost:5129",
		"test",
		func(context.Context) (kukeonv1.Client, error) { return client, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func postCreate(t *testing.T, handler http.Handler, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return postCreateRaw(t, handler, path, string(data), "http://localhost:5129")
}

func postCreateRaw(t *testing.T, handler http.Handler, path, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://localhost:5129"+path, strings.NewReader(body))
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
