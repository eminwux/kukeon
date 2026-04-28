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
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/eminwux/kukeon/internal/consts"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// UserGroupRemover removes a system user and group from the host. The real
// implementation shells out to userdel/groupdel; tests inject a stub.
type UserGroupRemover func(ctx context.Context, user, group string) (UserGroupRemovalReport, error)

// UserGroupRemovalReport mirrors the user/group fields on UninstallReport
// without forcing the implementation to know the report type's layout.
type UserGroupRemovalReport struct {
	UserExisted  bool
	UserRemoved  bool
	GroupExisted bool
	GroupRemoved bool
}

// UninstallOptions configures Uninstall.
type UninstallOptions struct {
	// SocketDir is the parent directory of the kukeond unix socket
	// (typically /run/kukeon). Removed recursively when non-empty.
	SocketDir string
	// SystemUser/SystemGroup are the names of the kukeon system user and
	// group to remove. Defaults to "kukeon"/"kukeon" when empty.
	SystemUser  string
	SystemGroup string
	// SkipUserGroup skips userdel/groupdel. Set in tests where touching
	// the host's /etc/passwd is undesirable.
	SkipUserGroup bool
	// UserGroupRemover overrides the default userdel/groupdel routine.
	// Tests inject a stub; production callers leave it nil.
	UserGroupRemover UserGroupRemover
}

// RealmPurgeOutcome reports the result of purging a single realm.
type RealmPurgeOutcome struct {
	Name      string
	Namespace string
	Purged    bool
	Err       error
}

// UninstallReport summarizes what Uninstall did.
type UninstallReport struct {
	Realms          []RealmPurgeOutcome
	SocketDir       string
	SocketDirExists bool
	SocketDirRemove bool
	RunPath         string
	RunPathExists   bool
	RunPathRemove   bool
	UserName        string
	UserExisted     bool
	UserRemoved     bool
	GroupName       string
	GroupExisted    bool
	GroupRemoved    bool
}

// Uninstall performs a comprehensive teardown of all kukeon runtime state.
// Steps (in order):
//  1. Purge every realm with --cascade --force (drains spaces/stacks/cells/
//     containers + tasks; the kukeond cell living inside `kuke-system` is
//     killed and deleted as part of that cascade). Both well-known realms
//     (`default`, `kuke-system`) are purged unconditionally so containerd
//     namespaces left behind by an earlier partial uninstall are cleaned up
//     even when on-disk metadata is already gone.
//  2. RemoveAll on SocketDir (typically /run/kukeon).
//  3. RemoveAll on the run path (typically /opt/kukeon).
//  4. Remove the kukeon system user and group (no-op if absent).
//
// Any step's error is recorded in the report. The first non-nil step error
// is returned so callers can surface "uninstall failed at step X" without
// dropping subsequent best-effort cleanup.
func (b *Exec) Uninstall(opts UninstallOptions) (UninstallReport, error) {
	report := UninstallReport{
		SocketDir: opts.SocketDir,
		RunPath:   b.opts.RunPath,
	}

	user := opts.SystemUser
	if user == "" {
		user = "kukeon"
	}
	group := opts.SystemGroup
	if group == "" {
		group = "kukeon"
	}
	report.UserName = user
	report.GroupName = group

	var firstErr error
	recordErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Step 1: enumerate realms and purge each.
	realms, listErr := b.collectRealmsForUninstall()
	if listErr != nil {
		// If listing fails we still attempt the well-known realms — partial
		// uninstall cleanup must not be blocked by a stale metadata read.
		b.logger.WarnContext(
			b.ctx,
			"uninstall: failed to list realms; proceeding with well-known realms only",
			"error",
			listErr,
		)
	}

	for _, realm := range realms {
		outcome := RealmPurgeOutcome{
			Name:      realm.Metadata.Name,
			Namespace: realm.Spec.Namespace,
		}
		if _, err := b.PurgeRealm(realm, true, true); err != nil {
			outcome.Err = err
			recordErr(fmt.Errorf("purge realm %q: %w", realm.Metadata.Name, err))
		} else {
			outcome.Purged = true
		}
		report.Realms = append(report.Realms, outcome)
	}

	// Step 2: tear down /run/kukeon.
	if opts.SocketDir != "" {
		exists, removed, err := removePathIfExists(opts.SocketDir)
		report.SocketDirExists = exists
		report.SocketDirRemove = removed
		if err != nil {
			recordErr(fmt.Errorf("remove socket dir %q: %w", opts.SocketDir, err))
		}
	}

	// Step 3: tear down /opt/kukeon.
	if b.opts.RunPath != "" {
		exists, removed, err := removePathIfExists(b.opts.RunPath)
		report.RunPathExists = exists
		report.RunPathRemove = removed
		if err != nil {
			recordErr(fmt.Errorf("remove run path %q: %w", b.opts.RunPath, err))
		}
	}

	// Step 4: drop the kukeon system user/group.
	if !opts.SkipUserGroup {
		remover := opts.UserGroupRemover
		if remover == nil {
			remover = removeSystemUserGroup
		}
		userReport, err := remover(b.ctx, user, group)
		report.UserExisted = userReport.UserExisted
		report.UserRemoved = userReport.UserRemoved
		report.GroupExisted = userReport.GroupExisted
		report.GroupRemoved = userReport.GroupRemoved
		if err != nil {
			recordErr(fmt.Errorf("remove user/group: %w", err))
		}
	}

	return report, firstErr
}

// collectRealmsForUninstall returns every realm that should be purged,
// merging on-disk metadata with the two well-known kukeon realms (so a
// partial uninstall whose metadata was already wiped still cleans up
// containerd namespaces by name).
func (b *Exec) collectRealmsForUninstall() ([]intmodel.Realm, error) {
	wellKnown := []intmodel.Realm{
		{
			Metadata: intmodel.RealmMetadata{Name: consts.KukeonDefaultRealmName},
			Spec:     intmodel.RealmSpec{Namespace: consts.RealmNamespace(consts.KukeonDefaultRealmName)},
		},
		{
			Metadata: intmodel.RealmMetadata{Name: consts.KukeSystemRealmName},
			Spec:     intmodel.RealmSpec{Namespace: consts.RealmNamespace(consts.KukeSystemRealmName)},
		},
	}

	listed, err := b.runner.ListRealms()
	if err != nil {
		return wellKnown, err
	}

	seen := make(map[string]struct{}, len(listed)+len(wellKnown))
	out := make([]intmodel.Realm, 0, len(listed)+len(wellKnown))
	for _, realm := range listed {
		seen[realm.Metadata.Name] = struct{}{}
		out = append(out, realm)
	}
	for _, realm := range wellKnown {
		if _, ok := seen[realm.Metadata.Name]; ok {
			continue
		}
		out = append(out, realm)
	}
	return out, nil
}

// removePathIfExists is a thin wrapper around os.RemoveAll that reports
// whether the path was present before removal. It treats os.IsNotExist as
// "already gone" so a re-run of `kuke uninstall` on a clean machine is a
// no-op rather than an error.
func removePathIfExists(path string) (bool, bool, error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, false, nil
		}
		return false, false, statErr
	}
	if rmErr := os.RemoveAll(path); rmErr != nil {
		return true, false, rmErr
	}
	return true, true, nil
}

// removeSystemUserGroup invokes userdel/groupdel for the kukeon system
// account. Both lookups go through `id`/`getent` so the function is a
// pure no-op on hosts where the account never existed (matches the
// "no-op if absent" acceptance criterion). userdel runs before groupdel
// so the primary group can be removed once the user is gone.
func removeSystemUserGroup(ctx context.Context, user, group string) (UserGroupRemovalReport, error) {
	var report UserGroupRemovalReport

	report.UserExisted = lookupUser(ctx, user)
	if report.UserExisted {
		if delErr := exec.CommandContext(ctx, "userdel", user).Run(); delErr != nil {
			// userdel returns 6 ("specified user doesn't exist") if the
			// account vanished between lookup and delete — treat that as
			// idempotent success.
			if lookupUser(ctx, user) {
				return report, fmt.Errorf("userdel %q: %w", user, delErr)
			}
			report.UserRemoved = true
		} else {
			report.UserRemoved = true
		}
	}

	report.GroupExisted = lookupGroup(ctx, group)
	if report.GroupExisted {
		if delErr := exec.CommandContext(ctx, "groupdel", group).Run(); delErr != nil {
			if lookupGroup(ctx, group) {
				return report, fmt.Errorf("groupdel %q: %w", group, delErr)
			}
			report.GroupRemoved = true
		} else {
			report.GroupRemoved = true
		}
	}
	return report, nil
}

func lookupUser(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "id", "-u", name).Run() == nil
}

func lookupGroup(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "getent", "group", name).Run() == nil
}
