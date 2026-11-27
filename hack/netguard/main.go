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

// Command netguard is the network guard scripts/dev-init.sh runs when it is
// nested inside a kukeon cell (docs/dev-init.md). It reads the cell's network
// over netlink, so the cell needs no iproute2.
//
//	netguard check [--server-configuration PATH] [--run-path PATH]
//	    Exit 1 when a pod subnet the run can use overlaps the cell's
//	    interface networks, routes or gateways. There is no override.
//	netguard snapshot --out FILE
//	    Record the cell's uplink (default routes, their links and
//	    addresses) and whether each gateway answers ICMP.
//	netguard verify --baseline FILE --phase TEXT
//	    Compare the uplink against the snapshot. Exit 1 on structural
//	    damage, 3 when only a gateway that answered at baseline stopped
//	    answering. It reports; it repairs nothing.
//
// Run it as root, under the same sudo environment as the kuke commands it
// guards: it reads the run path's metadata and opens a raw ICMP socket.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/hostnet"
)

const (
	exitOK         = 0
	exitConflict   = 1
	exitUsage      = 2
	exitEgressOnly = 3
)

// baseline is the snapshot file written by `snapshot` and read by `verify`.
type baseline struct {
	Uplink hostnet.Uplink `json:"uplink"`
	// Probed is false when no raw ICMP socket was available; gateway
	// reachability is then not checked at all.
	Probed bool `json:"probed"`
	// Answering lists the gateways that answered ICMP at baseline. Only
	// these are re-probed: a gateway that never answered is no baseline.
	Answering []netip.Addr `json:"answering"`
}

type guard struct {
	readState func(context.Context) (hostnet.State, error)
	probe     func(netip.Addr) (bool, error)
	lookupEnv func(string) (string, bool)
	stdout    io.Writer
	stderr    io.Writer
}

func main() {
	g := guard{
		readState: hostnet.Read,
		probe:     probeGateway,
		lookupEnv: os.LookupEnv,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
	}
	os.Exit(g.run(context.Background(), os.Args[1:]))
}

func (g guard) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(g.stderr, "usage: netguard check|snapshot|verify [flags]")
		return exitUsage
	}
	var err error
	code := exitOK
	switch args[0] {
	case "check":
		code, err = g.check(ctx, args[1:])
	case "snapshot":
		err = g.snapshot(ctx, args[1:])
	case "verify":
		code, err = g.verify(ctx, args[1:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintf(g.stderr, "netguard %s: %v\n", args[0], err)
		return exitUsage
	}
	return code
}

func (g guard) check(ctx context.Context, args []string) (int, error) {
	fl := flag.NewFlagSet("check", flag.ContinueOnError)
	fl.SetOutput(g.stderr)
	serverPath := fl.String("server-configuration", "", "kukeond ServerConfiguration kuke init will read")
	runPath := fl.String("run-path", "", "run path kuke init will use")
	if err := fl.Parse(args); err != nil {
		return exitUsage, err
	}
	res, err := resolveConfig(g.lookupEnv, *serverPath, *runPath)
	if err != nil {
		return exitUsage, err
	}
	st, err := g.readState(ctx)
	if err != nil {
		return exitUsage, err
	}
	own := hostnet.OwnBridges(res.ownBridges())
	failed := false
	for _, c := range res.Candidates {
		conflicts := hostnet.Conflicts(c.Subnet, st, own)
		if len(conflicts) == 0 {
			fmt.Fprintf(g.stdout, "netguard: pod subnet %s (%s) is clear of this cell's network\n", c.Subnet, c.Source)
			continue
		}
		failed = true
		fmt.Fprintf(g.stderr, "netguard: pod subnet %s (%s) overlaps this cell's network:\n", c.Subnet, c.Source)
		for _, conflict := range conflicts {
			fmt.Fprintf(g.stderr, "    %s\n", conflict)
		}
	}
	if !failed {
		return exitOK, nil
	}
	fmt.Fprintf(g.stderr, `
netguard: refusing to continue. A nested kukeond bridge on an overlapping subnet
          takes over addresses this cell routes through: claiming its gateway
          makes the gateway a local address and cuts the cell's egress (#1079).
          To fix it, give the nested instance a block that is disjoint from every
          entry above:
            - KUKEON_PROFILE=dev make dev-init   (writes kukeond-dev.yaml with
              podSubnetCIDR 10.89.0.0/16 unless the file already exists), or
            - set podSubnetCIDR in %s (and in KUKE_CONFIGURATION's file, if set)
              and unset a conflicting KUKEON_POD_SUBNET_CIDR.
          A persisted space keeps its subnet until it is purged:
            sudo ./kuke purge space <space> --realm <realm> --cascade
          (for the kuke-system realm: sudo ./kuke daemon reset --purge-system).
`, res.ServerConfig)
	return exitConflict, nil
}

func (g guard) snapshot(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fl.SetOutput(g.stderr)
	out := fl.String("out", "", "file to write the baseline to")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	st, err := g.readState(ctx)
	if err != nil {
		return err
	}
	b := baseline{Uplink: hostnet.UplinkOf(st), Probed: true}
	for _, r := range b.Uplink.DefaultRoutes {
		if !r.Gateway.IsValid() {
			continue
		}
		ok, probeErr := g.probe(r.Gateway)
		if errors.Is(probeErr, errProbeUnavailable) {
			b.Probed = false
			fmt.Fprintf(g.stdout, "netguard: %v; gateway reachability will not be checked\n", probeErr)
			break
		}
		if probeErr != nil {
			return probeErr
		}
		if ok {
			b.Answering = append(b.Answering, r.Gateway)
		} else {
			fmt.Fprintf(g.stdout, "netguard: gateway %s does not answer ICMP now; its reachability "+
				"has no working baseline and will not be checked\n", r.Gateway)
		}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(*out, data, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(g.stdout, "netguard: uplink baseline: %s\n", describe(b))
	return nil
}

func (g guard) verify(ctx context.Context, args []string) (int, error) {
	fl := flag.NewFlagSet("verify", flag.ContinueOnError)
	fl.SetOutput(g.stderr)
	path := fl.String("baseline", "", "file written by snapshot")
	phase := fl.String("phase", "now", "when the check runs, for the report")
	if err := fl.Parse(args); err != nil {
		return exitUsage, err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return exitUsage, err
	}
	var b baseline
	if err = json.Unmarshal(data, &b); err != nil {
		return exitUsage, fmt.Errorf("%s: %w", *path, err)
	}
	st, err := g.readState(ctx)
	if err != nil {
		return exitUsage, err
	}
	damage := b.Uplink.Damage(st)
	var silent []string
	for _, gw := range b.Answering {
		ok, probeErr := g.probe(gw)
		switch {
		case probeErr != nil:
			silent = append(silent, fmt.Sprintf("gateway %s could not be re-probed: %v", gw, probeErr))
		case !ok:
			silent = append(silent, fmt.Sprintf("gateway %s answered ICMP at baseline and no longer does", gw))
		}
	}
	if len(damage) > 0 {
		fmt.Fprintf(g.stderr, "\nnetguard: STRUCTURAL DAMAGE to this cell's uplink %s:\n", *phase)
		for _, d := range damage {
			fmt.Fprintf(g.stderr, "    %s\n", d)
		}
		fmt.Fprintln(g.stderr, "netguard: a HostNetwork CNI DEL against this cell's own netns (#1219) has this "+
			"signature.\n          Nothing was repaired; re-plumb the uplink before re-running.")
	}
	if len(silent) > 0 {
		fmt.Fprintf(g.stderr, "\nnetguard: EGRESS REGRESSION %s (reported separately from structure):\n", *phase)
		for _, s := range silent {
			fmt.Fprintf(g.stderr, "    %s\n", s)
		}
	}
	switch {
	case len(damage) > 0:
		return exitConflict, nil
	case len(silent) > 0:
		return exitEgressOnly, nil
	}
	fmt.Fprintf(g.stdout, "netguard: uplink preserved %s: %s\n", *phase, describe(b))
	return exitOK, nil
}

func describe(b baseline) string {
	if len(b.Uplink.DefaultRoutes) == 0 {
		return "no default route (nothing to compare)"
	}
	var parts []string
	for _, r := range b.Uplink.DefaultRoutes {
		parts = append(parts, r.String())
	}
	for _, l := range b.Uplink.Links {
		state := "down"
		if l.Up {
			state = "up"
		}
		addrs := make([]string, len(l.Addresses))
		for i, a := range l.Addresses {
			addrs[i] = a.String()
		}
		parts = append(parts, fmt.Sprintf("%s %s [%s]", l.Name, state, strings.Join(addrs, " ")))
	}
	switch {
	case !b.Probed:
		parts = append(parts, "gateway reachability not probed")
	case len(b.Answering) > 0:
		gws := make([]string, len(b.Answering))
		for i, gw := range b.Answering {
			gws[i] = gw.String()
		}
		parts = append(parts, "answering gateways "+strings.Join(gws, " "))
	}
	return strings.Join(parts, "; ")
}
