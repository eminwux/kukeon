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
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/hostnet"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

// Each suite owns the checked pool exclusively; each run path gets a /20
// (sixteen /24 spaces). The lock covers separate go test processes too. Keep
// the lock file: unlinking it would let waiters lock different inodes.
//
//nolint:gochecknoglobals // suite-wide reservations shared by parallel tests and TestMain
var e2eNetwork struct {
	mu sync.Mutex

	once    sync.Once
	err     error
	lock    *os.File
	pool    netip.Prefix
	next    uint32
	subnets map[string]string
	bridges map[string]bool
	egress  map[string]bool
}

func TestMain(m *testing.M) {
	code := m.Run()
	if e2eNetwork.lock != nil {
		// The pool was empty at preflight. Any remaining route/address or egress
		// chain is a leak; never delete arbitrary links or rules here or hide a
		// failed test cleanup.
		if err := verifyE2ENetworkCleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "E2E network cleanup failed: %v\n", err)
			code = 1
		}
		_ = e2eNetwork.lock.Close()
	}
	os.Exit(code)
}

// Snapshot names too: a bridge left without an address is still a leak.
func bridgeNames() (map[string]bool, error) {
	st, err := readHostNetwork()
	if err != nil {
		return nil, err
	}
	return st.Bridges(), nil
}

// readHostNetwork reads links, addresses and routes over netlink, so the
// harness needs no iproute2 (`ip`) on the host.
func readHostNetwork() (hostnet.State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hostnet.Read(ctx)
}

func verifyE2ENetworkCleanup() error {
	names, err := bridgeNames()
	if err != nil {
		return err
	}
	for name := range names {
		if strings.HasPrefix(name, "k-") && !e2eNetwork.bridges[name] {
			return fmt.Errorf("test-created bridge %s remains (not deleting it automatically)", name)
		}
	}
	egress, err := egressArtifacts()
	if err != nil {
		return err
	}
	var leaked []string
	for artifact := range egress {
		if !e2eNetwork.egress[artifact] {
			leaked = append(leaked, artifact)
		}
	}
	if len(leaked) > 0 {
		// List every entry: a rule's comment names the realm and space,
		// which map back to the tests that allocated them.
		sort.Strings(leaked)
		for i, artifact := range leaked {
			leaked[i] = attributeEgressArtifact(artifact)
		}
		return fmt.Errorf(
			"test-created egress iptables entries remain (not deleting them automatically):\n%s",
			strings.Join(leaked, "\n"),
		)
	}
	return checkNetworkPool(e2eNetwork.pool)
}

// nameOwners maps each generated realm/space name to the test that
// allocated it. Tests run in parallel and a leaked egress rule only names
// the realm and space, so the suite-level leak report needs this to say
// which test leaked it (issue #1469).
//
//nolint:gochecknoglobals // suite-wide registry shared by parallel tests and TestMain
var nameOwners sync.Map

// recordNameOwner registers t as the owner of name and returns name.
func recordNameOwner(t *testing.T, name string) string {
	t.Helper()
	nameOwners.Store(name, t.Name())
	return name
}

// attributeEgressArtifact appends the owning tests to an egress rule whose
// comment names a kukeon realm and space ("kukeon:<realm>:<space>:<kind>").
// Artifacts without such a comment, or with unregistered names, are
// returned unchanged.
func attributeEgressArtifact(artifact string) string {
	_, rest, found := strings.Cut(artifact, `--comment "kukeon:`)
	if !found {
		return artifact
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 3 {
		return artifact
	}
	var owners []string
	for _, name := range parts[:2] {
		if owner, ok := nameOwners.Load(name); ok {
			owners = append(owners, fmt.Sprintf("%s from %s", name, owner))
		}
	}
	if len(owners) == 0 {
		return artifact
	}
	return artifact + " [" + strings.Join(owners, "; ") + "]"
}

// requireInitEgressRemoved fails t when, after every other cleanup of t
// has run, an egress chain for a space `kuke init` seeds is left behind.
// Chain names are host-global, so the suite-level check alone cannot say
// which test leaked one.
func requireInitEgressRemoved(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		egress, err := egressArtifacts()
		if err != nil {
			t.Errorf("snapshot egress iptables state: %v", err)
			return
		}
		for _, scope := range [][2]string{
			{consts.KukeonDefaultRealmName, consts.KukeonDefaultSpaceName},
			{consts.KukeSystemRealmName, consts.KukeSystemSpaceName},
		} {
			chain := "filter: chain " + netpolicy.NewAdmitAllPolicy(scope[0], scope[1], "").ChainName()
			if egress[chain] && !e2eNetwork.egress[chain] {
				t.Errorf("%s/%s egress %s remains after cleanup", scope[0], scope[1], chain)
			}
		}
	})
}

// egressArtifacts snapshots the per-space egress chains and their dispatch
// rules. Their bridges can already be gone, so the bridge check misses them,
// and a run path removed afterwards leaves no daemon able to reconcile them.
func egressArtifacts() (map[string]bool, error) {
	artifacts := make(map[string]bool)
	if _, err := exec.LookPath("iptables-save"); err != nil {
		return artifacts, nil // no iptables: the daemon installs no egress chains
	}
	for _, table := range []string{"filter", "nat"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, "iptables-save", "-t", table).Output()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("iptables-save -t %s: %w", table, err)
		}
		for artifact := range parseEgressArtifacts(table, out) {
			artifacts[artifact] = true
		}
	}
	return artifacts, nil
}

// parseEgressArtifacts keys entries by table so the same line in filter and
// nat stays distinct. The KUKEON-EGRESS master chain itself is shared by every
// space and intentionally outlives them, so only its rules count.
func parseEgressArtifacts(table string, save []byte) map[string]bool {
	artifacts := make(map[string]bool)
	for _, line := range strings.Split(string(save), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, ":KUKE-EGR-"):
			name, _, _ := strings.Cut(line[1:], " ")
			artifacts[table+": chain "+name] = true
		case strings.HasPrefix(line, "-A KUKEON-EGRESS "), strings.HasPrefix(line, "-A KUKE-EGR-"):
			artifacts[table+": "+line] = true
		}
	}
	return artifacts
}

// validateNetworkPool ignores the default destination but never its gateway.
// All route tables and interface addresses participate, including DOWN links.
func validateNetworkPool(pool netip.Prefix, st hostnet.State) error {
	conflicts := hostnet.Conflicts(pool, st, nil)
	if len(conflicts) == 0 {
		return nil
	}
	parts := make([]string, len(conflicts))
	for i, c := range conflicts {
		parts[i] = c.String()
	}
	return fmt.Errorf("pod pool %s overlaps %s", pool, strings.Join(parts, "; "))
}

func checkNetworkPool(pool netip.Prefix) error {
	st, err := readHostNetwork()
	if err != nil {
		return err
	}
	return validateNetworkPool(pool, st)
}

func initE2ENetwork() {
	raw := os.Getenv("E2E_POD_CIDR_POOL")
	if raw == "" {
		raw = "10.192.0.0/12"
	}
	pool, err := netip.ParsePrefix(raw)
	if err != nil || !pool.Addr().Is4() || pool.Bits() > 20 || pool != pool.Masked() {
		e2eNetwork.err = fmt.Errorf("E2E_POD_CIDR_POOL must be an aligned IPv4 CIDR of /20 or wider: %q", raw)
		return
	}
	// Host-wide rather than per-pool: differently configured overlapping pools
	// must not race. Fail promptly; never silently run without the lock.
	lock, err := os.OpenFile("/run/kukeon-e2e-network.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		e2eNetwork.err = err
		return
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		e2eNetwork.err = fmt.Errorf("another E2E suite owns the network lock: %w", err)
		return
	}
	if err = checkNetworkPool(pool); err != nil {
		_ = lock.Close()
		e2eNetwork.err = err
		return
	}
	bridges, err := bridgeNames()
	if err != nil {
		_ = lock.Close()
		e2eNetwork.err = err
		return
	}
	egress, err := egressArtifacts()
	if err != nil {
		_ = lock.Close()
		e2eNetwork.err = err
		return
	}
	e2eNetwork.bridges, e2eNetwork.egress = bridges, egress
	e2eNetwork.lock, e2eNetwork.pool = lock, pool
	e2eNetwork.subnets = make(map[string]string)
}

func reserveE2ESubnet(t *testing.T, runPath string) {
	t.Helper()
	e2eNetwork.once.Do(initE2ENetwork)
	if e2eNetwork.err != nil {
		t.Fatalf("E2E network preflight: %v", e2eNetwork.err)
	}
	e2eNetwork.mu.Lock()
	defer e2eNetwork.mu.Unlock()
	subnet, err := e2eSubnet(e2eNetwork.pool, e2eNetwork.next)
	if err != nil {
		t.Fatal(err)
	}
	e2eNetwork.next++
	e2eNetwork.subnets[runPath] = subnet.String()
	// init's containerized daemon reads this same explicit configuration.
	body := fmt.Sprintf(
		"apiVersion: v1beta1\nkind: ServerConfiguration\nspec:\n  podSubnetCIDR: %s\n",
		subnet,
	)
	if err = os.WriteFile(filepath.Join(runPath, "kukeond.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	clientBody := strings.Replace(body, "kind: ServerConfiguration", "kind: ClientConfiguration", 1)
	if err = os.WriteFile(filepath.Join(runPath, "kuke.yaml"), []byte(clientBody), 0o600); err != nil {
		t.Fatal(err)
	}
}

func e2eSubnet(pool netip.Prefix, index uint32) (netip.Prefix, error) {
	count := uint32(1) << uint(20-pool.Bits())
	if index >= count {
		return netip.Prefix{}, fmt.Errorf("E2E pod pool %s exhausted", pool)
	}
	base := pool.Addr().As4()
	binary.BigEndian.PutUint32(base[:], binary.BigEndian.Uint32(base[:])+index*(1<<12))
	return netip.PrefixFrom(netip.AddrFrom4(base), 20), nil
}

func e2eRunEnv(env []string, args []string) []string {
	if env == nil {
		env = os.Environ()
	}
	var runPath string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--run-path" {
			runPath = args[i+1]
			break
		}
	}
	if runPath == "" {
		return env
	}
	e2eNetwork.mu.Lock()
	subnet := e2eNetwork.subnets[runPath]
	e2eNetwork.mu.Unlock()
	if subnet == "" {
		return env
	} // CLI argument/error tests may use dummy paths.
	result := make([]string, 0, len(env)+2)
	for _, value := range env {
		if !strings.HasPrefix(value, "KUKEON_POD_SUBNET_CIDR=") &&
			!strings.HasPrefix(value, "KUKEOND_CONFIGURATION=") &&
			!strings.HasPrefix(value, "KUKE_CONFIGURATION=") {
			result = append(result, value)
		}
	}
	return append(
		result,
		"KUKEON_POD_SUBNET_CIDR="+subnet,
		"KUKEOND_CONFIGURATION="+filepath.Join(runPath, "kukeond.yaml"),
		"KUKE_CONFIGURATION="+filepath.Join(runPath, "kuke.yaml"),
	)
}

func TestE2ERunEnvUsesRunPathClientConfiguration(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "run")
	e2eNetwork.mu.Lock()
	previousSubnets := e2eNetwork.subnets
	e2eNetwork.subnets = map[string]string{runPath: "10.192.0.0/20"}
	e2eNetwork.mu.Unlock()
	t.Cleanup(func() {
		e2eNetwork.mu.Lock()
		e2eNetwork.subnets = previousSubnets
		e2eNetwork.mu.Unlock()
	})

	env := e2eRunEnv([]string{
		"KEEP=value",
		"KUKE_CONFIGURATION=/home/test/.kuke/kuke.yaml",
	}, []string{"--run-path", runPath})
	if got, want := env[len(env)-1], "KUKE_CONFIGURATION="+filepath.Join(runPath, "kuke.yaml"); got != want {
		t.Fatalf("client configuration = %q, want %q (env: %v)", got, want, env)
	}
	if got, want := env[0], "KEEP=value"; got != want {
		t.Fatalf("preserved environment = %q, want %q", got, want)
	}
}

// The overlap rules themselves are covered in internal/hostnet; this pins
// the harness's use of them on the suite's pool.
func TestE2ENetworkPreflight(t *testing.T) {
	parent := hostnet.State{
		Addresses: []hostnet.Address{{Link: "eth0", Prefix: netip.MustParsePrefix("10.88.0.15/24")}},
		Routes: []hostnet.Route{
			{
				Table: hostnet.MainTable, Link: "eth0", Dst: netip.MustParsePrefix("0.0.0.0/0"),
				Gateways: []netip.Addr{netip.MustParseAddr("10.88.0.1")},
			},
			{Table: hostnet.MainTable, Link: "eth0", Dst: netip.MustParsePrefix("10.88.0.0/24")},
		},
	}
	if err := validateNetworkPool(netip.MustParsePrefix("10.192.0.0/12"), parent); err != nil {
		t.Fatalf("safe pool rejected: %v", err)
	}
	err := validateNetworkPool(netip.MustParsePrefix("10.88.0.0/16"), parent)
	if err == nil || !strings.Contains(err.Error(), "gateway 10.88.0.1 dev eth0") {
		t.Fatalf("parent gateway not rejected: %v", err)
	}
}

func TestE2EEgressArtifacts(t *testing.T) {
	save := []byte(`*filter
:INPUT ACCEPT [0:0]
:KUKE-EGR-feb2b3cb - [0:0]
:KUKEON-EGRESS - [0:0]
-A FORWARD -j KUKEON-EGRESS
-A KUKE-EGR-feb2b3cb -m comment --comment "kukeon:rcomp:scomp:default" -j ACCEPT
-A KUKEON-EGRESS -i k-fb4e74c1 -m comment --comment "kukeon:rcomp:scomp:dispatch" -j KUKE-EGR-feb2b3cb
-A KUKEON-FORWARD ! -i k-+ -o k-+ -j ACCEPT
COMMIT
`)
	got := parseEgressArtifacts("filter", save)
	want := map[string]bool{
		"filter: chain KUKE-EGR-feb2b3cb": true,
		`filter: -A KUKE-EGR-feb2b3cb -m comment --comment "kukeon:rcomp:scomp:default" -j ACCEPT`: true,
		`filter: -A KUKEON-EGRESS -i k-fb4e74c1 -m comment --comment "kukeon:rcomp:scomp:dispatch" ` +
			`-j KUKE-EGR-feb2b3cb`: true,
	}
	if len(got) != len(want) {
		t.Fatalf("artifacts = %v, want %v", got, want)
	}
	for artifact := range want {
		if !got[artifact] {
			t.Fatalf("missing %q in %v", artifact, got)
		}
	}
}

func TestE2ESubnetReservations(t *testing.T) {
	pool := netip.MustParsePrefix("10.192.0.0/12")
	seen := map[netip.Prefix]bool{}
	for i := range uint32(256) {
		subnet, err := e2eSubnet(pool, i)
		if err != nil || seen[subnet] || !pool.Contains(subnet.Addr()) {
			t.Fatalf("reservation %d: %s %v", i, subnet, err)
		}
		seen[subnet] = true
	}
	if _, err := e2eSubnet(pool, 256); err == nil {
		t.Fatal("expected exhaustion")
	}
}

// Verify effective daemon configuration on the real CLI, not
// only the arithmetic that reserves the blocks. Creating an empty space writes
// its CNI config but does not attach a workload or claim a gateway address.
func TestE2ENetworkConfiguredSubnet(t *testing.T) {
	t.Parallel()
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realm, space := generateUniqueRealmName(t), generateUniqueSpaceName(t)
	t.Cleanup(func() { cleanupRealm(t, host, realm) })
	args := append(buildKukeDaemonArgs(host), "create", "realm", realm)
	runReturningBinary(t, nil, kuke, args...)
	args = append(buildKukeDaemonArgs(host), "create", "space", space, "--realm", realm)
	runReturningBinary(t, nil, kuke, args...)
	data, err := os.ReadFile(filepath.Join(runPath, "data", realm, space, "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		SubnetCIDR string `json:"subnetCIDR"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	assigned, err := netip.ParsePrefix(state.SubnetCIDR)
	if err != nil {
		t.Fatal(err)
	}
	e2eNetwork.mu.Lock()
	reserved := netip.MustParsePrefix(e2eNetwork.subnets[runPath])
	e2eNetwork.mu.Unlock()
	if assigned.Bits() != 24 || !reserved.Contains(assigned.Addr()) {
		t.Fatalf("assigned %s outside %s", assigned, reserved)
	}
}

func TestE2EAttributeEgressArtifact(t *testing.T) {
	realm := recordNameOwner(t, "e-r-attr1")
	space := recordNameOwner(t, "e-sp-attr2")
	rule := `filter: -A KUKEON-EGRESS -i k-caddc8f8 -m comment --comment "kukeon:` +
		realm + `:` + space + `:dispatch" -j KUKE-EGR-145a1e32`
	want := rule + " [e-r-attr1 from " + t.Name() + "; e-sp-attr2 from " + t.Name() + "]"
	if got := attributeEgressArtifact(rule); got != want {
		t.Fatalf("attributeEgressArtifact = %q, want %q", got, want)
	}
	for _, unchanged := range []string{
		"filter: chain KUKE-EGR-145a1e32",
		`filter: -A KUKEON-EGRESS -m comment --comment "kukeon:e-r-unknown:e-sp-unknown:dispatch" -j X`,
	} {
		if got := attributeEgressArtifact(unchanged); got != unchanged {
			t.Fatalf("attributeEgressArtifact(%q) = %q, want unchanged", unchanged, got)
		}
	}
}
