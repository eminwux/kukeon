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

package main

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/hostnet"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// nestedCell is a kukeon-dev-root cell whose parent uses 10.88.0.0/16.
func nestedCell() hostnet.State {
	return hostnet.State{
		Links: []hostnet.Link{{Index: 1, Name: "lo", Up: true}, {Index: 2, Name: "eth0", Kind: "veth", Up: true}},
		Addresses: []hostnet.Address{
			{Link: "lo", Prefix: pfx("127.0.0.1/8")},
			{Link: "eth0", Prefix: pfx("10.88.0.15/24")},
		},
		Routes: []hostnet.Route{
			{
				Table: hostnet.MainTable, Link: "eth0", Dst: pfx("0.0.0.0/0"),
				Gateways: []netip.Addr{netip.MustParseAddr("10.88.0.1")},
			},
			{Table: hostnet.MainTable, Link: "eth0", Dst: pfx("10.88.0.0/24")},
		},
	}
}

func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func serverYAML(subnet string) string {
	body := "apiVersion: v1beta1\nkind: ServerConfiguration\nspec:\n  cgroupRoot: /kukeon-dev\n"
	if subnet != "" {
		body += "  podSubnetCIDR: " + subnet + "\n"
	}
	return body
}

func candidates(res resolution) []string {
	var out []string
	for _, c := range res.Candidates {
		out = append(out, c.Subnet.String()+" "+c.Source)
	}
	return out
}

func TestResolveConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	runPath := filepath.Join(dir, "run")
	dev := writeFile(t, filepath.Join(dir, "kukeond-dev.yaml"), serverYAML("10.89.0.0/16"))
	bare := writeFile(t, filepath.Join(dir, "bare.yaml"), serverYAML(""))
	client := writeFile(t, filepath.Join(dir, "kuke-dev.yaml"),
		"apiVersion: v1beta1\nkind: ClientConfiguration\nspec:\n  podSubnetCIDR: 10.90.0.0/16\n")
	missing := filepath.Join(dir, "absent.yaml")

	cases := []struct {
		name       string
		env        map[string]string
		serverFlag string
		want       []string
	}{
		{
			name:       "flag path with podSubnetCIDR",
			serverFlag: dev,
			want:       []string{"10.89.0.0/16 podSubnetCIDR in " + dev},
		},
		{
			name: "env path when no flag",
			env:  map[string]string{"KUKEOND_CONFIGURATION": dev},
			want: []string{"10.89.0.0/16 podSubnetCIDR in " + dev},
		},
		{
			name:       "flag beats env path",
			env:        map[string]string{"KUKEOND_CONFIGURATION": dev},
			serverFlag: missing,
			want:       []string{"10.88.0.0/16 compiled-in default; no podSubnetCIDR in " + missing},
		},
		{
			// KUKEON_PROFILE=dev alone is no proof: an existing file without
			// podSubnetCIDR leaves the default in effect.
			name:       "generated file without podSubnetCIDR",
			serverFlag: bare,
			want:       []string{"10.88.0.0/16 compiled-in default; no podSubnetCIDR in " + bare},
		},
		{
			// kuke init reads the env var; the kukeond it starts reads the
			// file only, so both are in effect.
			name:       "env subnet does not hide the file subnet",
			env:        map[string]string{"KUKEON_POD_SUBNET_CIDR": "10.91.0.0/16"},
			serverFlag: dev,
			want: []string{
				"10.91.0.0/16 KUKEON_POD_SUBNET_CIDR environment variable",
				"10.89.0.0/16 podSubnetCIDR in " + dev,
			},
		},
		{
			name:       "client configuration",
			env:        map[string]string{"KUKE_CONFIGURATION": client},
			serverFlag: dev,
			want: []string{
				"10.89.0.0/16 podSubnetCIDR in " + dev,
				"10.90.0.0/16 podSubnetCIDR in " + client,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res, err := resolveConfig(envMap(tt.env), tt.serverFlag, runPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := candidates(res); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("candidates = %q, want %q", got, tt.want)
			}
		})
	}

	if _, err := resolveConfig(envMap(map[string]string{"KUKEON_POD_SUBNET_CIDR": "fd00::/64"}), dev, runPath); err == nil {
		t.Fatal("an IPv6 override must fail the check, not be ignored")
	}
}

func TestResolveConfigPersistedSpaces(t *testing.T) {
	dir := t.TempDir()
	runPath := filepath.Join(dir, "run")
	dev := writeFile(t, filepath.Join(dir, "kukeond-dev.yaml"), serverYAML("10.89.0.0/16"))
	root := fs.MetadataRoot(runPath)
	conf := filepath.Join(root, "default", "default", "network.conflist")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteSpaceNetworkConfig(conf, "default-default", "10.89.0.0/24"); err != nil {
		t.Fatal(err)
	}
	// An allocator assignment whose conflist was not written yet, left by a
	// run on the default profile.
	writeFile(t, filepath.Join(root, "old", "space", cni.SubnetStateFileName),
		`{"version":"v1","subnetCIDR":"10.88.3.0/24"}`)
	// Realm metadata without a network is not a space.
	writeFile(t, filepath.Join(root, "default", "realm.json"), "{}")

	res, err := resolveConfig(envMap(nil), dev, runPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"10.89.0.0/16 podSubnetCIDR in " + dev,
		"10.89.0.0/24 persisted subnet of space default/default",
		"10.88.3.0/24 persisted subnet of space old/space",
	}
	if got := candidates(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %q, want %q", got, want)
	}
	bridge := cni.SafeBridgeName("default-default")
	if got := res.ownBridges(); !reflect.DeepEqual(got, map[string]netip.Prefix{bridge: pfx("10.89.0.0/24")}) {
		t.Fatalf("ownBridges = %v", got)
	}

	writeFile(t, filepath.Join(root, "bad", "space", cni.SubnetStateFileName), "{")
	if _, err = resolveConfig(envMap(nil), dev, runPath); err == nil {
		t.Fatal("unreadable space state must fail the check")
	}
}

func runGuard(t *testing.T, g guard, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	g.stdout, g.stderr = &out, &out
	if g.lookupEnv == nil {
		g.lookupEnv = envMap(nil)
	}
	return g.run(context.Background(), args), out.String()
}

func stateFunc(st *hostnet.State) func(context.Context) (hostnet.State, error) {
	return func(context.Context) (hostnet.State, error) { return *st, nil }
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	runPath := filepath.Join(dir, "run")
	dev := writeFile(t, filepath.Join(dir, "kukeond-dev.yaml"), serverYAML("10.89.0.0/16"))
	st := nestedCell()
	g := guard{readState: stateFunc(&st)}

	code, out := runGuard(t, g, "check", "--run-path", runPath, "--server-configuration", filepath.Join(dir, "none"))
	if code != exitConflict || !strings.Contains(out, "gateway 10.88.0.1 dev eth0") ||
		!strings.Contains(out, "KUKEON_PROFILE=dev make dev-init") {
		t.Fatalf("default subnet in a 10.88 cell: code %d\n%s", code, out)
	}

	code, out = runGuard(t, g, "check", "--run-path", runPath, "--server-configuration", dev)
	if code != exitOK {
		t.Fatalf("disjoint subnet rejected: code %d\n%s", code, out)
	}

	// Rerun: the first run's space bridge is up with its gateway address.
	conf := filepath.Join(fs.MetadataRoot(runPath), "default", "default", "network.conflist")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteSpaceNetworkConfig(conf, "default-default", "10.89.0.0/24"); err != nil {
		t.Fatal(err)
	}
	bridge := cni.SafeBridgeName("default-default")
	st.Links = append(st.Links, hostnet.Link{Index: 3, Name: bridge, Kind: "bridge", Up: true})
	st.Addresses = append(st.Addresses, hostnet.Address{Link: bridge, Prefix: pfx("10.89.0.1/24")})
	st.Routes = append(st.Routes, hostnet.Route{Table: hostnet.MainTable, Link: bridge, Dst: pfx("10.89.0.0/24")})
	if code, out = runGuard(t, g, "check", "--run-path", runPath, "--server-configuration", dev); code != exitOK {
		t.Fatalf("own bridge rejected on rerun: code %d\n%s", code, out)
	}

	// A bridge that is not this instance's still conflicts.
	st.Addresses = append(st.Addresses, hostnet.Address{Link: "k-foreign", Prefix: pfx("10.89.5.1/24")})
	code, out = runGuard(t, g, "check", "--run-path", runPath, "--server-configuration", dev)
	if code != exitConflict || !strings.Contains(out, "interface address 10.89.5.1/24 dev k-foreign") {
		t.Fatalf("foreign bridge accepted: code %d\n%s", code, out)
	}
}

func TestSnapshotVerify(t *testing.T) {
	base := filepath.Join(t.TempDir(), "uplink.json")
	st := nestedCell()
	answers := true
	g := guard{
		readState: stateFunc(&st),
		probe:     func(netip.Addr) (bool, error) { return answers, nil },
	}
	if code, out := runGuard(t, g, "snapshot", "--out", base); code != exitOK {
		t.Fatalf("snapshot: code %d\n%s", code, out)
	}
	if code, out := runGuard(t, g, "verify", "--baseline", base, "--phase", "after reset"); code != exitOK ||
		!strings.Contains(out, "uplink preserved after reset") {
		t.Fatalf("unchanged uplink: code %d\n%s", code, out)
	}

	answers = false
	code, out := runGuard(t, g, "verify", "--baseline", base)
	if code != exitEgressOnly || !strings.Contains(out, "EGRESS REGRESSION") || strings.Contains(out, "STRUCTURAL") {
		t.Fatalf("egress-only regression: code %d\n%s", code, out)
	}

	st.Links = st.Links[:1]
	st.Routes = nil
	code, out = runGuard(t, g, "verify", "--baseline", base)
	if code != exitConflict || !strings.Contains(out, "STRUCTURAL DAMAGE") ||
		!strings.Contains(out, "uplink link eth0 is gone") {
		t.Fatalf("deleted uplink: code %d\n%s", code, out)
	}

	// A gateway that did not answer at baseline is not checked later.
	st = nestedCell()
	if code, out = runGuard(t, g, "snapshot", "--out", base); code != exitOK ||
		!strings.Contains(out, "no working baseline") {
		t.Fatalf("snapshot without answering gateway: code %d\n%s", code, out)
	}
	if code, out = runGuard(t, g, "verify", "--baseline", base); code != exitOK {
		t.Fatalf("silent gateway at baseline must not fail verify: code %d\n%s", code, out)
	}
}

func TestEchoRequest(t *testing.T) {
	msg := echoRequest(0x1234, 2)
	if checksum(msg) != 0 {
		t.Fatalf("checksum of a checksummed message = %#x, want 0", checksum(msg))
	}
	reply := append([]byte(nil), msg...)
	reply[0] = icmpEchoReply
	if !isEchoReply(reply, 0x1234, 2) || isEchoReply(reply, 0x1234, 3) || isEchoReply(msg, 0x1234, 2) {
		t.Fatal("isEchoReply matched the wrong message")
	}
}
