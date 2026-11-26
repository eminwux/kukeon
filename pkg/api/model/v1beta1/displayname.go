// Copyright 2026 Emiliano Spinella (eminwux)
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

package v1beta1

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidDisplayName is the reason NormalizeDisplayName refuses a name.
var ErrInvalidDisplayName = errors.New("invalid display name")

// NormalizeDisplayName trims a display name (AnnotationDisplayName) and checks
// it: at most MaxDisplayNameLength characters and no control characters. An
// empty result is valid and means "no display name".
func NormalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrInvalidDisplayName)
	}
	if n := utf8.RuneCountInString(name); n > MaxDisplayNameLength {
		return "", fmt.Errorf("%w: %d characters, at most %d allowed", ErrInvalidDisplayName, n, MaxDisplayNameLength)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: control characters are not allowed", ErrInvalidDisplayName)
		}
	}
	return name, nil
}
