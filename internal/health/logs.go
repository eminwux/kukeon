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

package health

import (
	"bufio"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// maxLogLine bounds one log line. A longer line is skipped, not truncated
// into a different signature.
const maxLogLine = 1 << 20

// parseLine reads one slog text-handler line (key=value pairs, values quoted
// when they hold spaces) into its fields. ok is false for a line that is not
// in that shape, such as a panic trace or output of a child process.
func parseLine(line string) (map[string]string, bool) {
	fields := map[string]string{}
	rest := strings.TrimSpace(line)
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 || strings.ContainsAny(rest[:eq], " \t\"") {
			return nil, false
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		var value string
		if strings.HasPrefix(rest, "\"") {
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				return nil, false
			}
			unquoted, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, false
			}
			value, rest = unquoted, rest[len(quoted):]
		} else {
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				end = len(rest)
			}
			value, rest = rest[:end], rest[end:]
		}
		fields[key] = value
		rest = strings.TrimLeft(rest, " \t")
	}
	_, hasLevel := fields["level"]
	_, hasMsg := fields["msg"]
	return fields, hasLevel && hasMsg
}

// readLine returns the next line without its newline. A line longer than
// maxLogLine is consumed and returned empty, so one runaway line cannot stop
// the scan or be cut into a different signature.
func readLine(reader *bufio.Reader) (string, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, isPrefix, err := reader.ReadLine()
		if err != nil {
			if tooLong {
				return "", err
			}
			return string(buf), err
		}
		if !tooLong {
			buf = append(buf, chunk...)
			if len(buf) > maxLogLine {
				tooLong, buf = true, nil
			}
		}
		if !isPrefix {
			return string(buf), nil
		}
	}
}

// errorLevel reports whether a slog level string is ERROR or above
// ("ERROR", "ERROR+2").
func errorLevel(level string) bool {
	return strings.HasPrefix(strings.ToUpper(level), "ERROR")
}

// GroupLogErrors reads kukeond's log stream and groups its error-level
// entries with a time in [since, until] by signature. Groups are ordered by
// count, then newest first, then signature, so the result is deterministic.
// Entries without a parseable time are skipped: they cannot be placed in a
// window.
func GroupLogErrors(r io.Reader, since, until time.Time) ([]kukeonv1.HealthLogGroup, error) {
	groups := map[string]*kukeonv1.HealthLogGroup{}
	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := readLine(reader)
		if err != nil && err != io.EOF {
			return nil, err
		}
		done := err == io.EOF
		if fields, ok := parseLine(line); ok && errorLevel(fields["level"]) {
			at, parseErr := time.Parse(time.RFC3339Nano, fields["time"])
			if parseErr == nil && !at.Before(since) && !at.After(until) {
				errText := fields["error"]
				if errText == "" {
					errText = fields["err"]
				}
				signature := Signature(fields["msg"], errText)
				group, seen := groups[signature]
				if !seen {
					group = &kukeonv1.HealthLogGroup{Signature: signature, FirstSeen: at, LastSeen: at, Sample: line}
					groups[signature] = group
				}
				group.Count++
				if at.Before(group.FirstSeen) {
					group.FirstSeen = at
				}
				if !at.Before(group.LastSeen) {
					group.LastSeen, group.Sample = at, line
				}
			}
		}
		if done {
			break
		}
	}
	out := make([]kukeonv1.HealthLogGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Signature < out[j].Signature
	})
	return out, nil
}
