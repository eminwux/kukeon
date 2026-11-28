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

// Package containerrecording serves an Attachable container's whole terminal
// recording for the ListContainerRecording and ReadContainerRecordingSegment
// RPCs (issue #1783): every segment sbsh retains, closed and live, as stored.
//
// It reads with sbsh's exported pkg/capture, whose package comment states the
// segment naming as a contract, and works on a capture directory with no sbsh
// process running — a stopped cell's recording is still served. It neither
// decompresses nor parses: a closed segment is usually gzip, about a tenth
// of its raw size, so serving it as stored keeps one segment to about one
// read at the ReadContainerLog bound. The browser decompresses and decodes.
//
// Unlike internal/containerlog, which reads only the live segment, the
// format is the caller's: the file name kukeond resolved says which.
package containerrecording

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/sbsh/pkg/capture"
)

// List returns the retained segments of the capture at canonical, oldest
// first, the live segment last. A capture with no output yet has none.
func List(canonical string) ([]kukeonv1.RecordingSegment, error) {
	segs, err := capture.Segments(canonical)
	if err != nil {
		return nil, err
	}
	out := make([]kukeonv1.RecordingSegment, 0, len(segs))
	for _, s := range segs {
		out = append(out, kukeonv1.RecordingSegment{
			Seq: s.Seq, Size: s.Size, Compressed: s.Compressed, Live: s.Live,
		})
	}
	return out, nil
}

// ReadSegment returns one bounded chunk of segment req.Seq of the capture at
// canonical, as stored. A seq that is not retained is
// errdefs.ErrRecordingSegmentGone.
//
// The segment is found by listing, then opened. Rotation renames files under
// the reader, so:
//
//   - A closed segment that sbsh compressed after it was listed is found
//     under its ".gz" name (capture.OpenStored); one that vanished is listed
//     again once, and is gone only when the second listing lacks its seq.
//   - A live segment can rotate between listing and open, after which the
//     canonical path already holds the next live segment. After opening it,
//     the closed segments are listed again: when one carries the seq, the
//     rotation may have happened first, so that closed segment is read
//     instead, with Live false. That is how a client following the live
//     segment learns of the rotation.
func ReadSegment(canonical string, req kukeonv1.RecordingSegmentRequest) (kukeonv1.RecordingSegmentResult, error) {
	f, seg, compressed, err := openSegment(canonical, req.Seq)
	if errors.Is(err, fs.ErrNotExist) {
		f, seg, compressed, err = openSegment(canonical, req.Seq)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return kukeonv1.RecordingSegmentResult{}, fmt.Errorf(
			"%w: segment %d of %s", errdefs.ErrRecordingSegmentGone, req.Seq, canonical,
		)
	}
	if err != nil {
		return kukeonv1.RecordingSegmentResult{}, err
	}
	defer func() { _ = f.Close() }()
	return readChunk(f, req, compressed, seg.Live)
}

// openSegment lists the capture, finds seq and opens it, applying the
// live-rotation check ReadSegment documents. A seq missing from the listing,
// or a file gone before it was opened, is fs.ErrNotExist.
func openSegment(canonical string, seq uint64) (*os.File, capture.Segment, bool, error) {
	segs, err := capture.Segments(canonical)
	if err != nil {
		return nil, capture.Segment{}, false, err
	}
	seg, ok := findSeq(segs, seq)
	if !ok {
		return nil, capture.Segment{}, false, fs.ErrNotExist
	}
	f, compressed, err := capture.OpenStored(seg)
	if err != nil || !seg.Live {
		return f, seg, compressed, err
	}
	closed, err := capture.ClosedSegments(canonical)
	if err != nil {
		_ = f.Close()
		return nil, capture.Segment{}, false, err
	}
	rotated, ok := findSeq(closed, seq)
	if !ok {
		// No closed segment has seq yet, so the rename had not happened when
		// the file was opened: f is the live segment seq.
		return f, seg, compressed, nil
	}
	_ = f.Close()
	f, compressed, err = capture.OpenStored(rotated)
	return f, rotated, compressed, err
}

func findSeq(segs []capture.Segment, seq uint64) (capture.Segment, bool) {
	for _, s := range segs {
		if s.Seq == seq {
			return s, true
		}
	}
	return capture.Segment{}, false
}

func readChunk(
	f *os.File, req kukeonv1.RecordingSegmentRequest, compressed, live bool,
) (kukeonv1.RecordingSegmentResult, error) {
	info, err := f.Stat()
	if err != nil {
		return kukeonv1.RecordingSegmentResult{}, err
	}
	size := info.Size()
	res := kukeonv1.RecordingSegmentResult{Size: size, Compressed: compressed, Live: live}
	start := max(req.Offset, 0)
	if start > size {
		res.Start, res.Next = start, start
		return res, nil
	}
	n := min(clampLimit(req.MaxBytes), size-start)
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return kukeonv1.RecordingSegmentResult{}, fmt.Errorf("read %s: %w", f.Name(), err)
	}
	res.Data = buf[:read]
	res.Start = start
	res.Next = start + int64(read)
	res.More = res.Next < size
	return res, nil
}

// clampLimit applies ReadContainerLog's bounds to a requested MaxBytes.
func clampLimit(maxBytes int) int64 {
	limit := int64(maxBytes)
	if limit <= 0 {
		limit = kukeonv1.ContainerLogDefaultMaxBytes
	}
	return min(limit, kukeonv1.ContainerLogMaxBytes)
}
