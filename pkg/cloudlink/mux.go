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

// Package cloudlink is the kukeon cloud link: an outbound, authenticated
// connection from a host's `kuke serve` to a relay, over which the relay
// reaches the host's existing /api routes without any inbound port.
//
// # Transport
//
// The host dials the relay's link endpoint as a WebSocket over TLS, and
// authenticates with the client certificate it received at enrollment
// (mutual TLS). One link carries many concurrent streams. Each stream is a
// byte stream with net.Conn semantics; the relay opens one per proxied
// request and writes a plain HTTP/1.1 request on it, and the host serves it
// with its ordinary HTTP handler. A WebSocket route (a cell terminal) is an
// HTTP upgrade on its stream, so the link carries request/response and
// WebSocket traffic alike, and the host's routes stay unchanged.
//
// Every WebSocket message on the link is one binary frame:
//
//	byte 0     frame type
//	bytes 1-4  stream ID, big endian
//	bytes 5-   payload
//
// The frame types are open (no payload), data (stream bytes), close (the
// sender writes no more, like a TCP FIN), reset (abort the stream in both
// directions) and window (a 4-byte big-endian credit increment). The side
// that dialed the link numbers the streams it opens with odd IDs, the side
// that accepted it with even IDs. Each stream has a receive window of
// [Window] bytes: a sender never has more unacknowledged data in flight than
// the credit its peer granted, so one slow stream cannot stall the others.
// Both sides send WebSocket pings; a link that stays silent for
// [IdleTimeout] is closed, and every open stream fails with [ErrLinkClosed].
//
// # Enrollment and identity
//
// [Enroll] exchanges a one-time enrollment token for a [Credential]: a
// client certificate for a key generated on the host (the private key never
// leaves it), the relay's identity public key and the link URL. A request
// the relay forwards carries the user it authenticated, signed with the
// relay's identity key ([SignRequest]); the host checks it with
// [VerifyRequest] and records the user.
//
// # Reference relay
//
// [Relay] is a complete, in-memory relay: enrollment, the link endpoint and
// a user-facing proxy. Tests use it, and a production relay can embed it or
// follow it. The proxy authenticates the user ([Relay.Authenticate]) and
// then authorizes that user for the host the request names
// ([Relay.Authorize]); an embedder supplies both, and the relay serves no
// proxied request without them.
package cloudlink

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Frame types.
const (
	frameOpen   byte = 1
	frameData   byte = 2
	frameClose  byte = 3
	frameReset  byte = 4
	frameWindow byte = 5
)

const (
	headerLen = 5
	// maxFramePayload bounds the payload of one data frame.
	maxFramePayload = 32 << 10
	// Window is the per-stream receive window both sides start with.
	Window = 256 << 10
	// PingInterval is how often each side pings the other.
	PingInterval = 15 * time.Second
	// IdleTimeout closes a link from which nothing, not even a ping or pong,
	// arrived for this long.
	IdleTimeout = 3 * PingInterval
	// frameWriteTimeout bounds one WebSocket write.
	frameWriteTimeout = 30 * time.Second
	// acceptBacklog bounds streams opened by the peer but not yet accepted.
	acceptBacklog = 128
	// maxStreams bounds the open streams on one link.
	maxStreams = 4096
	// windowPayloadLen is the size of a window frame's credit increment.
	windowPayloadLen = 4
)

var (
	// ErrLinkClosed is the error of every stream operation after the link
	// it runs on closed, and of Open and Accept on a closed session.
	ErrLinkClosed = errors.New("cloud link closed")
	// ErrStreamReset is the error of a stream its peer aborted.
	ErrStreamReset = errors.New("cloud link stream reset by peer")
	// errProtocol closes a link whose peer broke the framing rules.
	errProtocol = errors.New("cloud link protocol error")
	// errTooManyStreams refuses an Open past maxStreams.
	errTooManyStreams = errors.New("cloud link: too many open streams")
)

// Session multiplexes streams over one WebSocket connection.
type Session struct {
	conn   *websocket.Conn
	dialer bool

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint32]*Stream
	nextID  uint32
	err     error

	accept    chan *Stream
	done      chan struct{}
	closeOnce sync.Once
}

// NewSession starts a session over conn. dialer is true on the side that
// dialed the WebSocket (the host) and false on the side that accepted it (the
// relay); it only decides how the two sides number their streams. The session
// owns conn and closes it when the session closes.
func NewSession(conn *websocket.Conn, dialer bool) *Session {
	s := &Session{
		conn:    conn,
		dialer:  dialer,
		streams: map[uint32]*Stream{},
		accept:  make(chan *Stream, acceptBacklog),
		done:    make(chan struct{}),
	}
	if dialer {
		s.nextID = 1
	} else {
		s.nextID = 2
	}
	conn.SetReadLimit(headerLen + maxFramePayload)
	s.extendReadDeadline()
	conn.SetPongHandler(func(string) error {
		s.extendReadDeadline()
		return nil
	})
	conn.SetPingHandler(func(data string) error {
		s.extendReadDeadline()
		// A failed pong surfaces as a failed read or write soon after.
		_ = conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(frameWriteTimeout))
		return nil
	})
	go s.readLoop()
	go s.pingLoop()
	return s
}

func (s *Session) extendReadDeadline() {
	_ = s.conn.SetReadDeadline(time.Now().Add(IdleTimeout))
}

// Done is closed when the session closes.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err reports why the session closed; nil while it is open.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the link. Every open stream fails with ErrLinkClosed.
func (s *Session) Close() error {
	s.closeWith(ErrLinkClosed)
	return nil
}

func (s *Session) closeWith(cause error) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.err = cause
		streams := make([]*Stream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		s.streams = map[uint32]*Stream{}
		s.mu.Unlock()
		close(s.done)
		_ = s.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = s.conn.Close()
		for _, st := range streams {
			st.terminate(ErrLinkClosed)
		}
	})
}

func (s *Session) pingLoop() {
	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(frameWriteTimeout)); err != nil {
				s.closeWith(fmt.Errorf("%w: ping: %w", ErrLinkClosed, err))
				return
			}
		}
	}
}

// writeFrame sends one frame. A failed write closes the session.
func (s *Session) writeFrame(kind byte, id uint32, payload []byte) error {
	// The peer's read limit is headerLen+maxFramePayload, so a larger frame
	// would only tear the link down; refuse it before allocating.
	if len(payload) > maxFramePayload {
		return fmt.Errorf("%w: frame payload %d bytes exceeds %d", errProtocol, len(payload), maxFramePayload)
	}
	buf := make([]byte, headerLen+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:headerLen], id)
	copy(buf[headerLen:], payload)
	select {
	case <-s.done:
		return ErrLinkClosed
	default:
	}
	s.writeMu.Lock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(frameWriteTimeout))
	err := s.conn.WriteMessage(websocket.BinaryMessage, buf)
	s.writeMu.Unlock()
	if err != nil {
		s.closeWith(fmt.Errorf("%w: write: %w", ErrLinkClosed, err))
		return ErrLinkClosed
	}
	return nil
}

func (s *Session) readLoop() {
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			s.closeWith(fmt.Errorf("%w: %w", ErrLinkClosed, err))
			return
		}
		s.extendReadDeadline()
		if kind != websocket.BinaryMessage || len(data) < headerLen {
			s.closeWith(fmt.Errorf("%w: malformed frame", errProtocol))
			return
		}
		if err = s.dispatch(data[0], binary.BigEndian.Uint32(data[1:headerLen]), data[headerLen:]); err != nil {
			s.closeWith(err)
			return
		}
	}
}

// ownID reports whether id is in the range this side allocates.
func (s *Session) ownID(id uint32) bool {
	return (id%2 == 1) == s.dialer
}

func (s *Session) dispatch(kind byte, id uint32, payload []byte) error {
	if id == 0 {
		return fmt.Errorf("%w: stream 0", errProtocol)
	}
	if kind == frameOpen {
		return s.handleOpen(id)
	}
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	if st == nil {
		// A stream this side already closed or reset: late frames are
		// expected and dropped.
		return nil
	}
	switch kind {
	case frameData:
		if !st.receive(payload) {
			// The peer overran the window it was granted.
			s.forget(id)
			_ = s.writeFrame(frameReset, id, nil)
			st.terminate(ErrStreamReset)
		}
	case frameClose:
		st.receiveClose()
	case frameReset:
		s.forget(id)
		st.terminate(ErrStreamReset)
	case frameWindow:
		if len(payload) != windowPayloadLen {
			return fmt.Errorf("%w: window frame of %d bytes", errProtocol, len(payload))
		}
		st.addCredit(int(binary.BigEndian.Uint32(payload)))
	default:
		return fmt.Errorf("%w: frame type %d", errProtocol, kind)
	}
	return nil
}

func (s *Session) handleOpen(id uint32) error {
	if s.ownID(id) {
		return fmt.Errorf("%w: peer opened stream %d from this side's range", errProtocol, id)
	}
	s.mu.Lock()
	if s.err != nil {
		// The session is closing; readLoop stops after this frame.
		s.mu.Unlock()
		return nil //nolint:nilerr // not an error of this frame
	}
	if _, exists := s.streams[id]; exists {
		s.mu.Unlock()
		return fmt.Errorf("%w: stream %d opened twice", errProtocol, id)
	}
	if len(s.streams) >= maxStreams {
		s.mu.Unlock()
		return s.writeFrame(frameReset, id, nil)
	}
	st := newStream(s, id)
	s.streams[id] = st
	s.mu.Unlock()
	select {
	case s.accept <- st:
	default:
		s.forget(id)
		return s.writeFrame(frameReset, id, nil)
	}
	return nil
}

func (s *Session) forget(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

// Open opens a new stream to the peer.
func (s *Session) Open(ctx context.Context) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return nil, ErrLinkClosed
	}
	if len(s.streams) >= maxStreams {
		s.mu.Unlock()
		return nil, errTooManyStreams
	}
	id := s.nextID
	s.nextID += 2
	st := newStream(s, id)
	s.streams[id] = st
	s.mu.Unlock()
	if err := s.writeFrame(frameOpen, id, nil); err != nil {
		s.forget(id)
		return nil, err
	}
	return st, nil
}

// Accept waits for the next stream the peer opens.
func (s *Session) Accept() (*Stream, error) {
	select {
	case st := <-s.accept:
		return st, nil
	case <-s.done:
		return nil, ErrLinkClosed
	}
}

// Listener returns the session as a net.Listener of the streams the peer
// opens, for an http.Server. Closing it closes the session.
func (s *Session) Listener() net.Listener { return sessionListener{s} }

type sessionListener struct{ s *Session }

func (l sessionListener) Accept() (net.Conn, error) {
	st, err := l.s.Accept()
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (l sessionListener) Close() error   { return l.s.Close() }
func (l sessionListener) Addr() net.Addr { return linkAddr("link") }

// linkAddr is the net.Addr of a session and its streams.
type linkAddr string

func (linkAddr) Network() string  { return "cloudlink" }
func (a linkAddr) String() string { return string(a) }

// Stream is one bidirectional byte stream on a link. It implements net.Conn.
type Stream struct {
	s  *Session
	id uint32

	mu         sync.Mutex
	buf        []byte
	consumed   int
	credit     int
	remoteFin  bool
	finSent    bool
	closed     bool
	err        error // set when the stream was reset or the link closed
	readReady  chan struct{}
	writeReady chan struct{}

	readDeadline  deadline
	writeDeadline deadline
}

func newStream(s *Session, id uint32) *Stream {
	return &Stream{
		s:             s,
		id:            id,
		credit:        Window,
		readReady:     make(chan struct{}, 1),
		writeReady:    make(chan struct{}, 1),
		readDeadline:  makeDeadline(),
		writeDeadline: makeDeadline(),
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// receive appends peer data; false when it overruns the window.
func (st *Stream) receive(p []byte) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed || st.err != nil {
		return true
	}
	if len(st.buf)+len(p) > Window {
		return false
	}
	st.buf = append(st.buf, p...)
	signal(st.readReady)
	return true
}

func (st *Stream) receiveClose() {
	st.mu.Lock()
	st.remoteFin = true
	done := st.finSent || st.closed
	st.mu.Unlock()
	signal(st.readReady)
	if done {
		st.s.forget(st.id)
	}
}

func (st *Stream) addCredit(n int) {
	st.mu.Lock()
	st.credit += n
	st.mu.Unlock()
	signal(st.writeReady)
}

func (st *Stream) terminate(err error) {
	st.mu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.mu.Unlock()
	signal(st.readReady)
	signal(st.writeReady)
}

// Read reads stream bytes; io.EOF after the peer closed its side.
func (st *Stream) Read(p []byte) (int, error) {
	for {
		st.mu.Lock()
		if len(st.buf) > 0 {
			n := copy(p, st.buf)
			st.buf = st.buf[n:]
			if len(st.buf) == 0 {
				st.buf = nil
			}
			st.consumed += n
			grant := 0
			if st.consumed >= Window/2 && !st.remoteFin {
				grant, st.consumed = st.consumed, 0
			}
			st.mu.Unlock()
			if grant > 0 {
				var payload [4]byte
				binary.BigEndian.PutUint32(payload[:], uint32(grant)) //nolint:gosec // grant <= Window
				_ = st.s.writeFrame(frameWindow, st.id, payload[:])
			}
			return n, nil
		}
		switch {
		case st.remoteFin:
			st.mu.Unlock()
			return 0, io.EOF
		case st.closed:
			st.mu.Unlock()
			return 0, net.ErrClosed
		case st.err != nil:
			err := st.err
			st.mu.Unlock()
			return 0, err
		}
		st.mu.Unlock()
		select {
		case <-st.readReady:
		case <-st.readDeadline.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
}

// Write writes stream bytes, waiting for window credit as needed.
func (st *Stream) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		st.mu.Lock()
		switch {
		case st.closed, st.finSent:
			st.mu.Unlock()
			return total, net.ErrClosed
		case st.err != nil:
			err := st.err
			st.mu.Unlock()
			return total, err
		}
		select {
		case <-st.writeDeadline.wait():
			st.mu.Unlock()
			return total, os.ErrDeadlineExceeded
		default:
		}
		if st.credit == 0 {
			st.mu.Unlock()
			select {
			case <-st.writeReady:
			case <-st.writeDeadline.wait():
				return total, os.ErrDeadlineExceeded
			}
			continue
		}
		n := min(len(p), st.credit, maxFramePayload)
		st.credit -= n
		st.mu.Unlock()
		if err := st.s.writeFrame(frameData, st.id, p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

// CloseWrite tells the peer this side writes no more; reading continues.
func (st *Stream) CloseWrite() error {
	st.mu.Lock()
	if st.finSent || st.closed || st.err != nil {
		// Already closed, or aborted: there is nothing left to close.
		st.mu.Unlock()
		return nil //nolint:nilerr // CloseWrite on a dead stream is a no-op
	}
	st.finSent = true
	done := st.remoteFin
	st.mu.Unlock()
	err := st.s.writeFrame(frameClose, st.id, nil)
	if done {
		st.s.forget(st.id)
	}
	return err
}

// Close closes both directions. The peer reads the bytes already sent and
// then io.EOF; a peer still writing gets ErrStreamReset.
func (st *Stream) Close() error {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return nil
	}
	st.closed = true
	sendFin := !st.finSent && st.err == nil
	sendReset := !st.remoteFin && st.err == nil
	st.finSent = true
	st.buf = nil
	st.mu.Unlock()
	st.s.forget(st.id)
	signal(st.readReady)
	signal(st.writeReady)
	if sendFin {
		_ = st.s.writeFrame(frameClose, st.id, nil)
	}
	if sendReset {
		_ = st.s.writeFrame(frameReset, st.id, nil)
	}
	return nil
}

// LocalAddr implements net.Conn.
func (st *Stream) LocalAddr() net.Addr { return linkAddr("link") }

// RemoteAddr implements net.Conn; it names the stream.
func (st *Stream) RemoteAddr() net.Addr { return linkAddr(fmt.Sprintf("link/%d", st.id)) }

// SetDeadline implements net.Conn.
func (st *Stream) SetDeadline(t time.Time) error {
	st.readDeadline.set(t)
	st.writeDeadline.set(t)
	return nil
}

// SetReadDeadline implements net.Conn.
func (st *Stream) SetReadDeadline(t time.Time) error {
	st.readDeadline.set(t)
	return nil
}

// SetWriteDeadline implements net.Conn.
func (st *Stream) SetWriteDeadline(t time.Time) error {
	st.writeDeadline.set(t)
	return nil
}

// deadline is a resettable deadline whose wait channel closes when it passes
// (the scheme net.Pipe uses).
type deadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{}
}

func makeDeadline() deadline { return deadline{cancel: make(chan struct{})} }

func (d *deadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // the timer fired; wait for it to close cancel
	}
	d.timer = nil
	closed := isClosed(d.cancel)
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		cancel := d.cancel
		d.timer = time.AfterFunc(dur, func() { close(cancel) })
		return
	}
	if !closed {
		close(d.cancel)
	}
}

func (d *deadline) wait() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}

func isClosed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
