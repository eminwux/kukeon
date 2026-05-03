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

package shared

import (
	"errors"

	"github.com/eminwux/sbsh/pkg/attach"
)

// AttachExit classifies how an in-process sbsh attach loop ended.
// `kuke attach` and `kuke run -a` both drive the same loop and need to
// branch on the same three outcomes.
type AttachExit int

const (
	// AttachExitDetached signals a clean ^]^] (or peer-issued Detach
	// RPC). The remote terminal is still alive and `kuke run -a --rm`
	// must NOT kill the cell — the operator may want to re-attach.
	AttachExitDetached AttachExit = iota

	// AttachExitPeerClosed signals the remote terminal dropped the
	// connection (workload exited, peer hung up). From the user's
	// perspective the session ended cleanly (exit 0), but `kuke run
	// -a --rm` should fire KillCell so a long-lived root (e.g.
	// `sleep infinity`) does not pin the cell.
	AttachExitPeerClosed

	// AttachExitError signals an unrecoverable controller error
	// (control socket lost, RPC failure, context cancel, …). The
	// caller surfaces the error to the user and `kuke run -a --rm`
	// fires KillCell so a half-detached session does not leak the
	// cell.
	AttachExitError
)

// ClassifyAttachExit maps the error returned by
// `github.com/eminwux/sbsh/pkg/attach`.Run to an AttachExit. nil and
// detach map to AttachExitDetached / AttachExitPeerClosed respectively;
// any other non-nil error is AttachExitError.
//
// sbsh v0.10.1 made attach.ErrDetached and attach.ErrPeerClosed public
// (sbsh#192) so embedders can branch on errors.Is rather than the
// pre-v0.10.1 substring match on "close requested: read/write routines
// exited".
func ClassifyAttachExit(err error) AttachExit {
	switch {
	case err == nil:
		// pkg/attach.Run already collapses context-canceled to nil; we
		// treat a nil return as "operator ended the session cleanly",
		// which for `kuke run -a --rm` means "leave the cell alive" —
		// same semantics as ErrDetached.
		return AttachExitDetached
	case errors.Is(err, attach.ErrDetached):
		return AttachExitDetached
	case errors.Is(err, attach.ErrPeerClosed):
		return AttachExitPeerClosed
	default:
		return AttachExitError
	}
}

// IsCleanAttachExit reports whether err describes a benign session end
// — either a clean detach or a peer-side close. Both map to exit 0
// from the user's perspective (no error is surfaced to the shell);
// callers that need to differentiate detach-vs-peer-close (e.g. to
// decide whether to keep a managed cell alive) should use
// ClassifyAttachExit instead.
func IsCleanAttachExit(err error) bool {
	switch ClassifyAttachExit(err) {
	case AttachExitDetached, AttachExitPeerClosed:
		return true
	case AttachExitError:
		return false
	default:
		return false
	}
}
