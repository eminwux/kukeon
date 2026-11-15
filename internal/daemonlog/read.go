// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package daemonlog reads bounded, line-aligned chunks of kukeond's own log
// stream for the ReadDaemonLogs RPC (issue #1386, step 7).
package daemonlog

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// Read returns one chunk of the log file at path, following the cursor
// contract documented on kukeonv1.DaemonLogRequest and DaemonLogResult.
// Open/stat/read errors are returned unwrapped from os so callers can test
// them with errors.Is (os.ErrNotExist, os.ErrPermission).
func Read(path string, req kukeonv1.DaemonLogRequest) (kukeonv1.DaemonLogResult, error) {
	limit := int64(req.MaxBytes)
	if limit <= 0 {
		limit = kukeonv1.DaemonLogDefaultMaxBytes
	}
	if limit > kukeonv1.DaemonLogMaxBytes {
		limit = kukeonv1.DaemonLogMaxBytes
	}

	f, err := os.Open(path)
	if err != nil {
		return kukeonv1.DaemonLogResult{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return kukeonv1.DaemonLogResult{}, err
	}
	size := info.Size()

	res := kukeonv1.DaemonLogResult{Source: path, Size: size}
	offset := req.Offset
	if offset > size {
		// The source shrank (truncated or replaced) since the caller's last
		// read: its cursor no longer addresses the same bytes.
		res.Reset = true
		offset = -1
	}

	var (
		start int64
		buf   []byte
	)
	if offset < 0 {
		start = max(size-limit, 0)
		if buf, err = readAt(f, start, size-start); err != nil {
			return kukeonv1.DaemonLogResult{}, err
		}
		if start > 0 {
			res.Truncated = true
			// Drop the partial first line the window cut into. A window whose
			// only newline (if any) ends it lies inside one over-long line;
			// keep it raw rather than return nothing.
			if i := bytes.IndexByte(buf, '\n'); i >= 0 && i < len(buf)-1 {
				buf = buf[i+1:]
				start += int64(i) + 1
			}
		}
		buf = completeLines(buf, int64(len(buf)) >= limit)
	} else {
		start = offset
		n := min(limit, size-offset)
		if buf, err = readAt(f, start, n); err != nil {
			return kukeonv1.DaemonLogResult{}, err
		}
		buf = completeLines(buf, n >= limit)
		res.More = n >= limit && start+int64(len(buf)) < size
	}

	res.Start = start
	res.Next = start + int64(len(buf))
	res.Data = strings.ToValidUTF8(string(buf), "�")
	return res, nil
}

// completeLines trims buf to its last newline, holding back a line still
// being written. A full-bound buffer with no newline is one over-long line:
// return it as-is so the cursor advances instead of stalling forever.
func completeLines(buf []byte, full bool) []byte {
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		return buf[:i+1]
	}
	if full {
		return buf
	}
	return nil
}

func readAt(f *os.File, start, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, start)
	// A writer may not have flushed past the stat'd size, or the file may
	// have shrunk between Stat and ReadAt; keep what was read.
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", f.Name(), err)
	}
	return buf[:read], nil
}
