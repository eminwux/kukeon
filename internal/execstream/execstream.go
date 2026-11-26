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

// Package execstream is the frame protocol `kuke exec` speaks on the
// connection kukeond hands back from ExecContainer. The daemon side
// (internal/client/local), the CLI (cmd/kuke/exec) and the web server
// (internal/webserver) all read and write it through this package.
//
// A frame is one type byte, a 4-byte big-endian payload length, and the
// payload (at most MaxPayload bytes):
//
//	1 stdin      client -> daemon  bytes
//	2 stdin EOF  client -> daemon  empty
//	3 resize     client -> daemon  cols and rows as two big-endian uint16
//	4 stdout     daemon -> client  bytes
//	5 stderr     daemon -> client  bytes
//	6 exit       daemon -> client  JSON {"code":N,"error":"..."}; last frame
//
// A connection that closes without an exit frame is an error on the client.
package execstream

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// Frame types.
const (
	TypeStdin    byte = 1
	TypeStdinEOF byte = 2
	TypeResize   byte = 3
	TypeStdout   byte = 4
	TypeStderr   byte = 5
	TypeExit     byte = 6
)

const (
	// MaxPayload is the largest payload a single frame carries.
	MaxPayload = 32 * 1024

	headerSize = 5
	resizeSize = 4
	// maxExitError bounds the error text of an exit frame so the JSON
	// payload always fits one frame, whatever escaping it needs.
	maxExitError = 4 * 1024
)

// Exit is the payload of the exit frame. Error is set when the process
// could not be started or waited on; Code is then 1.
type Exit struct {
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

// WriteFrame writes one frame to w with a single Write call, so callers
// that serialize their writes never interleave frames.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: %d bytes", errdefs.ErrExecFrameTooLarge, len(payload))
	}
	buf := make([]byte, headerSize+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:headerSize], uint32(len(payload))) //nolint:gosec // bounded by MaxPayload
	copy(buf[headerSize:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame from r. It returns io.EOF when r ends on a
// frame boundary and io.ErrUnexpectedEOF when it ends inside a frame. A
// header that claims more than MaxPayload fails with ErrExecFrameTooLarge
// before anything of that size is allocated.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > MaxPayload {
		return 0, nil, fmt.Errorf("%w: %d bytes", errdefs.ErrExecFrameTooLarge, size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return header[0], payload, nil
}

// EncodeResize returns the payload of a resize frame.
func EncodeResize(cols, rows uint16) []byte {
	payload := make([]byte, resizeSize)
	binary.BigEndian.PutUint16(payload[0:2], cols)
	binary.BigEndian.PutUint16(payload[2:4], rows)
	return payload
}

// DecodeResize parses the payload of a resize frame.
func DecodeResize(payload []byte) (uint16, uint16, error) {
	if len(payload) != resizeSize {
		return 0, 0, fmt.Errorf("%w: resize payload is %d bytes", errdefs.ErrExecMalformedFrame, len(payload))
	}
	return binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4]), nil
}

// EncodeExit returns the payload of an exit frame. An over-long error text
// is cut so the payload fits one frame.
func EncodeExit(exit Exit) ([]byte, error) {
	if len(exit.Error) > maxExitError {
		exit.Error = exit.Error[:maxExitError]
	}
	return json.Marshal(exit)
}

// DecodeExit parses the payload of an exit frame.
func DecodeExit(payload []byte) (Exit, error) {
	var exit Exit
	if err := json.Unmarshal(payload, &exit); err != nil {
		return Exit{}, fmt.Errorf("%w: exit payload: %w", errdefs.ErrExecMalformedFrame, err)
	}
	return exit, nil
}

// Writer serializes frames onto one connection. It is safe for concurrent
// use: the stdout and stderr copiers of an exec session share one Writer,
// and a frame is always written whole.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter returns a Writer that frames onto w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteFrame writes one frame; the payload must fit MaxPayload.
func (w *Writer) WriteFrame(typ byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WriteFrame(w.w, typ, payload)
}

// WriteData writes p as frames of type typ, split at MaxPayload. An empty p
// writes nothing.
func (w *Writer) WriteData(typ byte, p []byte) error {
	for len(p) > 0 {
		n := min(len(p), MaxPayload)
		if err := w.WriteFrame(typ, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// Stream returns an io.Writer whose writes become frames of type typ.
func (w *Writer) Stream(typ byte) io.Writer {
	return streamWriter{w: w, typ: typ}
}

// StdinEOF writes the stdin EOF frame.
func (w *Writer) StdinEOF() error {
	return w.WriteFrame(TypeStdinEOF, nil)
}

// Resize writes a resize frame.
func (w *Writer) Resize(cols, rows uint16) error {
	return w.WriteFrame(TypeResize, EncodeResize(cols, rows))
}

// Exit writes the exit frame.
func (w *Writer) Exit(exit Exit) error {
	payload, err := EncodeExit(exit)
	if err != nil {
		return err
	}
	return w.WriteFrame(TypeExit, payload)
}

// Receive is the caller's side of a session: it reads the daemon's frames
// until the exit frame, copying stdout and stderr payloads to the given
// writers, and returns the exit. A connection that ends first is
// ErrExecSessionLost. Frames of other types are skipped.
func Receive(r io.Reader, stdout, stderr io.Writer) (Exit, error) {
	for {
		typ, payload, err := ReadFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return Exit{}, errdefs.ErrExecSessionLost
			}
			return Exit{}, fmt.Errorf("%w: %w", errdefs.ErrExecSessionLost, err)
		}
		switch typ {
		case TypeStdout:
			if _, err = stdout.Write(payload); err != nil {
				return Exit{}, fmt.Errorf("write command output: %w", err)
			}
		case TypeStderr:
			if _, err = stderr.Write(payload); err != nil {
				return Exit{}, fmt.Errorf("write command output: %w", err)
			}
		case TypeExit:
			return DecodeExit(payload)
		}
	}
}

type streamWriter struct {
	w   *Writer
	typ byte
}

func (s streamWriter) Write(p []byte) (int, error) {
	if err := s.w.WriteData(s.typ, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
