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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// metadataDirMode is the mode applied to the parent directory of a
// metadata file: setgid + rwx-r-x---. The setgid bit lets newly-created
// children inherit the kukeon group, matching the `/opt/kukeon` layout
// `kuke init` sets up via sysuser.ChownTreeAndChmod. Group-traverse +
// no-world-access keeps the host-side path reachable for kukeon-group
// operators without exposing it to anyone else.
const metadataDirMode os.FileMode = os.ModeSetgid | 0o0750

func existsFilePath(filepath string) bool {
	_, err := os.Stat(filepath)
	if err == nil {
		return true
	}
	// Only return false if the error is specifically "file does not exist"
	// For other errors (like permission denied), return true so we attempt
	// to read the file anyway and let the read operation provide the actual error
	return !os.IsNotExist(err)
}

// WriteMetadata persists the JSON-encoded payload at `file` while holding
// an exclusive flock on the sidecar lock file. The flock is the daemon /
// `kuke --no-daemon` cross-boundary mutex for the data file's inode; it
// prevents torn writes when more than one process — or more than one
// goroutine — tries to update the same metadata document concurrently.
func WriteMetadata(ctx context.Context, logger *slog.Logger, metadata any, file string) error {
	return WithExclusiveLock(ctx, logger, file, func() error {
		return writeMetadataUnlocked(ctx, logger, metadata, file)
	})
}

// writeMetadataUnlocked is the inner write helper invoked under the
// sidecar exclusive flock. It assumes the caller already holds the lock
// — calling it without the lock is a torn-write footgun and is only
// safe through WriteMetadata, WriteMetadataNoLock, or WriteMetadataCAS.
func writeMetadataUnlocked(ctx context.Context, logger *slog.Logger, metadata any, file string) error {
	logger.DebugContext(ctx, "creating metadata", "file", file)

	if existsFilePath(file) {
		logger.InfoContext(ctx, "metadata file already exists, overwriting", "file", file)
		err := writeMetadataFile(ctx, logger, metadata, file)
		if err != nil {
			logger.ErrorContext(ctx, "failed to write metadata file", "file", file, "error", err)
			return fmt.Errorf("failed to write metadata file: %w", err)
		}
		logger.DebugContext(ctx, "metadata file written", "file", file)
		return nil
	}

	parentDir := filepath.Dir(file)
	if err := os.MkdirAll(parentDir, metadataDirMode); err != nil {
		logger.ErrorContext(ctx, "failed to write metadata dir", "file", file, "error", err)
		return fmt.Errorf("mkdir terminal dir: %w", err)
	}
	// MkdirAll honors only the rwx permission bits — Linux silently strips
	// the explicit setgid bit unless it's inherited from the parent (and
	// /tmp on test hosts has no setgid, breaking the inheritance chain).
	// Apply it after the fact so a newly-created cell or per-container dir
	// is host-side traversable for the kukeon group, matching the layout
	// `kuke init` sets up on /opt/kukeon. Existing dirs are re-chmoded to
	// the canonical mode — idempotent and self-healing for hosts upgraded
	// from a daemon that wrote 0o2700 (issue #260 gate 2).
	if err := os.Chmod(parentDir, metadataDirMode|os.ModeSetgid); err != nil {
		logger.ErrorContext(ctx, "failed to chmod metadata dir", "dir", parentDir, "error", err)
		return fmt.Errorf("chmod metadata dir: %w", err)
	}

	err := writeMetadataFile(ctx, logger, metadata, file)
	if err != nil {
		logger.ErrorContext(ctx, "failed to write metadata file", "file", file, "error", err)
		return fmt.Errorf("failed to write metadata file: %w", err)
	}
	logger.InfoContext(ctx, "metadata file written", "file", file)
	return nil
}

func writeMetadataFile(ctx context.Context, logger *slog.Logger, metadata any, file string) error {
	marshaled, marshalErr := json.MarshalIndent(metadata, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("marshal %s: %w", file, marshalErr)
	}
	marshaled = append(marshaled, '\n') // gocritic: assign result to same slice

	const filePerm = 0o644 // mnd: magic number
	if writeErr := atomicWriteFile(ctx, logger, file, marshaled, filePerm); writeErr != nil {
		return fmt.Errorf("write %s: %w", file, writeErr)
	}
	return nil
}

// atomicWriteFile writes to a temp file in the same dir, fsyncs, then renames.
func atomicWriteFile(_ context.Context, _ *slog.Logger, file string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(file)

	f, createErr := os.CreateTemp(dir, ".meta-*.tmp")
	if createErr != nil {
		return createErr
	}
	tmp := f.Name()
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp) // safe if already renamed
	}()

	if chmodErr := f.Chmod(mode); chmodErr != nil {
		return fmt.Errorf("chmod: %w", chmodErr)
	}
	if _, writeErr := f.Write(data); writeErr != nil {
		return fmt.Errorf("write: %w", writeErr)
	}
	if syncErr := f.Sync(); syncErr != nil { // flush file
		return fmt.Errorf("fsync: %w", syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("close: %w", closeErr)
	}

	// Best-effort dir sync after rename for extra safety (Linux/Unix).
	if renameErr := os.Rename(tmp, file); renameErr != nil {
		return fmt.Errorf("rename: %w", renameErr)
	}
	if d, openErr := os.Open(dir); openErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func ReadMetadata[T any](ctx context.Context, logger *slog.Logger, file string) (T, error) {
	var zero T
	logger.DebugContext(ctx, "reading metadata", "file", file)

	if !existsFilePath(file) {
		logger.ErrorContext(ctx, "metadata file does not exist", "file", file)
		return zero, fmt.Errorf("metadata file does not exist: %w", errdefs.ErrMissingMetadataFile)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return zero, fmt.Errorf("read %s: %w", file, err)
	}
	var out T
	if err = json.Unmarshal(data, &out); err != nil {
		return zero, fmt.Errorf("unmarshal %s: %w", file, err)
	}
	return out, nil
}

// ReadRaw reads the metadata file and returns the raw bytes for external
// decoding while holding a shared flock on the sidecar lock file. The
// shared flock keeps concurrent ReadRaw calls non-blocking against each
// other but blocks them against any in-flight WriteMetadata holder, so a
// reader never observes a partially-written or post-rename-but-pre-sync
// payload.
//
// A nonexistent data file short-circuits to ErrMissingMetadataFile
// without acquiring the flock or materialising the sidecar — that keeps
// "probe whether this realm exists" reads from leaving lock-file detritus
// in directories that were never written to.
func ReadRaw(ctx context.Context, logger *slog.Logger, file string) ([]byte, error) {
	logger.DebugContext(ctx, "reading raw metadata", "file", file)
	if !existsFilePath(file) {
		logger.ErrorContext(ctx, "metadata file does not exist", "file", file)
		return nil, fmt.Errorf("metadata file does not exist: %w", errdefs.ErrMissingMetadataFile)
	}
	var raw []byte
	err := WithSharedLock(ctx, logger, file, func() error {
		data, readErr := ReadRawNoLock(ctx, logger, file)
		if readErr != nil {
			return readErr
		}
		raw = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// DeleteMetadata removes a metadata file and its directory if empty.
//
// The sidecar lock file at <file>.lock is removed alongside the data
// file so the parent-directory cleanup path still fires — without this,
// the leftover .lock entry would keep the empty-dir branch from
// triggering. The lock is not acquired during deletion because the
// caller is teardown logic that owns the entity exclusively (cell /
// stack / space / realm delete); racing writers on a being-deleted
// resource is out of scope for the phase-1 primitives.
func DeleteMetadata(ctx context.Context, logger *slog.Logger, file string) error {
	logger.DebugContext(ctx, "deleting metadata", "file", file)

	if !existsFilePath(file) {
		logger.DebugContext(ctx, "metadata file does not exist, skipping deletion", "file", file)
		return nil // Idempotent: file doesn't exist, consider it deleted
	}

	// Delete the metadata file
	if err := os.Remove(file); err != nil {
		logger.ErrorContext(ctx, "failed to delete metadata file", "file", file, "error", err)
		return fmt.Errorf("failed to delete metadata file %s: %w", file, err)
	}

	logger.InfoContext(ctx, "deleted metadata file", "file", file)

	// Best-effort removal of the sidecar lock file. A missing sidecar is
	// fine (pre-flock writes or a writer that never created it); any
	// other error is logged but not surfaced so a stale lock leftover
	// cannot block the higher-level teardown.
	lockPath := LockFilePath(file)
	if rmErr := os.Remove(lockPath); rmErr != nil && !os.IsNotExist(rmErr) {
		logger.DebugContext(ctx, "could not remove sidecar lock file", "file", lockPath, "error", rmErr)
	}

	// Try to remove the parent directory if it's empty
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Directory might not exist or we can't read it - that's OK
		logger.DebugContext(ctx, "could not read directory, skipping directory removal", "dir", dir, "error", err)
		return nil
	}

	// If directory is empty (only . and ..), remove it
	if len(entries) == 0 {
		if err = os.Remove(dir); err != nil {
			logger.DebugContext(ctx, "could not remove empty directory", "dir", dir, "error", err)
			// Don't fail if we can't remove the directory
		} else {
			logger.DebugContext(ctx, "removed empty metadata directory", "dir", dir)
		}
	}

	return nil
}
