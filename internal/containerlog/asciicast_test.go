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
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

const castHeader = `{"version":2,"width":80,"height":24,"timestamp":1700000000}` + "\n"

func castEvent(t *testing.T, at float64, code, data string) string {
	t.Helper()
	line, err := json.Marshal([]any{at, code, data})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

func readCast(t *testing.T, path string, offset int64, maxBytes int) kukeonv1.ContainerLogResult {
	t.Helper()
	res, err := containerlog.ReadAsciicast(path, kukeonv1.ContainerLogRequest{Offset: offset, MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// follow reads the recording forward from 0 the way `kuke log` does,
// returning the concatenated output and the final cursor.
func follow(t *testing.T, path string, offset int64, maxBytes int) (string, int64) {
	t.Helper()
	var out strings.Builder
	for range 10000 {
		res := readCast(t, path, offset, maxBytes)
		out.Write(res.Data)
		offset = res.Next
		if !res.More {
			return out.String(), offset
		}
	}
	t.Fatal("follow did not converge")
	return "", 0
}

func TestReadAsciicast_DecodesWholeRecording(t *testing.T) {
	content := castHeader +
		castEvent(t, 0.1, "o", "$ echo héllo ─\r\n") +
		castEvent(t, 0.2, "r", "100x30") +
		castEvent(t, 0.3, "o", "héllo ─\r\n") +
		castHeader + // a second run
		castEvent(t, 0.0, "o", "again\r\n")
	path := writeStream(t, []byte(content))

	res := readCast(t, path, 0, 0)
	want := "$ echo héllo ─\r\nhéllo ─\r\nagain\r\n"
	if string(res.Data) != want {
		t.Fatalf("data %q, want %q", res.Data, want)
	}
	size := int64(len(content))
	if res.Start != 0 || res.Next != size || res.Size != size || res.More || res.Truncated || res.Source != path {
		t.Fatalf("got %+v", res)
	}
}

func TestReadAsciicast_FollowReturnsEveryByteOnce(t *testing.T) {
	var content strings.Builder
	var want strings.Builder
	content.WriteString(castHeader)
	for i := range 200 {
		data := fmt.Sprintf("line %d ─ ünïcode\r\n", i)
		content.WriteString(castEvent(t, float64(i)/10, "o", data))
		want.WriteString(data)
		if i%50 == 0 {
			content.WriteString(castEvent(t, float64(i)/10, "r", "90x20"))
		}
	}
	path := writeStream(t, []byte(content.String()))

	// Bounds that cut lines at every possible place.
	for _, maxBytes := range []int{1, 7, 33, 64, 100, 4096} {
		got, next := follow(t, path, 0, maxBytes)
		if got != want.String() {
			t.Fatalf("maxBytes %d: got %d bytes, want %d", maxBytes, len(got), want.Len())
		}
		if next != int64(content.Len()) {
			t.Fatalf("maxBytes %d: cursor %d, want %d", maxBytes, next, content.Len())
		}
	}
}

func TestReadAsciicast_CutLineAtChunkEnd(t *testing.T) {
	first := castHeader + castEvent(t, 0.1, "o", "one\r\n")
	second := castEvent(t, 0.2, "o", "two\r\n")
	path := writeStream(t, []byte(first+second))

	// The bound ends inside the second event: only the first is returned,
	// and the cursor stops at the line boundary.
	res := readCast(t, path, 0, len(first)+3)
	if string(res.Data) != "one\r\n" || res.Next != int64(len(first)) || !res.More {
		t.Fatalf("first chunk %+v", res)
	}
	res = readCast(t, path, res.Next, len(first)+3)
	if string(res.Data) != "two\r\n" || res.Next != int64(len(first+second)) || res.More {
		t.Fatalf("second chunk %+v", res)
	}
}

func TestReadAsciicast_LineLongerThanMaxBytes(t *testing.T) {
	long := strings.Repeat("x", 300)
	content := castHeader + castEvent(t, 0.1, "o", long) + castEvent(t, 0.2, "o", "tail")
	path := writeStream(t, []byte(content))

	got, next := follow(t, path, 0, 64)
	if got != long+"tail" || next != int64(len(content)) {
		t.Fatalf("got %d bytes (cursor %d)", len(got), next)
	}

	// A single read at an offset inside nothing but the long line still
	// returns the whole line.
	res := readCast(t, path, int64(len(castHeader)), 16)
	if string(res.Data) != long || !res.More {
		t.Fatalf("long line read %+v", res)
	}
}

func TestReadAsciicast_LineOverMaxEventLineIsSkipped(t *testing.T) {
	huge := castEvent(t, 0.1, "o", strings.Repeat("y", containerlog.MaxEventLine))
	content := castHeader + huge + castEvent(t, 0.2, "o", "after")
	path := writeStream(t, []byte(content))

	res := readCast(t, path, int64(len(castHeader)), 0)
	if len(res.Data) != 0 || res.Next != int64(len(castHeader+huge)) || !res.More {
		t.Fatalf("huge line: %d bytes, next %d, more %v", len(res.Data), res.Next, res.More)
	}
	res = readCast(t, path, res.Next, 0)
	if string(res.Data) != "after" {
		t.Fatalf("after huge line %+v", res)
	}
}

func TestReadAsciicast_LineStillBeingWritten(t *testing.T) {
	done := castHeader + castEvent(t, 0.1, "o", "done\r\n")
	partial := `[0.2, "o", "half`
	path := writeStream(t, []byte(done+partial))

	res := readCast(t, path, 0, 0)
	if string(res.Data) != "done\r\n" || res.Next != int64(len(done)) || res.More {
		t.Fatalf("first read %+v", res)
	}
	res = readCast(t, path, res.Next, 0)
	if len(res.Data) != 0 || res.Next != int64(len(done)) || res.More {
		t.Fatalf("partial line was returned: %+v", res)
	}

	// Once the line is complete it is returned once.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(" line\"]\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	res = readCast(t, path, res.Next, 0)
	if string(res.Data) != "half line" || res.More {
		t.Fatalf("completed line %+v", res)
	}
}

func TestReadAsciicast_TailAlignsToLineStart(t *testing.T) {
	var content strings.Builder
	content.WriteString(castHeader)
	for i := range 50 {
		content.WriteString(castEvent(t, float64(i), "o", fmt.Sprintf("<%02d>", i)))
	}
	path := writeStream(t, []byte(content.String()))
	size := int64(content.Len())

	res := readCast(t, path, -1, 100)
	if !res.Truncated || res.Next != size || res.More {
		t.Fatalf("tail %+v", res)
	}
	if res.Start <= size-100 || content.String()[res.Start-1] != '\n' {
		t.Fatalf("tail start %d is not the first line start in the window", res.Start)
	}
	if !strings.HasSuffix(string(res.Data), "<49>") || strings.Contains(string(res.Data), `"`) {
		t.Fatalf("tail data %q", res.Data)
	}

	// A window that starts exactly on a line boundary keeps that line.
	lastLine := castEvent(t, 49, "o", "<49>")
	res = readCast(t, path, -1, len(lastLine))
	if string(res.Data) != "<49>" || res.Start != size-int64(len(lastLine)) || !res.Truncated {
		t.Fatalf("boundary tail %+v", res)
	}

	// A tail of a recording smaller than the bound is the whole recording.
	res = readCast(t, path, -1, 1<<20)
	if res.Truncated || res.Start != 0 || !bytes.HasPrefix(res.Data, []byte("<00>")) {
		t.Fatalf("whole tail %+v", res)
	}
}

func TestReadAsciicast_HeadersAndResizesOnly(t *testing.T) {
	content := castHeader + castEvent(t, 0.1, "r", "100x30")
	path := writeStream(t, []byte(content))
	res := readCast(t, path, 0, 0)
	if len(res.Data) != 0 || res.Next != int64(len(content)) || res.More {
		t.Fatalf("got %+v", res)
	}
}

func TestReadAsciicast_OffsetPastEnd(t *testing.T) {
	path := writeStream(t, []byte(castHeader))
	res := readCast(t, path, 1000, 0)
	if len(res.Data) != 0 || res.Start != 1000 || res.Next != 1000 || res.Size != int64(len(castHeader)) {
		t.Fatalf("got %+v", res)
	}
}

// TestRead_RawUnchangedByAsciicastContent pins that Read never decodes: a raw
// stream that happens to look like a recording is returned verbatim.
func TestRead_RawUnchangedByAsciicastContent(t *testing.T) {
	content := castHeader + castEvent(t, 0.1, "o", "x")
	path := writeStream(t, []byte(content))
	res := read(t, path, 0, 0)
	if string(res.Data) != content {
		t.Fatalf("raw read decoded: %q", res.Data)
	}
}
