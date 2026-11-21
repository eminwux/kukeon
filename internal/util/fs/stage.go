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

package fs

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// WriteFileAtomic stages the binaries and shims kukeon places under
// <RunPath>/bin (kuketty, kukepause, the kuke client and its shim): it writes
// r to dst through the sibling dst+".tmp", sets the tmp file's mode, and
// renames it over dst, so a concurrent reader — a cell's bind mount, an exec
// through the shim — never observes a partial file. The rename stays on the
// destination's filesystem, so the move is a single atomic syscall.
//
// The explicit chmod matters: O_CREATE applies the mode only when it creates
// the file, and through the umask, so without it a restrictive umask or a tmp
// left by a crashed earlier attempt could drop the execute bit. label names
// what is staged in every error, so a failed stage is attributable.
func WriteFileAtomic(dst string, r io.Reader, mode os.FileMode, label string) error {
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %s staged tmp %q: %w", label, tmp, err)
	}
	if _, err = io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s staged tmp %q: %w", label, tmp, err)
	}
	if err = out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s staged tmp %q: %w", label, tmp, err)
	}
	if err = os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod %s staged tmp %q: %w", label, tmp, err)
	}
	if err = os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s staged %q -> %q: %w", label, tmp, dst, err)
	}
	return nil
}

// CopyFileAtomic stages a copy of the file at src as dst with WriteFileAtomic.
func CopyFileAtomic(src, dst string, mode os.FileMode, label string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s source %q: %w", label, src, err)
	}
	defer func() { _ = in.Close() }()
	return WriteFileAtomic(dst, in, mode, label)
}

// WriteFileIfChanged stages content as dst with WriteFileAtomic, unless dst
// already holds exactly that content, and reports whether it wrote. Leaving an
// up-to-date file alone keeps its inode across repeated stages.
func WriteFileIfChanged(dst string, content []byte, mode os.FileMode, label string) (bool, error) {
	if current, err := os.ReadFile(dst); err == nil && bytes.Equal(current, content) {
		return false, nil
	}
	if err := WriteFileAtomic(dst, bytes.NewReader(content), mode, label); err != nil {
		return false, err
	}
	return true, nil
}
