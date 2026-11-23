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

package update_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/kuke/update"
	"github.com/spf13/cobra"
)

func TestMain(m *testing.M) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 0 })
	code := m.Run()
	restore()
	os.Exit(code)
}

const (
	repo      = "ghcr.io/eminwux/kukeon"
	edgeOld   = "v0.10.0-77-gaaaaaaa"
	edgeNew   = "v0.10.0-80-gbbbbbbb"
	edgeNewer = "v0.11.0-2-gccccccc"
)

// fakeBinary is the content of a fake kuke: the fake exec reads its version
// from it, and whether it knows `kuke team init --upgrade`.
func fakeBinary(version string, upgrade bool) string {
	s := "fake-kuke " + version
	if upgrade {
		s += " upgrade"
	}
	return s
}

func binaryVersion(t *testing.T, path string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 || fields[0] != "fake-kuke" {
		return "", false
	}
	return fields[1], slices.Contains(fields, "upgrade")
}

// release is one published release on the fake server.
type release struct {
	kuke      string // version the kuke asset reports
	badSum    bool
	noAsset   bool
	noUpgrade bool // the kuke asset predates kuke team init --upgrade
}

type world struct {
	t *testing.T

	mu       sync.Mutex
	edge     string             // edge VERSION ("" = not published)
	edgeRel  release            // the edge assets
	latest   string             // releases/latest tag_name
	releases map[string]release // tag -> assets
	requests []string

	daemon       string // version the daemon reports ("" = unreachable)
	image        string // image the kukeond container runs
	recreates    []string
	recreateFail map[string]bool // image -> recreate fails
	strictFail   bool
	deleted      []string
	deleteErr    error
	teamRuns     int
	teamErr      error
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return &world{t: t, releases: map[string]release{}, recreateFail: map[string]bool{}}
}

func (w *world) serve() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.requests = append(w.requests, r.URL.Path)
		path := r.URL.Path
		if path == "/repos/eminwux/kukeon/releases/latest" {
			if w.latest == "" {
				http.NotFound(rw, r)
				return
			}
			_, _ = fmt.Fprintf(rw, `{"tag_name": %q}`, w.latest)
			return
		}
		const prefix = "/eminwux/kukeon/releases/download/"
		if !strings.HasPrefix(path, prefix) {
			http.NotFound(rw, r)
			return
		}
		tag, asset, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
		var rel release
		if tag == "edge" {
			if w.edge == "" {
				http.NotFound(rw, r)
				return
			}
			if asset == "VERSION" {
				_, _ = fmt.Fprintln(rw, w.edge)
				return
			}
			rel = w.edgeRel
		} else {
			var ok bool
			if rel, ok = w.releases[tag]; !ok {
				http.NotFound(rw, r)
				return
			}
		}
		if rel.noAsset {
			http.NotFound(rw, r)
			return
		}
		name, isSum := strings.CutSuffix(asset, ".sha256")
		content := assetContent(name, rel)
		if isSum {
			sum := sha256.Sum256([]byte(content))
			digest := hex.EncodeToString(sum[:])
			if rel.badSum {
				digest = strings.Repeat("0", 64)
			}
			_, _ = fmt.Fprintf(rw, "%s  %s\n", digest, name)
			return
		}
		_, _ = io.WriteString(rw, content)
	}))
	w.t.Cleanup(srv.Close)
	return srv
}

func assetContent(name string, rel release) string {
	if strings.HasPrefix(name, "kuke-linux-") {
		return fakeBinary(rel.kuke, !rel.noUpgrade)
	}
	return "content of " + name + " " + rel.kuke
}

func (w *world) exec(_ context.Context, out, _ io.Writer, path string, args ...string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	version, upgrade := binaryVersion(w.t, path)
	joined := strings.Join(args, " ")
	switch {
	case joined == "version --no-daemon":
		_, _ = fmt.Fprintf(out, "Client: %s\n", version)
		return nil
	case strings.HasPrefix(joined, "daemon recreate --kukeond-image "):
		image := args[3]
		w.recreates = append(w.recreates, image)
		if w.recreateFail[image] {
			return errors.New("recreate failed")
		}
		w.image = image
		w.daemon = version
		return nil
	case joined == "version --strict":
		_, _ = fmt.Fprintf(out, "Client: %s\nDaemon: %s\n", version, w.daemon)
		if w.strictFail || version != w.daemon {
			return errors.New("version mismatch")
		}
		return nil
	case joined == "team init --help":
		if upgrade {
			_, _ = fmt.Fprintln(out, "      --upgrade   Re-pin the kukeon source")
		}
		return nil
	case joined == "team init --upgrade":
		w.teamRuns++
		return w.teamErr
	}
	return fmt.Errorf("unexpected child %s %s", path, joined)
}

type fakeImages struct{ w *world }

func (f fakeImages) DaemonImage(context.Context) (string, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	return f.w.image, nil
}

func (f fakeImages) DeleteImage(_ context.Context, realm, ref string) error {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if realm != "kuke-system" {
		return fmt.Errorf("delete in realm %s", realm)
	}
	if f.w.deleteErr != nil {
		return f.w.deleteErr
	}
	f.w.deleted = append(f.w.deleted, ref)
	return nil
}

func (f fakeImages) PruneImages(context.Context, string) error { return nil }
func (f fakeImages) Close() error                              { return nil }

// host is an install prefix holding kuke at version, kukeond as its hard
// link, kukebuild and kukepause.
func host(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "kuke"), fakeBinary(version, true))
	if err := os.Link(filepath.Join(dir, "kuke"), filepath.Join(dir, "kukeond")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "kukebuild"), "old kukebuild")
	write(t, filepath.Join(dir, "kukepause"), "old kukepause")
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type result struct {
	out string
	err error
}

func (w *world) run(t *testing.T, version, prefix, stdin string, args ...string) result {
	t.Helper()
	srv := w.serve()
	cmd := update.NewUpdateCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(stdin))
	deps := update.Deps{
		Version:       version,
		ImageRepo:     repo,
		DownloadBase:  srv.URL,
		APIBase:       srv.URL,
		HTTPClient:    srv.Client(),
		Exec:          w.exec,
		Images:        func(*cobra.Command) (update.ImageClient, error) { return fakeImages{w}, nil },
		DaemonVersion: func(*cobra.Command) string { w.mu.Lock(); defer w.mu.Unlock(); return w.daemon },
		LookPath:      func(string) (string, error) { return "", errors.New("not on PATH") },
		Arch:          "amd64",
	}
	cmd.SetContext(context.WithValue(context.Background(), update.MockDepsKey{}, deps))
	cmd.SetArgs(append([]string{"--install-prefix", prefix}, args...))
	err := cmd.Execute()
	return result{out: buf.String(), err: err}
}

func (w *world) requested(sub string) bool {
	return slices.ContainsFunc(w.requests, func(p string) bool { return strings.Contains(p, sub) })
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA != nil || errB != nil {
		t.Fatalf("stat: %v %v", errA, errB)
	}
	sa, okA := ia.Sys().(*syscall.Stat_t)
	sb, okB := ib.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Ino == sb.Ino
}

// edgeHost is an edge host at edgeOld whose daemon matches.
func edgeHost(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	w.edge, w.edgeRel = edgeNew, release{kuke: edgeNew}
	w.daemon, w.image = edgeOld, repo+":sha-aaaaaaa"
	return w, host(t, edgeOld)
}

func TestEdgeHostBehindUpdates(t *testing.T) {
	w, prefix := edgeHost(t)
	r := w.run(t, edgeOld, prefix, "")
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != edgeNew {
		t.Fatalf("kuke is %q, want %s", got, edgeNew)
	}
	if !sameInode(t, filepath.Join(prefix, "kuke"), filepath.Join(prefix, "kukeond")) {
		t.Fatal("kukeond is not a hard link to the new kuke")
	}
	if !strings.Contains(read(t, filepath.Join(prefix, "kukebuild")), edgeNew) ||
		!strings.Contains(read(t, filepath.Join(prefix, "kukepause")), edgeNew) {
		t.Fatal("kukebuild or kukepause not replaced")
	}
	if !slices.Equal(w.recreates, []string{repo + ":sha-bbbbbbb"}) {
		t.Fatalf("recreates = %v", w.recreates)
	}
	if !slices.Equal(w.deleted, []string{repo + ":sha-aaaaaaa"}) {
		t.Fatalf("deleted = %v", w.deleted)
	}
	if w.teamRuns != 1 {
		t.Fatalf("team runs = %d, want 1", w.teamRuns)
	}
	if !strings.Contains(r.out, "updated "+edgeOld+" -> "+edgeNew+" (edge)") {
		t.Fatalf("output:\n%s", r.out)
	}
	if strings.Index(r.out, "Daemon: "+edgeNew) > strings.Index(r.out, "updated ") {
		t.Fatalf("the version check must come before the success line:\n%s", r.out)
	}
	entries, _ := os.ReadDir(prefix)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".kuke-update-") {
			t.Fatalf("temporary directory %s left behind", e.Name())
		}
	}
}

func TestEdgeHostCurrentIsUpToDate(t *testing.T) {
	w, prefix := edgeHost(t)
	w.edge = edgeOld
	r := w.run(t, edgeOld, prefix, "")
	if r.err != nil || !strings.Contains(r.out, "already up to date ("+edgeOld+")") {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if w.requested("kuke-linux") || len(w.recreates) != 0 || w.teamRuns != 0 {
		t.Fatalf("requests=%v recreates=%v team=%d", w.requests, w.recreates, w.teamRuns)
	}
}

func TestEdgeHostCurrentDaemonOlderFinishes(t *testing.T) {
	w, prefix := edgeHost(t)
	w.edge = edgeOld
	w.daemon = "v0.10.0-70-g9999999"
	r := w.run(t, edgeOld, prefix, "")
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if w.requested("kuke-linux") {
		t.Fatal("binaries downloaded although kuke is current")
	}
	if !slices.Equal(w.recreates, []string{repo + ":sha-aaaaaaa"}) {
		t.Fatalf("recreates = %v", w.recreates)
	}
}

func TestStableHostMovesToLatestTag(t *testing.T) {
	w := newWorld(t)
	w.latest = "v0.11.0"
	w.releases["v0.11.0"] = release{kuke: "v0.11.0"}
	w.edge, w.edgeRel = edgeNewer, release{kuke: edgeNewer}
	w.daemon, w.image = "v0.10.0", repo+":v0.10.0"
	prefix := host(t, "v0.10.0")
	r := w.run(t, "v0.10.0", prefix, "")
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if !slices.Equal(w.recreates, []string{repo + ":v0.11.0"}) {
		t.Fatalf("recreates = %v", w.recreates)
	}
	if w.requested("/edge/") {
		t.Fatalf("a stable host requested the edge build: %v", w.requests)
	}
}

func TestStableHostChannelEdgeMovesToEdge(t *testing.T) {
	w := newWorld(t)
	w.edge, w.edgeRel = edgeNewer, release{kuke: edgeNewer}
	w.daemon, w.image = "v0.10.0", repo+":v0.10.0"
	prefix := host(t, "v0.10.0")
	r := w.run(t, "v0.10.0", prefix, "", "--channel", "edge")
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if !slices.Equal(w.recreates, []string{repo + ":sha-ccccccc"}) {
		t.Fatalf("recreates = %v", w.recreates)
	}
}

func TestEdgeHostChannelStable(t *testing.T) {
	t.Run("edge ahead of the latest tag changes nothing", func(t *testing.T) {
		w, prefix := edgeHost(t)
		w.latest = "v0.10.0"
		r := w.run(t, edgeOld, prefix, "", "--channel", "stable")
		if r.err != nil {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if !strings.Contains(r.out, "Nothing changed.") ||
			!strings.Contains(r.out, "will move this host at the next release") {
			t.Fatalf("output:\n%s", r.out)
		}
		if w.requested("kuke-linux") || len(w.recreates) != 0 {
			t.Fatalf("requests=%v recreates=%v", w.requests, w.recreates)
		}
	})
	t.Run("a newer tag moves the host", func(t *testing.T) {
		w, prefix := edgeHost(t)
		w.latest = "v0.11.0"
		w.releases["v0.11.0"] = release{kuke: "v0.11.0"}
		r := w.run(t, edgeOld, prefix, "", "--channel", "stable")
		if r.err != nil {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != "v0.11.0" {
			t.Fatalf("kuke is %q", got)
		}
	})
}

func TestVersionDowngrade(t *testing.T) {
	setup := func(t *testing.T) (*world, string) {
		t.Helper()
		w := newWorld(t)
		w.releases["v0.9.1"] = release{kuke: "v0.9.1"}
		w.daemon, w.image = "v0.10.0", repo+":v0.10.0"
		return w, host(t, "v0.10.0")
	}
	t.Run("answer no", func(t *testing.T) {
		w, prefix := setup(t)
		r := w.run(t, "v0.10.0", prefix, "no\n", "--version", "v0.9.1")
		if !errors.Is(r.err, update.ErrDowngradeNotConfirmed) {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if !strings.Contains(r.out, "older than the installed v0.10.0") || len(w.recreates) != 0 ||
			w.requested("kuke-linux") {
			t.Fatalf("recreates=%v\n%s", w.recreates, r.out)
		}
	})
	t.Run("non-interactive counts as no", func(t *testing.T) {
		w, prefix := setup(t)
		r := w.run(t, "v0.10.0", prefix, "", "--version", "v0.9.1")
		if !errors.Is(r.err, update.ErrDowngradeNotConfirmed) {
			t.Fatalf("err = %v", r.err)
		}
	})
	t.Run("answer yes", func(t *testing.T) {
		w, prefix := setup(t)
		if r := w.run(t, "v0.10.0", prefix, "yes\n", "--version", "v0.9.1"); r.err != nil {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if !slices.Equal(w.recreates, []string{repo + ":v0.9.1"}) {
			t.Fatalf("recreates = %v", w.recreates)
		}
	})
	t.Run("--yes", func(t *testing.T) {
		w, prefix := setup(t)
		if r := w.run(t, "v0.10.0", prefix, "", "--version", "v0.9.1", "--yes"); r.err != nil {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != "v0.9.1" {
			t.Fatalf("kuke is %q", got)
		}
	})
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--version", "sha-abc1234"},
		{"--version", edgeNew},
		{"--version", "v0.10.0", "--channel", "edge"},
		{"--channel", "nightly"},
	} {
		w, prefix := edgeHost(t)
		r := w.run(t, edgeOld, prefix, "", args...)
		if r.err == nil || len(w.requests) != 0 {
			t.Fatalf("%v: err = %v requests=%v", args, r.err, w.requests)
		}
	}
}

func TestDevelopmentBuild(t *testing.T) {
	t.Run("dirty version", func(t *testing.T) {
		w, prefix := edgeHost(t)
		r := w.run(t, "v0.10.0-3-gabc1234-dirty", prefix, "")
		if !errors.Is(r.err, update.ErrDevelopmentBuild) || !strings.Contains(r.err.Error(), "--channel") {
			t.Fatalf("err = %v", r.err)
		}
		if len(w.requests) != 0 {
			t.Fatalf("requests = %v", w.requests)
		}
	})
	t.Run("symlinked binaries", func(t *testing.T) {
		w, _ := edgeHost(t)
		checkout := host(t, edgeOld)
		prefix := t.TempDir()
		for _, name := range []string{"kuke", "kukeond", "kukebuild", "kukepause"} {
			if err := os.Symlink(filepath.Join(checkout, name), filepath.Join(prefix, name)); err != nil {
				t.Fatal(err)
			}
		}
		r := w.run(t, edgeOld, prefix, "")
		if !errors.Is(r.err, update.ErrDevelopmentBuild) || !strings.Contains(r.err.Error(), "--channel") {
			t.Fatalf("err = %v", r.err)
		}

		w, _ = edgeHost(t)
		r = w.run(t, edgeOld, prefix, "", "--channel", "edge")
		if r.err != nil {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		for _, name := range []string{"kuke", "kukeond", "kukebuild", "kukepause"} {
			info, err := os.Lstat(filepath.Join(prefix, name))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("%s is not a regular file: %v %v", name, info, err)
			}
		}
		if got, _ := binaryVersion(t, filepath.Join(checkout, "kuke")); got != edgeOld {
			t.Fatal("the checkout's kuke was written to")
		}
		if read(t, filepath.Join(checkout, "kukebuild")) != "old kukebuild" {
			t.Fatal("the checkout's kukebuild was written to")
		}
	})
}

func TestDownloadFailuresChangeNothing(t *testing.T) {
	cases := map[string]func(w *world){
		"checksum mismatch":   func(w *world) { w.edgeRel.badSum = true },
		"binary reports else": func(w *world) { w.edgeRel.kuke = edgeNewer },
		"missing asset":       func(w *world) { w.edgeRel.noAsset = true },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			w, prefix := edgeHost(t)
			breakIt(w)
			r := w.run(t, edgeOld, prefix, "")
			if r.err == nil || !strings.Contains(r.err.Error(), "nothing changed") {
				t.Fatalf("err = %v\n%s", r.err, r.out)
			}
			if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != edgeOld {
				t.Fatalf("kuke is %q", got)
			}
			if read(t, filepath.Join(prefix, "kukebuild")) != "old kukebuild" || len(w.recreates) != 0 {
				t.Fatal("prefix or daemon changed")
			}
		})
	}
}

func TestRecreateFailureRestores(t *testing.T) {
	for _, mode := range []string{"recreate", "version check"} {
		t.Run(mode, func(t *testing.T) {
			w, prefix := edgeHost(t)
			if mode == "recreate" {
				w.recreateFail[repo+":sha-bbbbbbb"] = true
			} else {
				w.strictFail = true
			}
			r := w.run(t, edgeOld, prefix, "")
			if !errors.Is(r.err, update.ErrUpdateFailed) {
				t.Fatalf("err = %v\n%s", r.err, r.out)
			}
			if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != edgeOld {
				t.Fatalf("kuke is %q, want the previous %s", got, edgeOld)
			}
			if !sameInode(t, filepath.Join(prefix, "kuke"), filepath.Join(prefix, "kukeond")) {
				t.Fatal("kukeond is not a hard link to the restored kuke")
			}
			if read(t, filepath.Join(prefix, "kukebuild")) != "old kukebuild" {
				t.Fatal("kukebuild not restored")
			}
			want := []string{repo + ":sha-bbbbbbb", repo + ":sha-aaaaaaa"}
			if !slices.Equal(w.recreates, want) {
				t.Fatalf("recreates = %v, want %v", w.recreates, want)
			}
			if len(w.deleted) != 0 || w.teamRuns != 0 {
				t.Fatalf("deleted=%v team=%d", w.deleted, w.teamRuns)
			}
			if !strings.Contains(r.err.Error(), "back on "+edgeOld) {
				t.Fatalf("err = %v", r.err)
			}
		})
	}
}

func TestImageDeleteFailureIsAWarning(t *testing.T) {
	w, prefix := edgeHost(t)
	w.deleteErr = errors.New("in use")
	r := w.run(t, edgeOld, prefix, "")
	if r.err != nil || !strings.Contains(r.out, "warning: previous kukeond image") {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
}

func TestTeamStep(t *testing.T) {
	t.Run("failure is a warning", func(t *testing.T) {
		w, prefix := edgeHost(t)
		w.teamErr = errors.New("git: not found")
		r := w.run(t, edgeOld, prefix, "")
		if r.err != nil || !strings.Contains(r.out, "kuke team was not upgraded") ||
			!strings.Contains(r.out, "run kuke team init --upgrade") {
			t.Fatalf("err = %v\n%s", r.err, r.out)
		}
		if got, _ := binaryVersion(t, filepath.Join(prefix, "kuke")); got != edgeNew || w.daemon != edgeNew {
			t.Fatal("binaries or daemon rolled back after a team failure")
		}
	})
	t.Run("skip-team", func(t *testing.T) {
		w, prefix := edgeHost(t)
		if r := w.run(t, edgeOld, prefix, "", "--skip-team"); r.err != nil || w.teamRuns != 0 {
			t.Fatalf("err = %v team=%d", r.err, w.teamRuns)
		}
	})
	t.Run("target without --upgrade", func(t *testing.T) {
		w := newWorld(t)
		w.releases["v0.9.1"] = release{kuke: "v0.9.1", noUpgrade: true}
		w.daemon, w.image = "v0.10.0", repo+":v0.10.0"
		prefix := host(t, "v0.10.0")
		r := w.run(t, "v0.10.0", prefix, "", "--version", "v0.9.1", "--yes")
		if r.err != nil || w.teamRuns != 0 || !strings.Contains(r.out, "has no kuke team init --upgrade") {
			t.Fatalf("err = %v team=%d\n%s", r.err, w.teamRuns, r.out)
		}
	})
}
