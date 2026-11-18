// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package health implements `kuke health`: kukeond's health digest and the
// stored health reports (issue #1509).
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/health"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ExitFindings is the exit code of `kuke health --exit-code` when the digest
// holds findings. 0 is a clean digest and 1 is a command failure.
const ExitFindings = 2

// MockClientKey injects a kukeonv1.Client via context in tests.
type MockClientKey struct{}

// lastReport is the --since value that starts the window at the newest
// stored report's end.
const lastReport = "last-report"

const (
	formatText     = "text"
	formatJSON     = "json"
	formatYAML     = "yaml"
	formatMarkdown = "markdown"
)

// FindingsError is returned by `kuke health --exit-code` for a digest with
// findings. The digest is already printed; the error only carries the code.
type FindingsError struct{}

func (FindingsError) Error() string { return "health digest has findings" }

// ExitCode is read by the root command runner.
func (FindingsError) ExitCode() int { return ExitFindings }

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mock, ok := cmd.Context().Value(MockClientKey{}).(kukeonv1.Client); ok {
		return mock, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

// NewHealthCmd builds `kuke health` and its report subcommands.
func NewHealthCmd() *cobra.Command {
	var (
		since    string
		output   string
		exitCode bool
	)
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show kukeond's health digest and stored health reports",
		Long: "Show kukeond's health digest: host and state checks evaluated in the daemon,\n" +
			"error-level kukeond log entries grouped by signature, and cells in a Failed or\n" +
			"Error state. The digest is computed by kukeond, so it reports host facts whether\n" +
			"kuke runs on the host or in a cell that mounts the kukeond socket.\n\n" +
			"With --exit-code: 0 when the digest is clean, 2 when it has findings, 1 when the\n" +
			"command fails.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := parseSince(since, time.Now())
			if err != nil {
				return report(cmd, err)
			}
			if err = checkFormat(output, formatText, formatJSON, formatYAML); err != nil {
				return report(cmd, err)
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return report(cmd, err)
			}
			defer func() { _ = client.Close() }()
			digest, err := client.ReadHealthDigest(cmd.Context(), request)
			if err != nil {
				return report(cmd, err)
			}
			if err = printDigest(cmd.OutOrStdout(), digest, output); err != nil {
				return report(cmd, err)
			}
			if exitCode && !digest.Clean {
				return FindingsError{}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "",
		"Start of the window: a duration (90m), an RFC3339 time, or last-report (default 1h)")
	cmd.Flags().StringVarP(&output, "output", "o", formatText, "Output format: text, json, yaml")
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "Exit 2 when the digest has findings")
	cmd.AddCommand(newReportsCmd(), newReportCmd())
	return cmd
}

// report prints err once and returns it, since the commands silence cobra's
// own error printing to keep FindingsError quiet.
func report(cmd *cobra.Command, err error) error {
	fmt.Fprintln(cmd.ErrOrStderr(), "Error:", err)
	return err
}

func checkFormat(got string, allowed ...string) error {
	for _, format := range allowed {
		if got == format {
			return nil
		}
	}
	return fmt.Errorf("unsupported output format %q (use %s)", got, strings.Join(allowed, ", "))
}

// parseSince turns --since into a digest request.
func parseSince(value string, now time.Time) (kukeonv1.HealthDigestRequest, error) {
	switch {
	case value == "":
		return kukeonv1.HealthDigestRequest{}, nil
	case value == lastReport:
		return kukeonv1.HealthDigestRequest{SinceLastReport: true}, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return kukeonv1.HealthDigestRequest{}, fmt.Errorf("--since %q: a duration must be positive", value)
		}
		return kukeonv1.HealthDigestRequest{Since: now.Add(-d).UTC()}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return kukeonv1.HealthDigestRequest{}, fmt.Errorf(
			"--since %q: use a duration (90m), an RFC3339 time, or %s", value, lastReport)
	}
	if at.After(now) {
		return kukeonv1.HealthDigestRequest{}, fmt.Errorf("--since %q is in the future", value)
	}
	return kukeonv1.HealthDigestRequest{Since: at.UTC()}, nil
}

func printStructured(w io.Writer, value any, format string) error {
	if format == formatJSON {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	raw, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func printDigest(w io.Writer, digest kukeonv1.HealthDigest, format string) error {
	if format != formatText {
		return printStructured(w, digest, format)
	}
	verdict := "clean"
	if !digest.Clean {
		verdict = "findings"
	}
	fmt.Fprintf(w, "Health digest %s to %s: %s\n\n", stamp(digest.Since), stamp(digest.Until), verdict)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tSTATUS\tDETAIL")
	for _, check := range digest.Checks {
		fmt.Fprintf(tw, "%s/%s\t%s\t%s\n", check.Section, check.Name, strings.ToUpper(check.Status), check.Detail)
	}
	_ = tw.Flush()
	for _, check := range digest.Checks {
		if check.Status != kukeonv1.HealthStatusOK && check.Remediation != "" {
			fmt.Fprintf(w, "  %s/%s: %s\n", check.Section, check.Name, check.Remediation)
		}
	}

	fmt.Fprintln(w)
	switch {
	case digest.LogNote != "":
		fmt.Fprintln(w, digest.LogNote)
	case len(digest.LogErrors) == 0:
		fmt.Fprintln(w, "No error-level kukeond log entries in the window.")
	default:
		fmt.Fprintf(w, "Error-level kukeond log entries (%s):\n", digest.LogSource)
		for _, group := range digest.LogErrors {
			fmt.Fprintf(w, "  %dx %s\n     first %s, last %s\n     %s\n",
				group.Count, group.Signature, stamp(group.FirstSeen), stamp(group.LastSeen), group.Sample)
		}
	}

	fmt.Fprintln(w)
	if len(digest.Cells) == 0 {
		fmt.Fprintln(w, "No cells in Failed or Error state.")
		return nil
	}
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CELL\tSTATE\tCLASS\tSINCE\tREASON")
	for _, cell := range digest.Cells {
		fmt.Fprintf(tw, "%s/%s/%s/%s\t%s\t%s\t%s\t%s\n",
			cell.Realm, cell.Space, cell.Stack, cell.Name, cell.State, cell.Classification,
			stamp(cell.Since), strings.TrimSpace(cell.Reason+" "+cell.Message))
	}
	return tw.Flush()
}

func newReportsCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:           "reports",
		Short:         "List the health reports kukeond stores, newest first",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkFormat(output, formatText, formatJSON, formatYAML); err != nil {
				return report(cmd, err)
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return report(cmd, err)
			}
			defer func() { _ = client.Close() }()
			reports, err := client.ListHealthReports(cmd.Context())
			if err != nil {
				return report(cmd, err)
			}
			if reports == nil {
				reports = []kukeonv1.HealthReportSummary{}
			}
			w := cmd.OutOrStdout()
			if output != formatText {
				return printStructured(w, reports, output)
			}
			if len(reports) == 0 {
				fmt.Fprintln(w, "No health reports stored.")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tCREATED\tFINDINGS\tRECURRING\tFILED\tSUMMARY")
			for _, r := range reports {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n",
					r.ID, stamp(r.CreatedAt), r.Findings, r.Recurring, r.Filed, r.Summary)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", formatText, "Output format: text, json, yaml")
	return cmd
}

func newReportCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:           "report <id|latest>",
		Short:         "Show one stored health report, or submit one",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkFormat(output, formatMarkdown, formatJSON, formatYAML); err != nil {
				return report(cmd, err)
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return report(cmd, err)
			}
			defer func() { _ = client.Close() }()
			stored, err := client.ReadHealthReport(cmd.Context(), args[0])
			if err != nil {
				return report(cmd, err)
			}
			w := cmd.OutOrStdout()
			if output != formatMarkdown {
				return printStructured(w, stored, output)
			}
			_, err = io.WriteString(w, Markdown(stored))
			return err
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", formatMarkdown, "Output format: markdown, json, yaml")
	cmd.AddCommand(newSubmitCmd())
	return cmd
}

func newSubmitCmd() *cobra.Command {
	var file, fileIssues string
	cmd := &cobra.Command{
		Use:   "submit -f <file>",
		Short: "Validate and store a health report",
		Long: "Validate a health report document (JSON or YAML; - reads stdin) and store it in\n" +
			"kukeond. kukeond assigns id and createdAt when the document leaves them out, and\n" +
			"marks findings the previous report already carried as recurring. Storing a report\n" +
			"files nothing anywhere.\n\n" +
			"--file-issues <owner>/<repo> is the opt-in exception: once the document passes the\n" +
			"same validation kukeond applies, and before storing, kuke opens one\n" +
			"GitHub issue per kukeon finding (or comments on the open issue that already carries\n" +
			"its signature) and records the URL in the report. workload and unclear findings are\n" +
			"never filed. It needs a token in " + TokenEnv + ".",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return report(cmd, errors.New("-f <file> is required"))
			}
			var raw []byte
			var err error
			if file == "-" {
				raw, err = io.ReadAll(cmd.InOrStdin())
			} else {
				raw, err = os.ReadFile(file)
			}
			if err != nil {
				return report(cmd, fmt.Errorf("read report: %w", err))
			}
			document, err := DecodeReport(raw)
			if err != nil {
				return report(cmd, err)
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return report(cmd, err)
			}
			defer func() { _ = client.Close() }()
			var filingErr error
			var opened, commented int
			if fileIssues != "" {
				filer := &Filer{Repo: fileIssues, Token: os.Getenv(TokenEnv), API: os.Getenv(APIEnv)}
				if filer.Version, err = client.PingVersion(cmd.Context()); err != nil || filer.Version == "" {
					filer.Version = "unknown"
				}
				// Filing is public and not undone, so a report kukeond
				// would reject must not reach GitHub first.
				if err = validateForFiling(cmd.Context(), client, document, time.Now()); err != nil {
					return report(cmd, err)
				}
				previous := previousReport(cmd.Context(), client, document.CreatedAt)
				// A filing failure does not lose the sweep: the report is
				// still stored, with the URLs obtained so far.
				opened, commented, filingErr = filer.File(cmd.Context(), &document, previous)
			}
			stored, err := client.SubmitHealthReport(cmd.Context(), document)
			if err != nil {
				if opened+commented > 0 || filingErr != nil {
					// The issues are public already: leave a record of
					// them, since no stored report carries their URLs.
					filedBeforeFailure(cmd, fileIssues, &document, opened, commented, filingErr)
				}
				return report(cmd, err)
			}
			recurring := 0
			for _, finding := range stored.Findings {
				if finding.Recurring {
					recurring++
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stored health report %s (%d findings, %d recurring)\n",
				stored.ID, len(stored.Findings), recurring)
			if fileIssues != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Filed in %s: %d issues opened, %d commented\n",
					fileIssues, opened, commented)
			}
			if filingErr != nil {
				return report(cmd, fmt.Errorf("the report is stored, but filing did not complete: %w", filingErr))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Report document (JSON or YAML); - reads stdin")
	cmd.Flags().StringVar(&fileIssues, "file-issues", "",
		"Opt-in: file kukeon findings as GitHub issues in <owner>/<repo> (needs "+TokenEnv+")")
	return cmd
}

// filedBeforeFailure writes to stderr what filing did before a submission
// failed: the counts, each issue URL, and the filing error if there was one.
func filedBeforeFailure(
	cmd *cobra.Command, repo string, document *kukeonv1.HealthReport, opened, commented int, filingErr error,
) {
	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "The report was not stored, but filing in %s already ran: %d issues opened, %d commented\n",
		repo, opened, commented)
	for _, finding := range document.Findings {
		if finding.IssueURL != "" {
			fmt.Fprintf(w, "  %s: %s\n", finding.Signature, finding.IssueURL)
		}
	}
	if filingErr != nil {
		fmt.Fprintln(w, "Filing did not complete:", filingErr)
	}
}

// validateForFiling runs kukeond's schema check on document as it will be
// submitted after filing: id and createdAt filled the way kukeond fills them
// when absent, and issueUrl cleared on the findings the filer never files. It
// also refuses an id kukeond already stores. Filing runs only when it passes.
func validateForFiling(
	ctx context.Context, client kukeonv1.Client, document kukeonv1.HealthReport, now time.Time,
) error {
	candidate := document
	if document.Findings != nil {
		candidate.Findings = make([]kukeonv1.HealthFinding, len(document.Findings))
		copy(candidate.Findings, document.Findings)
	}
	// kukeond derives a missing id from createdAt, so a document with
	// createdAt but no id is stored under a predictable id: check that one.
	// Without createdAt, kukeond's own clock picks the id.
	checkID := document.ID
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	} else if checkID == "" {
		checkID = health.ReportID(candidate.CreatedAt)
	}
	if candidate.ID == "" {
		candidate.ID = health.ReportID(candidate.CreatedAt)
	}
	for i := range candidate.Findings {
		if candidate.Findings[i].Classification != kukeonv1.HealthClassKukeon {
			candidate.Findings[i].IssueURL = ""
		}
	}
	if err := health.Validate(&candidate); err != nil {
		return fmt.Errorf("%w; nothing was filed", err)
	}
	if checkID != "" {
		if _, err := client.ReadHealthReport(ctx, checkID); err == nil {
			return fmt.Errorf("%w: a report with id %q is already stored; nothing was filed",
				errdefs.ErrHealthReportInvalid, checkID)
		}
	}
	return nil
}

// previousReport returns the report kukeond's recurring check will compare
// against: the newest stored report not after createdAt, or the newest one
// when kukeond assigns createdAt itself. nil when there is none.
func previousReport(
	ctx context.Context, client kukeonv1.Client, createdAt time.Time,
) *kukeonv1.HealthReport {
	id := kukeonv1.HealthReportLatest
	if !createdAt.IsZero() {
		summaries, err := client.ListHealthReports(ctx)
		if err != nil {
			return nil
		}
		id = ""
		for _, summary := range summaries { // newest first
			if !summary.CreatedAt.After(createdAt) {
				id = summary.ID
				break
			}
		}
		if id == "" {
			return nil
		}
	}
	previous, err := client.ReadHealthReport(ctx, id)
	if err != nil {
		return nil
	}
	return &previous
}

// DecodeReport reads a report document strictly: an unknown field is an
// error, so a misspelled key is not silently dropped. JSON is a subset of
// YAML, so one decoder covers both.
func DecodeReport(raw []byte) (kukeonv1.HealthReport, error) {
	var document kukeonv1.HealthReport
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return kukeonv1.HealthReport{}, fmt.Errorf("decode report: %w", err)
	}
	return document, nil
}

// Markdown renders a stored report for reading: its header, each finding,
// then the body the sweep wrote.
func Markdown(r kukeonv1.HealthReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Health report %s\n\n", r.ID)
	fmt.Fprintf(&b, "- Created: %s\n- Window: %s to %s\n- Submitted by: %s/%s/%s/%s\n\n%s\n",
		stamp(r.CreatedAt), stamp(r.Since), stamp(r.Until),
		r.Cell.Realm, r.Cell.Space, r.Cell.Stack, r.Cell.Name, strings.TrimSpace(r.Summary))
	if len(r.Findings) == 0 {
		b.WriteString("\nNo findings.\n")
	}
	for _, finding := range r.Findings {
		title := finding.Title
		if title == "" {
			title = finding.Signature
		}
		recurrence := "new"
		if finding.Recurring {
			recurrence = "recurring"
		}
		fmt.Fprintf(&b, "\n## %s\n\n- Classification: %s\n- Severity: %s\n- Recurrence: %s\n- Signature: `%s`\n",
			title, finding.Classification, finding.Severity, recurrence, finding.Signature)
		if finding.IssueURL != "" {
			fmt.Fprintf(&b, "- Issue: %s\n", finding.IssueURL)
		}
		b.WriteString("\nEvidence:\n\n")
		for _, evidence := range finding.Evidence {
			fmt.Fprintf(&b, "```\n%s\n```\n", strings.TrimRight(evidence, "\n"))
		}
		if len(finding.SuggestedCommands) > 0 {
			b.WriteString("\nSuggested commands:\n\n")
			for _, command := range finding.SuggestedCommands {
				fmt.Fprintf(&b, "- `%s`\n", command)
			}
		}
	}
	if body := strings.TrimSpace(r.Body); body != "" {
		fmt.Fprintf(&b, "\n---\n\n%s\n", body)
	}
	return b.String()
}
