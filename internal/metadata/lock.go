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

package metadata

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// lockFilePerm is the mode used for newly-created sidecar lock files. The
// sidecar carries no payload — its only role is to anchor the flock — so
// it inherits the data file's 0o644 and the parent dir's 0o0750 + setgid
// keeps it out of reach of non-kukeon-group operators.
const lockFilePerm os.FileMode = 0o644

// LockFileSuffix is appended to a metadata file path to derive its
// sidecar lock file. Exported so callers that iterate directories
// containing metadata.json files (list/walk paths) can filter out the
// sidecar instead of mis-treating it as another metadata document.
const LockFileSuffix = ".lock"

// LockFilePath returns the sidecar lock file path for a metadata file.
// The sidecar lives next to the metadata file at <file>.lock.
//
// Locks are kept on the sidecar, not the data file, because WriteMetadata
// persists via tmp-file + rename. Renaming swaps the data file's inode
// out from under any flock the caller might have held on the previous
// inode; the sidecar's inode is never replaced, so the lock identity
// remains stable across writes.
func LockFilePath(file string) string {
	return file + LockFileSuffix
}

// acquireFlock opens (creating if absent) the sidecar lock file for the
// metadata file at `file` and acquires the requested flock mode. The
// returned release closes the underlying file descriptor, which the
// kernel uses to drop the flock.
//
// The parent directory is created up-front so the sidecar has somewhere
// to live; this matches WriteMetadata's existing MkdirAll on the
// create-new path.
func acquireFlock(file string, exclusive bool) (func(), error) {
	parent := filepath.Dir(file)
	if mkErr := os.MkdirAll(parent, metadataDirMode); mkErr != nil {
		return nil, fmt.Errorf("mkdir lock parent %s: %w", parent, mkErr)
	}
	lockPath := LockFilePath(file)
	f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, lockFilePerm)
	if openErr != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, openErr)
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if flockErr := syscall.Flock(int(f.Fd()), mode); flockErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %s: %w", lockPath, flockErr)
	}
	return func() {
		// Close releases the flock atomically with closing the fd.
		_ = f.Close()
	}, nil
}

// WithExclusiveLock acquires the sidecar exclusive flock for the
// metadata file at `file`, runs fn while holding it, then releases the
// lock. fn must NOT call any flock-acquiring API on the same file or it
// will deadlock — BSD flock is non-reentrant on the same open-file
// description, and each call here opens a fresh descriptor.
//
// Use the WriteMetadataNoLock / ReadRawNoLock helpers from inside fn to
// touch the data file while holding the lock.
func WithExclusiveLock(_ context.Context, _ *slog.Logger, file string, fn func() error) error {
	release, err := acquireFlock(file, true)
	if err != nil {
		return fmt.Errorf("acquire exclusive lock: %w", err)
	}
	defer release()
	return fn()
}

// WithSharedLock is the read-only counterpart of WithExclusiveLock: it
// acquires the sidecar shared flock for the duration of fn. Multiple
// concurrent shared holders are admitted; an exclusive holder blocks
// every new shared acquisition until it releases.
func WithSharedLock(_ context.Context, _ *slog.Logger, file string, fn func() error) error {
	release, err := acquireFlock(file, false)
	if err != nil {
		return fmt.Errorf("acquire shared lock: %w", err)
	}
	defer release()
	return fn()
}

// WriteMetadataNoLock is the inner write helper used by WriteMetadata
// and WriteMetadataCAS. It is exported for callers that already hold
// the sidecar flock via WithExclusiveLock. The caller is responsible
// for the flock; if you are not sure, call WriteMetadata instead.
func WriteMetadataNoLock(ctx context.Context, logger *slog.Logger, metadata any, file string) error {
	return writeMetadataUnlocked(ctx, logger, metadata, file)
}

// ReadRawNoLock is the inner read helper used by ReadRaw and
// WriteMetadataCAS. It is exported for callers that already hold the
// sidecar flock via WithExclusiveLock or WithSharedLock. The caller is
// responsible for the flock; if you are not sure, call ReadRaw instead.
//
// Returns ErrMissingMetadataFile (wrapped) when the data file does not
// exist; that result is consistent regardless of whether the caller
// holds the flock.
func ReadRawNoLock(_ context.Context, _ *slog.Logger, file string) ([]byte, error) {
	if !existsFilePath(file) {
		return nil, fmt.Errorf("metadata file does not exist: %w", errdefs.ErrMissingMetadataFile)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return data, nil
}

// WriteMetadataCAS writes new metadata only if the on-disk bytes match
// the caller's observed prior bytes. The read-compare-write window runs
// under a single exclusive flock on the sidecar lock file.
//
// prior == nil asserts "no file should exist yet" — the write fails
// with ErrStaleResource if a data file is present. Conversely, if a
// data file exists on disk but its bytes disagree with prior (including
// the case where prior is non-nil but the data file was deleted), the
// write fails with ErrStaleResource and the caller must re-read and
// retry.
//
// The bytes compared are the raw payload — the exact return value of
// ReadRaw / ReadRawNoLock. Callers must pass back the bytes they
// observed, not a re-marshaled document, because JSON serialization is
// not guaranteed to be byte-stable across edits.
func WriteMetadataCAS(
	ctx context.Context,
	logger *slog.Logger,
	prior []byte,
	metadata any,
	file string,
) error {
	return WithExclusiveLock(ctx, logger, file, func() error {
		var current []byte
		if existsFilePath(file) {
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read %s for cas: %w", file, err)
			}
			current = data
		}
		if !bytes.Equal(prior, current) {
			return fmt.Errorf("cas check failed for %s: %w", file, errdefs.ErrStaleResource)
		}
		return writeMetadataUnlocked(ctx, logger, metadata, file)
	})
}
