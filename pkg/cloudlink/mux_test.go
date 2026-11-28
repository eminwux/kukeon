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

package cloudlink

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// sessionPair connects a dialer and an acceptor session over a real
// WebSocket.
func sessionPair(t *testing.T) (*Session, *Session) {
	t.Helper()
	accepted := make(chan *Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		accepted <- NewSession(conn, false)
	}))
	t.Cleanup(srv.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = resp.Body.Close()
	dialer := NewSession(conn, true)
	acceptor := <-accepted
	t.Cleanup(func() {
		_ = dialer.Close()
		_ = acceptor.Close()
	})
	return dialer, acceptor
}

func TestSessionStreamRoundTripBeyondWindow(t *testing.T) {
	dialer, acceptor := sessionPair(t)
	payload := make([]byte, 3*Window+123)
	_, _ = rand.Read(payload)

	done := make(chan []byte, 1)
	go func() {
		st, err := dialer.Accept()
		if err != nil {
			t.Errorf("accept: %v", err)
			done <- nil
			return
		}
		got, err := io.ReadAll(st)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		// Echo it back, then close.
		if _, err = st.Write(got); err != nil {
			t.Errorf("echo: %v", err)
		}
		_ = st.Close()
		done <- got
	}()

	st, err := acceptor.Open(context.Background())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if st.id%2 != 0 {
		t.Fatalf("acceptor opened odd stream %d", st.id)
	}
	if _, err = st.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	echoed, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got := <-done; !bytes.Equal(got, payload) {
		t.Fatalf("peer read %d bytes, want %d", len(got), len(payload))
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("echo is %d bytes, want %d", len(echoed), len(payload))
	}
	_ = st.Close()
}

func TestWriteFrameRefusesOversizedPayload(t *testing.T) {
	dialer, _ := sessionPair(t)
	err := dialer.writeFrame(frameData, 1, make([]byte, maxFramePayload+1))
	if !errors.Is(err, errProtocol) {
		t.Fatalf("oversized frame: got %v, want %v", err, errProtocol)
	}
	select {
	case <-dialer.done:
		t.Fatal("refusing an oversized frame closed the session")
	default:
	}
	if err = dialer.writeFrame(frameData, 1, make([]byte, maxFramePayload)); err != nil {
		t.Fatalf("frame at the limit: %v", err)
	}
}

func TestSessionCloseFailsOpenStreams(t *testing.T) {
	dialer, acceptor := sessionPair(t)
	st, err := acceptor.Open(context.Background())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	peer, err := dialer.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	readErr := make(chan error, 1)
	go func() {
		_, readErr2 := peer.Read(make([]byte, 1))
		readErr <- readErr2
	}()
	_ = acceptor.Close()
	select {
	case err = <-readErr:
		if !errors.Is(err, ErrLinkClosed) {
			t.Fatalf("read on a dropped link = %v, want ErrLinkClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not fail after the link closed")
	}
	if _, err = st.Write([]byte("x")); !errors.Is(err, ErrLinkClosed) {
		t.Fatalf("write after close = %v, want ErrLinkClosed", err)
	}
	if _, err = acceptor.Open(context.Background()); !errors.Is(err, ErrLinkClosed) {
		t.Fatalf("open after close = %v, want ErrLinkClosed", err)
	}
}

func TestStreamCloseResetsPeerWriter(t *testing.T) {
	dialer, acceptor := sessionPair(t)
	st, err := acceptor.Open(context.Background())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	peer, err := dialer.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err = st.Write([]byte("last words")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.Close()
	got, err := io.ReadAll(peer)
	if err != nil || string(got) != "last words" {
		t.Fatalf("peer read %q, %v; want the bytes sent before close, then EOF", got, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = peer.Write([]byte("x")); errors.Is(err, ErrStreamReset) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer write after close = %v, want ErrStreamReset", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamReadDeadline(t *testing.T) {
	dialer, acceptor := sessionPair(t)
	if _, err := acceptor.Open(context.Background()); err != nil {
		t.Fatalf("open: %v", err)
	}
	peer, err := dialer.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err = peer.Read(make([]byte, 1)); !errors.Is(err, errDeadline()) {
		t.Fatalf("read past the deadline = %v, want a deadline error", err)
	}
	_ = peer.SetReadDeadline(time.Time{})
	_ = peer.SetReadDeadline(time.Now().Add(-time.Second))
	if _, err = peer.Read(make([]byte, 1)); !errors.Is(err, errDeadline()) {
		t.Fatalf("read with a past deadline = %v, want a deadline error", err)
	}
}

func errDeadline() error { return os.ErrDeadlineExceeded }
