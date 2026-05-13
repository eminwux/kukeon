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

package controller

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
)

// MountReleaseAttempt records the outcome of trying to release a single
// mountpoint enumerated under a kukeon-owned directory at uninstall time.
//
// Released is true when the mount no longer pins the host directory (either
// because the plain unmount succeeded or the lazy MNT_DETACH fallback did).
// Err is non-nil only on the residual-mount path: the plain unmount and the
// lazy detach both failed, so the operator must intervene by hand.
type MountReleaseAttempt struct {
	Target   string
	Released bool
	Err      error
}

// MountReleaser releases live bind mounts under the given root directory so
// the subsequent rmdir of root succeeds. The production implementation reads
// /proc/self/mounts and calls syscall.Unmount; tests inject a stub.
//
// The empty return slice means "no mounts under root" — distinct from an
// error, which signals the enumerator itself failed (e.g., /proc/self/mounts
// is unreadable).
type MountReleaser func(root string) ([]MountReleaseAttempt, error)

// releaseMountsUnder enumerates every mountpoint at or below root in
// /proc/self/mounts and tries to unmount each — first MNT_DETACH-less so a
// clean unmount surfaces, then with MNT_DETACH as a fallback when the kernel
// reports the mount busy. The returned slice has one entry per enumerated
// mountpoint, ordered deepest-first so nested mounts drain before their
// parent.
func releaseMountsUnder(root string) ([]MountReleaseAttempt, error) {
	if root == "" {
		return nil, nil
	}
	targets, err := enumerateMountsUnder(root)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}
	out := make([]MountReleaseAttempt, 0, len(targets))
	for _, target := range targets {
		out = append(out, releaseMount(target))
	}
	return out, nil
}

// releaseMount makes a best-effort attempt to unmount target. A plain unmount
// is tried first because that produces a fully-detached mount with no lingering
// references; if it fails (typically EBUSY because a process inside a still-
// running container is holding the mount open), a lazy MNT_DETACH retry is the
// fallback. Lazy detach removes the mount from the filesystem namespace
// immediately, so the subsequent rmdir of the parent dir succeeds even though
// the kernel keeps the mount alive until the last fd closes.
func releaseMount(target string) MountReleaseAttempt {
	if err := syscall.Unmount(target, 0); err == nil {
		return MountReleaseAttempt{Target: target, Released: true}
	} else {
		firstErr := err
		if lazyErr := syscall.Unmount(target, syscall.MNT_DETACH); lazyErr == nil {
			return MountReleaseAttempt{Target: target, Released: true}
		} else {
			return MountReleaseAttempt{
				Target: target,
				Err: fmt.Errorf(
					"unmount %q: %w (lazy detach also failed: %v)",
					target, firstErr, lazyErr,
				),
			}
		}
	}
}

// enumerateMountsUnder reads /proc/self/mounts and returns the targets of
// every mountpoint at or below root. The result is sorted deepest-first
// (more path components first, ties broken lexicographically descending) so
// callers can unmount nested mounts before their parent without needing to
// reason about hierarchy themselves. Comparing on slash count is enough —
// kukeon mountpoints never embed escaped path separators in their targets.
func enumerateMountsUnder(root string) ([]string, error) {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, fmt.Errorf("open /proc/self/mounts: %w", err)
	}
	defer f.Close()
	return parseMountsUnder(f, root)
}

// parseMountsUnder is the file-format-aware half of enumerateMountsUnder,
// split out so unit tests can feed synthetic /proc/self/mounts content
// without having to provision real mounts.
func parseMountsUnder(r io.Reader, root string) ([]string, error) {
	prefix := strings.TrimRight(root, "/")
	if prefix == "" {
		// A bare "/" prefix would match every mount on the host — refuse so a
		// caller passing an unintended empty/root path cannot turn uninstall
		// into a host-wide unmount sweep.
		return nil, fmt.Errorf("enumerate mounts: refusing to enumerate from root %q", root)
	}
	var matches []string
	scanner := bufio.NewScanner(r)
	// /proc/self/mounts lines can in principle exceed the default 64 KiB
	// scanner buffer when option lists grow long; bump the cap to 1 MiB so
	// the enumerator stays robust against the same.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		// Format (procfs(5)): source target fstype options dump pass
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		target := decodeMountPath(fields[1])
		if target == prefix || strings.HasPrefix(target, prefix+"/") {
			matches = append(matches, target)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read /proc/self/mounts: %w", err)
	}
	sort.Slice(matches, func(i, j int) bool {
		ci := strings.Count(matches[i], "/")
		cj := strings.Count(matches[j], "/")
		if ci != cj {
			return ci > cj
		}
		return matches[i] > matches[j]
	})
	return matches, nil
}

// decodeMountPath unescapes the octal-encoded characters /proc/self/mounts
// uses for whitespace, tabs, backslashes, and newlines in the target field
// (space is encoded as "\040", tab as "\011"). Paths without those characters
// round-trip unchanged.
func decodeMountPath(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) {
			if c, ok := parseOctalTriplet(s[i+1 : i+4]); ok {
				b.WriteByte(c)
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// parseOctalTriplet parses exactly three octal digits ("0" through "7") into
// a single byte. Returns ok=false on any non-octal digit or if the resulting
// value would overflow a byte — the caller falls back to passing the bytes
// through verbatim in that case.
func parseOctalTriplet(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	var n int
	for i := 0; i < 3; i++ {
		c := s[i]
		if c < '0' || c > '7' {
			return 0, false
		}
		n = n*8 + int(c-'0')
	}
	if n > 0xFF {
		return 0, false
	}
	return byte(n), true
}
