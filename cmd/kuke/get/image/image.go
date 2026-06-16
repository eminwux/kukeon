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

// Package image hosts the `kuke get image` subcommand: cross-realm-by-default
// image listing under the `kuke get <kind>` family. The previous
// `kuke image get` (#211) was retired here (#824) so images line up with the
// other realm-scoped kinds (cells, stacks, …) under the `get` verb.
//
// Image methods are in-process by design (#226) — the kukeond RPC does not
// serve them — so this leaf wires a `*local.Client` directly the same way
// `cmd/kuke/image` does. The persistent `--no-daemon` on the `get` parent is
// therefore a no-op for this leaf: every invocation runs in-process.
package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	getshared "github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey injects a fake Client via context for tests, mirroring
// the pattern used by `cmd/kuke/image`.
type MockControllerKey struct{}

// Client is the narrow surface this leaf uses. It is satisfied by
// `*local.Client` and by per-test fakes. ListRealms is in the set because
// the cross-realm default fans out across the realm controller's view —
// images are realm-scoped artefacts and the daemon does not serve them, so
// the fanout happens here in-process.
type Client interface {
	io.Closer

	ListRealms(ctx context.Context) ([]v1beta1.RealmDoc, error)
	ListImages(ctx context.Context, realm string) (kukeonv1.ListImagesResult, error)
	GetImage(ctx context.Context, realm, ref string) (kukeonv1.GetImageResult, error)
}

// NewImageCmd builds the `kuke get image` subcommand. With no positional and
// no `--realm`, it lists images across every realm. With `--realm <r>` it
// narrows to one. With a positional `<ref>` it renders that one image as a
// single table row by default (the full document via `-o yaml` / `-o json`).
func NewImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "image [ref]",
		Aliases:       []string{"images", "img"},
		Short:         "Get or list images in a realm's containerd namespace (cross-realm by default)",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runImageCmd,
	}

	cmd.Flags().String("realm", "", "Filter images by realm name; omit to list across every realm")
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func runImageCmd(cmd *cobra.Command, args []string) error {
	wide, format, err := resolveOutput(cmd)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(cmd.Flag("realm").Value.String())
	client := resolveClient(cmd)
	defer func() { _ = client.Close() }()

	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		ref := strings.TrimSpace(args[0])
		if realm == "" {
			realm = consts.KukeonDefaultRealmName
		}
		res, getErr := client.GetImage(cmd.Context(), realm, ref)
		if getErr != nil {
			if errors.Is(getErr, errdefs.ErrImageNotFound) {
				return fmt.Errorf("image %q not found in realm %q: %w", ref, realm, errdefs.ErrImageNotFound)
			}
			return getErr
		}
		if format == getshared.OutputFormatYAML || format == getshared.OutputFormatJSON {
			return printImage(cmd, res.Image, format)
		}
		// table / wide: render the single found image as a one-row table with
		// the same columns as the list view (kubectl parity).
		return printImages(
			cmd,
			[]kukeonv1.ListImagesResult{{Realm: realm, Images: []kukeonv1.ImageInfo{res.Image}}},
			format,
			wide,
		)
	}

	if realm != "" {
		res, listErr := client.ListImages(cmd.Context(), realm)
		if listErr != nil {
			return listErr
		}
		return printImages(cmd, []kukeonv1.ListImagesResult{res}, format, wide)
	}

	realms, listErr := client.ListRealms(cmd.Context())
	if listErr != nil {
		return listErr
	}
	results := make([]kukeonv1.ListImagesResult, 0, len(realms))
	for _, r := range realms {
		name := strings.TrimSpace(r.Metadata.Name)
		if name == "" {
			continue
		}
		res, perRealmErr := client.ListImages(cmd.Context(), name)
		if perRealmErr != nil {
			return fmt.Errorf("realm %q: %w", name, perRealmErr)
		}
		results = append(results, res)
	}
	return printImages(cmd, results, format, wide)
}

// resolveOutput sits between the cobra flag and `ParseOutputFormat` so the
// new `wide` value is normalised to a `table` format plus a bool, leaving
// the shared yaml/json/table parser untouched.
func resolveOutput(cmd *cobra.Command) (bool, getshared.OutputFormat, error) {
	raw := strings.TrimSpace(cmd.Flag("output").Value.String())
	if strings.EqualFold(raw, "wide") {
		_ = cmd.Flags().Set("output", "table")
		fmt, err := getshared.ParseOutputFormat(cmd)
		return true, fmt, err
	}
	fmt, err := getshared.ParseOutputFormat(cmd)
	return false, fmt, err
}

func resolveClient(cmd *cobra.Command) Client {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(Client); ok {
		return mockClient
	}
	logger, err := kukshared.LoggerFromCmd(cmd)
	if err != nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return local.New(cmd.Context(), logger, controller.Options{
		RunPath:          viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket: viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
	})
}

func printImage(cmd *cobra.Command, img kukeonv1.ImageInfo, format getshared.OutputFormat) error {
	switch format {
	case getshared.OutputFormatJSON:
		return getshared.PrintJSON(cmd, img)
	case getshared.OutputFormatYAML, getshared.OutputFormatTable:
		return getshared.PrintYAML(cmd, img)
	default:
		return getshared.PrintYAML(cmd, img)
	}
}

func printImages(
	cmd *cobra.Command,
	results []kukeonv1.ListImagesResult,
	format getshared.OutputFormat,
	wide bool,
) error {
	switch format {
	case getshared.OutputFormatYAML:
		return getshared.PrintYAML(cmd, results)
	case getshared.OutputFormatJSON:
		return getshared.PrintJSON(cmd, results)
	case getshared.OutputFormatTable:
		total := 0
		for _, r := range results {
			total += len(r.Images)
		}
		if total == 0 {
			cmd.Println("No images found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SIZE", "AGE"}
		if wide {
			headers = append(headers, "CREATED", "DIGEST")
		}
		rows := make([][]string, 0, total)
		now := time.Now()
		for _, r := range results {
			for _, img := range r.Images {
				row := []string{img.Name, r.Realm, formatSize(img.Size), formatAge(img.CreatedAt, now)}
				if wide {
					row = append(row, formatCreated(img.CreatedAt), img.Digest)
				}
				rows = append(rows, row)
			}
		}
		getshared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return getshared.PrintYAML(cmd, results)
	}
}

// formatSize renders a size in human-friendly bytes; -1 surfaces as "-" so
// the operator sees a missing value rather than a misleading "0 B".
func formatSize(size int64) string {
	if size < 0 {
		return "-"
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

// formatCreated renders the image's creation time as RFC3339 in UTC. A zero
// time surfaces as "-" because containerd does not always populate it for
// images imported from `docker save` tarballs.
func formatCreated(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// formatAge renders the elapsed time since the image was created using a
// kubectl-style coarse unit (s/m/h/d). A zero CreatedAt collapses to "-".
func formatAge(t, now time.Time) string {
	const hoursPerDay = 24
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/hoursPerDay))
	}
}
