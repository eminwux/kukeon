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

// Package cloud hosts `kuke cloud` (issue #1755): enroll this host with a
// cloud relay and show the state of its cloud link.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cloudhost"
	"github.com/eminwux/kukeon/pkg/cloudlink"
	"github.com/spf13/cobra"
)

// DefaultRelayURL is the relay `kuke cloud enroll` uses without --url.
const DefaultRelayURL = "https://cloud.kukeon.io"

// enrollTimeout bounds the enrollment exchange and the credential write.
const enrollTimeout = time.Minute

// statusTimeout bounds the credential read of `kuke cloud status`.
const statusTimeout = 10 * time.Second

// MockControllerKey injects a Client via context for tests.
type MockControllerKey struct{}

// MockStatusFileKey injects the link status file path for tests.
type MockStatusFileKey struct{}

// Client is the part of the daemon client `kuke cloud` uses.
type Client interface {
	io.Closer
	cloudhost.SecretReader
	cloudhost.SecretWriter
}

// NewCloudCmd builds `kuke cloud`.
func NewCloudCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud",
		Short: "Enroll this host with a cloud relay and show its cloud link",
		Long: "The cloud link is an outbound, authenticated connection from `kuke serve` to a relay,\n" +
			"through which the relay reaches this host's API with no inbound port.\n\n" +
			"  kuke cloud enroll <token> [--url <relay>]   exchange a one-time token for a host credential\n" +
			"  kuke cloud status                           show the enrollment and the link state\n" +
			"  kuke serve --cloud-link <url>               keep the link up while serving",
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}
	cmd.AddCommand(newEnrollCmd())
	cmd.AddCommand(newStatusCmd())
	return cmd
}

func resolveClient(cmd *cobra.Command) (Client, error) {
	if mock, ok := cmd.Context().Value(MockControllerKey{}).(Client); ok {
		return mock, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func statusFile(cmd *cobra.Command) string {
	if path, ok := cmd.Context().Value(MockStatusFileKey{}).(string); ok {
		return path
	}
	if !config.HomeDirKnown() {
		return ""
	}
	return filepath.Join(config.DefaultKukeDir(), cloudhost.StatusFileName)
}

func newEnrollCmd() *cobra.Command {
	var relayURL, caFile, hostname string
	cmd := &cobra.Command{
		Use:   "enroll <token>",
		Short: "Exchange a one-time enrollment token for this host's cloud link credential",
		Long: "Generates the link key on this host, sends its certificate request with the token to\n" +
			"the relay, and stores the issued credential as Secret " + cloudhost.SecretRef() + ".\n" +
			"The private key never leaves the host. Enrolling again replaces the credential.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			var serverCA string
			if caFile != "" {
				data, err := os.ReadFile(caFile)
				if err != nil {
					return fmt.Errorf("read --ca-file: %w", err)
				}
				serverCA = string(data)
			}
			if hostname == "" {
				hostname, _ = os.Hostname()
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx, cancel := context.WithTimeout(cmd.Context(), enrollTimeout)
			defer cancel()
			// Reach the daemon before spending the one-time token.
			previous, err := cloudhost.LoadCredential(ctx, client)
			if err != nil && !errors.Is(err, cloudlink.ErrNotEnrolled) {
				return err
			}
			cred, err := cloudlink.Enroll(ctx, cloudlink.EnrollOptions{
				RelayURL: relayURL, Token: args[0], Hostname: hostname, ServerCA: serverCA,
			})
			if err != nil {
				return err
			}
			if err = cloudhost.StoreCredential(ctx, client, cred); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if previous != nil {
				_, _ = fmt.Fprintf(
					out,
					"Replaced the enrollment of host %s (%s).\n",
					previous.HostID,
					previous.RelayURL,
				)
			}
			_, _ = fmt.Fprintf(out, "Enrolled with %s as host %s.\n", cred.RelayURL, cred.HostID)
			_, _ = fmt.Fprintf(out, "Credential stored in Secret %s.\n", cloudhost.SecretRef())
			_, _ = fmt.Fprintf(out, "Start the link: kuke serve --cloud-link %s\n", cred.LinkURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&relayURL, "url", DefaultRelayURL, "The relay's https:// URL")
	cmd.Flags().StringVar(&caFile, "ca-file", "",
		"PEM CA bundle trusted for the relay's server certificate, in addition to the system roots; kept for the link")
	cmd.Flags().StringVar(&hostname, "hostname", "", "The host name reported to the relay (default: this host's name)")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this host's cloud enrollment and link state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx, cancel := context.WithTimeout(cmd.Context(), statusTimeout)
			defer cancel()
			out := cmd.OutOrStdout()
			cred, err := cloudhost.LoadCredential(ctx, client)
			if errors.Is(err, cloudlink.ErrNotEnrolled) && cred == nil {
				_, _ = fmt.Fprintln(out, "Enrollment:  not enrolled (run `kuke cloud enroll <token>`)")
				return printLink(out, statusFile(cmd), "")
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Enrollment:  host %s with %s\n", cred.HostID, cred.RelayURL)
			_, _ = fmt.Fprintf(out, "Enrolled:    %s\n", cred.EnrolledAt.Local().Format(time.RFC3339))
			if notAfter, certErr := cred.NotAfter(); certErr == nil {
				expiry := notAfter.Local().Format(time.RFC3339)
				if time.Now().After(notAfter) {
					expiry += " (expired; enroll again)"
				}
				_, _ = fmt.Fprintf(out, "Certificate: expires %s\n", expiry)
			}
			_, _ = fmt.Fprintf(out, "Link URL:    %s\n", cred.LinkURL)
			return printLink(out, statusFile(cmd), cred.LinkURL)
		},
	}
}

// printLink prints the state a running `kuke serve --cloud-link` reports.
func printLink(out io.Writer, path, linkURL string) error {
	if path == "" {
		_, err := fmt.Fprintln(out, "Link:        unknown (no home directory for the status file)")
		return err
	}
	status, err := cloudhost.ReadStatus(path)
	if errors.Is(err, cloudhost.ErrNoStatus) {
		hint := ""
		if linkURL != "" {
			hint = fmt.Sprintf(" (start it with `kuke serve --cloud-link %s`)", linkURL)
		}
		_, err = fmt.Fprintf(out, "Link:        not running%s\n", hint)
		return err
	}
	if err != nil {
		return err
	}
	now := time.Now()
	line := status.Status
	if age := cloudhost.Age(now, status.Since); age != "" {
		line += " for " + age
	}
	_, _ = fmt.Fprintf(out, "Link:        %s (kuke serve pid %d, %s)\n", line, status.PID, status.URL)
	if status.Error != "" {
		_, _ = fmt.Fprintf(out, "Last error:  %s\n", status.Error)
	}
	if !status.Retry.IsZero() && status.Retry.After(now) {
		_, _ = fmt.Fprintf(out, "Retry in:    %s\n", status.Retry.Sub(now).Round(time.Second))
	}
	return nil
}
