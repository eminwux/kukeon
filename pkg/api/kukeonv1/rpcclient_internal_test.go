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

package kukeonv1

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

const testSockPath = "/run/kukeon/kukeond.sock"

// TestWrapDialErrorBareEACCES verifies the EACCES branch adds the usermod
// remediation hint and preserves the underlying error for errors.Is.
func TestWrapDialErrorBareEACCES(t *testing.T) {
	got := wrapDialError(testSockPath, syscall.EACCES)
	if got == nil {
		t.Fatal("wrapDialError returned nil for non-nil input")
	}
	msg := got.Error()
	if !strings.Contains(msg, "usermod -aG kukeon $USER") {
		t.Errorf("missing usermod hint in error: %q", msg)
	}
	if !strings.Contains(msg, testSockPath) {
		t.Errorf("socket path not in error: %q", msg)
	}
	if !errors.Is(got, syscall.EACCES) {
		t.Error("errors.Is(got, syscall.EACCES) = false, want true")
	}
}

// TestWrapDialErrorEACCESBuriedInOpError verifies the hint still fires when
// EACCES is wrapped in the *net.OpError + *os.SyscallError shape that
// net.Dialer.DialContext actually returns.
func TestWrapDialErrorEACCESBuriedInOpError(t *testing.T) {
	dialErr := &net.OpError{
		Op:  "dial",
		Net: "unix",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES},
	}
	got := wrapDialError(testSockPath, dialErr)
	if !strings.Contains(got.Error(), "usermod -aG kukeon $USER") {
		t.Errorf("missing usermod hint when EACCES is wrapped: %q", got.Error())
	}
	if !errors.Is(got, syscall.EACCES) {
		t.Error("errors.Is(got, syscall.EACCES) = false through OpError, want true")
	}
}

// TestWrapDialErrorFsPermission verifies the hint also fires for the
// portable fs.ErrPermission sentinel.
func TestWrapDialErrorFsPermission(t *testing.T) {
	got := wrapDialError(testSockPath, fs.ErrPermission)
	if !strings.Contains(got.Error(), "usermod -aG kukeon $USER") {
		t.Errorf("missing usermod hint for fs.ErrPermission: %q", got.Error())
	}
}

// TestWrapDialErrorOtherErrorUnchanged verifies non-permission errors keep
// their existing wrap and do not receive the usermod hint.
func TestWrapDialErrorOtherErrorUnchanged(t *testing.T) {
	cases := []error{
		syscall.ECONNREFUSED, // daemon not running
		syscall.ENOENT,       // socket missing
		errors.New("i/o timeout"),
	}
	for _, c := range cases {
		got := wrapDialError(testSockPath, c)
		msg := got.Error()
		if strings.Contains(msg, "usermod") {
			t.Errorf("non-permission error gained usermod hint: input=%v, got=%q", c, msg)
		}
		if !strings.Contains(msg, "dial kukeond at "+testSockPath) {
			t.Errorf("standard wrap prefix missing: %q", msg)
		}
		if !errors.Is(got, c) {
			t.Errorf("underlying error not preserved for %v", c)
		}
	}
}
