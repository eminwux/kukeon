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

// Package build hosts the `kuke build` CLI shim. It is a thin wrapper that
// locates the standalone `kukebuild` binary on PATH and exec's it with the
// parsed flags. `kukebuild` embeds BuildKit as a library (issue #522); keeping
// it a separate binary — exec'd here rather than imported — is what keeps
// BuildKit's transitive moby / runc / grpc closure out of the `kuke` and
// `kukeond` import sets (the same rationale as kuketty). This package
// therefore imports none of BuildKit.
package build

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
)

// kukebuildBinary is the name `kuke build` looks up on PATH.
const kukebuildBinary = "kukebuild"

// lookPath / execHandoff are indirected as package vars so tests can drive the
// PATH-lookup and exec-handoff paths without a real kukebuild on disk. In
// production lookPath is exec.LookPath and execHandoff is syscall.Exec, which
// replaces the kuke process image with kukebuild so the operator sees
// kukebuild's progress + exit code directly (a true exec, per the issue).
//
//nolint:gochecknoglobals // test seams for the production PATH lookup + exec
var (
	lookPath    = exec.LookPath
	execHandoff = syscall.Exec
)

// NewBuildCmd builds the `kuke build` subcommand.
func NewBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "build [-f Dockerfile] [-t name:tag] [--realm <name>] [--build-arg K=V]... " +
			"[--secret id=NAME,src=PATH]... [--cache-to type=local,dest=PATH]... " +
			"[--cache-from type=local,src=PATH]... [--platform os/arch,...] [--push] <context>",
		Short: "Build an OCI image from a Dockerfile into a realm's containerd namespace",
		Long: "Build an OCI image from a Dockerfile using kukeon's native builder.\n\n" +
			"`kuke build` is a thin shim: it locates the `kukebuild` binary on PATH and\n" +
			"exec's it. `kukebuild` embeds BuildKit, builds the context with the Dockerfile\n" +
			"frontend, and writes the resulting image into the containerd namespace mapped\n" +
			"to --realm (<realm>.kukeon.io), ready for `kuke image get` and `kuke create`.\n\n" +
			"No docker or standalone buildkitd is required — only a running host containerd.\n\n" +
			"Build-time secrets: --secret id=NAME,src=PATH mounts a host file into the build\n" +
			"at /run/secrets/NAME, consumed by a Dockerfile `RUN --mount=type=secret,id=NAME`.\n" +
			"File-based secrets only; the flag is repeatable.\n\n" +
			"Build cache: --cache-to type=local,dest=PATH exports the build cache to a local\n" +
			"directory and --cache-from type=local,src=PATH imports it back, reusing layers\n" +
			"across builds. Only type=local is supported; both flags are repeatable.\n\n" +
			"Multi-platform: --platform linux/amd64,linux/arm64 builds for each target arch\n" +
			"and writes the result as a single manifest list — one image reference covering\n" +
			"every arch, not per-arch tags (operators expecting name:tag-amd64 / -arm64 will\n" +
			"not find them). `kuke image get --realm <name>` shows the list; each per-arch\n" +
			"manifest is individually pullable. Distinct from a Dockerfile's $BUILDPLATFORM\n" +
			"arg, which selects the build host's platform; --platform selects the output set.\n\n" +
			"Registry push: --push pushes the built image to its tag's registry after a\n" +
			"successful build. The tag must be a fully qualified registry reference\n" +
			"(REGISTRY/REPO:TAG); a bare name:tag is rejected. Push is additive — the image\n" +
			"is still written to the realm's containerd namespace. Credentials resolve in\n" +
			"order: (1) $DOCKER_CONFIG/config.json when DOCKER_CONFIG is set, (2)\n" +
			"~/.docker/config.json, (3) the KUKEON_REGISTRY_AUTH env var (base64 user:pass).",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runBuild,
	}

	cmd.Flags().StringP("file", "f", "", "Path to the Dockerfile (default <context>/Dockerfile)")
	cmd.Flags().StringP("tag", "t", "", "Image reference for the built image, name:tag (required)")
	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the image lands in <realm>.kukeon.io")
	cmd.Flags().StringArray("build-arg", nil, "Set a build-time variable KEY=VALUE (repeatable)")
	cmd.Flags().String(
		"kukeond-config",
		"",
		"Path to kukeond.yaml; resolves the realm namespace suffix (default /etc/kukeon/kukeond.yaml)",
	)
	cmd.Flags().StringArray(
		"secret",
		nil,
		"Expose a file secret to the build at /run/secrets/NAME: id=NAME,src=PATH (repeatable)",
	)
	cmd.Flags().StringArray("cache-to", nil, "Export build cache: type=local,dest=PATH (repeatable)")
	cmd.Flags().StringArray("cache-from", nil, "Import build cache: type=local,src=PATH (repeatable)")
	cmd.Flags().String(
		"platform",
		"",
		"Comma-separated os/arch[/variant] targets (e.g. linux/amd64,linux/arm64); multiple targets "+
			"produce a single manifest list, not per-arch tags",
	)
	cmd.Flags().Bool(
		"push",
		false,
		"After a successful build, push the image to its tag's registry (requires a fully qualified "+
			"REGISTRY/REPO:TAG); credentials resolve from $DOCKER_CONFIG/config.json, then ~/.docker/config.json, "+
			"then $KUKEON_REGISTRY_AUTH",
	)

	return cmd
}

// runBuild resolves kukebuild on PATH and exec's it with the forwarded flags.
// A missing kukebuild surfaces as a clear "install kukebuild" error and a
// non-zero exit; a found kukebuild replaces this process via execHandoff so
// its progress and exit code reach the operator unmediated.
func runBuild(cmd *cobra.Command, args []string) error {
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return err
	}
	if strings.TrimSpace(tag) == "" {
		return errdefs.ErrImageTagRequired
	}

	binPath, err := lookPath(kukebuildBinary)
	if err != nil {
		return fmt.Errorf(
			"%w: `kuke build` shells out to `%s`, which was not found on PATH — "+
				"install it (e.g. `make kukebuild` then put it on PATH)",
			errdefs.ErrKukebuildNotFound, kukebuildBinary,
		)
	}

	argv, err := buildArgv(cmd, args)
	if err != nil {
		return err
	}

	// True exec hand-off: replace the kuke process with kukebuild so the
	// operator sees kukebuild's own stdout/stderr and exit code. execHandoff
	// only returns on failure (e.g. ENOEXEC); on success it never returns.
	return execHandoff(binPath, argv, os.Environ())
}

// buildArgv assembles the kukebuild argv from the parsed `kuke build` flags
// and the positional context directory. argv[0] is the binary name by exec
// convention. Flags are forwarded in kukebuild's long-flag form.
func buildArgv(cmd *cobra.Command, args []string) ([]string, error) {
	tag, err := cmd.Flags().GetString("tag")
	if err != nil {
		return nil, err
	}
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, err
	}
	realm, err := cmd.Flags().GetString("realm")
	if err != nil {
		return nil, err
	}
	buildArgs, err := cmd.Flags().GetStringArray("build-arg")
	if err != nil {
		return nil, err
	}
	kukeondConfig, err := cmd.Flags().GetString("kukeond-config")
	if err != nil {
		return nil, err
	}
	secrets, err := cmd.Flags().GetStringArray("secret")
	if err != nil {
		return nil, err
	}
	cacheTo, err := cmd.Flags().GetStringArray("cache-to")
	if err != nil {
		return nil, err
	}
	cacheFrom, err := cmd.Flags().GetStringArray("cache-from")
	if err != nil {
		return nil, err
	}
	platform, err := cmd.Flags().GetString("platform")
	if err != nil {
		return nil, err
	}
	push, err := cmd.Flags().GetBool("push")
	if err != nil {
		return nil, err
	}

	argv := []string{kukebuildBinary, "--tag", tag, "--realm", realm}
	if strings.TrimSpace(file) != "" {
		argv = append(argv, "--file", file)
	}
	if strings.TrimSpace(kukeondConfig) != "" {
		argv = append(argv, "--kukeond-config", kukeondConfig)
	}
	for _, ba := range buildArgs {
		argv = append(argv, "--build-arg", ba)
	}
	for _, s := range secrets {
		argv = append(argv, "--secret", s)
	}
	for _, c := range cacheTo {
		argv = append(argv, "--cache-to", c)
	}
	for _, c := range cacheFrom {
		argv = append(argv, "--cache-from", c)
	}
	if strings.TrimSpace(platform) != "" {
		argv = append(argv, "--platform", platform)
	}
	if push {
		argv = append(argv, "--push")
	}
	// Context directory is positional and validated by cobra.ExactArgs(1).
	argv = append(argv, args[0])
	return argv, nil
}
