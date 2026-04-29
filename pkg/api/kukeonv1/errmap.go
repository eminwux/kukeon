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

package kukeonv1

import (
	"errors"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// APIError is a serializable error. Kind identifies the error class; client
// code maps it back to an errdefs.* sentinel so that errors.Is(err, errdefs.X)
// keeps working across the wire.
type APIError struct {
	Kind    string
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// kindToSentinel maps wire Kind values to local errdefs sentinels.
var kindToSentinel = map[string]error{
	"CellNotFound":            errdefs.ErrCellNotFound,
	"RealmNotFound":           errdefs.ErrRealmNotFound,
	"SpaceNotFound":           errdefs.ErrSpaceNotFound,
	"StackNotFound":           errdefs.ErrStackNotFound,
	"ContainerNotFound":       errdefs.ErrContainerNotFound,
	"NetworkNotFound":         errdefs.ErrNetworkNotFound,
	"CellNameRequired":        errdefs.ErrCellNameRequired,
	"RealmNameRequired":       errdefs.ErrRealmNameRequired,
	"SpaceNameRequired":       errdefs.ErrSpaceNameRequired,
	"StackNameRequired":       errdefs.ErrStackNameRequired,
	"ContainerNameRequired":   errdefs.ErrContainerNameRequired,
	"ResourceHasDependencies": errdefs.ErrResourceHasDependencies,
	"CreateCell":              errdefs.ErrCreateCell,
	"CreateRealm":             errdefs.ErrCreateRealm,
	"CreateSpace":             errdefs.ErrCreateSpace,
	"CreateStack":             errdefs.ErrCreateStack,
	"ContainerExists":         errdefs.ErrContainerExists,
	"ConversionFailed":        errdefs.ErrConversionFailed,
	"AttachNotSupported":      errdefs.ErrAttachNotSupported,
	"ImageNotFound":           errdefs.ErrImageNotFound,
}

// sentinelToKind is the reverse lookup, populated lazily.
var sentinelToKind = func() map[error]string {
	m := make(map[error]string, len(kindToSentinel))
	for kind, sentinel := range kindToSentinel {
		m[sentinel] = kind
	}
	return m
}()

// KindFromError inspects err and returns the best-matching wire Kind, falling
// back to "Unknown" when nothing registered matches.
func KindFromError(err error) string {
	if err == nil {
		return ""
	}
	for sentinel, kind := range sentinelToKind {
		if errors.Is(err, sentinel) {
			return kind
		}
	}
	return "Unknown"
}

// ToAPIError converts a Go error into a wire APIError. Returns nil for nil.
func ToAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	return &APIError{
		Kind:    KindFromError(err),
		Message: err.Error(),
	}
}

// FromAPIError reconstructs a Go error that unwraps to the matching errdefs
// sentinel when the Kind is recognized, so callers can use errors.Is.
// The error's Error() returns the verbatim wire Message (no prefix added):
// the server already embeds the sentinel in the message via %w wrapping, and
// we don't want to stutter "failed to X: failed to X: ...".
func FromAPIError(e *APIError) error {
	if e == nil {
		return nil
	}
	sentinel := kindToSentinel[e.Kind]
	return &wireError{msg: e.Message, sentinel: sentinel}
}

// wireError carries a wire error message verbatim and unwraps to the
// sentinel matching the APIError.Kind so errors.Is works transparently.
type wireError struct {
	msg      string
	sentinel error
}

func (w *wireError) Error() string { return w.msg }
func (w *wireError) Unwrap() error { return w.sentinel }
