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

package controller

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Connector resolution (issue #1548). A connector is a kind of access a cell
// needs; a blueprint component that carries a connector descriptor is a
// requirement for it, and an identity — a stored Secret — fills it. The daemon
// keeps no catalog: everything it knows about a connector is the descriptor
// the component carries, plus the two delivery handlers written here (the
// secret-slot one shared by env and file, and git).

// stateVolumeValue is the Config value naming the volume a harness keeps its
// state — and therefore its login — in.
const stateVolumeValue = "STATE_VOLUME"

// connectorRequirement is one connector a Config requires: an enabled
// blueprint component carrying a connector descriptor.
type connectorRequirement struct {
	// Component is the blueprint component's name.
	Component string
	// Connector is the descriptor the component carries.
	Connector v1beta1.BlueprintConnector
	// Slot is the secret slot the identity fills (env and file delivery).
	Slot string
}

// connectorRequirements lists the connectors the enabled components of a
// blueprint require, in declaration order. bp is the blueprint before its
// components are folded away.
func connectorRequirements(bp v1beta1.CellBlueprintDoc, enabled []string) []connectorRequirement {
	var out []connectorRequirement
	for _, comp := range bp.Spec.Components {
		if comp.Connector == nil || !slices.Contains(enabled, comp.Name) {
			continue
		}
		req := connectorRequirement{Component: comp.Name, Connector: *comp.Connector}
		if len(comp.Secrets) > 0 {
			req.Slot = strings.TrimSpace(comp.Secrets[0].Name)
		}
		out = append(out, req)
	}
	return out
}

// cellConnectorLayer turns the per-cell `--connector` overrides into the level
// MergeDefaultsForCell applies over the Config: a secret fill for a connector
// delivered through a slot, a git identity reference for the git connector.
// It returns the overrides as they are recorded in the cell's provenance — a
// reference without a realm is looked up in the cell's own.
//
// An override must name a connector the Config requires, and that connector
// must take an identity. reqs already reflects a per-cell method choice
// (#1551), so the identity fills the slot of the method the cell uses.
func cellConnectorLayer(
	cfgName, realm string, reqs []connectorRequirement, overrides map[string]v1beta1.ContainerSecretRef,
) (*v1beta1.ScopeDefaults, map[string]v1beta1.ContainerSecretRef, error) {
	if len(overrides) == 0 {
		return nil, nil, nil
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)

	layer := &v1beta1.ScopeDefaults{}
	recorded := make(map[string]v1beta1.ContainerSecretRef, len(overrides))
	for _, name := range names {
		req, ok := findRequirement(reqs, name)
		if !ok {
			return nil, nil, fmt.Errorf(
				"%w: config %q requires no connector %q (its connectors: %s)",
				errdefs.ErrConnectorUnknown, cfgName, name, requirementNames(reqs))
		}
		ref := trimSecretRef(overrides[name])
		if ref.Name == "" {
			return nil, nil, fmt.Errorf(
				"%w: connector %q: the override names no secret", errdefs.ErrConnectorIdentity, name)
		}
		if ref.Realm == "" {
			ref.Realm = realm
		}
		switch req.Connector.Delivery {
		case v1beta1.ConnectorDeliveryGit:
			layer.Git = &v1beta1.ContainerGit{IdentityRef: &ref}
		case v1beta1.ConnectorDeliveryEnv, v1beta1.ConnectorDeliveryFile:
			if layer.Secrets == nil {
				layer.Secrets = map[string]v1beta1.CellConfigSecretFill{}
			}
			fill := ref
			layer.Secrets[req.Slot] = v1beta1.CellConfigSecretFill{SecretRef: &fill}
		default:
			return nil, nil, fmt.Errorf(
				"%w: config %q uses connector %q through its %q method (%s delivery), which takes no identity",
				errdefs.ErrConnectorIdentity, cfgName, name, req.Connector.Method, req.Connector.Delivery)
		}
		recorded[name] = ref
	}
	return layer, recorded, nil
}

// cellMethodComponents applies the per-cell `--connector-method` choices
// (#1551) to the components a Config enables. A connector's methods are the
// blueprint's components that carry its descriptor with a method name, and
// they are alternatives (one group), so a choice disables every method
// component of the connector and enables the one of the requested method. It
// returns the components to flatten with and the choices as they are recorded
// in the cell's provenance.
//
// A choice must name a connector the blueprint declares methods for, a method
// one of those components delivers, and a connector whose method components
// are all optional — a blueprint that always enables one leaves no choice.
func cellMethodComponents(
	cfgName string, bp v1beta1.CellBlueprintDoc, enabled []string, methods map[string]string,
) ([]string, map[string]string, error) {
	if len(methods) == 0 {
		return enabled, nil, nil
	}
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)

	out := append([]string(nil), enabled...)
	recorded := make(map[string]string, len(methods))
	for _, key := range names {
		name, method := strings.TrimSpace(key), strings.TrimSpace(methods[key])
		if method == "" {
			return nil, nil, fmt.Errorf(
				"%w: connector %q: the choice names no method", errdefs.ErrConnectorMethod, name)
		}
		var (
			members []v1beta1.BlueprintComponent
			picked  string
			known   []string
		)
		for _, comp := range bp.Spec.Components {
			if comp.Connector == nil || comp.Connector.Name != name || comp.Connector.Method == "" {
				continue
			}
			members = append(members, comp)
			known = append(known, comp.Connector.Method)
			if comp.Connector.Method == method {
				picked = comp.Name
			}
		}
		if len(members) == 0 {
			return nil, nil, fmt.Errorf(
				"%w: config %q has no connector %q with methods to choose from (connectors with methods: %s)",
				errdefs.ErrConnectorMethod, cfgName, name, methodConnectorNames(bp))
		}
		if picked == "" {
			return nil, nil, fmt.Errorf("%w: connector %q has no method %q (methods: %s)",
				errdefs.ErrConnectorMethod, name, method, strings.Join(known, ", "))
		}
		for _, comp := range members {
			if !comp.Optional {
				return nil, nil, fmt.Errorf(
					"%w: config %q: blueprint %q always enables component %q, so connector %q has no method to choose",
					errdefs.ErrConnectorMethod, cfgName, bp.Metadata.Name, comp.Name, name)
			}
			out = slices.DeleteFunc(out, func(c string) bool { return strings.TrimSpace(c) == comp.Name })
		}
		out = append(out, picked)
		recorded[name] = method
	}
	return out, recorded, nil
}

// methodConnectorNames lists the connectors a blueprint declares methods for.
func methodConnectorNames(bp v1beta1.CellBlueprintDoc) string {
	var names []string
	for _, comp := range bp.Spec.Components {
		if comp.Connector != nil && comp.Connector.Method != "" {
			names = append(names, comp.Connector.Name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	sort.Strings(names)
	return strings.Join(slices.Compact(names), ", ")
}

func findRequirement(reqs []connectorRequirement, name string) (connectorRequirement, bool) {
	for _, req := range reqs {
		if req.Connector.Name == name {
			return req, true
		}
	}
	return connectorRequirement{}, false
}

func requirementNames(reqs []connectorRequirement) string {
	if len(reqs) == 0 {
		return "none"
	}
	names := make([]string, 0, len(reqs))
	for _, req := range reqs {
		names = append(names, req.Connector.Name)
	}
	sort.Strings(names)
	return strings.Join(slices.Compact(names), ", ")
}

func trimSecretRef(ref v1beta1.ContainerSecretRef) v1beta1.ContainerSecretRef {
	return v1beta1.ContainerSecretRef{
		Name:  strings.TrimSpace(ref.Name),
		Realm: strings.TrimSpace(ref.Realm),
		Space: strings.TrimSpace(ref.Space),
		Stack: strings.TrimSpace(ref.Stack),
		Cell:  strings.TrimSpace(ref.Cell),
	}
}

// readableSecret reads the part of a Secret a render may look at — its type,
// connector tag and identity fields. Material is never read.
func readableSecret(r runner.Runner, ref v1beta1.ContainerSecretRef) (v1beta1.SecretSpec, error) {
	got, err := r.GetSecret(intmodel.Secret{Metadata: intmodel.SecretMetadata{
		Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
	}})
	if err != nil {
		return v1beta1.SecretSpec{}, err
	}
	return v1beta1.SecretSpec{
		Type: got.Spec.EffectiveType(), Connector: got.Spec.Connector, Identity: got.Spec.Identity,
	}, nil
}

// gitIdentityExpander expands `git.identityRef` references into the author,
// committer, credentials and signing they stand for (issue #1548, §3). It is
// the git delivery handler. Expansion happens per level, before the levels are
// merged, so the fields a level states next to the reference override the
// expanded ones and the existing merge rules (credentials by host, the signing
// group replaced whole) apply to an identity like to any hand-written block.
type gitIdentityExpander struct {
	r runner.Runner
	// desc is the git connector descriptor the blueprint carries; nil when it
	// has no git connector component.
	desc *v1beta1.BlueprintConnector
	// refs records the identity each level's default git block named and that
	// was read, by origin.
	refs map[string]v1beta1.ContainerSecretRef
	// named records the levels whose default git block named an identity,
	// whether or not it could be read.
	named map[string]bool
	// errs is the expansion error of each level's default git block, by origin;
	// repoErrs that of each repo fill, by origin and then repo name. A level a
	// deeper one overrides does not reach the cell, so only the winning level's
	// error counts — see settle.
	errs     map[string]error
	repoErrs map[string]map[string]error
	// err is the expansion error that reaches the cell, set by settle once the
	// levels are merged. A render fails on it; a status reports it.
	err error
}

// gitLevels are the levels a git identity can be named at, deepest first.
func gitLevels() []string {
	return []string{
		v1beta1.RenderOriginCell, v1beta1.RenderOriginConfig,
		v1beta1.RenderOriginStack, v1beta1.RenderOriginSpace, v1beta1.RenderOriginRealm,
	}
}

// gitIdentity is a git identity Secret, read.
type gitIdentity struct {
	ref            v1beta1.ContainerSecretRef
	sshKey         bool
	author         *v1beta1.GitIdentity
	committer      *v1beta1.GitIdentity
	hosts          []string
	sign           []string
	allowedSigners *v1beta1.ContainerSecretRef
}

// load reads the identity a reference names and checks it is one.
func (e *gitIdentityExpander) load(ref v1beta1.ContainerSecretRef) (gitIdentity, error) {
	ref = trimSecretRef(ref)
	where := connector.FormatSecretRef(ref)
	spec, err := readableSecret(e.r, ref)
	if err != nil {
		if errors.Is(err, errdefs.ErrSecretNotFound) {
			return gitIdentity{}, fmt.Errorf(
				"%w: git.identityRef %s: the secret does not exist", errdefs.ErrConnectorIdentity, where)
		}
		return gitIdentity{}, fmt.Errorf("read git identity %s: %w", where, err)
	}
	if strings.TrimSpace(spec.Connector) == "" {
		return gitIdentity{}, fmt.Errorf(
			"%w: git.identityRef %s: the secret is not a git identity (it carries no connector tag); "+
				"create it with `kuke create secret --connector %s`",
			errdefs.ErrConnectorIdentity, where, connector.GitIdentityTag)
	}
	desc := v1beta1.BlueprintConnector{
		Name:        connector.GitIdentityTag,
		Delivery:    v1beta1.ConnectorDeliveryGit,
		SecretTypes: []string{v1beta1.SecretTypeSSHKey, v1beta1.SecretTypeBasicAuth},
	}
	if e.desc != nil {
		desc = *e.desc
	} else if spec.Connector != connector.GitIdentityTag {
		return gitIdentity{}, fmt.Errorf(
			"%w: git.identityRef %s: it is an identity of connector %q, not %q",
			errdefs.ErrConnectorIdentity, where, spec.Connector, connector.GitIdentityTag)
	}
	if e.desc != nil {
		if checkErr := connector.CheckSecret(desc, spec); checkErr != nil {
			return gitIdentity{}, fmt.Errorf("git.identityRef %s: %w", where, checkErr)
		}
	} else if !slices.Contains(desc.SecretTypes, spec.Type) {
		return gitIdentity{}, fmt.Errorf(
			"%w: git.identityRef %s: a git identity is an ssh-key or basic-auth secret, not %s",
			errdefs.ErrConnectorIdentity, where, spec.Type)
	}

	id := gitIdentity{ref: ref, sshKey: spec.Type == v1beta1.SecretTypeSSHKey}
	person := func(field string) (*v1beta1.GitIdentity, error) {
		value := strings.TrimSpace(spec.Identity[field])
		if value == "" {
			return nil, nil //nolint:nilnil // an unset identity field is not an error
		}
		name, email, parseErr := connector.ParsePerson(value)
		if parseErr != nil {
			return nil, fmt.Errorf(
				"%w: git.identityRef %s: identity field %q: %w", errdefs.ErrConnectorIdentity, where, field, parseErr)
		}
		return &v1beta1.GitIdentity{Name: name, Email: email}, nil
	}
	if id.author, err = person(connector.GitFieldAuthor); err != nil {
		return gitIdentity{}, err
	}
	if id.committer, err = person(connector.GitFieldCommitter); err != nil {
		return gitIdentity{}, err
	}
	id.hosts = connector.ParseList(spec.Identity[connector.GitFieldHosts])
	if id.sshKey {
		id.sign = connector.ParseList(spec.Identity[connector.GitFieldSign])
		if value := strings.TrimSpace(spec.Identity[connector.GitFieldAllowedSignersRef]); value != "" {
			signers, parseErr := connector.ParseSecretRef(value, ref)
			if parseErr != nil {
				return gitIdentity{}, fmt.Errorf(
					"%w: git.identityRef %s: identity field %q: %w",
					errdefs.ErrConnectorIdentity, where, connector.GitFieldAllowedSignersRef, parseErr)
			}
			id.allowedSigners = &signers
		}
	}
	return id, nil
}

// settle picks, once the levels are merged, the expansion error that reaches
// the cell: that of the deepest level whose default git block names an
// identity, and that of each repo fill that wins its repo (values carries the
// level each repo fill came from). An identity a deeper level overrides is
// shadowed: it fails neither the render nor the status, as a shadowed secret
// fill does not. Without a git connector the Config does not ask for a git
// identity, so a Realm, Space or Stack default that cannot be read does not fail
// it — otherwise one broken default would take down every cell in its scope;
// an identity the Config itself names still does.
func (e *gitIdentityExpander) settle(values []v1beta1.RenderedValue) {
	e.err = nil
	for _, origin := range gitLevels() {
		if !e.named[origin] {
			continue
		}
		own := origin == v1beta1.RenderOriginCell || origin == v1beta1.RenderOriginConfig
		if err := e.errs[origin]; err != nil && (e.desc != nil || own) {
			e.err = err
			return
		}
		break
	}
	repoOrigins := map[string]string{}
	for _, v := range values {
		if v.Kind == v1beta1.RenderKindRepo {
			repoOrigins[v.Key] = v.Origin
		}
	}
	names := make([]string, 0, len(repoOrigins))
	for name := range repoOrigins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := e.repoErrs[repoOrigins[name]][name]; err != nil {
			e.err = err
			return
		}
	}
}

// container expands the reference of a default git block. The returned block
// carries no reference; a nil or reference-less block is returned as is.
func (e *gitIdentityExpander) container(origin string, git *v1beta1.ContainerGit) *v1beta1.ContainerGit {
	if git == nil || git.IdentityRef == nil {
		return git
	}
	explicit := *git
	explicit.IdentityRef = nil
	if e.named == nil {
		e.named = map[string]bool{}
	}
	e.named[origin] = true
	id, err := e.load(*git.IdentityRef)
	if err != nil {
		if e.errs == nil {
			e.errs = map[string]error{}
		}
		e.errs[origin] = fmt.Errorf("%s: %w", origin, err)
		return &explicit
	}
	if e.refs == nil {
		e.refs = map[string]v1beta1.ContainerSecretRef{}
	}
	e.refs[origin] = id.ref

	expanded := &v1beta1.ContainerGit{Author: id.author, Committer: id.committer}
	for _, host := range id.hosts {
		ref := id.ref
		expanded.Credentials = append(expanded.Credentials, v1beta1.GitCredential{Host: host, SecretRef: &ref})
	}
	if id.sshKey {
		ref := id.ref
		expanded.Signing = &v1beta1.GitSigning{SecretRef: &ref, AllowedSignersRef: id.allowedSigners}
		expanded.Sign = id.sign
	}
	merged, _ := cellconfig.MergeGit([]cellconfig.GitLayer{
		{Origin: origin, Git: expanded},
		{Origin: origin, Git: &explicit},
	})
	return merged
}

// repo expands the reference of one source's git block: the identity becomes
// that source's author, committer, credential and — for an SSH key — signing
// key. The identity's hosts are not consulted: naming it on a repo says it is
// the one that repo uses.
func (e *gitIdentityExpander) repo(origin, name string, git *v1beta1.RepoGit) *v1beta1.RepoGit {
	if git == nil || git.IdentityRef == nil {
		return git
	}
	out := *git
	out.IdentityRef = nil
	id, err := e.load(*git.IdentityRef)
	if err != nil {
		if e.repoErrs == nil {
			e.repoErrs = map[string]map[string]error{}
		}
		if e.repoErrs[origin] == nil {
			e.repoErrs[origin] = map[string]error{}
		}
		e.repoErrs[origin][name] = fmt.Errorf("%s repo %s: %w", origin, name, err)
		return &out
	}
	if out.Author == nil {
		out.Author = id.author
	}
	if out.Committer == nil {
		out.Committer = id.committer
	}
	if out.Credential == nil {
		ref := id.ref
		out.Credential = &v1beta1.GitCredential{SecretRef: &ref}
	}
	if id.sshKey {
		if out.Signing == nil {
			ref := id.ref
			out.Signing = &v1beta1.GitSigning{SecretRef: &ref, AllowedSignersRef: id.allowedSigners}
		}
		if len(out.Sign) == 0 {
			out.Sign = id.sign
		}
	}
	return &out
}

// repos expands the references of a set of repo fills.
func (e *gitIdentityExpander) repos(
	origin string, fills map[string]v1beta1.CellConfigRepoFill,
) map[string]v1beta1.CellConfigRepoFill {
	needs := false
	for _, fill := range fills {
		if fill.Git != nil && fill.Git.IdentityRef != nil {
			needs = true
			break
		}
	}
	if !needs {
		return fills
	}
	out := make(map[string]v1beta1.CellConfigRepoFill, len(fills))
	for name, fill := range fills {
		fill.Git = e.repo(origin, name, fill.Git)
		out[name] = fill
	}
	return out
}

// defaults expands the references of one defaults level.
func (e *gitIdentityExpander) defaults(origin string, d *v1beta1.ScopeDefaults) *v1beta1.ScopeDefaults {
	if d == nil {
		return nil
	}
	out := *d
	out.Git = e.container(origin, d.Git)
	out.Repos = e.repos(origin, d.Repos)
	return &out
}

// identity returns the git identity that wins the merge — the one the deepest
// level named — and that level; nil when that identity could not be read.
func (e *gitIdentityExpander) identity() (*v1beta1.ContainerSecretRef, string) {
	for _, origin := range gitLevels() {
		if !e.named[origin] {
			continue
		}
		if ref, ok := e.refs[origin]; ok {
			return &ref, origin
		}
		return nil, ""
	}
	return nil, ""
}

// connectorStatuses reports how every requirement resolves against the merged
// Config: the identity that fills it and the level it came from, or why
// nothing does.
func connectorStatuses(
	r runner.Runner,
	reqs []connectorRequirement,
	merged v1beta1.CellConfigDoc,
	values []v1beta1.RenderedValue,
	git *gitIdentityExpander,
) []v1beta1.ConnectorStatus {
	origins := map[string]string{}
	gitOrigin := ""
	for _, v := range values {
		switch v.Kind {
		case v1beta1.RenderKindSecret:
			origins[v.Key] = v.Origin
		case v1beta1.RenderKindGit:
			if gitOrigin == "" && strings.HasPrefix(v.Key, "default.") {
				gitOrigin = v.Origin
			}
		}
	}

	out := make([]v1beta1.ConnectorStatus, 0, len(reqs))
	for _, req := range reqs {
		desc := req.Connector
		status := v1beta1.ConnectorStatus{
			Connector: desc.Name,
			Title:     desc.Title,
			Component: req.Component,
			Method:    desc.Method,
			Delivery:  desc.Delivery,
			Slot:      req.Slot,
			State:     v1beta1.ConnectorUnresolved,
			Required:  connector.NeedsIdentity(desc.Delivery) && !desc.Optional,
		}
		switch desc.Delivery {
		case v1beta1.ConnectorDeliveryGit:
			gitStatus(&status, merged.Spec.Git, gitOrigin, git)
		case v1beta1.ConnectorDeliveryEnv, v1beta1.ConnectorDeliveryFile:
			slotStatus(r, &status, desc, merged.Spec.Secrets[req.Slot], origins[req.Slot])
		default:
			// A login kept in the harness state volume has no stored identity.
			status.State = v1beta1.ConnectorResolved
			status.Volume = strings.TrimSpace(merged.Spec.Values[stateVolumeValue])
		}
		out = append(out, status)
	}
	return out
}

// gitStatus fills the status of the git connector from the merged git block.
func gitStatus(
	status *v1beta1.ConnectorStatus, merged *v1beta1.ContainerGit, origin string, git *gitIdentityExpander,
) {
	if git.err != nil {
		status.Message = git.err.Error()
		return
	}
	if merged == nil || (merged.Author == nil && len(merged.Credentials) == 0 &&
		merged.Signing == nil && merged.SigningKey == "") {
		status.Message = "no git identity is set on the cell, the Config or a Realm, Space or Stack default"
		return
	}
	status.State = v1beta1.ConnectorResolved
	status.Origin = origin
	if ref, refOrigin := git.identity(); ref != nil {
		status.Identity, status.Origin = ref, refOrigin
	}
}

// slotStatus fills the status of a connector delivered through a secret slot.
func slotStatus(
	r runner.Runner,
	status *v1beta1.ConnectorStatus,
	desc v1beta1.BlueprintConnector,
	fill v1beta1.CellConfigSecretFill,
	origin string,
) {
	if fill.SecretRef == nil {
		status.Message = fmt.Sprintf(
			"no identity fills secret slot %q on the cell, the Config or a Realm, Space or Stack default",
			status.Slot)
		return
	}
	ref := trimSecretRef(*fill.SecretRef)
	status.Identity, status.Origin = &ref, origin
	spec, err := readableSecret(r, ref)
	if err != nil {
		if errors.Is(err, errdefs.ErrSecretNotFound) {
			status.Message = fmt.Sprintf("secret %s does not exist", connector.FormatSecretRef(ref))
			return
		}
		status.Message = fmt.Sprintf("read secret %s: %v", connector.FormatSecretRef(ref), err)
		return
	}
	if checkErr := connector.CheckSecret(desc, spec); checkErr != nil {
		status.Message = fmt.Sprintf("secret %s: %v", connector.FormatSecretRef(ref), checkErr)
		return
	}
	status.State = v1beta1.ConnectorResolved
}

// connectorError is the error a render fails with when a connector cannot
// deliver what the cell needs: a required connector nothing fills, or an
// identity that was named — by the cell, the Config or a default — and cannot
// be used. It names the connector, so the operator knows which identity to
// supply. A connector the cell may start without and that names no identity
// is not an error.
func connectorError(cfgName string, statuses []v1beta1.ConnectorStatus, git *gitIdentityExpander) error {
	if git.err != nil {
		return fmt.Errorf("config %q: %w", cfgName, git.err)
	}
	for _, status := range statuses {
		if status.State == v1beta1.ConnectorResolved {
			continue
		}
		if status.Identity != nil {
			return fmt.Errorf(
				"%w: config %q, connector %q: the identity set at the %s level cannot be used: %s",
				errdefs.ErrConnectorIdentity, cfgName, status.Connector, status.Origin, status.Message)
		}
		if !status.Required {
			continue
		}
		hint := "set a default git identity (git.identityRef) on the realm, space or stack, " +
			"or set it in the CellConfig's git block"
		if status.Slot != "" {
			hint = fmt.Sprintf(
				"set a default for secret slot %q on the realm, space or stack, or fill it in the CellConfig",
				status.Slot)
		}
		return fmt.Errorf(
			"%w: config %q requires connector %q (component %q) and no identity fills it: %s, "+
				"or pass --connector %s=<secret> for one cell",
			errdefs.ErrConnectorUnresolved, cfgName, status.Connector, status.Component, hint, status.Connector)
	}
	return nil
}

// connectorOverridesFromProvenance converts the overrides a cell recorded back
// into the binding a re-render uses.
func connectorOverridesFromProvenance(
	in map[string]intmodel.ContainerSecretRef,
) map[string]v1beta1.ContainerSecretRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]v1beta1.ContainerSecretRef, len(in))
	for name, ref := range in {
		out[name] = v1beta1.ContainerSecretRef{
			Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
		}
	}
	return out
}
