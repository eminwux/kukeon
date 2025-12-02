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

package shared

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/apply/parser"
)

// ReadFileOrStdin reads from a file or stdin if file is "-".
// Returns the reader and a cleanup function that should be called when done.
// If file is "-", the cleanup function is a no-op.
func ReadFileOrStdin(file string) (io.Reader, func() error, error) {
	if file == "-" {
		return os.Stdin, func() error { return nil }, nil
	}

	f, err := os.Open(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file %q: %w", file, err)
	}

	return f, f.Close, nil
}

// ParseAndValidateDocuments parses and validates YAML documents from a reader.
// Returns the parsed documents and any validation errors encountered.
// If there are validation errors, they are returned as a slice, but the function
// still returns the successfully parsed documents.
func ParseAndValidateDocuments(reader io.Reader) ([]parser.Document, []*parser.ValidationError, error) {
	// Parse YAML documents
	rawDocs, err := parser.ParseDocuments(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Parse and validate each document
	docs := make([]parser.Document, 0, len(rawDocs))
	var validationErrors []*parser.ValidationError

	for i, rawDoc := range rawDocs {
		doc, parseErr := parser.ParseDocument(i, rawDoc)
		if parseErr != nil {
			validationErrors = append(validationErrors, &parser.ValidationError{
				Index: i,
				Err:   parseErr,
			})
			continue
		}

		validationErr := parser.ValidateDocument(doc)
		if validationErr != nil {
			validationErrors = append(validationErrors, validationErr)
			continue
		}

		docs = append(docs, *doc)
	}

	return docs, validationErrors, nil
}

// FormatValidationErrors formats validation errors into a single error message.
// If all errors are parsing errors (contain "failed to parse"), it returns
// a YAML parsing error instead of a validation error.
func FormatValidationErrors(validationErrors []*parser.ValidationError) error {
	if len(validationErrors) == 0 {
		return nil
	}

	// Check if all errors are parsing errors
	allParsingErrors := true
	for _, validationErr := range validationErrors {
		errMsg := validationErr.Error()
		if !strings.Contains(errMsg, "failed to parse") {
			allParsingErrors = false
			break
		}
	}

	// If all errors are parsing errors, return as YAML parsing error
	if allParsingErrors && len(validationErrors) > 0 {
		// Return the first error as a YAML parsing error
		return fmt.Errorf("failed to parse YAML: %w", validationErrors[0].Err)
	}

	// Otherwise, return as validation errors
	var errMsgs []string
	for _, validationErr := range validationErrors {
		errMsgs = append(errMsgs, validationErr.Error())
	}
	return fmt.Errorf("validation errors:\n  %s", strings.Join(errMsgs, "\n  "))
}
