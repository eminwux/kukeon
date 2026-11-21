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

package v1beta1

// Connector delivery handlers (issue #1548). A connector is a kind of access a
// cell needs — a harness login, an API token, a git identity, a registry
// credential. How its identity reaches the cell is one of a small closed set
// of handlers; a connector that uses env, file or stateVolume needs no code.
const (
	// ConnectorDeliveryEnv sets the environment variable the connector
	// component's secret slot names.
	ConnectorDeliveryEnv = "env"
	// ConnectorDeliveryFile mounts the secret at the path the connector
	// component's secret slot names.
	ConnectorDeliveryFile = "file"
	// ConnectorDeliveryStateVolume keeps a login in the harness state
	// directory: the connector has no stored identity.
	ConnectorDeliveryStateVolume = "stateVolume"
	// ConnectorDeliveryGit delivers a git identity through the cell's git
	// block (author, committer, credentials and signing).
	ConnectorDeliveryGit = "git"
	// ConnectorDeliveryRegistry delivers a registry credential to the image
	// pulls of a Realm. It is never part of a cell.
	ConnectorDeliveryRegistry = "registry"
)

// Scopes a connector default may be set at.
const (
	ConnectorScopeRealm = "realm"
	ConnectorScopeSpace = "space"
	ConnectorScopeStack = "stack"
)

// Connector identity field types. Every identity field is stored as a string
// in Secret.spec.identity; the type says how the string is read.
const (
	// ConnectorFieldString is free text. The default.
	ConnectorFieldString = "string"
	// ConnectorFieldList is a comma-separated list.
	ConnectorFieldList = "list"
	// ConnectorFieldPerson is a name and an email, written `Name <email>`.
	ConnectorFieldPerson = "person"
	// ConnectorFieldSecret references another Secret, written
	// `name[@realm[/space[/stack]]]`; without a scope it is looked up next to
	// the identity itself.
	ConnectorFieldSecret = "secret"
)

// ConnectorField declares one readable field of a connector identity. The
// schema drives `kuke create secret` flags and the web UI form, and the daemon
// checks a Secret's spec.identity against it when it resolves the Secret.
type ConnectorField struct {
	// Name is the key in Secret.spec.identity. Required.
	Name string `json:"name"                  yaml:"name"`
	// Type is one of string (default when empty), list, person or secret.
	Type string `json:"type,omitempty"        yaml:"type,omitempty"`
	// Required marks a field every identity of the connector must set.
	Required bool `json:"required,omitempty"    yaml:"required,omitempty"`
	// Help is a one-line description shown next to the flag or input.
	Help string `json:"help,omitempty"        yaml:"help,omitempty"`
	// Enum lists the accepted values (of the string, or of each list entry).
	Enum []string `json:"enum,omitempty"        yaml:"enum,omitempty"`
	// SecretTypes limits the field to identities of these Secret types; empty
	// allows it on every type the connector accepts.
	SecretTypes []string `json:"secretTypes,omitempty" yaml:"secretTypes,omitempty"`
}

// BlueprintConnector is the copy of a connector definition a blueprint
// component carries. `kuke team` and the web UI read the connector catalog
// embedded in kuke; the daemon keeps no catalog and validates Secret tags,
// per-cell overrides and resolution against this copy alone, so a daemon needs
// no upgrade to accept a connector a newer kuke defines.
type BlueprintConnector struct {
	// Name is the connector's name: the value of Secret.spec.connector on its
	// identities and the key of `--connector <name>=…`. Required.
	Name string `json:"name"                  yaml:"name"`
	// Title is the connector's display name.
	Title string `json:"title,omitempty"       yaml:"title,omitempty"`
	// Method names which of the connector's methods this component delivers.
	Method string `json:"method,omitempty"      yaml:"method,omitempty"`
	// Delivery is the handler that delivers the identity: env, file,
	// stateVolume or git. Required.
	Delivery string `json:"delivery"              yaml:"delivery"`
	// SecretTypes are the Secret types the connector accepts.
	SecretTypes []string `json:"secretTypes,omitempty" yaml:"secretTypes,omitempty"`
	// Scopes are the scopes a default identity may be set at.
	Scopes []string `json:"scopes,omitempty"      yaml:"scopes,omitempty"`
	// Fields is the schema of the readable identity fields.
	Fields []ConnectorField `json:"fields,omitempty"      yaml:"fields,omitempty"`
	// Optional marks a connector whose cells start without an identity. By
	// default an enabled connector that needs an identity must resolve one.
	Optional bool `json:"optional,omitempty"    yaml:"optional,omitempty"`
}

// Connector resolution states.
const (
	// ConnectorResolved means the connector has an identity (or needs none).
	ConnectorResolved = "resolved"
	// ConnectorUnresolved means no cell override, Config fill or scope
	// default supplies an identity, or the one named cannot be used.
	ConnectorUnresolved = "unresolved"
)

// ConnectorStatus reports how one connector a CellConfig requires resolves:
// the identity that fills it and the level it came from, or that nothing
// does. It is display-safe — an identity is named by its Secret coordinates,
// never its material.
type ConnectorStatus struct {
	// Connector is the connector's name.
	Connector string `json:"connector"           yaml:"connector"`
	// Title is the connector's display name.
	Title string `json:"title,omitempty"     yaml:"title,omitempty"`
	// Component is the blueprint component that requires it.
	Component string `json:"component"           yaml:"component"`
	// Method is the connector method the Config uses.
	Method string `json:"method,omitempty"    yaml:"method,omitempty"`
	// Delivery is the handler delivering the identity.
	Delivery string `json:"delivery"            yaml:"delivery"`
	// Slot is the secret slot the identity fills (env and file delivery).
	Slot string `json:"slot,omitempty"      yaml:"slot,omitempty"`
	// State is resolved or unresolved.
	State string `json:"state"               yaml:"state"`
	// Required reports whether a cell cannot be created while the connector
	// is unresolved.
	Required bool `json:"required,omitempty"  yaml:"required,omitempty"`
	// Identity is the Secret that fills the connector; nil when unresolved
	// or when the delivery needs none (a login kept in the state volume).
	Identity *ContainerSecretRef `json:"identity,omitempty"  yaml:"identity,omitempty"`
	// Origin is the level the identity came from: cell, config, stack, space
	// or realm.
	Origin string `json:"origin,omitempty"    yaml:"origin,omitempty"`
	// Volume is the state volume holding the login of a stateVolume
	// connector.
	Volume string `json:"volume,omitempty"    yaml:"volume,omitempty"`
	// VolumeRef is that state volume with its scope, when the Config names
	// one (STATE_VOLUME_REALM, and optionally _SPACE and _STACK).
	VolumeRef *VolumeRef `json:"volumeRef,omitempty" yaml:"volumeRef,omitempty"`
	// Message says why the connector is unresolved.
	Message string `json:"message,omitempty"   yaml:"message,omitempty"`
}
