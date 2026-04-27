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

package cni

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// SubnetStateVersion is the schema version stamped into per-space network.json
// files written by the allocator. Bump it (and add a migration path) before
// changing the on-disk shape.
const SubnetStateVersion = "v1"

// SubnetStateFileName is the per-space allocator state file. It lives next to
// the existing network.conflist under <run-path>/<realm>/<space>/.
const SubnetStateFileName = "network.json"

// DefaultSubnetParentCIDR is the parent block subdivided into per-space /24s
// by the default SubnetAllocator. It matches the historical fixed bridge
// subnet, so spaces created post-allocator land inside the same /16 the
// shared-subnet conflists used.
const DefaultSubnetParentCIDR = defaultSubnetCIDR

// DefaultSubnetPrefixLen is the prefix length the allocator carves out of the
// parent CIDR. /24 yields 256 distinct /24 chunks of a /16, more than the
// realistic per-host space count, while still leaving 254 usable IPs per
// space.
const DefaultSubnetPrefixLen = 24

// ipv4Bits is the bit width of an IPv4 address. Hoisted so the bit-shift
// arithmetic that turns prefix lengths into address spans does not get
// flagged as a magic number, and so a future v6 fork has a single knob.
const ipv4Bits = 32

// ipv4OctetBits is the bit width of one octet of an IPv4 address. Used by
// uint32ToIP to peel octets off a packed uint32 address.
const ipv4OctetBits = 8

// SubnetState is the on-disk shape persisted at <space>/network.json. The
// allocator owns this file end-to-end; callers should not write it directly.
type SubnetState struct {
	Version    string `json:"version"`
	SubnetCIDR string `json:"subnetCIDR"`
}

// SubnetAllocator hands out per-space subnets carved from a parent CIDR and
// persists each assignment as a per-space network.json. The on-disk files are
// the source of truth: Allocate scans them at call time to discover which
// subnets are already in use, so the allocator survives daemon restarts
// without a separate cache.
type SubnetAllocator struct {
	mu         sync.Mutex
	runPath    string
	parentCIDR string
	prefixLen  int
	parsedNet  *net.IPNet
	parentSize int
	parentBits int
	subnetSpan uint32
}

// NewSubnetAllocator builds an allocator that subdivides parentCIDR into
// /prefixLen chunks and persists assignments under <runPath>/<realm>/<space>/.
// Use NewDefaultSubnetAllocator for the standard /24 of /16 layout.
func NewSubnetAllocator(runPath, parentCIDR string, prefixLen int) (*SubnetAllocator, error) {
	_, ipNet, err := net.ParseCIDR(parentCIDR)
	if err != nil {
		return nil, fmt.Errorf("%w: parent %q: %w", errdefs.ErrInvalidSubnetCIDR, parentCIDR, err)
	}
	if ipNet.IP.To4() == nil {
		return nil, fmt.Errorf("%w: parent %q must be IPv4", errdefs.ErrInvalidSubnetCIDR, parentCIDR)
	}
	parentBits, _ := ipNet.Mask.Size()
	if prefixLen <= parentBits || prefixLen > ipv4Bits {
		return nil, fmt.Errorf(
			"%w: prefix /%d must be longer than parent /%d and at most /%d",
			errdefs.ErrInvalidSubnetCIDR, prefixLen, parentBits, ipv4Bits,
		)
	}
	// Both shift amounts are bounded above by ipv4Bits and below by zero
	// (validated immediately above), so the int->uint conversions cannot
	// overflow at runtime. gosec G115 has no way to see that constraint, so
	// we mark each conversion explicitly rather than disabling the linter
	// repo-wide.
	span := uint32(1) << uint(ipv4Bits-prefixLen)           //nolint:gosec // bounded by validation
	totalSubnets := uint32(1) << uint(prefixLen-parentBits) //nolint:gosec // bounded by validation
	return &SubnetAllocator{
		runPath:    runPath,
		parentCIDR: parentCIDR,
		prefixLen:  prefixLen,
		parsedNet:  ipNet,
		parentSize: int(totalSubnets),
		parentBits: parentBits,
		subnetSpan: span,
	}, nil
}

// NewDefaultSubnetAllocator constructs the standard allocator: /24 chunks of
// 10.88.0.0/16 persisted under runPath.
func NewDefaultSubnetAllocator(runPath string) *SubnetAllocator {
	a, err := NewSubnetAllocator(runPath, DefaultSubnetParentCIDR, DefaultSubnetPrefixLen)
	if err != nil {
		// The default arguments are constants, so this can never fail at
		// runtime. Constructor returns *SubnetAllocator for ergonomic use at
		// daemon startup; if the constants ever drift apart, a panic here
		// surfaces the bug immediately rather than at first space-create.
		panic(fmt.Sprintf("kukeon: default subnet allocator misconfigured: %v", err))
	}
	return a
}

// ParentCIDR returns the parent block this allocator subdivides. Exposed for
// tests.
func (a *SubnetAllocator) ParentCIDR() string { return a.parentCIDR }

// PrefixLen returns the per-space prefix length (e.g. 24 for /24 chunks).
func (a *SubnetAllocator) PrefixLen() int { return a.prefixLen }

// statePath returns the on-disk path for the (realm, space) network-state file.
func (a *SubnetAllocator) statePath(realm, space string) string {
	return filepath.Join(a.runPath, realm, space, SubnetStateFileName)
}

// LoadAssigned returns the subnet currently persisted for (realm, space), or
// "" with no error when no assignment exists yet. Malformed state files
// surface as ErrSubnetStateCorrupt so callers can decide whether to repair or
// fail loudly.
func (a *SubnetAllocator) LoadAssigned(realm, space string) (string, error) {
	return a.readState(a.statePath(realm, space))
}

func (a *SubnetAllocator) readState(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read subnet state %s: %w", path, err)
	}
	var st SubnetState
	if uErr := json.Unmarshal(data, &st); uErr != nil {
		return "", fmt.Errorf("%w: %s: %w", errdefs.ErrSubnetStateCorrupt, path, uErr)
	}
	if strings.TrimSpace(st.SubnetCIDR) == "" {
		return "", fmt.Errorf("%w: %s: subnetCIDR is empty", errdefs.ErrSubnetStateCorrupt, path)
	}
	return st.SubnetCIDR, nil
}

// Allocate returns the subnet currently assigned to (realm, space). If none
// is persisted, the allocator picks the lowest free chunk inside the parent
// CIDR, writes <run-path>/<realm>/<space>/network.json, and returns the new
// subnet. Calls are serialized under an instance-wide mutex so concurrent
// space-create requests never hand out the same subnet.
func (a *SubnetAllocator) Allocate(realm, space string) (string, error) {
	if strings.TrimSpace(realm) == "" {
		return "", fmt.Errorf("%w: realm name is required", errdefs.ErrConfig)
	}
	if strings.TrimSpace(space) == "" {
		return "", fmt.Errorf("%w: space name is required", errdefs.ErrConfig)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	target := a.statePath(realm, space)
	if existing, err := a.readState(target); err != nil {
		return "", err
	} else if existing != "" {
		return existing, nil
	}

	used, err := a.usedSubnetsLocked()
	if err != nil {
		return "", err
	}

	free, err := a.firstFreeLocked(used)
	if err != nil {
		return "", err
	}

	if writeErr := writeSubnetState(target, free); writeErr != nil {
		return "", writeErr
	}
	return free, nil
}

// Release removes the persisted state for (realm, space) so the subnet
// becomes available for re-allocation. Idempotent: missing state is success.
func (a *SubnetAllocator) Release(realm, space string) error {
	if strings.TrimSpace(realm) == "" || strings.TrimSpace(space) == "" {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	target := a.statePath(realm, space)
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove subnet state %s: %w", target, err)
	}
	return nil
}

// usedSubnetsLocked walks <runPath>/<realm>/<space>/network.json and returns
// the set of subnets currently in use. Realm and space names are derived from
// the directory layout, so the allocator does not need to know about realm
// metadata. Caller must hold a.mu.
func (a *SubnetAllocator) usedSubnetsLocked() (map[string]struct{}, error) {
	used := make(map[string]struct{})
	realmEntries, err := os.ReadDir(a.runPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return used, nil
		}
		return nil, fmt.Errorf("read run-path %s: %w", a.runPath, err)
	}
	for _, realm := range realmEntries {
		if !realm.IsDir() {
			continue
		}
		realmDir := filepath.Join(a.runPath, realm.Name())
		spaceEntries, sErr := os.ReadDir(realmDir)
		if sErr != nil {
			if errors.Is(sErr, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read realm dir %s: %w", realmDir, sErr)
		}
		for _, space := range spaceEntries {
			if !space.IsDir() {
				continue
			}
			path := filepath.Join(realmDir, space.Name(), SubnetStateFileName)
			subnet, rErr := a.readState(path)
			if rErr != nil {
				// A corrupt network.json on disk must not block fresh
				// allocations elsewhere: failing the scan turns one bad
				// file into a host-wide denial of service for space
				// creates. Surface the path through the daemon log so an
				// operator can clean it up, then keep scanning for
				// healthy spaces.
				slog.Default().Warn("kukeon: skipping corrupt subnet state file during allocator scan",
					"path", path, "error", rErr)
				continue
			}
			if subnet == "" {
				continue
			}
			used[subnet] = struct{}{}
		}
	}
	return used, nil
}

// firstFreeLocked returns the lowest /prefixLen chunk inside the parent CIDR
// not present in used. Caller must hold a.mu.
func (a *SubnetAllocator) firstFreeLocked(used map[string]struct{}) (string, error) {
	base := a.parsedNet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("%w: parent %q is not IPv4", errdefs.ErrInvalidSubnetCIDR, a.parentCIDR)
	}
	baseInt := ipToUint32(base)
	for i := range a.parentSize {
		// i ranges over [0, parentSize) and parentSize fits in uint32 by
		// construction (it equals 1 << (prefixLen-parentBits) and prefixLen
		// is bounded by ipv4Bits). gosec G115 cannot see the constraint.
		offset := uint32(i) * a.subnetSpan //nolint:gosec // bounded by validation
		candidate := uint32ToIP(baseInt + offset)
		cidr := fmt.Sprintf("%s/%d", candidate.String(), a.prefixLen)
		if _, taken := used[cidr]; taken {
			continue
		}
		return cidr, nil
	}
	return "", fmt.Errorf("%w: parent %s exhausted at /%d", errdefs.ErrSubnetExhausted, a.parentCIDR, a.prefixLen)
}

func writeSubnetState(path, subnet string) error {
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr != nil {
		return fmt.Errorf("mkdir subnet state dir: %w", mkErr)
	}
	data, err := json.MarshalIndent(SubnetState{
		Version:    SubnetStateVersion,
		SubnetCIDR: subnet,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subnet state: %w", err)
	}
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return fmt.Errorf("write subnet state %s: %w", path, writeErr)
	}
	return nil
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	const o1, o2, o3 = ipv4OctetBits * 3, ipv4OctetBits * 2, ipv4OctetBits
	return uint32(ip[0])<<o1 | uint32(ip[1])<<o2 | uint32(ip[2])<<o3 | uint32(ip[3])
}

func uint32ToIP(v uint32) net.IP {
	const o1, o2, o3 = ipv4OctetBits * 3, ipv4OctetBits * 2, ipv4OctetBits
	return net.IPv4(byte(v>>o1), byte(v>>o2), byte(v>>o3), byte(v)).To4()
}
