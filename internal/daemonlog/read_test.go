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

package daemonlog_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/daemonlog"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func writeLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string, offset int64, maxBytes int) kukeonv1.DaemonLogResult {
	t.Helper()
	res, err := daemonlog.Read(path, kukeonv1.DaemonLogRequest{Offset: offset, MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRead_TailWholeSmallFile(t *testing.T) {
	path := writeLog(t, "one\ntwo\n")
	res := read(t, path, -1, 0)
	if res.Data != "one\ntwo\n" || res.Start != 0 || res.Next != 8 || res.Size != 8 || res.Truncated || res.Reset {
		t.Fatalf("got %+v", res)
	}
	if res.Source != path {
		t.Fatalf("source %q", res.Source)
	}
}

func TestRead_TailDropsPartialFirstLineAndMarksTruncated(t *testing.T) {
	// 10 bytes window over "alpha\nbeta\ngamma\n" (17 bytes) starts mid "beta".
	res := read(t, writeLog(t, "alpha\nbeta\ngamma\n"), -1, 10)
	if res.Data != "gamma\n" || res.Start != 11 || res.Next != 17 || !res.Truncated {
		t.Fatalf("got %+v", res)
	}
}

func TestRead_HoldsBackLineStillBeingWritten(t *testing.T) {
	path := writeLog(t, "done\npart")
	tail := read(t, path, -1, 0)
	if tail.Data != "done\n" || tail.Next != 5 {
		t.Fatalf("tail %+v", tail)
	}
	if fwd := read(t, path, tail.Next, 0); fwd.Data != "" || fwd.Next != 5 || fwd.More {
		t.Fatalf("forward %+v", fwd)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("ial\nnext\n")
	_ = f.Close()
	if fwd := read(t, path, tail.Next, 0); fwd.Data != "partial\nnext\n" || fwd.Start != 5 || fwd.Next != 18 {
		t.Fatalf("after append %+v", fwd)
	}
}

func TestRead_ForwardStopsAtBoundAndReportsMore(t *testing.T) {
	path := writeLog(t, "aaa\nbbb\nccc\n")
	first := read(t, path, 0, 6)
	if first.Data != "aaa\n" || first.Next != 4 || !first.More {
		t.Fatalf("first %+v", first)
	}
	second := read(t, path, first.Next, 8)
	if second.Data != "bbb\nccc\n" || second.Next != 12 || second.More {
		t.Fatalf("second %+v", second)
	}
}

func TestRead_OffsetBeyondEndResetsToTail(t *testing.T) {
	res := read(t, writeLog(t, "fresh\n"), 1000, 0)
	if !res.Reset || res.Data != "fresh\n" || res.Next != 6 {
		t.Fatalf("got %+v", res)
	}
}

func TestRead_OverlongLineAdvancesInBoundSizedPieces(t *testing.T) {
	path := writeLog(t, strings.Repeat("x", 10)+"\n")
	first := read(t, path, 0, 4)
	if first.Data != "xxxx" || first.Next != 4 || !first.More {
		t.Fatalf("first %+v", first)
	}
	tail := read(t, path, -1, 4)
	if tail.Data != "xxx\n" || !tail.Truncated {
		t.Fatalf("tail %+v", tail)
	}
}

func TestRead_ClampsMaxBytes(t *testing.T) {
	line := strings.Repeat("y", 1023) + "\n"
	path := writeLog(t, strings.Repeat(line, 2*1024)) // 2 MiB
	res := read(t, path, 0, kukeonv1.DaemonLogMaxBytes*4)
	if int64(len(res.Data)) != kukeonv1.DaemonLogMaxBytes || !res.More {
		t.Fatalf("len=%d more=%v", len(res.Data), res.More)
	}
	def := read(t, path, -1, 0)
	if len(def.Data) != kukeonv1.DaemonLogDefaultMaxBytes-len(line) || !def.Truncated {
		t.Fatalf("default tail len=%d", len(def.Data))
	}
}

func TestRead_ReplacesInvalidUTF8(t *testing.T) {
	res := read(t, writeLog(t, "ok \xff\n"), -1, 0)
	if res.Data != "ok �\n" || res.Next != 5 {
		t.Fatalf("got %+v", res)
	}
}

func TestRead_EmptyAndMissingFiles(t *testing.T) {
	if res := read(t, writeLog(t, ""), -1, 0); res.Data != "" || res.Next != 0 || res.Truncated {
		t.Fatalf("empty %+v", res)
	}
	_, err := daemonlog.Read(filepath.Join(t.TempDir(), "absent"), kukeonv1.DaemonLogRequest{Offset: -1})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file err = %v", err)
	}
}
