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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
		Use: "build [-f Dockerfile] [-t name:tag | --realm-image <name>] [--realm <name>] [--build-arg K=V]... " +
			"[--secret id=NAME,src=PATH]... [--cache-to type=local,dest=PATH]... " +
			"[--cache-from type=local,src=PATH]... [--platform os/arch,...] [--push] [<context>]",
		Short: "Build an OCI image from a Dockerfile into a realm's containerd namespace",
		Long: "Build an OCI image from a Dockerfile using kukeon's native builder.\n\n" +
			"Realm images: --realm-image NAME builds the realm's spec.images entry NAME, taking the tag,\n" +
			"Dockerfile, build args and context from the entry. Cells rendered from a CellConfig that use the\n" +
			"image are marked OutOfSync when the rebuilt digest changes.\n\n" +
			"`kuke build` is a thin shim: it locates the `kukebuild` binary on PATH and\n" +
			"exec's it. `kukebuild` embeds BuildKit, builds the context with the Dockerfile\n" +
			"frontend, and writes the resulting image into the containerd namespace mapped\n" +
			"to --realm (<realm>.kukeon.io), ready for `kuke get image` and `kuke create`.\n\n" +
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
			"not find them). `kuke get image --realm <name>` shows the list; each per-arch\n" +
			"manifest is individually pullable. Distinct from a Dockerfile's $BUILDPLATFORM\n" +
			"arg, which selects the build host's platform; --platform selects the output set.\n\n" +
			"Registry push: --push pushes the built image to its tag's registry after a\n" +
			"successful build. The tag must be a fully qualified registry reference\n" +
			"(REGISTRY/REPO:TAG); a bare name:tag is rejected. Push is additive — the image\n" +
			"is still written to the realm's containerd namespace. Credentials resolve in\n" +
			"order: (1) $DOCKER_CONFIG/config.json when DOCKER_CONFIG is set, (2)\n" +
			"~/.docker/config.json, (3) the KUKEON_REGISTRY_AUTH env var (base64 user:pass).",
		Args:          cobra.MaximumNArgs(1),
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
	cmd.Flags().String(
		"realm-image",
		"",
		"Build the realm's spec.images entry NAME: the tag, Dockerfile, build args and (when no <context> is "+
			"given) the context come from the entry; the tag defaults to kukeon.internal/<realm>/<name>:latest. "+
			"Mutually exclusive with --tag.",
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
	args, err := applyRealmImage(cmd, args)
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return errors.New("`kuke build` requires a <context> directory (or --realm-image with a build.context)")
	}

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
	// Context directory is positional (validated by runBuild).
	argv = append(argv, args[0])
	return argv, nil
}

// lookupRealm reads a realm through the daemon. A package var so tests can
// drive --realm-image without a running kukeond.
//
//nolint:gochecknoglobals // test seam for the daemon realm lookup
var lookupRealm = func(cmd *cobra.Command, realm string) (kukeonv1.GetRealmResult, error) {
	client, err := kukeshared.DaemonClientFromCmd(cmd)
	if err != nil {
		return kukeonv1.GetRealmResult{}, err
	}
	defer func() { _ = client.Close() }()
	return client.GetRealm(cmd.Context(), v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: realm}})
}

// applyRealmImage resolves `--realm-image <name>` against the realm's
// spec.images (#1418) and fills the build flags from the entry: --tag (the
// entry's resolved reference), --file, --build-arg, and the context when the
// operator passed none. A relative build.dockerfile resolves against the
// build context; explicit --file / --build-arg flags win over the entry.
func applyRealmImage(cmd *cobra.Command, args []string) ([]string, error) {
	name, err := cmd.Flags().GetString("realm-image")
	if err != nil || strings.TrimSpace(name) == "" {
		return args, err
	}
	if cmd.Flags().Changed("tag") {
		return nil, errors.New("--realm-image and --tag are mutually exclusive; the entry's tag is used")
	}
	realm, err := cmd.Flags().GetString("realm")
	if err != nil {
		return nil, err
	}
	res, err := lookupRealm(cmd, realm)
	if err != nil {
		return nil, err
	}
	if !res.MetadataExists {
		return nil, fmt.Errorf("%w: %q", errdefs.ErrRealmNotFound, realm)
	}
	img, ok := cellconfig.FindRealmImage(res.Realm.Spec.Images, name)
	if !ok {
		return nil, fmt.Errorf("realm %q declares no image %q in spec.images", realm, name)
	}
	if img.Build == nil {
		return nil, fmt.Errorf("realm image %q is a reference (%s), not a Dockerfile build", name, img.Ref)
	}
	if err = cmd.Flags().Set("tag", cellconfig.RealmImageRef(realm, img)); err != nil {
		return nil, err
	}
	if len(args) == 0 && strings.TrimSpace(img.Build.Context) != "" {
		args = []string{img.Build.Context}
	}
	if df := strings.TrimSpace(img.Build.Dockerfile); df != "" && !cmd.Flags().Changed("file") {
		// A relative build.dockerfile names a file inside the build context,
		// not one relative to the caller's working directory (kukebuild
		// resolves --file against the cwd).
		if !filepath.IsAbs(df) && len(args) > 0 {
			df = filepath.Join(args[0], df)
		}
		if err = cmd.Flags().Set("file", df); err != nil {
			return nil, err
		}
	}
	if err = applyRealmImageBuildArgs(cmd, img.Build.Args); err != nil {
		return nil, err
	}
	return args, nil
}

// applyRealmImageBuildArgs adds the entry's build args in key order, skipping
// any key the operator already set with --build-arg so the explicit flag wins.
func applyRealmImageBuildArgs(cmd *cobra.Command, entryArgs map[string]string) error {
	explicit, err := cmd.Flags().GetStringArray("build-arg")
	if err != nil {
		return err
	}
	set := make(map[string]bool, len(explicit))
	for _, kv := range explicit {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	keys := make([]string, 0, len(entryArgs))
	for k := range entryArgs {
		if !set[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err = cmd.Flags().Set("build-arg", k+"="+entryArgs[k]); err != nil {
			return err
		}
	}
	return nil
}
