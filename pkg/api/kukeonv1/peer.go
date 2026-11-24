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

package kukeonv1

import "context"

type peerUIDKey struct{}

// WithPeerUID records the uid of the user a call acts for (issue #1643): the
// daemon service sets it from the socket's SO_PEERCRED before it calls the
// in-process client, which reads it with PeerUIDFrom.
func WithPeerUID(ctx context.Context, uid int) context.Context {
	return context.WithValue(ctx, peerUIDKey{}, uid)
}

// PeerUIDFrom returns the uid WithPeerUID recorded, and whether one was.
func PeerUIDFrom(ctx context.Context) (int, bool) {
	uid, ok := ctx.Value(peerUIDKey{}).(int)
	return uid, ok
}
