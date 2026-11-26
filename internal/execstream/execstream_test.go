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

package execstream_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
)

func TestFrameRoundTrip(t *testing.T) {
	exit, err := execstream.EncodeExit(execstream.Exit{Code: 7, Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		typ     byte
		payload []byte
	}{
		{"stdin", execstream.TypeStdin, []byte("typed")},
		{"stdin EOF", execstream.TypeStdinEOF, nil},
		{"resize", execstream.TypeResize, execstream.EncodeResize(132, 43)},
		{"stdout", execstream.TypeStdout, bytes.Repeat([]byte{0xff}, execstream.MaxPayload)},
		{"stderr", execstream.TypeStderr, []byte("warn\n")},
		{"exit", execstream.TypeExit, exit},
	}
	var buf bytes.Buffer
	for _, tc := range cases {
		if err = execstream.WriteFrame(&buf, tc.typ, tc.payload); err != nil {
			t.Fatalf("%s: WriteFrame: %v", tc.name, err)
		}
	}
	for _, tc := range cases {
		typ, payload, readErr := execstream.ReadFrame(&buf)
		if readErr != nil {
			t.Fatalf("%s: ReadFrame: %v", tc.name, readErr)
		}
		if typ != tc.typ || !bytes.Equal(payload, tc.payload) {
			t.Fatalf("%s: got type %d with %d bytes, want type %d with %d bytes",
				tc.name, typ, len(payload), tc.typ, len(tc.payload))
		}
	}
	if _, _, err = execstream.ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("read at the end = %v, want io.EOF", err)
	}
}

func TestTypedPayloads(t *testing.T) {
	cols, rows, err := execstream.DecodeResize(execstream.EncodeResize(132, 43))
	if err != nil || cols != 132 || rows != 43 {
		t.Fatalf("resize round trip = %d x %d (%v), want 132 x 43", cols, rows, err)
	}
	if _, _, err = execstream.DecodeResize([]byte{1, 2, 3}); !errors.Is(err, errdefs.ErrExecMalformedFrame) {
		t.Fatalf("short resize = %v, want ErrExecMalformedFrame", err)
	}

	payload, err := execstream.EncodeExit(execstream.Exit{Code: 1, Error: "start failed"})
	if err != nil {
		t.Fatal(err)
	}
	exit, err := execstream.DecodeExit(payload)
	if err != nil || exit.Code != 1 || exit.Error != "start failed" {
		t.Fatalf("exit round trip = %+v (%v)", exit, err)
	}
	if _, err = execstream.DecodeExit([]byte("{")); !errors.Is(err, errdefs.ErrExecMalformedFrame) {
		t.Fatalf("broken exit = %v, want ErrExecMalformedFrame", err)
	}

	// An error text longer than a frame is cut, never rejected.
	long, err := execstream.EncodeExit(execstream.Exit{Code: 1, Error: string(bytes.Repeat([]byte{'"'}, 1<<20))})
	if err != nil {
		t.Fatal(err)
	}
	if len(long) > execstream.MaxPayload {
		t.Fatalf("exit payload is %d bytes, above the frame limit", len(long))
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	err := execstream.WriteFrame(&buf, execstream.TypeStdout, make([]byte, execstream.MaxPayload+1))
	if !errors.Is(err, errdefs.ErrExecFrameTooLarge) {
		t.Fatalf("WriteFrame = %v, want ErrExecFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes of a rejected frame", buf.Len())
	}
}

// TestReadFrameRejectsOversizedPayload pins that a header claiming a huge
// payload is refused before a buffer of that size is allocated.
func TestReadFrameRejectsOversizedPayload(t *testing.T) {
	const claimed = 1 << 31
	header := []byte{execstream.TypeStdout, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[1:], claimed)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := execstream.ReadFrame(bytes.NewReader(header))
	runtime.ReadMemStats(&after)

	if !errors.Is(err, errdefs.ErrExecFrameTooLarge) {
		t.Fatalf("ReadFrame = %v, want ErrExecFrameTooLarge", err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown >= claimed/2 {
		t.Fatalf("ReadFrame allocated %d bytes for a rejected frame", grown)
	}

	// One byte over the limit is refused too; the limit itself is not.
	binary.BigEndian.PutUint32(header[1:], execstream.MaxPayload+1)
	if _, _, err = execstream.ReadFrame(bytes.NewReader(header)); !errors.Is(err, errdefs.ErrExecFrameTooLarge) {
		t.Fatalf("ReadFrame one byte over the limit = %v, want ErrExecFrameTooLarge", err)
	}
}

func TestReadFrameShortReads(t *testing.T) {
	var full bytes.Buffer
	if err := execstream.WriteFrame(&full, execstream.TypeStdout, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	frame := full.Bytes()
	for _, cut := range []int{1, 4, 5, len(frame) - 1} {
		_, _, err := execstream.ReadFrame(bytes.NewReader(frame[:cut]))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("frame cut at %d bytes: ReadFrame = %v, want io.ErrUnexpectedEOF", cut, err)
		}
	}
}

func TestWriterSplitsLargeWrites(t *testing.T) {
	var buf bytes.Buffer
	w := execstream.NewWriter(&buf)
	data := bytes.Repeat([]byte("abc"), execstream.MaxPayload)
	if n, err := w.Stream(execstream.TypeStdout).Write(data); err != nil || n != len(data) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(data))
	}
	var got []byte
	for {
		typ, payload, err := execstream.ReadFrame(&buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if typ != execstream.TypeStdout || len(payload) == 0 || len(payload) > execstream.MaxPayload {
			t.Fatalf("frame type %d with %d bytes", typ, len(payload))
		}
		got = append(got, payload...)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("reassembled stream differs from the written bytes")
	}
}

// chunkWriter hands every Write to the underlying writer in small pieces
// and yields between them, so two unserialized writers would interleave.
type chunkWriter struct{ w io.Writer }

func (c chunkWriter) Write(p []byte) (int, error) {
	const chunk = 7
	written := 0
	for len(p) > 0 {
		n := min(len(p), chunk)
		m, err := c.w.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

func TestWriterConcurrentStreamsNeverInterleave(t *testing.T) {
	const size = 1 << 20
	pr, pw := io.Pipe()
	w := execstream.NewWriter(chunkWriter{w: pw})

	sources := map[byte][]byte{
		execstream.TypeStdout: bytes.Repeat([]byte("o"), size),
		execstream.TypeStderr: bytes.Repeat([]byte("e"), size),
	}
	var wg sync.WaitGroup
	for typ, data := range sources {
		wg.Go(func() {
			// Odd-sized writes so frame boundaries do not line up.
			stream := w.Stream(typ)
			for len(data) > 0 {
				n := min(len(data), 40_001)
				if _, err := stream.Write(data[:n]); err != nil {
					t.Errorf("stream %d: %v", typ, err)
					return
				}
				data = data[n:]
			}
		})
	}
	go func() {
		wg.Wait()
		_ = pw.Close()
	}()

	got := map[byte][]byte{}
	for {
		typ, payload, err := execstream.ReadFrame(pr)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v (frames interleaved)", err)
		}
		got[typ] = append(got[typ], payload...)
	}
	for typ, want := range sources {
		if !bytes.Equal(got[typ], want) {
			t.Errorf("stream %d: got %d bytes, want %d intact bytes", typ, len(got[typ]), len(want))
		}
	}
}

func TestReceive(t *testing.T) {
	var conn bytes.Buffer
	w := execstream.NewWriter(&conn)
	for _, step := range []func() error{
		func() error { return w.WriteData(execstream.TypeStdout, []byte("out-1 ")) },
		func() error { return w.WriteData(execstream.TypeStderr, []byte("err")) },
		func() error { return w.WriteFrame(99, []byte("from a newer daemon")) },
		func() error { return w.WriteData(execstream.TypeStdout, []byte("out-2")) },
		func() error { return w.Exit(execstream.Exit{Code: 7}) },
		func() error { return w.WriteData(execstream.TypeStdout, []byte("never read")) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	exit, err := execstream.Receive(&conn, &stdout, &stderr)
	if err != nil || exit.Code != 7 {
		t.Fatalf("Receive = %+v, %v; want exit 7", exit, err)
	}
	if stdout.String() != "out-1 out-2" || stderr.String() != "err" {
		t.Fatalf("stdout %q, stderr %q; want the streams kept apart", stdout.String(), stderr.String())
	}
}

func TestReceiveWithoutExitFrame(t *testing.T) {
	var whole bytes.Buffer
	if err := execstream.WriteFrame(&whole, execstream.TypeStdout, []byte("partial")); err != nil {
		t.Fatal(err)
	}
	for name, stream := range map[string][]byte{
		"closed at once":        nil,
		"closed after output":   whole.Bytes(),
		"closed inside a frame": whole.Bytes()[:whole.Len()-2],
	} {
		_, err := execstream.Receive(bytes.NewReader(stream), io.Discard, io.Discard)
		if !errors.Is(err, errdefs.ErrExecSessionLost) {
			t.Errorf("%s: Receive = %v, want ErrExecSessionLost", name, err)
		}
	}
}
