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
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
}

func TestMain(m *testing.M) {
	code := m.Run()
	if e2eNetwork.lock != nil {
		// The pool was empty at preflight. Any remaining route/address is a leak;
		// never delete arbitrary links here or hide a failed test cleanup.
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
	data, err := networkJSON("-j", "link", "show", "type", "bridge")
	if err != nil {
		return nil, err
	}
	var links []struct {
		Name string `json:"ifname"`
	}
	if err = json.Unmarshal(data, &links); err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, link := range links {
		names[link.Name] = true
	}
	return names, nil
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
	return checkNetworkPool(e2eNetwork.pool)
}

func networkJSON(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ip %v: %w: %s", args, err, out)
	}
	return out, nil
}

// validateNetworkPool ignores the default destination but never its gateway.
// All route tables and interface addresses participate, including DOWN links.
func validateNetworkPool(pool netip.Prefix, routes, addresses []byte) error {
	var rows []struct {
		Dst      string `json:"dst"`
		Gateway  string `json:"gateway"`
		Nexthops []struct {
			Gateway string `json:"gateway"`
		} `json:"nexthops"`
	}
	if err := json.Unmarshal(routes, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		gateways := []string{row.Gateway}
		for _, hop := range row.Nexthops {
			gateways = append(gateways, hop.Gateway)
		}
		for _, gateway := range gateways {
			if gateway == "" {
				continue
			}
			gw, err := netip.ParseAddr(gateway)
			if err != nil {
				return err
			}
			if pool.Contains(gw) {
				return fmt.Errorf("pod pool %s overlaps gateway %s", pool, gw)
			}
		}
		if row.Dst == "" || row.Dst == "default" {
			continue
		}
		dst, err := netip.ParsePrefix(row.Dst)
		if err != nil {
			addr, addrErr := netip.ParseAddr(row.Dst)
			if addrErr != nil {
				return err
			}
			dst = netip.PrefixFrom(addr, addr.BitLen())
		}
		if dst.Bits() != 0 && pool.Overlaps(dst) {
			return fmt.Errorf("pod pool %s overlaps route %s", pool, dst)
		}
	}
	var links []struct {
		Ifname    string `json:"ifname"`
		Addresses []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(addresses, &links); err != nil {
		return err
	}
	for _, link := range links {
		for _, address := range link.Addresses {
			addr, err := netip.ParseAddr(address.Local)
			if err != nil {
				return err
			}
			prefix := netip.PrefixFrom(addr, address.PrefixLen)
			if !prefix.IsValid() {
				return fmt.Errorf("invalid address on %s", link.Ifname)
			}
			if pool.Overlaps(prefix) {
				return fmt.Errorf("pod pool %s overlaps interface %s (%s)", pool, link.Ifname, prefix)
			}
		}
	}
	return nil
}

func checkNetworkPool(pool netip.Prefix) error {
	routes, err := networkJSON("-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return err
	}
	addresses, err := networkJSON("-j", "-4", "address", "show")
	if err != nil {
		return err
	}
	return validateNetworkPool(pool, routes, addresses)
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
	e2eNetwork.bridges = bridges
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

func TestE2ENetworkPreflight(t *testing.T) {
	cases := []struct {
		name, pool, routes, addresses string
		reject                        bool
	}{
		{"parent gateway", "10.88.0.0/16", `[{"dst":"default","gateway":"10.88.0.1"}]`, `[]`, true},
		{"multipath gateway", "10.88.0.0/16", `[{"dst":"default","nexthops":[{"gateway":"10.88.0.1"}]}]`, `[]`, true},
		{"unrelated route", "10.192.0.0/12", `[{"dst":"10.192.1.0/24"}]`, `[]`, true},
		{"local host route", "10.192.0.0/12", `[{"dst":"10.192.0.1"}]`, `[]`, true},
		{
			"down bridge",
			"10.192.0.0/12",
			`[]`,
			`[{"ifname":"k-leaked","addr_info":[{"local":"10.192.0.1","prefixlen":24}]}]`,
			true,
		},
		{
			"safe pool",
			"10.192.0.0/12",
			`[{"dst":"default","gateway":"10.88.0.1"},{"dst":"10.88.0.0/24"}]`,
			`[{"ifname":"eth0","addr_info":[{"local":"10.88.0.15","prefixlen":24}]}]`,
			false,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNetworkPool(netip.MustParsePrefix(tt.pool), []byte(tt.routes), []byte(tt.addresses))
			if (err != nil) != tt.reject {
				t.Fatalf("preflight = %v, reject=%v", err, tt.reject)
			}
		})
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
