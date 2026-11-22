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

package metadata_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
)

// levelRecorder is a slog.Handler that records the level of every record
// it receives, at every level, so a test can assert nothing reached Error.
type levelRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec)
	return nil
}

func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelRecorder) WithGroup(string) slog.Handler      { return h }

func (h *levelRecorder) assertNoErrorRecords(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range h.records {
		if rec.Level >= slog.LevelError {
			t.Errorf("unexpected error-level log record: %q", rec.Message)
		}
	}
}

// TestReadMetadata_MissingFile_NoErrorLog is the #1602 guard for the
// metadata reader: a missing file returns ErrMissingMetadataFile and leaves
// the logging decision to the caller instead of writing an ERROR record.
func TestReadMetadata_MissingFile_NoErrorLog(t *testing.T) {
	h := &levelRecorder{}
	file := filepath.Join(t.TempDir(), "absent", "metadata.json")

	_, err := metadata.ReadMetadata[map[string]any](context.Background(), slog.New(h), file)
	if !errors.Is(err, errdefs.ErrMissingMetadataFile) {
		t.Fatalf("ReadMetadata on missing file: err = %v; want ErrMissingMetadataFile", err)
	}
	h.assertNoErrorRecords(t)
}

// TestReadRaw_MissingFile_NoErrorLog is the ReadRaw twin of the test above;
// ReadRaw is the path every runner read*Internal helper (and so every list
// walker) goes through.
func TestReadRaw_MissingFile_NoErrorLog(t *testing.T) {
	h := &levelRecorder{}
	file := filepath.Join(t.TempDir(), "absent", "metadata.json")

	_, err := metadata.ReadRaw(context.Background(), slog.New(h), file)
	if !errors.Is(err, errdefs.ErrMissingMetadataFile) {
		t.Fatalf("ReadRaw on missing file: err = %v; want ErrMissingMetadataFile", err)
	}
	h.assertNoErrorRecords(t)
}
