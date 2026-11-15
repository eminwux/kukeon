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

package initialize

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// Client deliberately has no cell lifecycle methods. Init can only read scope,
// create explicitly sourced secrets, and apply blueprint/config documents.
type Client interface {
	Close() error
	Ping(context.Context) error
	ListRealms(context.Context) ([]api.RealmDoc, error)
	ListSpaces(context.Context, string) ([]api.SpaceDoc, error)
	ListStacks(context.Context, string, string) ([]api.StackDoc, error)
	ListCells(context.Context, string, string, string) ([]api.CellDoc, error)
	GetSecret(context.Context, api.SecretDoc) (kukeonv1.GetSecretResult, error)
	CreateSecret(context.Context, api.SecretDoc) (kukeonv1.CreateSecretResult, error)
	ApplyDocumentsForTeam(context.Context, []byte, string) (kukeonv1.ApplyDocumentsResult, error)
}

var _ Client = (*kukeonv1.UnixClient)(nil)

// Apply preflights all sources/scopes before writing anything to the daemon.
// It never retries an ambiguous write. Secrets stay outside the document stream.
func Apply(ctx context.Context, client Client, result *compose.Result) error {
	return apply(ctx, client, result, "kuke team init")
}

func apply(ctx context.Context, client Client, result *compose.Result, command string) error {
	raw, err := result.MarshalYAML()
	if err != nil {
		return err
	}
	if result.Entry == nil || len(result.Pairs) == 0 {
		return errors.New("preflight: empty team composition")
	}
	if err = client.Ping(ctx); err != nil {
		return fmt.Errorf("kukeond connection/preflight: %w", err)
	}
	for _, pair := range result.Pairs {
		md := pair.Blueprint.Metadata
		if err = checkScope(ctx, client, config.SecretRef{Realm: md.Realm, Space: md.Space, Stack: md.Stack}, command); err != nil {
			return err
		}
	}
	secrets, err := prepareSecrets(ctx, client, result, command)
	if err != nil {
		return err
	}
	for _, secret := range secrets {
		// Close most of the preflight/create window and reuse a competing
		// creator's value. The existing API has no atomic create-only gate.
		ref := secret.Metadata
		lookup := secret
		lookup.Spec.Data = ""
		got, lookupErr := client.GetSecret(ctx, lookup)
		if lookupErr != nil {
			return requestError("recheck", "secret "+ref.Name, lookupErr)
		}
		if got.MetadataExists {
			continue
		}
		if _, err = client.CreateSecret(ctx, secret); err != nil {
			return requestError(
				"creation (write not retried; rerun "+command+" to check existence)",
				"secret "+ref.Name,
				err,
			)
		}
	}
	applied, err := client.ApplyDocumentsForTeam(ctx, raw, result.Entry.Metadata.Name)
	if err != nil {
		return fmt.Errorf("kukeond apply RPC failed (application may be partial; registration unchanged): %w", err)
	}
	return checkResults(applied, len(result.Pairs)*resourcesPerPair, command)
}

func checkResults(result kukeonv1.ApplyDocumentsResult, expected int, command string) error {
	var failures []string
	accepted := 0
	for _, resource := range result.Resources {
		switch {
		case resource.Action == "failed" || resource.Error != "":
			failures = append(failures, fmt.Sprintf("%s %s: %s", resource.Kind, resource.Name, resource.Error))
		case resource.Action == "created" || resource.Action == "updated" || resource.Action == "unchanged":
			accepted++
		case resource.Action != "pruned":
			failures = append(
				failures,
				fmt.Sprintf("%s %s: unexpected action %q", resource.Kind, resource.Name, resource.Action),
			)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf(
			"per-resource application failed (%d accepted, %d failures); registration unchanged; rerun %s: %s",
			accepted,
			len(failures),
			command,
			strings.Join(failures, "; "),
		)
	}
	if accepted != expected {
		return fmt.Errorf(
			"incomplete apply response: %d of %d resources accepted; registration unchanged",
			accepted,
			expected,
		)
	}
	return nil
}

func prepareSecrets(
	ctx context.Context,
	client Client,
	result *compose.Result,
	command string,
) ([]api.SecretDoc, error) {
	// Deduplicate by complete runtime identity, not role/harness identifier.
	requirements := map[config.SecretRef]*config.SecretSource{}
	var order []config.SecretRef
	for _, pair := range result.Pairs {
		for _, required := range pair.Secrets {
			previous, exists := requirements[required.Reference]
			if !exists {
				order = append(order, required.Reference)
			}
			if previous != nil && required.Source != nil && *previous != *required.Source {
				return nil, fmt.Errorf("preflight: conflicting creation sources for secret %s", required.Reference.Name)
			}
			if !exists || previous == nil {
				requirements[required.Reference] = required.Source
			}
		}
	}
	var pending []api.SecretDoc
	for _, ref := range order {
		doc := api.SecretDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
			Metadata: api.SecretMetadata{
				Name:  ref.Name,
				Realm: ref.Realm,
				Space: ref.Space,
				Stack: ref.Stack,
				Cell:  ref.Cell,
			},
		}
		got, err := client.GetSecret(ctx, doc)
		if err != nil {
			return nil, requestError("lookup", "secret "+ref.Name, err)
		}
		if got.MetadataExists {
			continue
		}
		if err = checkScope(ctx, client, ref, command); err != nil {
			return nil, err
		}
		source := requirements[ref]
		if source == nil {
			return nil, missingSecret(ref, command)
		}
		value, err := readSecret(source, command)
		if err != nil {
			return nil, fmt.Errorf("preflight secret %s: %w", ref.Name, err)
		}
		doc.Spec.Data = value
		pending = append(pending, doc)
	}
	return pending, nil
}

func readSecret(source *config.SecretSource, command string) (string, error) {
	if (source.Env == "") == (source.File == "") {
		return "", errors.New("configure exactly one env or file creation source")
	}
	var value string
	if source.Env != "" {
		value = os.Getenv(source.Env)
	} else {
		raw, err := os.ReadFile(source.File)
		if err != nil {
			return "", errors.New("cannot read configured source file; check its path and permissions")
		}
		value = strings.TrimRight(string(raw), "\n")
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("configured source is missing or empty; supply its value and rerun %s", command)
	}
	return value, nil
}

func missingSecret(ref config.SecretRef, command string) error {
	if ref.Cell != "" {
		return fmt.Errorf(
			"required secret %s is missing at realm=%s space=%s stack=%s cell=%s; kuke create secret has no --cell flag: create a Secret manifest with these metadata coordinates using kuke apply -f /path/to/secret.yaml, then rerun %s",
			ref.Name,
			ref.Realm,
			ref.Space,
			ref.Stack,
			ref.Cell,
			command,
		)
	}
	createCommand := "kuke create secret " + ref.Name + " --realm " + ref.Realm
	if ref.Space != "" {
		createCommand += " --space " + ref.Space
	}
	if ref.Stack != "" {
		createCommand += " --stack " + ref.Stack
	}
	return fmt.Errorf(
		"required secret %s is missing; supply your credential file: %s --from-file /path/to/token; then rerun %s (use the same kuke --host/--configuration endpoint)",
		ref.Name,
		createCommand,
		command,
	)
}

func requestError(operation, subject string, err error) error {
	// A remote error can include supplied bytes. Report the operation and class,
	// never its text, including through Unwrap/verbose formatting.
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, rpc.ErrShutdown) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s %s: kukeond transport/connection failed", subject, operation)
	}
	return fmt.Errorf("%s %s: daemon rejected request; check permissions and scope", subject, operation)
}

// Scope preflight reads metadata only. GetRealm also probes containerd, which
// is unnecessary for blueprint/config application and would add a prerequisite.
func checkScope(ctx context.Context, client Client, ref config.SecretRef, command string) error {
	realms, err := client.ListRealms(ctx)
	if err != nil {
		return requestError("prerequisite lookup", "realm "+ref.Realm, err)
	}
	if !slices.ContainsFunc(realms, func(doc api.RealmDoc) bool { return doc.Metadata.Name == ref.Realm }) {
		return fmt.Errorf(
			"preflight: required realm %s is missing; create the realm with kuke before rerunning %s",
			ref.Realm, command,
		)
	}
	if ref.Space == "" {
		return nil
	}
	spaces, err := client.ListSpaces(ctx, ref.Realm)
	if err != nil {
		return requestError("prerequisite lookup", "space "+ref.Space, err)
	}
	if !slices.ContainsFunc(spaces, func(doc api.SpaceDoc) bool { return doc.Metadata.Name == ref.Space }) {
		return fmt.Errorf(
			"preflight: required space %s in realm %s is missing; create the parent scope with kuke before rerunning %s",
			ref.Space,
			ref.Realm,
			command,
		)
	}
	if ref.Stack == "" {
		return nil
	}
	stacks, err := client.ListStacks(ctx, ref.Realm, ref.Space)
	if err != nil {
		return requestError("prerequisite lookup", "stack "+ref.Stack, err)
	}
	if !slices.ContainsFunc(stacks, func(doc api.StackDoc) bool { return doc.Metadata.Name == ref.Stack }) {
		return fmt.Errorf(
			"preflight: required stack %s in realm %s / space %s is missing; create the parent scope with kuke before rerunning %s",
			ref.Stack,
			ref.Realm,
			ref.Space,
			command,
		)
	}
	if ref.Cell == "" {
		return nil
	}
	cells, err := client.ListCells(ctx, ref.Realm, ref.Space, ref.Stack)
	if err != nil {
		return requestError("prerequisite lookup", "cell "+ref.Cell, err)
	}
	if !slices.ContainsFunc(cells, func(doc api.CellDoc) bool { return doc.Metadata.Name == ref.Cell }) {
		return fmt.Errorf(
			"preflight: required parent cell %s in %s/%s/%s is missing; provision the parent with kuke before rerunning %s",
			ref.Cell,
			ref.Realm,
			ref.Space,
			ref.Stack,
			command,
		)
	}
	return nil
}
