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

// Package update implements `kuke update` (issue #1638): it moves the host to
// the newest build of its channel. The channel is read from the installed
// version string and never stored: vX.Y.Z is stable, vX.Y.Z-N-g<sha> is edge,
// anything else (or binaries that are symlinks, a `make install-dev` layout)
// is a development build that is never overwritten without --channel.
//
// The sequence is: resolve the target, download and verify kuke, kukebuild
// and kukepause, swap them in with renames, recreate the kukeond cell from the
// target's immutable image with the new binary, check that client and daemon
// report the same version, remove the previous kukeond image, then re-pin the
// kuke team with `kuke team init --upgrade`. A failed recreate or version
// check restores the previous binaries and daemon image.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultInstallPrefix = "/usr/local/bin"
	defaultDownloadBase  = "https://github.com"
	defaultAPIBase       = "https://api.github.com"
	defaultRepo          = "eminwux/kukeon"
	edgeReleaseTag       = "edge"

	metadataTimeout = 30 * time.Second
	assetTimeout    = 10 * time.Minute
	maxMetadataSize = 1 << 20
)

// binaries are the release assets kuke update replaces. kukeond is a hard
// link to kuke, not an asset.
//
//nolint:gochecknoglobals // fixed list
var binaries = []string{"kuke", "kukebuild", "kukepause"}

var (
	// ErrDevelopmentBuild is returned when the installed build has no
	// channel and none was given.
	ErrDevelopmentBuild = errors.New("development build")
	// ErrDowngradeNotConfirmed is returned when a downgrade to an older
	// --version is not confirmed.
	ErrDowngradeNotConfirmed = errors.New("downgrade not confirmed")
	// ErrUpdateFailed is returned when the update was rolled back.
	ErrUpdateFailed = errors.New("update failed")
)

// ImageClient is the in-process view of the kuke-system realm kuke update
// needs: the image the kukeond container runs, and image removal.
type ImageClient interface {
	DaemonImage(ctx context.Context) (string, error)
	DeleteImage(ctx context.Context, realm, ref string) error
	PruneImages(ctx context.Context, realm string) error
	Close() error
}

// Exec runs a child process with its stdout and stderr sent to out and
// errOut.
type Exec func(ctx context.Context, out, errOut io.Writer, path string, args ...string) error

// Deps are kuke update's side effects. Tests inject them through MockDepsKey;
// production fills every nil field with the real implementation.
type Deps struct {
	// Version is the installed version (default config.Version).
	Version string
	// ImageRepo is the kukeond image repository (default
	// config.KukeondImageRepo).
	ImageRepo string
	// DownloadBase and APIBase are the GitHub web and API roots.
	DownloadBase, APIBase string
	HTTPClient            *http.Client
	Exec                  Exec
	// Images opens the in-process image client.
	Images func(cmd *cobra.Command) (ImageClient, error)
	// DaemonVersion reports the running daemon's version, or "" when it is
	// unreachable.
	DaemonVersion func(cmd *cobra.Command) string
	// LookPath finds kuke on PATH (default exec.LookPath).
	LookPath func(string) (string, error)
	// Arch is the GOARCH of the assets (default runtime.GOARCH).
	Arch string
}

// MockDepsKey injects Deps through the command context.
type MockDepsKey struct{}

type options struct {
	channel    config.Channel
	version    string
	yes        bool
	skipTeam   bool
	prefix     string
	channelSet bool
}

// NewUpdateCmd builds the `kuke update` cobra command.
func NewUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update kuke and kukeond to the newest build of the host's channel",
		Long: "Move the host to the newest build of its channel: download and verify kuke, kukebuild " +
			"and kukepause, swap them in, recreate the kukeond daemon from the matching image, check " +
			"that client and daemon report the same version, remove the previous kukeond image, and " +
			"re-pin the kuke team with `kuke team init --upgrade`.\n\n" +
			"The channel is read from the installed version: vX.Y.Z is stable, vX.Y.Z-N-g<sha> is " +
			"edge. A development build (any other version, or binaries that are symlinks into a " +
			"checkout) is refused unless --channel is given. Cells in other realms and " +
			"/opt/kukeon/data/default are not touched. When the daemon recreate or the version " +
			"check fails, the previous binaries and daemon image are restored.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runUpdate,
	}
	cmd.Flags().String("channel", "", "Use this channel (edge or stable) instead of the inferred one")
	cmd.Flags().String("version", "",
		"Install this release tag (vX.Y.Z); edge builds cannot be pinned. Conflicts with --channel edge")
	cmd.Flags().BoolP("yes", "y", false, "Skip the downgrade confirmation")
	cmd.Flags().Bool("skip-team", false, "Do not run `kuke team init --upgrade` at the end")
	cmd.Flags().String("install-prefix", "",
		"Directory holding the kuke binaries (default: the directory of kuke on PATH, else "+
			defaultInstallPrefix+")")
	return cmd
}

func parseOptions(cmd *cobra.Command) (options, error) {
	var o options
	channel, _ := cmd.Flags().GetString("channel")
	o.version, _ = cmd.Flags().GetString("version")
	o.yes, _ = cmd.Flags().GetBool("yes")
	o.skipTeam, _ = cmd.Flags().GetBool("skip-team")
	o.prefix, _ = cmd.Flags().GetString("install-prefix")
	o.version = strings.TrimSpace(o.version)

	switch config.Channel(channel) {
	case config.ChannelEdge, config.ChannelStable:
		o.channel = config.Channel(channel)
		o.channelSet = true
	case config.ChannelNone:
	default:
		return o, fmt.Errorf("--channel %q: want edge or stable", channel)
	}
	if o.version != "" {
		if config.ParseVersion(o.version).Channel != config.ChannelStable {
			return o, fmt.Errorf("--version %q: want a release tag (vX.Y.Z); edge builds cannot be pinned, "+
				"use --channel edge for the newest one", o.version)
		}
		if o.channel == config.ChannelEdge {
			return o, errors.New("--version conflicts with --channel edge: --version installs a release tag")
		}
		o.channel = config.ChannelStable
	}
	return o, nil
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	o, err := parseOptions(cmd)
	if err != nil {
		return err
	}
	if err = kukshared.RequireRoot("kuke update"); err != nil {
		return err
	}
	d := resolveDeps(cmd)
	u := &updater{cmd: cmd, deps: d, opts: o, out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr()}
	return u.run(cmd.Context())
}

func resolveDeps(cmd *cobra.Command) Deps {
	var d Deps
	if injected, ok := cmd.Context().Value(MockDepsKey{}).(Deps); ok {
		d = injected
	}
	if d.Version == "" {
		d.Version = strings.TrimSpace(config.Version)
	}
	if d.ImageRepo == "" {
		d.ImageRepo = strings.TrimSpace(config.KukeondImageRepo)
		if d.ImageRepo == "" {
			d.ImageRepo = "ghcr.io/" + defaultRepo
		}
	}
	if d.DownloadBase == "" {
		d.DownloadBase = defaultDownloadBase
	}
	if d.APIBase == "" {
		d.APIBase = defaultAPIBase
	}
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{}
	}
	if d.Exec == nil {
		d.Exec = execChild
	}
	if d.Images == nil {
		d.Images = localImages
	}
	if d.DaemonVersion == nil {
		d.DaemonVersion = daemonVersion
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Arch == "" {
		d.Arch = runtime.GOARCH
	}
	return d
}

type updater struct {
	cmd    *cobra.Command
	deps   Deps
	opts   options
	out    io.Writer
	errOut io.Writer

	prefix  string
	repo    string
	current config.BuildVersion
	channel config.Channel
	target  string
}

func (u *updater) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(u.out, format, args...)
}

func (u *updater) warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(u.errOut, "warning: "+format, args...)
}

func (u *updater) run(ctx context.Context) error {
	u.prefix = u.installPrefix()
	u.repo = strings.TrimPrefix(u.deps.ImageRepo, "ghcr.io/")
	u.current = config.ParseVersion(u.deps.Version)

	// 1. The host's channel.
	devBuild := u.current.Channel == config.ChannelNone
	if isSymlink(filepath.Join(u.prefix, "kuke")) {
		devBuild = true
	}
	switch {
	case u.opts.channelSet || u.opts.version != "":
		u.channel = u.opts.channel
	case devBuild:
		detail := u.deps.Version
		if isSymlink(filepath.Join(u.prefix, "kuke")) {
			detail += ", " + filepath.Join(u.prefix, "kuke") + " is a symlink"
		}
		return fmt.Errorf("%w: this is a development build (%s); pass --channel edge or --channel stable",
			ErrDevelopmentBuild, detail)
	default:
		u.channel = u.current.Channel
	}

	// 2. The target.
	target, err := u.resolveTarget(ctx)
	if err != nil {
		return err
	}
	u.target = target
	targetVersion := config.ParseVersion(target)

	// 3. Decide.
	daemon := u.deps.DaemonVersion(u.cmd)
	if target == u.deps.Version && !devBuild {
		if daemon == target {
			u.printf("already up to date (%s)\n", target)
			return nil
		}
		u.printf("kuke is at %s but the daemon reports %q; finishing the update\n", target, daemonLabel(daemon))
		return u.finishDaemon(ctx)
	}
	if !devBuild {
		if stop, decideErr := u.decide(targetVersion); stop || decideErr != nil {
			return decideErr
		}
	}
	return u.install(ctx)
}

func daemonLabel(v string) string {
	if v == "" {
		return "unreachable"
	}
	return v
}

// decide applies the no-silent-downgrade rules. stop reports that nothing
// should change.
func (u *updater) decide(target config.BuildVersion) (bool, error) {
	if target.Compare(u.current) >= 0 {
		return false, nil
	}
	if u.opts.version != "" {
		u.warnf("%s is older than the installed %s; this downgrades kuke and the kukeond daemon\n",
			u.target, u.deps.Version)
		if u.opts.yes {
			return false, nil
		}
		ok, err := confirm(u.cmd.InOrStdin(), u.out)
		if err != nil {
			return true, err
		}
		if !ok {
			return true, fmt.Errorf("%w: nothing changed; pass --yes to downgrade to %s",
				ErrDowngradeNotConfirmed, u.target)
		}
		return false, nil
	}
	if u.channel == config.ChannelStable {
		u.printf("%s is ahead of the latest release %s; kuke update --channel stable will move this host "+
			"at the next release. Nothing changed.\n", u.deps.Version, u.target)
		if u.current.Channel == config.ChannelEdge {
			u.printf("This is not remembered: until then, a plain kuke update keeps following edge.\n")
		}
		return true, nil
	}
	// Edge target older than the installed build (a local build of a newer
	// commit): there is no rollback to an older edge build.
	u.printf("%s is ahead of the newest edge build %s. Nothing changed.\n", u.deps.Version, u.target)
	return true, nil
}

func confirm(in io.Reader, out io.Writer) (bool, error) {
	br := bufio.NewReader(in)
	for {
		_, _ = fmt.Fprint(out, "Proceed with the downgrade? (yes/no) ")
		line, err := br.ReadString('\n')
		exhausted := errors.Is(err, io.EOF)
		if err != nil && !exhausted {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "yes", "y":
			return true, nil
		case "no", "n", "":
			return false, nil
		}
		// An unrecognized answer asks again, unless the input is exhausted
		// (a non-interactive run), which counts as no.
		if exhausted {
			return false, nil
		}
	}
}

func (u *updater) installPrefix() string {
	if u.opts.prefix != "" {
		return u.opts.prefix
	}
	if p, err := u.deps.LookPath("kuke"); err == nil {
		dir := filepath.Dir(p)
		if abs, absErr := filepath.Abs(dir); absErr == nil {
			return abs
		}
		return dir
	}
	return defaultInstallPrefix
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func (u *updater) releaseTag() string {
	if u.channel == config.ChannelEdge {
		return edgeReleaseTag
	}
	return u.target
}

func (u *updater) resolveTarget(ctx context.Context) (string, error) {
	if u.opts.version != "" {
		return u.opts.version, nil
	}
	if u.channel == config.ChannelEdge {
		url := fmt.Sprintf("%s/%s/releases/download/%s/VERSION", u.deps.DownloadBase, u.repo, edgeReleaseTag)
		body, err := u.get(ctx, url, metadataTimeout)
		if err != nil {
			return "", fmt.Errorf("read the newest edge build: %w (no edge build published yet? "+
				"kuke update --channel stable installs the latest release)", err)
		}
		v := strings.TrimSpace(string(body))
		if config.ParseVersion(v).Channel != config.ChannelEdge {
			return "", fmt.Errorf("edge VERSION at %s is malformed: %q", url, v)
		}
		return v, nil
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", u.deps.APIBase, u.repo)
	body, err := u.get(ctx, url, metadataTimeout)
	if err != nil {
		return "", fmt.Errorf("read the latest release: %w", err)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err = json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("decode %s: %w", url, err)
	}
	if config.ParseVersion(release.TagName).Channel != config.ChannelStable {
		return "", fmt.Errorf("latest release tag %q at %s is not vX.Y.Z", release.TagName, url)
	}
	return release.TagName, nil
}

func (u *updater) get(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxMetadataSize))
}

func (u *updater) download(ctx context.Context, url, dest string) error {
	ctx, cancel := context.WithTimeout(ctx, assetTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // binaries are executable
	if err != nil {
		return err
	}
	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return f.Close()
}

// fetch downloads name and its .sha256 into dir and verifies the digest.
func (u *updater) fetch(ctx context.Context, dir, name string) (string, error) {
	asset := fmt.Sprintf("%s-linux-%s", name, u.deps.Arch)
	base := fmt.Sprintf("%s/%s/releases/download/%s/%s", u.deps.DownloadBase, u.repo, u.releaseTag(), asset)
	dest := filepath.Join(dir, name+".new")
	u.printf("downloading %s\n", base)
	if err := u.download(ctx, base, dest); err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	sum, err := u.get(ctx, base+".sha256", metadataTimeout)
	if err != nil {
		return "", fmt.Errorf("download %s.sha256: %w", asset, err)
	}
	fields := strings.Fields(string(sum))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s.sha256 is empty", asset)
	}
	f, err := os.Open(dest)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, fields[0]) {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset, fields[0], got)
	}
	if err = os.Chmod(dest, 0o755); err != nil { //nolint:gosec // binaries are executable
		return "", err
	}
	return dest, nil
}

func (u *updater) targetImage() string {
	return u.deps.ImageRepo + ":" + config.ParseVersion(u.target).ImageTag(u.target)
}

// swap is one replaced file: the new content was renamed over dest; backup
// holds the previous file ("" when dest did not exist).
type swap struct {
	dest, backup string
}

func (u *updater) install(ctx context.Context) error {
	// 4. Download and verify into a directory on the prefix's filesystem.
	tmp, err := os.MkdirTemp(u.prefix, ".kuke-update-")
	if err != nil {
		return fmt.Errorf("create a temporary directory in %s: %w", u.prefix, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	downloaded := map[string]string{}
	for _, name := range binaries {
		path, fetchErr := u.fetch(ctx, tmp, name)
		if fetchErr != nil {
			return fmt.Errorf("%w; nothing changed", fetchErr)
		}
		downloaded[name] = path
	}
	var vout strings.Builder
	if err = u.deps.Exec(ctx, &vout, io.Discard, downloaded["kuke"], "version", "--no-daemon"); err != nil {
		return fmt.Errorf("run the downloaded kuke: %w; nothing changed", err)
	}
	if got := firstLine(vout.String()); got != "Client: "+u.target {
		if u.channel == config.ChannelEdge {
			return fmt.Errorf("the edge build changed during the download (got %q, want %q); "+
				"run kuke update again; nothing changed", got, "Client: "+u.target)
		}
		return fmt.Errorf("the downloaded kuke reports %q, want %q; nothing changed", got, "Client: "+u.target)
	}

	// 5. Record the daemon image, then swap.
	previousImage, err := u.daemonImage(ctx)
	if err != nil {
		return fmt.Errorf("read the kukeond cell: %w; nothing changed", err)
	}
	swaps, err := u.swapBinaries(tmp, downloaded)
	if err != nil {
		if restoreErr := restore(swaps); restoreErr != nil {
			return errors.Join(fmt.Errorf("swap binaries: %w", err), fmt.Errorf("restore: %w", restoreErr))
		}
		return fmt.Errorf("swap binaries: %w; previous binaries restored", err)
	}

	// 6-7. Recreate with the new binary and check the versions.
	if failure := u.recreateAndCheck(ctx); failure != nil {
		return u.rollback(ctx, swaps, previousImage, failure)
	}

	// 9. Remove the previous image.
	u.removePrevious(ctx, previousImage)
	u.printf("updated %s -> %s (%s)\n", u.deps.Version, u.target, u.channel)

	// 10. Team.
	u.teamStep(ctx)
	return nil
}

// finishDaemon completes a half-done update: the binaries are already the
// target, the daemon is not.
func (u *updater) finishDaemon(ctx context.Context) error {
	previousImage, err := u.daemonImage(ctx)
	if err != nil {
		return fmt.Errorf("read the kukeond cell: %w", err)
	}
	if failure := u.recreateAndCheck(ctx); failure != nil {
		return fmt.Errorf("%w: %w; run sudo kuke daemon recreate --kukeond-image %s",
			ErrUpdateFailed, failure, u.targetImage())
	}
	u.removePrevious(ctx, previousImage)
	u.printf("updated the daemon to %s (%s)\n", u.target, u.channel)
	u.teamStep(ctx)
	return nil
}

func (u *updater) daemonImage(ctx context.Context) (string, error) {
	client, err := u.deps.Images(u.cmd)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	return client.DaemonImage(ctx)
}

func (u *updater) swapBinaries(tmp string, downloaded map[string]string) ([]swap, error) {
	var swaps []swap
	replace := func(src, name string) error {
		dest := filepath.Join(u.prefix, name)
		s := swap{dest: dest}
		if _, err := os.Lstat(dest); err == nil {
			s.backup = filepath.Join(tmp, name+".prev")
			if err = os.Link(dest, s.backup); err != nil {
				return fmt.Errorf("back up %s: %w", dest, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(src, dest); err != nil {
			if s.backup != "" {
				_ = os.Remove(s.backup)
			}
			return fmt.Errorf("replace %s: %w", dest, err)
		}
		swaps = append(swaps, s)
		return nil
	}
	for _, name := range binaries {
		if err := replace(downloaded[name], name); err != nil {
			return swaps, err
		}
	}
	// kukeond is a hard link to the new kuke.
	link := filepath.Join(tmp, "kukeond.new")
	if err := os.Link(filepath.Join(u.prefix, "kuke"), link); err != nil {
		return swaps, fmt.Errorf("link kukeond: %w", err)
	}
	if err := replace(link, "kukeond"); err != nil {
		return swaps, err
	}
	return swaps, nil
}

func restore(swaps []swap) error {
	var errs []error
	for i := len(swaps) - 1; i >= 0; i-- {
		s := swaps[i]
		if s.backup == "" {
			if err := os.Remove(s.dest); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if err := os.Rename(s.backup, s.dest); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", s.dest, err))
		}
	}
	return errors.Join(errs...)
}

func (u *updater) recreateAndCheck(ctx context.Context) error {
	kuke := filepath.Join(u.prefix, "kuke")
	if err := u.deps.Exec(ctx, u.out, u.errOut, kuke,
		"daemon", "recreate", "--kukeond-image", u.targetImage()); err != nil {
		return fmt.Errorf("kuke daemon recreate --kukeond-image %s: %w", u.targetImage(), err)
	}
	var vout strings.Builder
	err := u.deps.Exec(ctx, &vout, u.errOut, kuke, "version", "--strict")
	u.printf("%s", vout.String())
	if err != nil {
		return fmt.Errorf("kuke version --strict: %w", err)
	}
	if !hasLine(vout.String(), "Daemon: "+u.target) {
		return fmt.Errorf("kuke version --strict did not report Daemon: %s", u.target)
	}
	return nil
}

func (u *updater) rollback(ctx context.Context, swaps []swap, previousImage string, failure error) error {
	u.printf("update to %s failed: %v\n", u.target, failure)
	if err := restore(swaps); err != nil {
		return fmt.Errorf("%w: %w; restoring the previous binaries also failed: %w; "+
			"reinstall %s and run sudo kuke daemon recreate --kukeond-image %s",
			ErrUpdateFailed, failure, err, u.deps.Version, previousImage)
	}
	kuke := filepath.Join(u.prefix, "kuke")
	if err := u.deps.Exec(ctx, u.out, u.errOut, kuke,
		"daemon", "recreate", "--kukeond-image", previousImage); err != nil {
		return fmt.Errorf("%w: %w; the previous binaries are restored but recreating the previous daemon "+
			"also failed: %w; run sudo kuke daemon recreate --kukeond-image %s",
			ErrUpdateFailed, failure, err, previousImage)
	}
	return fmt.Errorf("%w: %w; the host is back on %s (daemon image %s)",
		ErrUpdateFailed, failure, u.deps.Version, previousImage)
}

func (u *updater) removePrevious(ctx context.Context, previousImage string) {
	if previousImage == "" || previousImage == u.targetImage() {
		return
	}
	client, err := u.deps.Images(u.cmd)
	if err != nil {
		u.warnf("previous kukeond image %s not removed: %v\n", previousImage, err)
		return
	}
	defer func() { _ = client.Close() }()
	if err = client.DeleteImage(ctx, consts.KukeSystemRealmName, previousImage); err != nil {
		u.warnf("previous kukeond image %s not removed: %v\n", previousImage, err)
		return
	}
	if err = client.PruneImages(ctx, consts.KukeSystemRealmName); err != nil {
		u.warnf("prune realm %s: %v\n", consts.KukeSystemRealmName, err)
	}
	u.printf("removed the previous kukeond image %s\n", previousImage)
}

func (u *updater) teamStep(ctx context.Context) {
	if u.opts.skipTeam {
		return
	}
	kuke := filepath.Join(u.prefix, "kuke")
	var help strings.Builder
	if err := u.deps.Exec(ctx, &help, io.Discard, kuke, "team", "init", "--help"); err != nil ||
		!strings.Contains(help.String(), "--upgrade") {
		u.printf("kuke %s has no kuke team init --upgrade; the kuke team was not re-pinned\n", u.target)
		return
	}
	if err := u.deps.Exec(ctx, u.out, u.errOut, kuke, "team", "init", "--upgrade"); err != nil {
		u.warnf("kuke team was not upgraded: %v; run kuke team init --upgrade\n", err)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

func hasLine(s, want string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func execChild(ctx context.Context, out, errOut io.Writer, path string, args ...string) error {
	c := exec.CommandContext(ctx, path, args...)
	c.Stdout, c.Stderr = out, errOut
	return c.Run()
}

func daemonVersion(cmd *cobra.Command) string {
	client, err := kukshared.DaemonClientFromCmd(cmd)
	if err != nil {
		return ""
	}
	defer func() { _ = client.Close() }()
	v, err := client.PingVersion(cmd.Context())
	if err != nil {
		return ""
	}
	return v
}

type localImageClient struct {
	client *local.Client
}

func localImages(cmd *cobra.Command) (ImageClient, error) {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return nil, errdefs.ErrLoggerNotFound
	}
	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}
	return &localImageClient{client: local.New(cmd.Context(), logger, controller.Options{
		RunPath:          runPath,
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	})}, nil
}

func (l *localImageClient) DaemonImage(ctx context.Context) (string, error) {
	res, err := l.client.GetCell(ctx, v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: consts.KukeSystemCellName, Labels: map[string]string{}},
		Spec: v1beta1.CellSpec{
			ID:      consts.KukeSystemCellName,
			RealmID: consts.KukeSystemRealmName,
			SpaceID: consts.KukeSystemSpaceName,
			StackID: consts.KukeSystemStackName,
		},
	})
	if err != nil {
		return "", err
	}
	if !res.MetadataExists {
		return "", errdefs.ErrHostNotInitialized
	}
	for _, c := range res.Cell.Spec.Containers {
		if c.ID == consts.KukeSystemContainerName {
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("the kukeond cell has no %q container", consts.KukeSystemContainerName)
}

func (l *localImageClient) DeleteImage(ctx context.Context, realm, ref string) error {
	_, err := l.client.DeleteImage(ctx, realm, ref)
	return err
}

func (l *localImageClient) PruneImages(ctx context.Context, realm string) error {
	_, err := l.client.PruneImages(ctx, realm)
	return err
}

func (l *localImageClient) Close() error { return l.client.Close() }
