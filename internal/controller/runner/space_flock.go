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

package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/eminwux/kukeon/internal/util/fs"
)

const (
	spaceLockDirMode  os.FileMode = 0o750
	spaceLockFileMode os.FileMode = 0o640
)

// spaceFileLock is the cross-process half of lockSpace. A nil value (no
// RunPath, or the lock file could not be taken) releases as a no-op.
type spaceFileLock struct {
	file *os.File
	path string
}

// acquireSpaceFileLock takes the exclusive flock on the space's lock file
// under <RunPath>/locks. A failure is logged and degrades to the in-process
// lock alone rather than failing the space operation.
func (r *Exec) acquireSpaceFileLock(realmName, spaceName string) *spaceFileLock {
	if r.opts.RunPath == "" {
		return nil
	}
	path := fs.SpaceLockPath(r.opts.RunPath, realmName, spaceName)
	f, err := lockFileExclusive(path)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("space lock file unavailable; serializing in-process only",
				"realm", realmName, "space", spaceName, "path", path, "error", err)
		}
		return nil
	}
	return &spaceFileLock{file: f, path: path}
}

// release drops the flock. With remove, the file is unlinked first, while
// still held: a waiter blocked on the unlinked inode then sees the path
// gone (or replaced) and retries on a fresh file, so two holders can never
// lock different inodes for the same space.
func (l *spaceFileLock) release(remove bool) {
	if l == nil {
		return
	}
	if remove {
		_ = os.Remove(l.path)
	}
	_ = l.file.Close()
}

// lockFileExclusive opens (creating if needed) the file at path and blocks
// until it holds an exclusive flock on the inode currently at that path.
func lockFileExclusive(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), spaceLockDirMode); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	for {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, spaceLockFileMode)
		if err != nil {
			return nil, fmt.Errorf("open lock file: %w", err)
		}
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("flock lock file: %w", err)
		}
		current, statErr := sameInode(f, path)
		if statErr != nil {
			_ = f.Close()
			return nil, statErr
		}
		if current {
			return f, nil
		}
		// The previous holder unlinked the file before releasing it.
		_ = f.Close()
	}
}

// sameInode reports whether f still refers to the file at path.
func sameInode(f *os.File, path string) (bool, error) {
	held, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat held lock file: %w", err)
	}
	onDisk, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat lock file: %w", err)
	}
	return os.SameFile(held, onDisk), nil
}
