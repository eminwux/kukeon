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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/cmd/kuke"
	execcmd "github.com/eminwux/kukeon/cmd/kuke/exec"
	healthcmd "github.com/eminwux/kukeon/cmd/kuke/health"
	"github.com/eminwux/kukeon/cmd/kukeond"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/gitcred/gitagent"
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
		"kuke":    kuke.NewKukeCmd,
		"kukeond": kukeond.NewKukeondCmd,
	}
}

func execRoot(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		// kuke health --exit-code returns FindingsError to exit with its own
		// code, and kuke exec returns its ExitError to exit with the code of
		// the process it ran in the container. Only those kukeon-owned types
		// are honoured: a wrapped *exec.ExitError also has an ExitCode method,
		// and a child process's code must not leak out as kuke's. Everything
		// else is a plain failure.
		var findings healthcmd.FindingsError
		if errors.As(err, &findings) {
			return findings.ExitCode()
		}
		var execExit execcmd.ExitError
		if errors.As(err, &execExit) {
			return execExit.ExitCode()
		}
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
	// SSH_ASKPASS mode: ssh-add runs this binary to read the passphrase of a
	// stored git key kukeond staged for a fetch (issue #1643). Print the
	// staged passphrase file and exit before anything else.
	if file := os.Getenv(gitagent.HostAskpassFileEnv); file != "" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		os.Exit(gitagent.Askpass(file, prompt, os.Stdout))
	}
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
