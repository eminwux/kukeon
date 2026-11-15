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

package launchcheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/distribution/reference"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

//go:embed probe.py
var probeScript string

const resource = "kukeon-operator-codex"

type executionReport struct {
	StartedAt      string       `json:"started_at"`
	Endpoint       string       `json:"endpoint"`
	RunnerRevision string       `json:"runner_revision"`
	RunnerDirty    bool         `json:"runner_dirty"`
	SourceRevision string       `json:"source_revision"`
	KukeVersion    string       `json:"kuke_version,omitempty"`
	Image          string       `json:"catalog_image,omitempty"`
	Digest         string       `json:"published_digest,omitempty"`
	Cell           string       `json:"cell,omitempty"`
	Commands       []string     `json:"commands"`
	Checks         []string     `json:"checks"`
	Failure        string       `json:"failure,omitempty"`
	Cleanup        []string     `json:"cleanup"`
	Probe          *probeResult `json:"probe,omitempty"`
	Outcome        string       `json:"outcome"`
}

type remoteDriver struct {
	t                                    *testing.T
	ctx                                  context.Context
	report                               *executionReport
	client                               kukeonv1.Client
	repo, home, host, binary, clientPath string
	cells                                []api.CellDoc
	secrets                              []api.SecretDoc
	blueprint                            api.CellBlueprintDoc
	pinned                               string
}

// TestRemoteOperator explicitly reserves an unused kukeon identity, checks init,
// and then requires a published image and authenticated operator evidence.
func TestRemoteOperator(t *testing.T) {
	host := os.Getenv("KUKETEAMS_REMOTE_HOST")
	if host == "" {
		t.Skip("set KUKETEAMS_REMOTE_HOST explicitly; see docs/team-remote-operator-check.md")
	}
	report := &executionReport{Outcome: "failed", StartedAt: time.Now().UTC().Format(time.RFC3339), Endpoint: host}
	t.Cleanup(func() { writeReport(t, report) })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	d := &remoteDriver{t: t, ctx: ctx, report: report, host: host}
	d.prepare()
	d.reserve()
	d.initialize()
	d.checkImageAndAuth()
	d.launch()
	d.waitForEvidence()
}

func writeReport(t *testing.T, report *executionReport) {
	t.Helper()
	if !t.Failed() {
		report.Outcome = "passed"
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal("encode sanitized execution report")
	}
	if destination := os.Getenv("KUKETEAMS_REMOTE_REPORT"); destination != "" {
		if err = os.WriteFile(destination, append(raw, '\n'), 0o600); err != nil {
			t.Error("write execution report")
		}
	}
	t.Log(string(raw))
}

func (d *remoteDriver) fail(message string) {
	d.t.Helper()
	d.report.Failure = message
	d.t.Fatal(message)
}

func (d *remoteDriver) run(name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(d.ctx, name, args...)
	// No ambient tokens, Git rewrites, client configuration, or KUKE_* overrides.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + d.home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	}
	cmd.Dir = d.home
	// Stderr may contain remote/auth details. Never print or persist it.
	return cmd.Output()
}

func (d *remoteDriver) prepare() {
	if !strings.HasPrefix(d.host, "unix:///") {
		d.fail("unmet prerequisite: an explicit local Unix kukeond endpoint is required")
	}
	repo, err := filepath.Abs("../../..")
	if err != nil {
		d.fail("resolve source checkout")
	}
	d.repo, d.home = repo, d.t.TempDir()
	raw, err := d.run("git", "-C", repo, "rev-parse", "HEAD")
	if err != nil {
		d.fail("unmet prerequisite: Git source revision unavailable")
	}
	d.report.RunnerRevision = strings.TrimSpace(string(raw))
	raw, err = d.run("git", "-C", repo, "status", "--porcelain")
	if err != nil {
		d.fail("cannot record driver worktree state")
	}
	d.report.RunnerDirty = len(bytes.TrimSpace(raw)) != 0
	revision := os.Getenv("KUKETEAMS_REMOTE_REVISION")
	if revision == "" {
		revision = d.report.RunnerRevision
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(revision) {
		d.fail("unmet prerequisite: source revision must be a full published Git commit SHA")
	}
	d.report.SourceRevision = revision
	d.binary = filepath.Join(repo, "kuke")
	if _, err = os.Stat(d.binary); err != nil {
		d.fail("unmet prerequisite: run make kuke first")
	}
	if _, err = exec.LookPath("kuke"); err != nil {
		d.fail("unmet prerequisite: kuke CLI is not installed")
	}
	raw, err = d.run("kuke", "--host", d.host, "version")
	if err != nil {
		d.fail("unmet prerequisite: kuke version/daemon handshake failed")
	}
	d.report.KukeVersion = strings.Join(
		regexp.MustCompile(`v[0-9][A-Za-z0-9.+-]*`).FindAllString(string(raw), -1),
		" / ",
	)
	d.client, err = kukeonv1.Dial(d.ctx, d.host)
	if err != nil {
		d.fail("unmet prerequisite: kukeond Unix endpoint cannot be configured")
	}
	d.t.Cleanup(func() { _ = d.client.Close() })
}

func (d *remoteDriver) reserve() {
	// Team pruning walks the whole daemon, so reserve kukeon globally.
	blueprints, err := d.client.ListBlueprints(d.ctx, "", "", "")
	if err != nil {
		d.fail("unmet prerequisite: cannot inspect blueprint scope")
	}
	for _, bp := range blueprints {
		if reserved(bp.Metadata.Name, bp.Metadata.Labels) {
			d.fail("unmet prerequisite: kukeon blueprint/team identity is already in use")
		}
	}
	configs, err := d.client.ListConfigs(d.ctx, "", "", "")
	if err != nil {
		d.fail("unmet prerequisite: cannot inspect config scope")
	}
	for _, cc := range configs {
		if reserved(cc.Metadata.Name, cc.Metadata.Labels) {
			d.fail("unmet prerequisite: kukeon config/team identity is already in use")
		}
	}
	d.cells, err = d.client.ListCells(d.ctx, "", "", "")
	if err != nil {
		d.fail("unmet prerequisite: cannot snapshot runtime cells")
	}
	for _, cell := range d.cells {
		if cell.Metadata.Labels[api.LabelTeam] == "kukeon" {
			d.fail("unmet prerequisite: an existing kukeon-team cell may share the test state")
		}
	}
	d.secrets, err = d.client.ListSecrets(d.ctx, "", "", "", "")
	if err != nil {
		d.fail("unmet prerequisite: cannot snapshot secret metadata")
	}
	d.t.Cleanup(d.cleanupDocuments)
}

func blueprintLookup() api.CellBlueprintDoc {
	return api.CellBlueprintDoc{
		Metadata: api.CellBlueprintMetadata{Name: resource, Realm: "default", Space: "default", Stack: "default"},
	}
}

func configLookup() api.CellConfigDoc {
	return api.CellConfigDoc{
		Metadata: api.CellConfigMetadata{Name: resource, Realm: "default", Space: "default", Stack: "default"},
	}
}

func (d *remoteDriver) cleanupDocuments() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// List first: missing resources are normal, including after a partial init.
	ccs, err := d.client.ListConfigs(ctx, "default", "default", "default")
	if err != nil {
		d.t.Error("cleanup: cannot inspect configs")
		return
	}
	for _, cc := range ccs {
		if cc.Metadata.Name != resource || cc.Metadata.Labels[api.LabelTeam] != "kukeon" {
			continue
		}
		if _, err = d.client.DeleteConfig(ctx, configLookup()); err != nil {
			d.t.Error("cleanup: delete test config failed")
		} else {
			d.report.Cleanup = append(d.report.Cleanup, "deleted test config "+resource)
		}
	}
	bps, err := d.client.ListBlueprints(ctx, "default", "default", "default")
	if err != nil {
		d.t.Error("cleanup: cannot inspect blueprints")
		return
	}
	for _, bp := range bps {
		if bp.Metadata.Name != resource || bp.Metadata.Labels[api.LabelTeam] != "kukeon" {
			continue
		}
		if _, err = d.client.DeleteBlueprint(ctx, blueprintLookup()); err != nil {
			d.t.Error("cleanup: delete test blueprint failed")
		} else {
			d.report.Cleanup = append(d.report.Cleanup, "deleted test blueprint "+resource)
		}
	}
}

func (d *remoteDriver) writeYAML(path string, value any) {
	d.t.Helper()
	data, err := yaml.Marshal(value)
	if err != nil || os.WriteFile(path, data, 0o600) != nil {
		d.fail("prepare isolated check configuration")
	}
}

func (d *remoteDriver) initialize() {
	cfg := &config.TeamsConfig{
		APIVersion: config.APIVersion,
		Kind:       config.Kind,
		Spec: config.Spec{Defaults: config.Defaults{
			Sources: []config.Source{
				{
					Name:      "kukeon",
					URL:       "https://github.com/eminwux/kukeon",
					Commit:    d.report.SourceRevision,
					ClonePath: "/home/user/kukeon-src",
				},
			},
			Roles:     []config.Role{{Name: "operator", Source: "kukeon", Path: "team/roles/operator"}},
			Harnesses: []config.Harness{{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(d.home, ".kuke"), 0o700); err != nil {
		d.fail("prepare isolated home")
	}
	declaration := config.Operator()
	declaration.Spec = cfg.Spec.Defaults
	cfg.Spec.Defaults = config.Defaults{}
	d.writeYAML(filepath.Join(d.home, ".kuke", "kuketeams.yaml"), cfg)
	preparedEntry, entryErr := config.Entry(declaration)
	if entryErr != nil {
		d.fail("prepare explicit operator declaration")
	}
	if err := os.MkdirAll(filepath.Join(d.home, ".kuke", "kuketeam.d"), 0o700); err != nil {
		d.fail("prepare registration directory")
	}
	d.writeYAML(filepath.Join(d.home, ".kuke", "kuketeam.d", "kukeon.yaml"), preparedEntry)
	d.clientPath = filepath.Join(d.home, "client.yaml")
	d.writeYAML(
		d.clientPath,
		map[string]any{
			"apiVersion": "v1beta1",
			"kind":       "ClientConfiguration",
			"spec":       map[string]string{"host": d.host},
		},
	)
	d.report.Commands = append(
		d.report.Commands,
		"kuke team init --host <selected Unix endpoint> --configuration <isolated client.yaml>",
	)
	if _, err := d.run(d.binary, "team", "init", "--host", d.host, "--configuration", d.clientPath); err != nil {
		d.fail("kuke team init failed; check scope prerequisites and published source accessibility")
	}
	entryRaw, err := os.ReadFile(filepath.Join(d.home, ".kuke", "kuketeam.d", "kukeon.yaml"))
	if err != nil {
		d.fail("kukeon registration is missing")
	}
	entry, err := config.DecodeEntry(bytes.NewReader(entryRaw))
	if err != nil || entry.Metadata.Name != "kukeon" {
		d.fail("kukeon registration is invalid")
	}
	bp, err := d.client.GetBlueprint(d.ctx, blueprintLookup())
	if err != nil || !bp.MetadataExists || bp.Blueprint.Metadata.Labels[api.LabelTeam] != "kukeon" {
		d.fail("kukeon blueprint readback failed")
	}
	cc, err := d.client.GetConfig(d.ctx, configLookup())
	if err != nil || !cc.MetadataExists || cc.Config.Metadata.Labels[api.LabelTeam] != "kukeon" ||
		cc.Config.Spec.Blueprint.Name != resource {
		d.fail("kukeon config readback failed")
	}
	containers := bp.Blueprint.Spec.Cell.Containers
	if len(containers) != 1 || containers[0].ID != "work" || len(containers[0].Secrets) != 0 ||
		len(cc.Config.Spec.Secrets) != 0 {
		d.fail("unexpected base operator container or secret bindings")
	}
	if cc.Config.Spec.Repos["kukeon"].Ref != d.report.SourceRevision {
		d.fail("runtime repository revision is not pinned")
	}
	d.blueprint = bp.Blueprint
	d.report.Image = containers[0].Image
	d.verifyInitIsolation()
}

func (d *remoteDriver) verifyInitIsolation() {
	afterCells, err := d.client.ListCells(d.ctx, "", "", "")
	if err != nil || !reflect.DeepEqual(cellRecipes(d.cells), cellRecipes(afterCells)) {
		d.fail("initialization changed runtime cell identities or recipes")
	}
	afterSecrets, err := d.client.ListSecrets(d.ctx, "", "", "", "")
	if err != nil || !reflect.DeepEqual(d.secrets, afterSecrets) {
		d.fail("base operator initialization changed secret metadata")
	}
	d.report.Checks = append(
		d.report.Checks,
		"init applied/read back operator blueprint and config",
		"kukeon TeamEntry decoded",
		"base Codex requires no secret bindings; secret metadata unchanged",
		"runtime cell identities and recipes unchanged by init",
	)
}

func (d *remoteDriver) checkImageAndAuth() {
	digest, err := publishedDigest(d.ctx, d.report.Image)
	if err != nil {
		d.fail(err.Error())
	}
	d.report.Digest = digest
	d.report.Checks = append(d.report.Checks, "published registry manifest fetched and hashed")
	volume, err := d.client.GetVolume(
		d.ctx,
		api.VolumeDoc{
			Metadata: api.VolumeMetadata{Name: resource, Realm: "default", Space: "default", Stack: "default"},
		},
	)
	if err != nil || !volume.MetadataExists {
		d.fail(
			"unmet prerequisite: pre-authenticated kukeon-operator-codex state volume is missing; see the setup guide",
		)
	}
	named, err := reference.ParseNormalizedNamed(d.report.Image)
	if err != nil {
		d.fail("invalid catalog image")
	}
	d.pinned = reference.TrimNamed(named).String() + "@" + digest
	// Pin only the newly created test blueprint to the published manifest digest.
	d.blueprint.Spec.Cell.Containers[0].Image = d.pinned
	pinnedPath := filepath.Join(d.home, "pinned-blueprint.yaml")
	d.writeYAML(pinnedPath, d.blueprint)
	d.report.Commands = append(
		d.report.Commands,
		"kuke --host <selected Unix endpoint> --configuration <isolated client.yaml> apply -f <test blueprint pinned to published digest>",
	)
	if _, err = d.run("kuke", "--host", d.host, "--configuration", d.clientPath, "apply", "-f", pinnedPath); err != nil {
		d.fail("pin test blueprint to published digest failed")
	}
}

func (d *remoteDriver) launch() {
	name := fmt.Sprintf("operator-check-%d", time.Now().UnixNano())
	d.report.Cell = name
	d.t.Cleanup(d.cleanupCell)
	prompt := "This is a read-only operator installation check. Use the available $create-cell-config skill only for inspection. First run cat to read its SKILL.md and bundled references/kukeon.md in full, even if their contents were already supplied. Do not create, apply, or launch anything, or inspect credentials. Then execute this exact Python probe and finish with a brief acknowledgement: python3 -c \"import base64; exec(compile(base64.b64decode('" + base64.StdEncoding.EncodeToString(
		[]byte(probeScript),
	) + "'), '<operator-check>', 'exec'))\""
	args := []string{
		"--host",
		d.host,
		"--configuration",
		d.clientPath,
		"run",
		"--from-config",
		resource,
		"--realm",
		"default",
		"--space",
		"default",
		"--stack",
		"default",
		"--name",
		name,
		"--env",
		"PROMPT=" + prompt,
		"--detach",
	}
	d.report.Commands = append(
		d.report.Commands,
		"kuke run --from-config kukeon-operator-codex --realm default --space default --stack default --name "+name+" --env PROMPT=<embedded read-only probe> --detach",
	)
	if _, err := d.run("kuke", args...); err != nil {
		d.fail("explicit kuke run failed; check image pull, state volume, and runtime prerequisites")
	}
}

func (d *remoteDriver) cleanupCell() {
	// Also handle an ambiguous launch: delete only our unique cell, never volumes.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	all, err := d.client.ListCells(ctx, "default", "default", "default")
	if err != nil {
		d.t.Error("cleanup: cannot inspect test cell")
		return
	}
	for _, cell := range all {
		if cell.Metadata.Name != d.report.Cell {
			continue
		}
		if cell.Metadata.Labels[api.LabelTeam] != "kukeon" {
			d.t.Error("cleanup: test cell ownership mismatch; left untouched")
			continue
		}
		if _, err = d.client.KillCell(ctx, cell); err != nil {
			d.t.Error("cleanup: kill test cell failed")
		} else {
			d.report.Cleanup = append(d.report.Cleanup, "killed test cell "+d.report.Cell+"; retained state volume")
		}
	}
}

func (d *remoteDriver) waitForEvidence() {
	lookup := api.CellDoc{
		Metadata: api.CellMetadata{Name: d.report.Cell},
		Spec:     api.CellSpec{RealmID: "default", SpaceID: "default", StackID: "default"},
	}
	running := false
	for d.ctx.Err() == nil {
		state, err := d.client.GetCell(d.ctx, lookup)
		if err != nil {
			d.fail("cell state readback failed")
		}
		for _, status := range state.Cell.Status.Containers {
			if status.Name == "work" && status.State == api.ContainerStateReady && state.RootContainerTaskRunning {
				running = true
			}
		}
		workImage := ""
		for _, container := range state.Cell.Spec.Containers {
			if container.ID == "work" {
				workImage = container.Image
			}
		}
		if workImage != d.pinned {
			d.fail("running cell image differs from the published digest")
		}
		output, logErr := d.run(
			"kuke",
			"--host",
			d.host,
			"--configuration",
			d.clientPath,
			"log",
			d.report.Cell,
			"--realm",
			"default",
			"--space",
			"default",
			"--stack",
			"default",
			"--container",
			"work",
		)
		if logErr == nil && d.acceptEvidence(output, running) {
			return
		}
		select {
		case <-d.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	d.fail(
		"timed out waiting for authenticated operator evidence; verify Codex authentication, model access, repository setup and log-path visibility",
	)
}

func (d *remoteDriver) acceptEvidence(output []byte, running bool) bool {
	proof, complete, err := inspectCapture(output)
	if err != nil {
		d.fail(err.Error())
	}
	if !complete {
		return false
	}
	if !running {
		d.fail("codex completed without observed live work container and root task")
	}
	requirements := fileHash(d.t, filepath.Join(d.repo, "team/harnesses/codex/context/requirements.toml"))
	hook := fileHash(d.t, filepath.Join(d.repo, "team/harnesses/codex/context/hooks/reflect-stop.py"))
	if err = proof.validate(d.report.SourceRevision, requirements, hook); err != nil {
		d.fail(err.Error())
	}
	d.report.Probe = proof
	d.report.Checks = append(
		d.report.Checks,
		"root task and work container observed running",
		"cell uses verified published digest",
		"authenticated Codex turn completed with command-generated probe",
		"operator instructions, skill discovery/reads, effective sandbox/approval and image policy/hook verified",
	)
	return true
}

func reserved(name string, labels map[string]string) bool {
	return name == resource || labels[api.LabelTeam] == "kukeon"
}

func cellRecipes(cells []api.CellDoc) map[string]api.CellSpec {
	out := map[string]api.CellSpec{}
	for _, cell := range cells {
		out[strings.Join([]string{cell.Spec.RealmID, cell.Spec.SpaceID, cell.Spec.StackID, cell.Metadata.Name}, "/")] = cell.Spec
	}
	return out
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read expected harness asset")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// publishedDigest uses the public GHCR anonymous pull protocol. It never reads
// host login files, invokes login, uses gh credentials, or records bearer tokens.
func publishedDigest(ctx context.Context, image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil || reference.Domain(named) != "ghcr.io" {
		return "", errors.New("unmet prerequisite: expected a published GHCR image")
	}
	tagged, ok := named.(reference.Tagged)
	if !ok {
		return "", errors.New("unmet prerequisite: catalog image must have a release tag")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(endpoint, token string) (*http.Response, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set(
			"Accept",
			"application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
		)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return client.Do(req)
	}
	response, err := get(
		"https://ghcr.io/token?service=ghcr.io&scope="+url.QueryEscape("repository:"+reference.Path(named)+":pull"),
		"",
	)
	if err != nil {
		return "", errors.New("unmet prerequisite: published image registry is unreachable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"unmet prerequisite: published image %s is not anonymously accessible (registry token HTTP %d)",
			image,
			response.StatusCode,
		)
	}
	var auth struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(response.Body).Decode(&auth) != nil || auth.Token == "" {
		return "", errors.New("unmet prerequisite: registry did not provide anonymous pull access")
	}
	manifest, err := get("https://ghcr.io/v2/"+reference.Path(named)+"/manifests/"+tagged.Tag(), auth.Token)
	if err != nil {
		return "", errors.New("unmet prerequisite: published manifest is unreachable")
	}
	defer func() { _ = manifest.Body.Close() }()
	if manifest.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unmet prerequisite: published image %s manifest HTTP %d", image, manifest.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(manifest.Body, 4<<20))
	if err != nil || !json.Valid(raw) {
		return "", errors.New("unmet prerequisite: invalid published manifest")
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if header := manifest.Header.Get("Docker-Content-Digest"); header != "" && header != digest {
		return "", errors.New("published manifest digest mismatch")
	}
	return digest, nil
}
