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

// Package containerlog reads bounded, byte-exact chunks of a container's
// output stream for the ReadContainerLog RPC (issue #1537).
//
// It is deliberately separate from internal/daemonlog: kukeond's own log is
// line-oriented text, so that reader holds back partial lines and replaces
// invalid UTF-8. A container stream may be an sbsh capture — a raw terminal
// byte stream with escape sequences, prompts that never end in a newline and
// arbitrary bytes — so this reader returns exactly the bytes on disk.
package containerlog

import (
	"fmt"
	"io"
	"os"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// Read returns one chunk of the stream at path, following the cursor
// contract documented on kukeonv1.ContainerLogRequest and
// ContainerLogResult. Open/stat/read errors are returned unwrapped from os
// so callers can test them with errors.Is (os.ErrNotExist,
// os.ErrPermission).
func Read(path string, req kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
	limit := int64(req.MaxBytes)
	if limit <= 0 {
		limit = kukeonv1.ContainerLogDefaultMaxBytes
	}
	if limit > kukeonv1.ContainerLogMaxBytes {
		limit = kukeonv1.ContainerLogMaxBytes
	}

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
		// The stream shrank (truncated or replaced) since the caller's last
		// read. Answer with no data and the cursor unchanged; Size < Offset
		// tells the caller to start over from 0.
		res.Start = req.Offset
		res.Next = req.Offset
		return res, nil
	}

	var start int64
	if req.Offset < 0 {
		start = max(size-limit, 0)
		res.Truncated = start > 0
	} else {
		start = req.Offset
	}
	buf, err := readAt(f, start, min(limit, size-start))
	if err != nil {
		return kukeonv1.ContainerLogResult{}, err
	}

	res.Data = buf
	res.Start = start
	res.Next = start + int64(len(buf))
	res.More = res.Next < size
	return res, nil
}

func readAt(f *os.File, start, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	read, err := f.ReadAt(buf, start)
	// The file may have shrunk between Stat and ReadAt; keep what was read.
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", f.Name(), err)
	}
	return buf[:read], nil
}
