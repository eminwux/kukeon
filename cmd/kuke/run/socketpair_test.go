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

package run_test

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// socketPair returns both ends of a connected unix stream socketpair, the
// shape of connection ConnectContainerTerminal hands back.
func socketPair() (*net.UnixConn, *net.UnixConn) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		panic(fmt.Sprintf("socketpair: %v", err))
	}
	wrap := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		defer func() { _ = f.Close() }()
		c, connErr := net.FileConn(f)
		if connErr != nil {
			panic(fmt.Sprintf("FileConn: %v", connErr))
		}
		return c.(*net.UnixConn)
	}
	return wrap(fds[0]), wrap(fds[1])
}

// Close releases the connections the fake handed out.
func (f *fakeClient) Close() error {
	for _, c := range f.conns {
		_ = c.Close()
	}
	return nil
}
