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
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedScheme is returned by Dial when the address scheme cannot be
// served by any compiled-in transport.
var ErrUnsupportedScheme = errors.New("unsupported kukeon host scheme")

// Dial parses addr and returns a connected Client.
//
// Supported schemes:
//   - unix:///absolute/path/to/socket
//
// Planned schemes:
//   - ssh://user@host[:port]  (tunnels to the remote unix socket)
func Dial(_ context.Context, addr string) (Client, error) {
	switch {
	case strings.HasPrefix(addr, "unix://"):
		path := strings.TrimPrefix(addr, "unix://")
		if path == "" {
			return nil, fmt.Errorf("%w: unix path is empty", ErrUnsupportedScheme)
		}
		return NewUnixClient(path), nil
	case strings.HasPrefix(addr, "ssh://"):
		return nil, fmt.Errorf("%w: ssh transport not yet implemented", ErrUnsupportedScheme)
	default:
		return nil, fmt.Errorf("%w: %q (expected unix:// or ssh://)", ErrUnsupportedScheme, addr)
	}
}
