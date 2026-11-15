// Copyright 2026 Emiliano Spinella (eminwux)
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

package bootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// These private seams make write, read, and permission failures deterministic,
// including when tests run as root. Production always uses localFiles.
type temporaryFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type fileOps struct {
	readFile   func(string) ([]byte, error)
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (temporaryFile, error)
	link       func(string, string) error
	rename     func(string, string) error
	remove     func(string) error
}

func localFiles() fileOps {
	return fileOps{
		readFile: os.ReadFile, mkdirAll: os.MkdirAll,
		createTemp: func(dir, pattern string) (temporaryFile, error) { return os.CreateTemp(dir, pattern) },
		link:       os.Link, rename: os.Rename, remove: os.Remove,
	}
}

// replace publishes a complete file atomically for an explicit reset.
func replace(ctx context.Context, path string, data []byte, files fileOps) error {
	temp, err := files.createTemp(filepath.Dir(path), ".kuketeams-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = files.remove(temp.Name()) }()
	if err = writeAndClose(temp, data); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = files.rename(temp.Name(), path); err != nil {
		return fmt.Errorf("replace completed file: %w", err)
	}
	return nil
}

// publish writes and closes a 0600 temporary file in the destination directory,
// then hard-links it to the final name. Link atomically fails with EEXIST rather
// than replacing a competing creator's file. Both names are on the same local
// filesystem. Filesystems without hard-link support fail safely; no rename or
// direct-write fallback may compromise complete-file, create-only publication.
func publish(ctx context.Context, path string, data []byte, files fileOps) error {
	temp, err := files.createTemp(filepath.Dir(path), ".kuketeams-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() { _ = files.remove(temp.Name()) }()
	if err = writeAndClose(temp, data); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = files.link(temp.Name(), path); err != nil {
		return fmt.Errorf("link completed file: %w", err)
	}
	return nil
}

func writeAndClose(file temporaryFile, data []byte) error {
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary file: %w", closeErr)
	}
	return nil
}
