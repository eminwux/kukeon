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

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/platforms"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestKuke_RunRuntimeEnvReuse checks the actual attachable shell, rather than
// only the OCI record: every run gets its own env while the writable snapshot
// and authored metadata remain intact. Regression for #1369.
func TestKuke_RunRuntimeEnvReuse(t *testing.T) {
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realm, space, stack, cell := generateUniqueRealmName(
		t,
	), generateUniqueSpaceName(
		t,
	), generateUniqueStackName(
		t,
	), generateUniqueCellName(
		t,
	)
	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realm, space, stack, cell)
		cleanupStackNoDaemon(t, runPath, realm, space, stack)
		cleanupSpaceNoDaemon(t, runPath, realm, space)
		cleanupRealmNoDaemon(t, runPath, realm)
	})
	invoke := func(args ...string) []byte {
		t.Helper()
		return runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), args...)...)
	}
	invoke("create", "realm", realm)
	invoke("create", "space", space, "--realm", realm)
	invoke("create", "stack", stack, "--realm", realm, "--space", space)
	namespace := realm + ".kukeon.io"
	imageRef := "kukeon.internal/runtime-env:latest"
	imageClient, imageErr := containerd.New("/run/containerd/containerd.sock")
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	defer imageClient.Close()
	imageCtx := namespaces.WithNamespace(context.Background(), namespace)
	source, imageErr := imageClient.Pull(imageCtx, "docker.io/library/busybox:latest", containerd.WithPullUnpack)
	if imageErr != nil {
		t.Fatal(imageErr)
	}
	publishRuntimeEnvImage(imageCtx, t, imageClient, source, imageRef, "original")
	fixture := filepath.Join(t.TempDir(), "runtime-env.yaml")
	doc := fmt.Sprintf(`apiVersion: v1beta1
kind: Cell
metadata:
  name: %s
spec:
  id: %s
  realmId: %s
  spaceId: %s
  stackId: %s
  containers:
    - id: root
      root: true
      image: docker.io/library/busybox:latest
      command: sleep
      args: ["3600"]
    - id: work
      image: %s
      command: /bin/sh
      attachable: true
      env: ["VALUE=spec"]
`, cell, cell, realm, space, stack, imageRef)
	if err := os.WriteFile(fixture, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := []string{"--realm", realm, "--space", space, "--stack", stack}
	var baselineSpec any
	type runtimeRecord struct {
		SnapshotKey string            `json:"SnapshotKey"`
		Labels      map[string]string `json:"Labels"`
		CreatedAt   string            `json:"CreatedAt"`
	}
	var baselineRecord runtimeRecord
	for i, expected := range []string{"first", "second", "spec"} {
		args := []string{"run", cell, "-d"}
		if i == 0 {
			args = []string{"run", "-f", fixture, "-d"}
		}
		if i < 2 {
			args = append(args, "--env", "VALUE="+expected, "--env", "RUNTIME_ONLY="+expected)
		}
		if i == 0 {
			args = append(args, "--env", "IMAGE_BASE=runtime")
		}
		invoke(append(args, scope...)...)
		output := invoke(append([]string{"get", "cell", cell, "-o", "json"}, scope...)...)
		authored, err := parseCellJSON(t, output)
		if err != nil {
			t.Fatal(err)
		}
		// Compare the entire authored spec, including env, after every invocation.
		var raw map[string]any
		if err = json.Unmarshal(output, &raw); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			baselineSpec = raw["spec"]
		} else if !reflect.DeepEqual(baselineSpec, raw["spec"]) {
			t.Fatalf("run changed persisted spec: %s", output)
		}
		for _, c := range authored.Spec.Containers {
			if c.ID != "work" {
				continue
			}
			if !reflect.DeepEqual(c.Env, []string{"VALUE=spec"}) {
				t.Fatalf("runtime env persisted: %v", c.Env)
			}
			ctrID := c.ContainerdID
			info, infoErr := exec.Command("ctr", "--namespace", realm+".kukeon.io", "containers", "info", ctrID).
				CombinedOutput()
			if infoErr != nil {
				t.Fatalf("container info: %v: %s", infoErr, info)
			}
			var record runtimeRecord
			if err = json.Unmarshal(info, &record); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				baselineRecord = record
			} else if !reflect.DeepEqual(record, baselineRecord) {
				t.Fatalf("runtime record replaced or labels changed: %s", info)
			}
		}
		waitForSocket(t, fs.ContainerSocketSymlinkPath(runPath, realm, space, stack, cell, "work"), 10*time.Second)
		session := startPTY(t, nil, kuke, append(append(buildKukeDaemonArgs(host), "attach", cell), scope...)...)
		t.Cleanup(session.Close)
		time.Sleep(attachConnectGrace)
		command := "printf 'ENV_RESULT:%s:%s\\n' \"$VALUE\" \"${RUNTIME_ONLY-unset}\"; "
		if i == 0 {
			command += "printf kept > /tmp/runtime-env-overlay; "
		}
		command += "printf 'IMAGE_RESULT:%s\\n' \"$IMAGE_BASE\"; printf 'OVERLAY_RESULT:%s\\n' \"$(cat /tmp/runtime-env-overlay)\"\n"
		if err = session.Write([]byte(command)); err != nil {
			t.Fatal(err)
		}
		runtimeOnly := expected
		if i == 2 {
			runtimeOnly = "unset"
		}
		if err = session.WaitForOutput([]byte("ENV_RESULT:"+expected+":"+runtimeOnly), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		imageDefault := "original"
		if i == 0 {
			imageDefault = "runtime"
		}
		if err = session.WaitForOutput([]byte("IMAGE_RESULT:"+imageDefault), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err = session.WaitForOutput([]byte("OVERLAY_RESULT:kept"), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err = session.Write(sbshDetachSequence); err != nil {
			t.Fatal(err)
		}
		code, out, err := session.Wait(attachExitTimeout)
		if err != nil || code != 0 {
			t.Fatalf("detach: code=%d err=%v output=%s", code, err, out)
		}
		invoke(append([]string{"stop", cell}, scope...)...)
		if i == 0 {
			publishRuntimeEnvImage(imageCtx, t, imageClient, source, imageRef, "repointed")
		}
		if i == 1 {
			if err = imageClient.ImageService().Delete(imageCtx, imageRef); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// publishRuntimeEnvImage changes only the image config, leaving its rootfs
// layers intact. Repointing this tag must not change a reused cell's defaults.
func publishRuntimeEnvImage(
	ctx context.Context,
	t *testing.T,
	client *containerd.Client,
	source containerd.Image,
	ref, value string,
) {
	t.Helper()
	// Keep the new config and manifest protected until the image record is
	// created. Without a lease, containerd GC can collect these blobs in the
	// gap between writing them and publishing the image that references them.
	ctx, releaseLease, err := client.WithLease(ctx)
	if err != nil {
		t.Fatalf("create image content lease: %v", err)
	}
	defer func() {
		if releaseErr := releaseLease(ctx); releaseErr != nil {
			t.Errorf("release image content lease: %v", releaseErr)
		}
	}()

	manifest, err := images.Manifest(ctx, client.ContentStore(), source.Target(), platforms.Default())
	if err != nil {
		t.Fatal(err)
	}
	blob, err := content.ReadBlob(ctx, client.ContentStore(), manifest.Config)
	if err != nil {
		t.Fatal(err)
	}
	var config imagespec.Image
	if err = json.Unmarshal(blob, &config); err != nil {
		t.Fatal(err)
	}
	config.Config.Env = append(config.Config.Env, "IMAGE_BASE="+value)
	blob, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := imagespec.Descriptor{
		MediaType: imagespec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	if err = content.WriteBlob(ctx, client.ContentStore(), "config-"+value, bytes.NewReader(blob), descriptor); err != nil {
		t.Fatal(err)
	}
	manifest.Config = descriptor
	manifest.MediaType = imagespec.MediaTypeImageManifest
	blob, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	descriptor = imagespec.Descriptor{
		MediaType: imagespec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	labels := map[string]string{"containerd.io/gc.ref.content.config": manifest.Config.Digest.String()}
	for i, layer := range manifest.Layers {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.l.%d", i)] = layer.Digest.String()
	}
	if err = content.WriteBlob(ctx, client.ContentStore(), "manifest-"+value, bytes.NewReader(blob), descriptor, content.WithLabels(labels)); err != nil {
		t.Fatal(err)
	}
	image := images.Image{Name: ref, Target: descriptor}
	if _, err = client.ImageService().Create(ctx, image); err != nil {
		if _, err = client.ImageService().Update(ctx, image, "target"); err != nil {
			t.Fatal(err)
		}
	}
}
