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

package containerlog

import (
	"bytes"
	"os"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/sbsh/pkg/capture"
)

// Stream formats. The caller decides which one a file holds from the file it
// resolved (capture.cast or the raw capture, see internal/client/local),
// never by sniffing: a runtime log may start with a JSON object that passes
// an asciicast header test.
const (
	FormatRaw       = "raw"
	FormatAsciicast = "asciicast"
)

// MaxEventLine is the longest asciicast line ReadAsciicast returns, sbsh's
// own bound on one event line: the writer emits one event per PTY read, so a
// real line is far shorter. A longer line is consumed and skipped, as sbsh's
// decoder does.
const MaxEventLine = 1 << 20

// scanBlock is the read size used while looking for the end of a line.
const scanBlock = 64 << 10

// ReadAsciicast returns one chunk of the asciicast recording at path, with
// the cursor contract of kukeonv1.ContainerLogRequest. Start, Next and Size
// are byte offsets into the file; Data is the terminal output of the whole
// lines in [Start, Next), decoded with sbsh's decoder, so headers and resize
// events contribute nothing and Data may be empty while Next advances.
//
//   - A chunk ends at its last newline. A line still being written (no
//     newline yet at the end of the file) is not returned and Next stays
//     before it, so a follower gets it once it is complete.
//   - A line longer than MaxBytes is still returned whole: the read extends
//     to its newline. A line longer than MaxEventLine is skipped.
//   - A tail read (Offset < 0) starts at the first line that begins inside
//     the newest MaxBytes, and sets Truncated when that is past 0.
//
// Errors are returned unwrapped from os, as Read's are.
func ReadAsciicast(path string, req kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
	limit := clampLimit(req.MaxBytes)

	f, err := os.Open(path)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	size := info.Size()

	res := kukeonv1.ContainerLogResult{Source: path, Size: size}
	if req.Offset > size {
		// Same as Read: the file shrank or was replaced, and Size < Offset
		// tells the caller to start over.
		res.Start = req.Offset
		res.Next = req.Offset
		return res, nil
	}

	start := req.Offset
	if start < 0 {
		start = max(size-limit, 0)
		if start > 0 {
			res.Truncated = true
			if start, err = lineStart(f, start, size); err != nil {
				return kukeonv1.ContainerLogResult{}, err
			}
		}
	}

	lines, next, more, err := wholeLines(f, start, size, limit)
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}
	res.Data = capture.DecodeAsciicast(lines)
	res.Start = start
	res.Next = next
	res.More = more
	return res, nil
}

// wholeLines reads the complete lines that start at start, up to limit bytes
// (further when the first line is longer). It returns their bytes, the offset
// just past them, and whether another read would make progress.
func wholeLines(f *os.File, start, size, limit int64) ([]byte, int64, bool, error) {
	n := min(limit, size-start)
	buf, err := readAt(f, start, n)
	if err != nil {
		return nil, start, false, err
	}
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		next := start + int64(i) + 1
		// When the read reached the end of the file, what follows the last
		// newline is a line still being written: nothing more to read yet.
		return buf[:i+1], next, start+int64(len(buf)) < size, nil
	}
	if start+int64(len(buf)) >= size {
		return nil, start, false, nil
	}

	// The chunk is the beginning of one line longer than limit.
	nl, err := indexNewline(f, start+int64(len(buf)), size)
	if err != nil || nl < 0 {
		return nil, start, false, err
	}
	next := nl + 1
	if next-start > MaxEventLine {
		return nil, next, next < size, nil
	}
	line, err := readAt(f, start, next-start)
	if err != nil {
		return nil, start, false, err
	}
	return line, next, next < size, nil
}

// lineStart returns the offset of the first line that begins at or after
// off: off itself when the byte before it is a newline, else the byte after
// the next newline (size when there is none).
func lineStart(f *os.File, off, size int64) (int64, error) {
	prev, err := readAt(f, off-1, 1)
	if err != nil {
		return 0, err
	}
	if len(prev) == 1 && prev[0] == '\n' {
		return off, nil
	}
	nl, err := indexNewline(f, off, size)
	if err != nil {
		return 0, err
	}
	if nl < 0 {
		return size, nil
	}
	return nl + 1, nil
}

// indexNewline returns the offset of the first newline at or after from, or
// -1 when there is none before size.
func indexNewline(f *os.File, from, size int64) (int64, error) {
	for off := from; off < size; {
		buf, err := readAt(f, off, min(scanBlock, size-off))
		if err != nil {
			return 0, err
		}
		if len(buf) == 0 {
			return -1, nil
		}
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return off + int64(i), nil
		}
		off += int64(len(buf))
	}
	return -1, nil
}
