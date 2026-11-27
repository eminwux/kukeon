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

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"
)

const (
	icmpEchoReply   = 0
	icmpEcho        = 8
	icmpHeaderLen   = 8
	probeAttempts   = 3
	probeTimeout    = time.Second
	probeBufferSize = 1500
	uint16Mask      = 0xffff
	checksumShift   = 16
	byteBits        = 8
)

// errProbeUnavailable means the gateway could not be probed at all (no raw
// ICMP socket), as opposed to probed without an answer.
var errProbeUnavailable = errors.New("ICMP probe unavailable")

// probeGateway sends up to probeAttempts ICMP echo requests to gw and
// reports whether one was answered. It needs a raw ICMP socket (root or
// CAP_NET_RAW); without one it returns errProbeUnavailable. A send that the
// kernel rejects (no route) counts as unanswered.
func probeGateway(gw netip.Addr) (bool, error) {
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "ip4:icmp", "0.0.0.0")
	if err != nil {
		return false, fmt.Errorf("%w: %w", errProbeUnavailable, err)
	}
	defer conn.Close()
	id := os.Getpid() & uint16Mask
	dst := &net.IPAddr{IP: gw.AsSlice()}
	buf := make([]byte, probeBufferSize)
	for seq := 1; seq <= probeAttempts; seq++ {
		if _, err = conn.WriteTo(echoRequest(id, seq), dst); err != nil {
			// An unroutable gateway is an unanswered probe, not a probe failure.
			return false, nil
		}
		deadline := time.Now().Add(probeTimeout)
		if err = conn.SetReadDeadline(deadline); err != nil {
			return false, err
		}
		for {
			n, from, readErr := conn.ReadFrom(buf)
			if readErr != nil {
				break // Timeout: try the next sequence number.
			}
			src, ok := from.(*net.IPAddr)
			if ok && src.IP.Equal(dst.IP) && isEchoReply(buf[:n], id, seq) {
				return true, nil
			}
		}
	}
	return false, nil
}

// echoRequest builds an ICMP echo request with a valid checksum.
func echoRequest(id, seq int) []byte {
	msg := make([]byte, icmpHeaderLen, icmpHeaderLen+len("kukeon-netguard"))
	msg[0] = icmpEcho
	binary.BigEndian.PutUint16(msg[4:], uint16(id&uint16Mask))  //nolint:gosec // masked to 16 bits
	binary.BigEndian.PutUint16(msg[6:], uint16(seq&uint16Mask)) //nolint:gosec // masked to 16 bits
	msg = append(msg, "kukeon-netguard"...)
	binary.BigEndian.PutUint16(msg[2:], checksum(msg))
	return msg
}

// isEchoReply reports whether msg (an ICMP message, IPv4 header already
// stripped by the raw socket) answers the request with id and seq.
func isEchoReply(msg []byte, id, seq int) bool {
	return len(msg) >= icmpHeaderLen && msg[0] == icmpEchoReply && msg[1] == 0 &&
		int(binary.BigEndian.Uint16(msg[4:])) == id&uint16Mask &&
		int(binary.BigEndian.Uint16(msg[6:])) == seq&uint16Mask
}

// checksum is the RFC 1071 Internet checksum.
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << byteBits
	}
	for sum>>checksumShift != 0 {
		sum = sum&uint16Mask + sum>>checksumShift
	}
	return ^uint16(sum) //nolint:gosec // folded to 16 bits above
}
