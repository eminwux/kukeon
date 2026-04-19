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

package kukeonv1_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// TestRoundTripPreservesSentinelAndMessage verifies that a server-side error
// wrapped with an errdefs sentinel survives a ToAPIError/FromAPIError round
// trip with (a) the sentinel preserved for errors.Is and (b) the message
// verbatim — no double prefix.
func TestRoundTripPreservesSentinelAndMessage(t *testing.T) {
	cause := errors.New("connection refused")
	serverErr := fmt.Errorf("%w: %w", errdefs.ErrCreateCell, cause)

	apiErr := kukeonv1.ToAPIError(serverErr)
	if apiErr == nil {
		t.Fatal("ToAPIError returned nil for non-nil input")
	}
	if apiErr.Kind != "CreateCell" {
		t.Errorf("Kind = %q, want %q", apiErr.Kind, "CreateCell")
	}
	if apiErr.Message != serverErr.Error() {
		t.Errorf("Message = %q, want %q", apiErr.Message, serverErr.Error())
	}

	clientErr := kukeonv1.FromAPIError(apiErr)

	if !errors.Is(clientErr, errdefs.ErrCreateCell) {
		t.Errorf("errors.Is(clientErr, ErrCreateCell) = false, want true")
	}

	// Crucial: no duplicate "failed to create cell: " prefix.
	got := clientErr.Error()
	if got != serverErr.Error() {
		t.Errorf("clientErr.Error() = %q, want verbatim %q", got, serverErr.Error())
	}
}

func TestFromAPIErrorUnknownKind(t *testing.T) {
	apiErr := &kukeonv1.APIError{Kind: "NotAKnownKind", Message: "whoops"}
	clientErr := kukeonv1.FromAPIError(apiErr)
	if clientErr == nil {
		t.Fatal("expected non-nil error")
	}
	if clientErr.Error() != "whoops" {
		t.Errorf("Error() = %q, want %q", clientErr.Error(), "whoops")
	}
	if errors.Is(clientErr, errdefs.ErrCreateCell) {
		t.Error("unknown kind should not unwrap to any sentinel")
	}
}

func TestNilRoundTrip(t *testing.T) {
	if kukeonv1.ToAPIError(nil) != nil {
		t.Error("ToAPIError(nil) should be nil")
	}
	if kukeonv1.FromAPIError(nil) != nil {
		t.Error("FromAPIError(nil) should be nil")
	}
}
