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

package reset

import (
	"context"
	"log/slog"

	"github.com/eminwux/kukeon/internal/netpolicy"
)

// RemoveSystemSpaceEgress exposes the --purge-system egress sweep so tests
// can drive it with a recording enforcer instead of host iptables.
func RemoveSystemSpaceEgress(
	ctx context.Context,
	logger *slog.Logger,
	enforcer netpolicy.Enforcer,
	systemDir string,
) {
	removeSystemSpaceEgress(ctx, logger, enforcer, systemDir)
}
