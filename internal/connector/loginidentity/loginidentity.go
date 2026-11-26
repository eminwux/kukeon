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

// Package loginidentity is what `kuke connector` and the web UI share about
// login identities (issue #1685). A login identity is a Volume tagged with its
// connector — the counterpart of a Secret tagged with one: the harness keeps
// its login in the state directory, and that directory is the Volume. The
// package lists them, builds the tagged Volume, and builds the identity cell:
// a short-lived cell that mounts only that Volume and runs the harness login.
// The Volume is the identity; the cell is a session and is never stored.
package loginidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// TypeLogin is the TYPE a login identity lists with, next to a Secret
// identity's opaque, basic-auth or ssh-key.
const TypeLogin = "login"

// harnessHome is the home directory of every harness image; a harness
// statePath is written relative to it (`$HOME/.claude`).
const harnessHome = "/home/user"

// defaultScope is the realm an identity lands in when none is named, and the
// space and stack its identity cell runs in when the Volume names none.
const defaultScope = "default"

// containerID is the identity cell's one container.
const containerID = "login"

// Identity is one login identity: a Volume and what is known about it.
type Identity struct {
	// Connector and Method are the Volume's tag, or — for a Volume that is
	// not tagged yet — those of the Config that keeps a login in it.
	Connector string
	Method    string
	// Volume is the Volume's name and scope.
	Volume v1beta1.VolumeRef
	// Exists reports that the Volume is on disk; Tagged that it carries the
	// connector tag. A Config's status can name a state volume before its
	// first cell start creates or tags it.
	Exists bool
	Tagged bool
	// Login is the login state; nil when unknown (the connector declares no
	// marker).
	Login *v1beta1.LoginStatus
	// UsedBy are the Configs whose status names the Volume, sorted.
	UsedBy []ConfigRef
}

// ConfigRef names a CellConfig and its scope.
type ConfigRef struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
	Space string `json:"space,omitempty"`
	Stack string `json:"stack,omitempty"`
}

// Status renders the login state for a table: `logged in`, `empty`, or `-`
// when it is unknown.
func (i Identity) Status() string {
	if i.Login == nil || i.Login.State == "" {
		return "-"
	}
	return i.Login.State
}

// Collect lists the login identities among volumes: every Volume tagged with
// a connector, and every Volume a Config's status names as the state volume of
// a login method — so a state volume that predates tagging, or that no cell
// has created yet, is not hidden. Each carries the Configs using it. The
// result is sorted by connector, then scope and name.
func Collect(volumes []v1beta1.VolumeDoc, configs []v1beta1.CellConfigDoc) []Identity {
	byRef := map[v1beta1.VolumeRef]*Identity{}
	var order []v1beta1.VolumeRef
	entry := func(ref v1beta1.VolumeRef) *Identity {
		if id, ok := byRef[ref]; ok {
			return id
		}
		order = append(order, ref)
		byRef[ref] = &Identity{Volume: ref}
		return byRef[ref]
	}
	stored := map[v1beta1.VolumeRef]v1beta1.VolumeDoc{}
	for _, vol := range volumes {
		ref := refOf(vol.Metadata)
		stored[ref] = vol
		if !vol.Spec.IsLoginIdentity() {
			continue
		}
		id := entry(ref)
		id.Connector, id.Method = vol.Spec.Connector, vol.Spec.Method
		id.Exists, id.Tagged, id.Login = true, true, vol.Status.Login
	}
	for _, cfg := range configs {
		if cfg.Status == nil {
			continue
		}
		for _, status := range cfg.Status.Connectors {
			if status.VolumeRef == nil {
				continue
			}
			ref := *status.VolumeRef
			login := status.Delivery == v1beta1.ConnectorDeliveryStateVolume
			if _, known := byRef[ref]; !known && !login {
				// The state volume of a token method is not an identity.
				continue
			}
			id := entry(ref)
			if !id.Tagged && login {
				id.Connector, id.Method = status.Connector, status.Method
				_, id.Exists = stored[ref]
				if id.Login == nil {
					id.Login = status.Login
				}
			}
			user := ConfigRef{
				Name: cfg.Metadata.Name, Realm: cfg.Metadata.Realm,
				Space: cfg.Metadata.Space, Stack: cfg.Metadata.Stack,
			}
			if !containsConfig(id.UsedBy, user) {
				id.UsedBy = append(id.UsedBy, user)
			}
		}
	}
	out := make([]Identity, 0, len(order))
	for _, ref := range order {
		id := *byRef[ref]
		sort.Slice(id.UsedBy, func(a, b int) bool { return configKey(id.UsedBy[a]) < configKey(id.UsedBy[b]) })
		out = append(out, id)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Connector != out[b].Connector {
			return out[a].Connector < out[b].Connector
		}
		return ScopePath(out[a].Volume)+"\x00"+out[a].Volume.Name < ScopePath(out[b].Volume)+"\x00"+out[b].Volume.Name
	})
	return out
}

func containsConfig(list []ConfigRef, want ConfigRef) bool {
	for _, have := range list {
		if have == want {
			return true
		}
	}
	return false
}

func configKey(c ConfigRef) string {
	return strings.Join([]string{c.Realm, c.Space, c.Stack, c.Name}, "\x00")
}

func refOf(md v1beta1.VolumeMetadata) v1beta1.VolumeRef {
	return v1beta1.VolumeRef(md)
}

// ScopePath renders a Volume's scope as `realm[/space[/stack]]`.
func ScopePath(ref v1beta1.VolumeRef) string {
	parts := []string{ref.Realm}
	for _, part := range []string{ref.Space, ref.Stack} {
		if part == "" {
			break
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/")
}

// UsedBy renders the Configs using an identity for a table: the first one and
// how many more (`kukeon-dev-claude (+4)`), or `-`.
func UsedBy(configs []ConfigRef) string {
	switch len(configs) {
	case 0:
		return "-"
	case 1:
		return configs[0].Name
	default:
		return fmt.Sprintf("%s (+%d)", configs[0].Name, len(configs)-1)
	}
}

// Method returns the method of conn that keeps a login in the state volume:
// the named one, or its default login method for an empty name.
func Method(conn *connector.Connector, name string) (connector.Method, error) {
	method, ok := conn.LoginMethod(strings.TrimSpace(name))
	if ok {
		return method, nil
	}
	if strings.TrimSpace(name) == "" {
		return connector.Method{}, fmt.Errorf(
			"%w: connector %q has no method that keeps a login in the state volume, so it has no login identities",
			errdefs.ErrVolumeIdentity, conn.Metadata.Name)
	}
	return connector.Method{}, fmt.Errorf(
		"%w: connector %q has no login method %q (its methods: %s)",
		errdefs.ErrVolumeIdentity, conn.Metadata.Name, name, strings.Join(conn.MethodNames(), ", "))
}

// VolumeDoc builds the Volume of a new login identity of conn: tagged with
// the connector and its login method, carrying the method's login marker, and
// retained — an identity outlives a cascade purge of its scope.
func VolumeDoc(conn *connector.Connector, method connector.Method, ref v1beta1.VolumeRef) v1beta1.VolumeDoc {
	spec := v1beta1.VolumeSpec{
		ReclaimPolicy: v1beta1.ReclaimRetain,
		Connector:     conn.Metadata.Name,
		Method:        method.Name,
	}
	if method.Login != nil {
		spec.LoginMarker = method.Login.Marker
	}
	return v1beta1.VolumeDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindVolume,
		Metadata:   v1beta1.VolumeMetadata(ref),
		Spec:       spec,
	}
}

// Harness is what an identity cell needs from the harness a connector
// authenticates: where the harness keeps its state, and an image that runs it.
type Harness struct {
	Name      string
	StatePath string
	Image     string
}

// LoadHarnessFrom reads the harness conn authenticates from source: its
// statePath with `$HOME` resolved, and the first image of its catalog.
func LoadHarnessFrom(source fs.FS, conn *connector.Connector) (Harness, error) {
	name := conn.Spec.Harness
	if name == "" {
		return Harness{}, fmt.Errorf("%w: connector %q authenticates no harness",
			errdefs.ErrVolumeIdentity, conn.Metadata.Name)
	}
	definition, err := harness.Load(source, name)
	if err != nil {
		return Harness{}, fmt.Errorf("harness %q: %w", name, err)
	}
	statePath := resolveHome(definition.Spec.StatePath)
	if statePath == "" || !path.IsAbs(statePath) {
		return Harness{}, fmt.Errorf("%w: harness %q keeps no state directory", errdefs.ErrVolumeIdentity, name)
	}
	catalog, err := harness.LoadCatalog(source, name, name)
	if err != nil {
		return Harness{}, fmt.Errorf("harness %q: %w", name, err)
	}
	return Harness{Name: name, StatePath: statePath, Image: catalog.Spec.Images[0].Image}, nil
}

func resolveHome(value string) string {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"${HOME}", "$HOME"} {
		if rest, ok := strings.CutPrefix(value, prefix); ok {
			return path.Clean(harnessHome + rest)
		}
	}
	return value
}

// CellRequest is everything an identity cell is built from.
type CellRequest struct {
	// Connector and Method name the login; Command is the method's login
	// argv.
	Connector string
	Method    string
	Command   []string
	// Volume is the login identity the cell mounts.
	Volume v1beta1.VolumeRef
	// StatePath is where the harness keeps its state; Image runs it.
	StatePath string
	Image     string
	// Session, when set, is stamped on the cell as LabelIdentitySession.
	Session string
}

// IdentityKey names a Volume with its scope, `name@realm[/space[/stack]]`:
// the value of the identity cell's LabelIdentityCell label.
func IdentityKey(ref v1beta1.VolumeRef) string { return ref.Name + "@" + ScopePath(ref) }

// CellName is the name of the identity cell of a Volume: one per Volume, so a
// second login on the same identity finds the first. A stack-scoped Volume's
// cell runs in that stack and is `login-<volume>`. A realm- or space-scoped
// one's runs in the `default` stack below it, beside the cells of any Volume
// of the same name scoped there, so its name carries a digest of the full
// scope.
func CellName(ref v1beta1.VolumeRef) string {
	if ref.Space != "" && ref.Stack != "" {
		return "login-" + ref.Name
	}
	sum := sha256.Sum256([]byte(IdentityKey(ref)))
	return "login-" + ref.Name + "-" + hex.EncodeToString(sum[:4])
}

// IsIdentityCellOf reports whether cell is the identity cell of the Volume ref.
func IsIdentityCellOf(cell v1beta1.CellDoc, ref v1beta1.VolumeRef) bool {
	return cell.Metadata.Labels[v1beta1.LabelIdentityCell] == IdentityKey(ref)
}

// SessionLive reports whether an identity cell is a session a client can
// attach to: running, ready, or still starting — a first start can spend a
// while pulling the image, and a second client must join it then, not replace
// it. A cell that ended and is not reaped yet is not live.
func SessionLive(got kukeonv1.GetCellResult) bool {
	if !got.MetadataExists {
		return false
	}
	state := got.Cell.Status.State
	return state == v1beta1.CellStateReady || state == v1beta1.CellStatePending || got.RootContainerTaskRunning
}

// CellScope is where the identity cell of a Volume runs: the Volume's realm,
// and its space and stack or `default` where the Volume names none.
func CellScope(ref v1beta1.VolumeRef) (string, string, string) {
	space, stack := ref.Space, ref.Stack
	if space == "" {
		space = defaultScope
	}
	if stack == "" {
		stack = defaultScope
	}
	return ref.Realm, space, stack
}

// CellDoc builds the identity cell: one attachable container on the harness
// image, the Volume mounted at the harness state directory, running the login
// command, deleted when it exits. It has no role, repos, secrets or other
// connector — nothing but the Volume.
func CellDoc(req CellRequest) (v1beta1.CellDoc, error) {
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w: connector %q declares no login command for its %q method (login.command in connector.yaml)",
			errdefs.ErrVolumeIdentity, req.Connector, req.Method)
	}
	if strings.TrimSpace(req.Image) == "" || strings.TrimSpace(req.StatePath) == "" {
		return v1beta1.CellDoc{}, errors.New("identity cell: image and state path are required")
	}
	name := CellName(req.Volume)
	realm, space, stack := CellScope(req.Volume)
	volume := req.Volume
	labels := map[string]string{
		v1beta1.LabelIdentityCell:      IdentityKey(req.Volume),
		v1beta1.LabelIdentityConnector: req.Connector,
	}
	if req.Session != "" {
		labels[v1beta1.LabelIdentitySession] = req.Session
	}
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.CellSpec{
			ID:         name,
			RealmID:    realm,
			SpaceID:    space,
			StackID:    stack,
			AutoDelete: true,
			Tty:        &v1beta1.CellTty{Default: containerID},
			Containers: []v1beta1.ContainerSpec{{
				ID:         containerID,
				Image:      req.Image,
				Command:    req.Command[0],
				Args:       append([]string{}, req.Command[1:]...),
				WorkingDir: harnessHome,
				Attachable: true,
				Volumes: []v1beta1.VolumeMount{{
					Kind:      v1beta1.VolumeKindVolume,
					VolumeRef: &volume,
					Target:    req.StatePath,
				}},
			}},
		},
	}, nil
}

// ImageFromConfigs returns the image a Config using the identity runs, so the
// login runs on the same image as the role's cells (a locally built one
// included); empty when none states one.
func ImageFromConfigs(users []ConfigRef, configs []v1beta1.CellConfigDoc) string {
	for _, user := range users {
		for _, cfg := range configs {
			md := cfg.Metadata
			if md.Name != user.Name || md.Realm != user.Realm || md.Space != user.Space || md.Stack != user.Stack {
				continue
			}
			if image := strings.TrimSpace(cfg.Spec.Values["IMAGE"]); image != "" {
				return image
			}
		}
	}
	return ""
}
