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

// Package kuketty defines the terminal-metadata schema kukeond renders for
// each attachable container and the kuketty in-container wrapper consumes.
//
// The schema is the entire contract between the daemon (producer) and
// kuketty (consumer) — kuketty exposes no runtime CLI flags and no env-var
// inputs. Splitting the schema into its own package keeps the import set
// kuketty pulls in to stdlib only, satisfying the phase-1 sizing rationale
// (per-process RSS + startup time at scale; see issue #165 Notes).
package kuketty

import (
	"encoding/json"
	"fmt"
)

// APIVersion is the schema version stamped into every rendered metadata file.
// Bumped on a breaking change to the on-disk format so a kuketty binary that
// loads an older or newer file refuses cleanly rather than silently
// misinterpreting fields.
const APIVersion = "kuketty.kukeon.io/v1alpha1"

// Kind is the schema discriminator. A single kind today; reserved for future
// non-Metadata documents the daemon may stage alongside.
const Kind = "TerminalMetadata"

// Metadata is the rendered-by-kukeond / consumed-by-kuketty document. JSON
// over YAML for the consumer-side parse: kuketty's import set must stay
// stdlib-only, and encoding/json is in stdlib while a YAML parser is not.
type Metadata struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Meta       Meta   `json:"metadata"`
	Spec       Spec   `json:"spec"`
}

// Meta carries identifying fields. ContainerID is the only field today; the
// realm/space/stack/cell coordinates live in kukeond's per-container
// filesystem layout and are reachable from the host path of the metadata
// file itself, so duplicating them in the schema would just rot.
type Meta struct {
	ContainerID string `json:"containerID,omitempty"`
}

// Spec is the runtime configuration kuketty applies on startup. Phase 1
// honors RunPath and Socket.Path (creates the inode); the remaining fields
// are declared in the schema so the kukeond → kuketty contract is fixed
// upfront, but kuketty's behavior for them lands in later phases:
//
//	Socket.Mode/Socket.GID                — phase 1 (#165)
//	Socket.* RPC serving (Accept loop)    — phase 1b (#410)
//	Capture.Path                          — phase 2  (#288)
//	Log.Path                              — phase 3  (#289)
//	Shell.Prompt + Shell.OnInit rendering — phase 4  (#290)
type Spec struct {
	// RunPath is the in-container directory kuketty treats as its run
	// root. Today, only Socket.Path's parent is read from here.
	RunPath string `json:"runPath"`
	// Socket configures the per-container attach control socket.
	Socket SocketSpec `json:"socket"`
	// Capture and Log are declared but unused in phase 1 — the paths the
	// daemon would stage them at, when phases 2/3 land kuketty's writers.
	Capture CaptureSpec `json:"capture,omitempty"`
	Log     LogSpec     `json:"log,omitempty"`
	// Shell carries kukeond's pre-rendered prompt + onInit. Declared in
	// phase 1 so the daemon's render path is stable; honored in phase 4.
	Shell ShellSpec `json:"shell,omitempty"`
}

// SocketSpec is the per-container attach control socket. Mode and GID are
// the kukeon-group ownership the daemon was already plumbing to sbsh — moved
// into the metadata file so kuketty applies them after listen() the same
// way sbsh did, preserving the host-side group-traversal contract `kuke
// init` sets up on /opt/kukeon (issue #258 repro A).
//
// Mode is the literal octal string sbsh accepted (e.g., "0660"); kept as a
// string so a zero value distinguishes "unset" from "0o000". Empty Mode
// means kuketty leaves the OS-default (umask-clipped) permissions in place.
// Zero GID is the legacy fallback for hosts with no kukeon group configured.
type SocketSpec struct {
	Path string `json:"path"`
	Mode string `json:"mode,omitempty"`
	GID  int    `json:"gid,omitempty"`
}

// CaptureSpec is the path kuketty's transcript writer will land at in phase
// 2. Mode/GID mirror SocketSpec; declared up front so the phase-1 daemon
// can render them without a schema bump later.
type CaptureSpec struct {
	Path string `json:"path,omitempty"`
	Mode string `json:"mode,omitempty"`
	GID  int    `json:"gid,omitempty"`
}

// LogSpec is the per-terminal log file kuketty's writer will land at in
// phase 3.
type LogSpec struct {
	Path string `json:"path,omitempty"`
	Mode string `json:"mode,omitempty"`
	GID  int    `json:"gid,omitempty"`
}

// ShellSpec carries kukeond's pre-rendered prompt + onInit. Profile
// resolution moves out of the terminal wrapper entirely (issue #165 redirect):
// kukeond resolves the TerminalProfile against the cell's
// container-tty block and stamps the result here. Phase 4 wires kuketty
// to apply the prompt + run the onInit stages.
type ShellSpec struct {
	Prompt string  `json:"prompt,omitempty"`
	OnInit []Stage `json:"onInit,omitempty"`
}

// Stage is a single onInit step. Script is the literal shell command run
// before the workload's first prompt; kuketty pipes it into the PTY in
// phase 4. Kept structured (not a flat []string) so future stages can
// carry a name or timeout without a breaking change.
type Stage struct {
	Script string `json:"script,omitempty"`
}

// Validate returns an error if required phase-1 fields are missing or the
// schema discriminator is wrong. Called by kuketty after unmarshal so a
// malformed file fails before the PTY is spawned.
func (m *Metadata) Validate() error {
	if m.APIVersion != APIVersion {
		return fmt.Errorf("kuketty metadata: apiVersion %q, want %q", m.APIVersion, APIVersion)
	}
	if m.Kind != Kind {
		return fmt.Errorf("kuketty metadata: kind %q, want %q", m.Kind, Kind)
	}
	if m.Spec.RunPath == "" {
		return fmt.Errorf("kuketty metadata: spec.runPath is required")
	}
	if m.Spec.Socket.Path == "" {
		return fmt.Errorf("kuketty metadata: spec.socket.path is required")
	}
	return nil
}

// Marshal renders m as indented JSON. Indented because the file is
// daemon-rendered, read once per container start, and read by humans during
// debugging far more often than by hot loops.
func Marshal(m *Metadata) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal kuketty metadata: %w", err)
	}
	return data, nil
}

// Unmarshal parses a metadata document and validates it. Returns the
// post-validation Metadata so callers can act on it without a second pass.
func Unmarshal(data []byte) (*Metadata, error) {
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse kuketty metadata: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
