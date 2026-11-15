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

package config_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/config"
)

func fixture(t *testing.T, name string) *config.TeamsConfig {
	t.Helper()
	data, err := os.ReadFile("../../testdata/config/" + name + ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFixturesAndRoundTrip(t *testing.T) {
	for _, name := range []string{"global", "empty-defaults", "empty-lists"} {
		t.Run(name, func(t *testing.T) {
			cfg := fixture(t, name)
			var encoded bytes.Buffer
			if err := config.Encode(&encoded, cfg); err != nil {
				t.Fatal(err)
			}
			decoded, err := config.Decode(&encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, decoded) {
				t.Fatalf("semantic values changed: %#v != %#v", cfg, decoded)
			}
		})
	}
	global := fixture(t, "global")
	if global.Spec.Defaults.Roles[1].Harness != "claude" {
		t.Fatal("role harness override lost")
	}
	if global.Spec.Git.Author == global.Spec.Git.Committer {
		t.Fatal("identity storage aliased")
	}
	omitted, empty := fixture(t, "empty-defaults"), fixture(t, "empty-lists")
	if omitted.Spec.Defaults.Sources != nil || omitted.Spec.Defaults.Roles != nil ||
		omitted.Spec.Defaults.Harnesses != nil {
		t.Fatal("omitted lists must remain nil")
	}
	if empty.Spec.Defaults.Sources == nil || empty.Spec.Defaults.Roles == nil || empty.Spec.Defaults.Harnesses == nil {
		t.Fatal("explicit empty lists must remain non-nil")
	}
}

func TestSelectionListRoundTrips(t *testing.T) {
	for bits := range 8 {
		t.Run(string(rune('0'+bits)), func(t *testing.T) {
			cfg := fixture(t, "empty-defaults")
			if bits&1 != 0 {
				cfg.Spec.Defaults.Sources = []config.Source{}
			}
			if bits&2 != 0 {
				cfg.Spec.Defaults.Roles = []config.Role{}
			}
			if bits&4 != 0 {
				cfg.Spec.Defaults.Harnesses = []config.Harness{}
			}
			var out bytes.Buffer
			if err := config.Encode(&out, cfg); err != nil {
				t.Fatal(err)
			}
			got, err := config.Decode(&out)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, got) {
				t.Fatal("omitted and empty selection lists changed")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*config.TeamsConfig)
		want   string
	}{
		{"version", func(c *config.TeamsConfig) { c.APIVersion = "v2" }, "apiVersion:"},
		{"kind", func(c *config.TeamsConfig) { c.Kind = "ProjectTeam" }, "kind:"},
		{"optional git", func(c *config.TeamsConfig) { c.Spec.Git = nil }, ""},
		{"empty git", func(c *config.TeamsConfig) { c.Spec.Git = &config.Git{} }, ""},
		{"only author", func(c *config.TeamsConfig) { c.Spec.Git.Committer = nil }, ""},
		{"only committer", func(c *config.TeamsConfig) { c.Spec.Git.Author = nil }, ""},
		{"blank author name", func(c *config.TeamsConfig) { c.Spec.Git.Author.Name = " " }, "spec.git.author.name:"},
		{"blank author email", func(c *config.TeamsConfig) { c.Spec.Git.Author.Email = "" }, "spec.git.author.email:"},
		{
			"blank committer name",
			func(c *config.TeamsConfig) { c.Spec.Git.Committer.Name = "" },
			"spec.git.committer.name:",
		},
		{
			"blank committer email",
			func(c *config.TeamsConfig) { c.Spec.Git.Committer.Email = "\t" },
			"spec.git.committer.email:",
		},
		{"missing key", func(c *config.TeamsConfig) { c.Spec.Git.SigningKey = " " }, "spec.git.signingKey:"},
		{"bad signing value", func(c *config.TeamsConfig) { c.Spec.Git.Sign = []string{"all"} }, "spec.git.sign[0]:"},
		{"blank signing value", func(c *config.TeamsConfig) { c.Spec.Git.Sign = []string{""} }, "spec.git.sign[0]:"},
		{"sign both", func(c *config.TeamsConfig) { c.Spec.Git.Sign = []string{"commits", "tags"} }, ""},
		{"key without signing", func(c *config.TeamsConfig) { c.Spec.Git.Sign = nil }, ""},
		{"no revision", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Branch = "" }, "exactly one"},
		{"two revisions", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Tag = "v1" }, "exactly one"},
		{"three revisions", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources[0].Tag = "v1"
			c.Spec.Defaults.Sources[0].Commit = "abc"
		}, "exactly one"},
		{
			"blank revision",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Branch = " " },
			"sources[0].branch:",
		},
		{"literal version branch", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Branch = "v1.4.0" }, ""},
		{"tag", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources[0].Branch = ""
			c.Spec.Defaults.Sources[0].Tag = "v1.4.0"
		}, ""},
		{"commit", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources[0].Branch = ""
			c.Spec.Defaults.Sources[0].Commit = "abcd"
		}, ""},
		{"blank URL", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].URL = " " }, "sources[0].url:"},
		{
			"blank source name",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Name = " " },
			"sources[0].name:",
		},
		{"blank role name", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Name = " " }, "roles[0].name:"},
		{
			"blank harness name",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses[0].Name = "" },
			"harnesses[0].name:",
		},
		{"duplicate source", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources = append(c.Spec.Defaults.Sources, c.Spec.Defaults.Sources[0])
		}, "sources[1].name: duplicate"},
		{"duplicate role", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Roles = append(c.Spec.Defaults.Roles, c.Spec.Defaults.Roles[0])
		}, "roles[2].name: duplicate"},
		{"duplicate harness", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Harnesses = append(c.Spec.Defaults.Harnesses, c.Spec.Defaults.Harnesses[0])
		}, "harnesses[2].name: duplicate"},
		{"cross-list names", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Roles[0].Name = "kuketeams"
			c.Spec.Defaults.Harnesses[0].Name = "kuketeams"
		}, ""},
		{
			"unknown role source",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Source = "missing" },
			"roles[0].source: unknown",
		},
		{
			"unknown harness source",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses[0].Source = "missing" },
			"harnesses[0].source: unknown",
		},
		{
			"blank source reference",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Source = " " },
			"roles[0].source:",
		},
		{
			"unknown harness",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Harness = "missing" },
			"roles[0].harness: unknown harness",
		},
		{
			"blank harness reference",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Harness = " " },
			"roles[0].harness:",
		},
		{"no harnesses", func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses = nil }, "roles[1].harness:"},
		{"explicit harness", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Harness = "claude" }, ""},
		{
			"blank image",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Needs.Image = []string{"go", " "} },
			"roles[0].needs.image[1]:",
		},
		{"empty image list", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Needs.Image = []string{} }, ""},
		{
			"no roles",
			func(c *config.TeamsConfig) { c.Spec.Defaults.Roles = nil },
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, before := fixture(t, "global"), fixture(t, "global")
			tc.change(cfg)
			tc.change(before)
			err := config.Validate(cfg)
			assertError(t, err, tc.want)
			if !reflect.DeepEqual(cfg, before) {
				t.Fatal("validation mutated input")
			}
		})
	}
	assertError(t, config.Validate(nil), "config:")
}

func TestDefinitionPaths(t *testing.T) {
	for _, definition := range []string{"", " ", "/roles/dev", "..", "../dev", "roles/../../dev", `C:\roles\dev`, `roles\..\dev`, "//host/dev", "file:dev", "role\x00dev"} {
		for _, selection := range []string{"role", "harness"} {
			t.Run(selection+"/"+definition, func(t *testing.T) {
				cfg := fixture(t, "global")
				if selection == "role" {
					cfg.Spec.Defaults.Roles[0].Path = definition
				} else {
					cfg.Spec.Defaults.Harnesses[0].Path = definition
				}
				assertError(t, config.Validate(cfg), ".path:")
			})
		}
	}
	for _, definition := range []string{"roles/dev", "roles/../dev", "."} {
		cfg := fixture(t, "global")
		cfg.Spec.Defaults.Roles[0].Path = definition
		assertError(t, config.Validate(cfg), "")
	}
}

func assertError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
	} else if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}

func TestStrictDecode(t *testing.T) {
	const header = "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\n"
	cases := []struct{ name, data, want string }{
		{"empty", "", "empty YAML"},
		{"comments", "# no document\n", "empty YAML"},
		{"null", "null", "expected mapping"},
		{"empty document", "---\n", "expected mapping"},
		{"malformed", header + "spec: [", "invalid YAML"},
		{"wrong version", "apiVersion: v2\nkind: TeamsConfig", "apiVersion:"},
		{"wrong kind", "apiVersion: kuketeams.io/v1\nkind: Team", "kind:"},
		{"missing version", "kind: TeamsConfig", "apiVersion:"},
		{"unknown root", header + "oops: true", "oops: unknown field"},
		{
			"unknown nested",
			header + "spec: {git: {author: {name: A, email: a, oops: true}}}",
			"spec.git.author.oops: unknown field",
		},
		{
			"unknown selection",
			header + "spec: {defaults: {roles: [{name: dev, oops: true}]}}",
			"spec.defaults.roles[0].oops: unknown field",
		},
		{"duplicate root", header + "kind: TeamsConfig", "kind: duplicate key"},
		{
			"duplicate nested",
			header + "spec: {defaults: {sources: [], sources: []}}",
			"spec.defaults.sources: duplicate key",
		},
		{"multiple docs", header + "---\n" + header, "exactly one YAML document"},
		{"trailing empty doc", header + "---\n", "exactly one YAML document"},
		{"trailing malformed doc", header + "---\n[", "trailing YAML"},
		{"list instead of object", "[]", "expected mapping"},
		{"wrong list type", header + "spec: {defaults: {roles: {}}}", "spec.defaults.roles: expected list"},
		{"null list", header + "spec: {defaults: {roles: null}}", "spec.defaults.roles: expected list"},
		{"wrong string type", header + "spec: {git: {signingKey: 123}}", "spec.git.signingKey: expected string"},
		{
			"removed harness default",
			header + "spec: {defaults: {harnesses: [{default: 'true'}]}}",
			"spec.defaults.harnesses[0].default: removed",
		},
		{"nonstring key", header + "spec: {1: bad}", "spec: expected string field name"},
		{"complex key", header + "spec: {? [x]: bad}", "spec: expected string field name"},
		{"cyclic alias", header + "spec: &loop {defaults: {roles: [*loop]}}", "cyclic YAML alias"},
		{
			"unknown through alias",
			header + "spec: {git: {author: &id {name: A, email: a}, committer: *id, sign: *id}}",
			"spec.git.sign: expected list",
		},
		{
			"blank explicit harness",
			header + "spec: {defaults: {roles: [{harness: ''}]}}",
			"spec.defaults.roles[0].harness: must be nonblank",
		},
		{
			"merge key",
			header + "spec: {git: {author: &id {name: A, email: a}, committer: {<<: *id}}}",
			"spec.git.committer: expected string field name",
		},
		{"valid aliases", header + "spec: {git: {author: &id {name: A, email: a}, committer: *id}}", ""},
		{"trailing comments", header + "# done\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Decode(strings.NewReader(tc.data))
			assertError(t, err, tc.want)
			if err != nil && cfg != nil {
				t.Fatal("decode returned an invalid model")
			}
		})
	}
	data, err := os.ReadFile("../../testdata/config/invalid-reference.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.Decode(bytes.NewReader(data))
	assertError(t, err, "spec.defaults.roles[0].source: unknown source")
}

func TestEncodeDoesNotMutate(t *testing.T) {
	cfg, before := fixture(t, "global"), fixture(t, "global")
	if err := config.Encode(io.Discard, cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("encoding mutated input")
	}
	cfg.Kind = "bad"
	var out bytes.Buffer
	assertError(t, config.Encode(&out, cfg), "kind:")
	if out.Len() != 0 {
		t.Fatal("invalid config was written")
	}
}

type brokenIO struct{ err error }

func (b brokenIO) Read([]byte) (int, error)  { return 0, b.err }
func (b brokenIO) Write([]byte) (int, error) { return 0, b.err }

func TestIOFailures(t *testing.T) {
	sentinel := errors.New("broken stream")
	const document = "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\n...\n"
	readers := []struct {
		name   string
		reader io.Reader
	}{
		{"immediate", brokenIO{sentinel}},
		{"after document", io.MultiReader(strings.NewReader(document), brokenIO{sentinel})},
		{"data and error", &dataErrorReader{strings.NewReader(document), sentinel}},
	}
	for _, tc := range readers {
		t.Run("reader/"+tc.name, func(t *testing.T) {
			_, err := config.Decode(tc.reader)
			if !errors.Is(err, sentinel) {
				t.Fatalf("original read error not preserved: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{"immediate", brokenIO{sentinel}, sentinel},
		{"partial", partialWriter{sentinel}, sentinel},
		{"short write", partialWriter{}, io.ErrShortWrite},
	} {
		t.Run("writer/"+tc.name, func(t *testing.T) {
			err := config.Encode(tc.writer, fixture(t, "global"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("original write error not preserved: %v", err)
			}
		})
	}
}

type dataErrorReader struct {
	io.Reader

	err error
}

func (r *dataErrorReader) Read(p []byte) (int, error) {
	n, _ := r.Reader.Read(p)
	return n, r.err
}

type partialWriter struct{ err error }

func (w partialWriter) Write(p []byte) (int, error) { return len(p) / 2, w.err }

func TestTaggedContainers(t *testing.T) {
	const header = "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\n"
	for _, tc := range []struct{ name, body, want string }{
		{"null mapping", "spec: !!null {}", "spec: expected mapping"},
		{"null sequence", "spec: {defaults: {sources: !!null []}}", "spec.defaults.sources: expected list"},
		{"null mapping alias", "spec: {git: {author: &id !!null {}, committer: *id}}", "spec.git.author: expected mapping"},
		{"null sequence alias", "spec: {defaults: {sources: &list !!null [], roles: *list}}", "spec.defaults.sources: expected list"},
		{"custom mapping", "spec: !custom {}", "spec: expected mapping"},
		{"custom sequence", "spec: {defaults: {sources: !custom []}}", "spec.defaults.sources: expected list"},
		{"explicit mapping", "spec: !!map {defaults: !!map {sources: !!seq []}}", ""},
		{"valid sequence alias", "spec: {defaults: {sources: &list !!seq [], roles: *list, harnesses: *list}}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Decode(strings.NewReader(header + tc.body))
			assertError(t, err, tc.want)
		})
	}
}

func TestInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	for _, tc := range []struct {
		field  string
		change func(*config.TeamsConfig)
	}{
		{"apiVersion", func(c *config.TeamsConfig) { c.APIVersion = invalid }},
		{"kind", func(c *config.TeamsConfig) { c.Kind = invalid }},
		{"spec.git.author.name", func(c *config.TeamsConfig) { c.Spec.Git.Author.Name = invalid }},
		{"spec.git.author.email", func(c *config.TeamsConfig) { c.Spec.Git.Author.Email = invalid }},
		{"spec.git.committer.name", func(c *config.TeamsConfig) { c.Spec.Git.Committer.Name = invalid }},
		{"spec.git.committer.email", func(c *config.TeamsConfig) { c.Spec.Git.Committer.Email = invalid }},
		{"spec.git.signingKey", func(c *config.TeamsConfig) { c.Spec.Git.Sign = nil; c.Spec.Git.SigningKey = invalid }},
		{"spec.git.allowedSigners", func(c *config.TeamsConfig) { c.Spec.Git.AllowedSigners = invalid }},
		{"spec.git.sign[0]", func(c *config.TeamsConfig) { c.Spec.Git.Sign[0] = invalid }},
		{"spec.defaults.sources[0].name", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Name = invalid }},
		{"spec.defaults.sources[0].url", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].URL = invalid }},
		{"spec.defaults.sources[0].branch", func(c *config.TeamsConfig) { c.Spec.Defaults.Sources[0].Branch = invalid }},
		{"spec.defaults.sources[0].tag", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources[0].Branch = ""
			c.Spec.Defaults.Sources[0].Tag = invalid
		}},
		{"spec.defaults.sources[0].commit", func(c *config.TeamsConfig) {
			c.Spec.Defaults.Sources[0].Branch = ""
			c.Spec.Defaults.Sources[0].Commit = invalid
		}},
		{"spec.defaults.roles[0].name", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Name = invalid }},
		{"spec.defaults.roles[0].source", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Source = invalid }},
		{"spec.defaults.roles[0].path", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Path = invalid }},
		{"spec.defaults.roles[0].harness", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Harness = invalid }},
		{"spec.defaults.roles[0].needs.image[0]", func(c *config.TeamsConfig) { c.Spec.Defaults.Roles[0].Needs.Image[0] = invalid }},
		{"spec.defaults.harnesses[0].name", func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses[0].Name = invalid }},
		{"spec.defaults.harnesses[0].source", func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses[0].Source = invalid }},
		{"spec.defaults.harnesses[0].path", func(c *config.TeamsConfig) { c.Spec.Defaults.Harnesses[0].Path = invalid }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			cfg := fixture(t, "global")
			tc.change(cfg)
			assertError(t, config.Validate(cfg), tc.field+":")
			var out bytes.Buffer
			assertError(t, config.Encode(&out, cfg), tc.field+":")
			if out.Len() != 0 {
				t.Fatal("invalid UTF-8 model was written")
			}
		})
	}
}

func TestUnicodeRoundTrip(t *testing.T) {
	cfg := fixture(t, "global")
	cfg.Spec.Git.Author.Name = "Zoë 🦊"
	cfg.Spec.Git.SigningKey = "鍵.pub"
	cfg.Spec.Git.AllowedSigners = "署名者"
	var out bytes.Buffer
	if err := config.Encode(&out, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := config.Decode(&out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, got) {
		t.Fatal("Unicode values changed during round trip")
	}
}
