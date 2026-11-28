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

package containerrecording_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/containerrecording"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/sbsh/pkg/capture"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// readWhole reads segment seq to its end in chunks of maxBytes, as the
// browser does, and returns the stored bytes and the last result.
func readWhole(t *testing.T, canonical string, seq uint64, maxBytes int) ([]byte, kukeonv1.RecordingSegmentResult) {
	t.Helper()
	var out []byte
	var offset int64
	for range 100000 {
		res, err := containerrecording.ReadSegment(canonical, kukeonv1.RecordingSegmentRequest{
			Seq: seq, Offset: offset, MaxBytes: maxBytes,
		})
		if err != nil {
			t.Fatalf("read seq %d at %d: %v", seq, offset, err)
		}
		if res.Start != offset {
			t.Fatalf("start %d, want %d", res.Start, offset)
		}
		out = append(out, res.Data...)
		offset = res.Next
		if !res.More {
			return out, res
		}
	}
	t.Fatal("segment read did not converge")
	return nil, kukeonv1.RecordingSegmentResult{}
}

const header = `{"version":2,"width":80,"height":24,"timestamp":1700000000}` + "\n"

// seedCapture lays out a capture the way sbsh leaves it: a compressed closed
// segment, a closed segment not compressed yet, and the live segment.
func seedCapture(t *testing.T) (string, [][]byte) {
	t.Helper()
	canonical := filepath.Join(t.TempDir(), "capture.cast")
	parts := [][]byte{
		[]byte(header + `[0.1,"o","zero\r\n"]` + "\n"),
		[]byte(header + `[5.0,"r","100x30"]` + "\n" + `[5.1,"o","one ─\r\n"]` + "\n"),
		[]byte(header + `[9.0,"o","live"]` + "\n"),
	}
	writeFile(t, capture.SegmentPath(canonical, 4)+capture.GzSuffix, gz(t, parts[0]))
	writeFile(t, capture.SegmentPath(canonical, 5), parts[1])
	writeFile(t, canonical, parts[2])
	// Not part of the capture: an in-flight compression and an unrelated file.
	writeFile(t, capture.SegmentPath(canonical, 6)+".gz.tmp", []byte("partial"))
	writeFile(t, canonical+".bak", []byte("other"))
	return canonical, parts
}

func TestListOrdersSegmentsOldestFirst(t *testing.T) {
	canonical, parts := seedCapture(t)
	segs, err := containerrecording.List(canonical)
	if err != nil {
		t.Fatal(err)
	}
	want := []kukeonv1.RecordingSegment{
		{Seq: 4, Size: int64(len(gz(t, parts[0]))), Compressed: true},
		{Seq: 5, Size: int64(len(parts[1]))},
		{Seq: 6, Size: int64(len(parts[2])), Live: true},
	}
	if len(segs) != len(want) {
		t.Fatalf("segments %+v", segs)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Fatalf("segment %d = %+v, want %+v", i, segs[i], want[i])
		}
	}
}

func TestListEmptyCapture(t *testing.T) {
	canonical := filepath.Join(t.TempDir(), "capture.cast")
	segs, err := containerrecording.List(canonical)
	if err != nil || len(segs) != 0 || segs == nil {
		t.Fatalf("List = %#v, %v; want an empty, non-nil list", segs, err)
	}
	missingDir := filepath.Join(t.TempDir(), "absent", "capture.cast")
	if segs, err = containerrecording.List(missingDir); err != nil || len(segs) != 0 {
		t.Fatalf("missing directory: %#v, %v", segs, err)
	}
}

// TestReadEverySegmentEqualsSbshReader pins the AC: reading every listed
// segment to its end, decompressing the compressed ones and concatenating
// equals the stream sbsh's own reader returns.
func TestReadEverySegmentEqualsSbshReader(t *testing.T) {
	canonical, _ := seedCapture(t)
	segs, err := containerrecording.List(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, maxBytes := range []int{0, 1, 13, 1 << 20} {
		var got []byte
		for _, s := range segs {
			stored, last := readWhole(t, canonical, s.Seq, maxBytes)
			if last.Compressed != s.Compressed || last.Live != s.Live || last.Size != s.Size {
				t.Fatalf("seq %d: result %+v, listed %+v", s.Seq, last, s)
			}
			if s.Compressed {
				stored = gunzip(t, stored)
			}
			got = append(got, stored...)
		}
		want, readErr := capture.ReadAll(canonical)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("maxBytes %d: concatenation differs from capture.ReadAll:\n%q\n%q", maxBytes, got, want)
		}
	}
}

func TestReadSegmentBounds(t *testing.T) {
	canonical, parts := seedCapture(t)
	res, err := containerrecording.ReadSegment(
		canonical,
		kukeonv1.RecordingSegmentRequest{Seq: 5, Offset: 3, MaxBytes: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Data) != string(parts[1][3:7]) || res.Start != 3 || res.Next != 7 || !res.More || res.Live {
		t.Fatalf("chunk %+v", res)
	}
	res, err = containerrecording.ReadSegment(canonical, kukeonv1.RecordingSegmentRequest{Seq: 5, Offset: 1 << 30})
	if err != nil || len(res.Data) != 0 || res.Start != 1<<30 || res.Next != 1<<30 || res.More {
		t.Fatalf("past end: %+v, %v", res, err)
	}
	res, err = containerrecording.ReadSegment(canonical, kukeonv1.RecordingSegmentRequest{Seq: 5, MaxBytes: 1 << 30})
	if err != nil || len(res.Data) != len(parts[1]) {
		t.Fatalf("clamped read: %d bytes, %v", len(res.Data), err)
	}
}

// TestReadSegmentAfterRotation follows the live segment by offset across a
// rotation: the same seq then reads as a closed segment with Live false,
// and re-reading it from 0 holds every byte, so no event is lost or repeated.
func TestReadSegmentAfterRotation(t *testing.T) {
	canonical, parts := seedCapture(t)

	first, last := readWhole(t, canonical, 6, 0)
	if !last.Live || !bytes.Equal(first, parts[2]) {
		t.Fatalf("live read %q (%+v)", first, last)
	}

	// The workload writes more, then sbsh rotates: rename to the next seq,
	// open a new live segment, compress the closed one.
	more := []byte(`[9.5,"o","-more"]` + "\n")
	full := append(append([]byte{}, parts[2]...), more...)
	writeFile(t, canonical, full)
	if err := os.Rename(canonical, capture.SegmentPath(canonical, 6)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, canonical, []byte(header+`[10.0,"o","next"]`+"\n"))

	res, err := containerrecording.ReadSegment(canonical, kukeonv1.RecordingSegmentRequest{Seq: 6, Offset: last.Next})
	if err != nil {
		t.Fatal(err)
	}
	if res.Live || !bytes.Equal(res.Data, more) {
		t.Fatalf("read after rotation %+v", res)
	}

	writeFile(t, capture.SegmentPath(canonical, 6)+capture.GzSuffix, gz(t, full))
	if err = os.Remove(capture.SegmentPath(canonical, 6)); err != nil {
		t.Fatal(err)
	}
	stored, last := readWhole(t, canonical, 6, 7)
	if last.Live || !last.Compressed || !bytes.Equal(gunzip(t, stored), full) {
		t.Fatalf("re-read compressed %+v", last)
	}

	segs, err := containerrecording.List(canonical)
	if err != nil || segs[len(segs)-1].Seq != 7 || !segs[len(segs)-1].Live {
		t.Fatalf("after rotation the live segment is seq 7: %+v, %v", segs, err)
	}
}

func TestReadSegmentGone(t *testing.T) {
	canonical, _ := seedCapture(t)
	if err := os.Remove(capture.SegmentPath(canonical, 4) + capture.GzSuffix); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{4, 99} {
		_, err := containerrecording.ReadSegment(canonical, kukeonv1.RecordingSegmentRequest{Seq: seq})
		if !errors.Is(err, errdefs.ErrRecordingSegmentGone) {
			t.Fatalf("seq %d: %v, want ErrRecordingSegmentGone", seq, err)
		}
	}
}
