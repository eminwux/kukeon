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

package containerlog_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func writeStream(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string, offset int64, maxBytes int) kukeonv1.ContainerLogResult {
	t.Helper()
	res, err := containerlog.Read(path, kukeonv1.ContainerLogRequest{Offset: offset, MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRead_ForwardWholeSmallFile(t *testing.T) {
	path := writeStream(t, []byte("one\ntwo\n"))
	res := read(t, path, 0, 0)
	if string(res.Data) != "one\ntwo\n" || res.Start != 0 || res.Next != 8 || res.Size != 8 ||
		res.More || res.Truncated {
		t.Fatalf("got %+v", res)
	}
	if res.Source != path {
		t.Fatalf("source %q, want %q", res.Source, path)
	}
}

func TestRead_ChunkBoundsAndCursorContinuation(t *testing.T) {
	content := []byte("0123456789abcdefghij") // 20 bytes
	path := writeStream(t, content)

	var (
		got    []byte
		offset int64
		chunks int
	)
	for {
		res := read(t, path, offset, 7)
		chunks++
		if len(res.Data) > 7 {
			t.Fatalf("chunk %d carries %d bytes, bound is 7", chunks, len(res.Data))
		}
		if res.Start != offset {
			t.Fatalf("chunk %d start %d, want %d", chunks, res.Start, offset)
		}
		got = append(got, res.Data...)
		offset = res.Next
		if !res.More {
			break
		}
	}
	if chunks != 3 {
		t.Fatalf("read in %d chunks, want 3 (7+7+6)", chunks)
	}
	if !bytes.Equal(got, content) || offset != int64(len(content)) {
		t.Fatalf("reassembled %q (next %d), want %q", got, offset, content)
	}

	// At the end: no data, cursor stays put, nothing more.
	res := read(t, path, offset, 7)
	if len(res.Data) != 0 || res.Next != offset || res.More {
		t.Fatalf("read at end: %+v", res)
	}
}

func TestRead_ClampsMaxBytes(t *testing.T) {
	content := bytes.Repeat([]byte("x"), kukeonv1.ContainerLogMaxBytes+10)
	path := writeStream(t, content)

	res := read(t, path, 0, kukeonv1.ContainerLogMaxBytes*4)
	if len(res.Data) != kukeonv1.ContainerLogMaxBytes || !res.More {
		t.Fatalf("len %d more %v, want clamp to %d with More", len(res.Data), res.More, kukeonv1.ContainerLogMaxBytes)
	}
	res = read(t, path, 0, 0)
	if len(res.Data) != kukeonv1.ContainerLogDefaultMaxBytes {
		t.Fatalf("default read len %d, want %d", len(res.Data), kukeonv1.ContainerLogDefaultMaxBytes)
	}
}

// TestRead_TailWithoutTrailingNewlineReturnedInFull pins the difference from
// daemonlog.Read: an sbsh capture often ends in a prompt with no newline, and
// those bytes must reach the caller now, not when a newline arrives.
func TestRead_TailWithoutTrailingNewlineReturnedInFull(t *testing.T) {
	content := []byte("build ok\r\n\x1b[1;32muser@cell\x1b[0m:~$ ")
	path := writeStream(t, content)

	res := read(t, path, 0, 0)
	if !bytes.Equal(res.Data, content) || res.Next != int64(len(content)) {
		t.Fatalf("data %q next %d, want %q next %d", res.Data, res.Next, content, len(content))
	}

	// The same holds when the bound cuts the stream mid-line.
	res = read(t, path, 0, 5)
	if string(res.Data) != "build" || res.Next != 5 || !res.More {
		t.Fatalf("bounded read %+v", res)
	}
}

func TestRead_NonUTF8BytesSurviveUnchanged(t *testing.T) {
	content := []byte{'a', 0xff, 0xfe, 0x00, 0xc3, '\n', 0x80, 'z'}
	path := writeStream(t, content)

	res := read(t, path, 0, 0)
	if !bytes.Equal(res.Data, content) {
		t.Fatalf("data %v, want %v", res.Data, content)
	}
}

func TestRead_ShrunkFileLeavesCursorAndReportsSize(t *testing.T) {
	path := writeStream(t, []byte("a long first generation\n"))
	first := read(t, path, 0, 0)

	if err := os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := read(t, path, first.Next, 0)
	if len(res.Data) != 0 || res.Next != first.Next || res.Size != 4 || res.More {
		t.Fatalf("read past shrunk end: %+v", res)
	}
	if res.Size >= first.Next {
		t.Fatalf("Size %d must drop below the cursor %d to signal the reset", res.Size, first.Next)
	}
}

func TestRead_NegativeOffsetTails(t *testing.T) {
	path := writeStream(t, []byte("0123456789"))

	res := read(t, path, -1, 4)
	if string(res.Data) != "6789" || res.Start != 6 || res.Next != 10 || !res.Truncated || res.More {
		t.Fatalf("tail %+v", res)
	}
	res = read(t, path, -1, 0)
	if string(res.Data) != "0123456789" || res.Start != 0 || res.Truncated {
		t.Fatalf("whole-file tail %+v", res)
	}
}

func TestRead_EmptyFile(t *testing.T) {
	path := writeStream(t, nil)
	res := read(t, path, 0, 0)
	if len(res.Data) != 0 || res.Next != 0 || res.Size != 0 || res.More {
		t.Fatalf("got %+v", res)
	}
}

func TestRead_MissingFileIsErrNotExist(t *testing.T) {
	_, err := containerlog.Read(filepath.Join(t.TempDir(), "absent"), kukeonv1.ContainerLogRequest{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}
