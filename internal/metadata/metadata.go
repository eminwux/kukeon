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

func existsFilePath(filepath string) bool {
	if _, err := os.Stat(filepath); err == nil {
		return true
	}
	return false
}

func WriteMetadata(ctx context.Context, logger *slog.Logger, metadata any, file string) error {
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

	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		logger.ErrorContext(ctx, "failed to write metadata dir", "file", file, "error", err)
		return fmt.Errorf("mkdir terminal dir: %w", err)
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

// ReadRaw reads the metadata file and returns the raw bytes for external decoding.
func ReadRaw(ctx context.Context, logger *slog.Logger, file string) ([]byte, error) {
	logger.DebugContext(ctx, "reading raw metadata", "file", file)
	if !existsFilePath(file) {
		logger.ErrorContext(ctx, "metadata file does not exist", "file", file)
		return nil, fmt.Errorf("metadata file does not exist: %w", errdefs.ErrMissingMetadataFile)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return data, nil
}
