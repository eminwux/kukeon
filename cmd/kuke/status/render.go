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

package status

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// renderJSON marshals the report as a stable, 2-space-indented JSON
// document. Status is rendered as its label ("OK"/"WARN"/"FAIL") rather
// than the iota value so the wire shape doesn't depend on enum order.
func renderJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// renderText prints the section-grouped human report. Section header
// lines are uppercased; each row is `  <NAME>  <STATUS>  <DETAIL>`
// padded so columns line up. Remediation lines print on the next line
// indented under their row, only when verbose is true or the row is not
// OK — the AC's "one-line remediation hint" lives there.
//
// The final line reports the top-level verdict ("Status: OK" /
// "Status: FAIL") so an operator scrolling to the bottom sees the
// overall result without re-scanning every row.
func renderText(w io.Writer, report Report, verbose bool) {
	if len(report.Checks) == 0 {
		fmt.Fprintln(w, "no checks reported")
		return
	}

	// Column widths sized to the widest name across all sections so the
	// status column lines up across the whole report rather than per
	// section. STATUS is always one of OK/WARN/FAIL so a fixed 4-char
	// budget covers it.
	nameWidth := 0
	for _, c := range report.Checks {
		if n := len(c.Name); n > nameWidth {
			nameWidth = n
		}
	}

	var currentSection string
	for _, c := range report.Checks {
		if c.Section != currentSection {
			if currentSection != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "%s\n", strings.ToUpper(c.Section))
			currentSection = c.Section
		}
		fmt.Fprintf(w, "  %-*s  %-4s  %s\n", nameWidth, c.Name, c.Status, c.Detail)
		if c.Remediation != "" && (verbose || c.Status != StatusOK) {
			fmt.Fprintf(w, "  %-*s        ↳ %s\n", nameWidth, "", c.Remediation)
		}
	}

	fmt.Fprintln(w)
	if report.OK {
		fmt.Fprintln(w, "Status: OK")
	} else {
		fmt.Fprintln(w, "Status: FAIL")
	}
}
