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

package image

import (
	"errors"
	"fmt"
	"strings"

	getshared "github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

// NewGetCmd builds the `kuke image get` subcommand. With no positional, it
// lists every image in the realm; with a positional ref, it describes that
// one image. The output format follows the rest of `kuke get …`: yaml/json
// for single resources, table/yaml/json for lists.
func NewGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "get [ref]",
		Aliases:       []string{"ls", "list"},
		Short:         "List or describe images in a realm's containerd namespace",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			realm, err := cmd.Flags().GetString("realm")
			if err != nil {
				return err
			}
			realm = strings.TrimSpace(realm)
			if realm == "" {
				return errdefs.ErrRealmNameRequired
			}

			outputFormat, err := getshared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				ref := strings.TrimSpace(args[0])
				getRes, getErr := client.GetImage(cmd.Context(), realm, ref)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrImageNotFound) {
						return fmt.Errorf("image %q not found in realm %q: %w", ref, realm, errdefs.ErrImageNotFound)
					}
					return getErr
				}
				return printImage(getRes.Image, outputFormat)
			}

			listRes, listErr := client.ListImages(cmd.Context(), realm)
			if listErr != nil {
				return listErr
			}
			return printImages(cmd, realm, listRes, outputFormat)
		},
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Target realm; the lookup runs in <realm>.kukeon.io")
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")

	return cmd
}

func printImage(img kukeonv1.ImageInfo, format getshared.OutputFormat) error {
	switch format {
	case getshared.OutputFormatJSON:
		return getshared.PrintJSON(img)
	case getshared.OutputFormatYAML, getshared.OutputFormatTable:
		return getshared.PrintYAML(img)
	default:
		return getshared.PrintYAML(img)
	}
}

func printImages(
	cmd *cobra.Command,
	realm string,
	result kukeonv1.ListImagesResult,
	format getshared.OutputFormat,
) error {
	switch format {
	case getshared.OutputFormatYAML:
		return getshared.PrintYAML(result)
	case getshared.OutputFormatJSON:
		return getshared.PrintJSON(result)
	case getshared.OutputFormatTable:
		if len(result.Images) == 0 {
			cmd.Printf("No images found in realm %q.\n", realm)
			return nil
		}
		headers := []string{"NAME", "SIZE", "CREATED"}
		rows := make([][]string, 0, len(result.Images))
		for _, img := range result.Images {
			rows = append(rows, []string{img.Name, formatSize(img.Size), formatCreated(img)})
		}
		getshared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return getshared.PrintYAML(result)
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
func formatCreated(img kukeonv1.ImageInfo) string {
	if img.CreatedAt.IsZero() {
		return "-"
	}
	return img.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
}
