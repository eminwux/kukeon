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
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/cmd/kuke"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/spf13/cobra"
)

type rootFactory func() (*cobra.Command, error)

type factoryMap map[string]rootFactory

// mockFactoryMapKey is used to inject mock factory maps in tests via context.
type mockFactoryMapKey struct{}

func getFactories(ctx context.Context) factoryMap {
	if mockFactories, ok := ctx.Value(mockFactoryMapKey{}).(factoryMap); ok {
		return mockFactories
	}
	return factoryMap{
		"kuke": kuke.NewKukeCmd,
	}
}

func execRoot(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		return 1
	}
	return 0
}

func runWithFactory(ctx context.Context, factory rootFactory) int {
	root, err := factory()
	if err != nil {
		return 1
	}

	root.SetContext(ctx)
	return execRoot(root)
}

func main() {
	logger := logging.NewNoopLogger()
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	// Select which subtree to run based on the executable name
	exe := filepath.Base(os.Args[0])

	// Get factories (may be mocked via context in tests)
	factories := getFactories(ctx)

	if factory, ok := factories[exe]; ok {
		os.Exit(runWithFactory(ctx, factory))
	}

	// Fallback to kukeon if KUKEON_DEBUG_MODE is set
	// This is useful for development and debugging purposes
	// as it allows to run kukeon even if the executable is not named kukeon
	// or sb. For example, when running from an IDE or a debugger.
	// In this case, KUKEON_DEBUG_MODE can be set to "kukeon"
	// to run the corresponding subtree.
	// If KUKEON_DEBUG_MODE is not set, an error is printed and the program exits.
	// This avoids confusion and ensures that the user is aware of the correct usage.
	debug := os.Getenv("KUKEON_DEBUG_MODE")
	if factory, ok := factories[debug]; ok {
		os.Exit(runWithFactory(ctx, factory))
	}

	fmt.Fprintf(os.Stderr, "unknown entry command: %s\n", exe)
	os.Exit(1)
}
