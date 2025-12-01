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

package ctr_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	socket := "/test/socket"

	client := ctr.NewClient(ctx, logger, socket)
	if client == nil {
		t.Fatal("NewClient() should not return nil")
	}

	// Test that client implements Client interface
	if _, ok := client.(ctr.Client); !ok {
		t.Error("NewClient() should return a Client interface")
	}

	// Test default namespace
	if client.Namespace() == "" {
		t.Error("Client should have a default namespace")
	}
}

func TestClientConnect(_ *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := ctr.NewClient(ctx, logger, "/test/socket")

	// Connect will fail without real containerd, but shouldn't panic
	// We test that the method exists and handles errors gracefully
	err := client.Connect()
	// Error is expected without real containerd socket, but method should not panic
	_ = err
}

func TestClientClose(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := ctr.NewClient(ctx, logger, "/test/socket")

	// Close should not panic even if not connected
	err := client.Close()
	if err != nil {
		// Error is acceptable if client wasn't connected
		t.Logf("Close() returned error (expected if not connected): %v", err)
	}
}

func TestClientConnectionState(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := ctr.NewClient(ctx, logger, "/test/socket")

	// Test initial state - not connected
	// Close should not error when not connected (idempotent)
	err := client.Close()
	if err != nil {
		// Close may return error if client wasn't connected, which is acceptable
		t.Logf("Close() returned error (expected if not connected): %v", err)
	}

	// Test that multiple Connect() calls are handled (should reuse connection)
	// This will fail without real containerd, but should not panic
	err1 := client.Connect()
	_ = err1 // Error expected without containerd

	// Second Connect() call should be handled (may reuse or create new)
	err2 := client.Connect()
	_ = err2 // Error expected without containerd

	// Close after potential connection attempts
	err = client.Close()
	_ = err // Error acceptable

	// Multiple Close() calls should be idempotent
	err = client.Close()
	if err != nil {
		// Close may return error, but should not panic
		t.Logf("Close() after second call returned error: %v", err)
	}
}

// Note: Full testing of Connect() and Close() requires:
// 1. Real containerd instance, OR
// 2. Mocking containerd.New() function
//
// The tests above verify basic structure and that methods don't panic.
// Integration tests should verify the full connection lifecycle.
