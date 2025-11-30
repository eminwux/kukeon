//go:build !integration

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

package controller_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// fakeRunner implements runner.Runner interface for testing.
type fakeRunner struct {
	// Realm methods
	GetRealmFn                       func(realm intmodel.Realm) (intmodel.Realm, error)
	ListRealmsFn                     func() ([]intmodel.Realm, error)
	CreateRealmFn                    func(realm intmodel.Realm) (intmodel.Realm, error)
	EnsureRealmFn                    func(realm intmodel.Realm) (intmodel.Realm, error)
	ExistsRealmContainerdNamespaceFn func(namespace string) (bool, error)
	DeleteRealmFn                    func(realm intmodel.Realm) error

	// Space methods
	GetSpaceFn             func(space intmodel.Space) (intmodel.Space, error)
	ListSpacesFn           func(realmName string) ([]intmodel.Space, error)
	CreateSpaceFn          func(space intmodel.Space) (intmodel.Space, error)
	EnsureSpaceFn          func(space intmodel.Space) (intmodel.Space, error)
	ExistsSpaceCNIConfigFn func(space intmodel.Space) (bool, error)
	DeleteSpaceFn          func(space intmodel.Space) error

	// Stack methods
	GetStackFn    func(stack intmodel.Stack) (intmodel.Stack, error)
	ListStacksFn  func(realmName, spaceName string) ([]intmodel.Stack, error)
	CreateStackFn func(stack intmodel.Stack) (intmodel.Stack, error)
	EnsureStackFn func(stack intmodel.Stack) (intmodel.Stack, error)
	DeleteStackFn func(stack intmodel.Stack) error

	// Cell methods
	GetCellFn                 func(cell intmodel.Cell) (intmodel.Cell, error)
	ListCellsFn               func(realmName, spaceName, stackName string) ([]intmodel.Cell, error)
	CreateCellFn              func(cell intmodel.Cell) (intmodel.Cell, error)
	EnsureCellFn              func(cell intmodel.Cell) (intmodel.Cell, error)
	StartCellFn               func(cell intmodel.Cell) error
	StopCellFn                func(cell intmodel.Cell) error
	KillCellFn                func(cell intmodel.Cell) error
	DeleteCellFn              func(cell intmodel.Cell) error
	ExistsCellRootContainerFn func(cell intmodel.Cell) (bool, error)
	UpdateCellMetadataFn      func(cell intmodel.Cell) error

	// Container methods
	ListContainersFn  func(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error)
	CreateContainerFn func(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error)
	EnsureContainerFn func(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error)
	StartContainerFn  func(cell intmodel.Cell, containerID string) error
	StopContainerFn   func(cell intmodel.Cell, containerID string) error
	KillContainerFn   func(cell intmodel.Cell, containerID string) error
	DeleteContainerFn func(cell intmodel.Cell, containerID string) error

	// Utility methods
	ExistsCgroupFn func(doc any) (bool, error)

	// Purge methods
	PurgeRealmFn     func(realm intmodel.Realm) error
	PurgeSpaceFn     func(space intmodel.Space) error
	PurgeStackFn     func(stack intmodel.Stack) error
	PurgeCellFn      func(cell intmodel.Cell) error
	PurgeContainerFn func(realm intmodel.Realm, containerID string) error

	// Bootstrap
	BootstrapCNIFn func(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)

	// Close
	CloseFn func() error
}

// Realm methods

func (f *fakeRunner) GetRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	if f.GetRealmFn != nil {
		return f.GetRealmFn(realm)
	}
	return intmodel.Realm{}, errors.New("unexpected call to GetRealm")
}

func (f *fakeRunner) ListRealms() ([]intmodel.Realm, error) {
	if f.ListRealmsFn != nil {
		return f.ListRealmsFn()
	}
	return nil, errors.New("unexpected call to ListRealms")
}

func (f *fakeRunner) CreateRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	if f.CreateRealmFn != nil {
		return f.CreateRealmFn(realm)
	}
	return intmodel.Realm{}, errors.New("unexpected call to CreateRealm")
}

func (f *fakeRunner) EnsureRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	if f.EnsureRealmFn != nil {
		return f.EnsureRealmFn(realm)
	}
	return intmodel.Realm{}, errors.New("unexpected call to EnsureRealm")
}

func (f *fakeRunner) ExistsRealmContainerdNamespace(namespace string) (bool, error) {
	if f.ExistsRealmContainerdNamespaceFn != nil {
		return f.ExistsRealmContainerdNamespaceFn(namespace)
	}
	return false, errors.New("unexpected call to ExistsRealmContainerdNamespace")
}

func (f *fakeRunner) DeleteRealm(realm intmodel.Realm) error {
	if f.DeleteRealmFn != nil {
		return f.DeleteRealmFn(realm)
	}
	return errors.New("unexpected call to DeleteRealm")
}

// Space methods

func (f *fakeRunner) GetSpace(space intmodel.Space) (intmodel.Space, error) {
	if f.GetSpaceFn != nil {
		return f.GetSpaceFn(space)
	}
	return intmodel.Space{}, errors.New("unexpected call to GetSpace")
}

func (f *fakeRunner) ListSpaces(realmName string) ([]intmodel.Space, error) {
	if f.ListSpacesFn != nil {
		return f.ListSpacesFn(realmName)
	}
	return nil, errors.New("unexpected call to ListSpaces")
}

func (f *fakeRunner) CreateSpace(space intmodel.Space) (intmodel.Space, error) {
	if f.CreateSpaceFn != nil {
		return f.CreateSpaceFn(space)
	}
	return intmodel.Space{}, errors.New("unexpected call to CreateSpace")
}

func (f *fakeRunner) EnsureSpace(space intmodel.Space) (intmodel.Space, error) {
	if f.EnsureSpaceFn != nil {
		return f.EnsureSpaceFn(space)
	}
	return intmodel.Space{}, errors.New("unexpected call to EnsureSpace")
}

func (f *fakeRunner) ExistsSpaceCNIConfig(space intmodel.Space) (bool, error) {
	if f.ExistsSpaceCNIConfigFn != nil {
		return f.ExistsSpaceCNIConfigFn(space)
	}
	return false, errors.New("unexpected call to ExistsSpaceCNIConfig")
}

func (f *fakeRunner) DeleteSpace(space intmodel.Space) error {
	if f.DeleteSpaceFn != nil {
		return f.DeleteSpaceFn(space)
	}
	return errors.New("unexpected call to DeleteSpace")
}

// Stack methods

func (f *fakeRunner) GetStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if f.GetStackFn != nil {
		return f.GetStackFn(stack)
	}
	return intmodel.Stack{}, errors.New("unexpected call to GetStack")
}

func (f *fakeRunner) ListStacks(realmName, spaceName string) ([]intmodel.Stack, error) {
	if f.ListStacksFn != nil {
		return f.ListStacksFn(realmName, spaceName)
	}
	return nil, errors.New("unexpected call to ListStacks")
}

func (f *fakeRunner) CreateStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if f.CreateStackFn != nil {
		return f.CreateStackFn(stack)
	}
	return intmodel.Stack{}, errors.New("unexpected call to CreateStack")
}

func (f *fakeRunner) EnsureStack(stack intmodel.Stack) (intmodel.Stack, error) {
	if f.EnsureStackFn != nil {
		return f.EnsureStackFn(stack)
	}
	return intmodel.Stack{}, errors.New("unexpected call to EnsureStack")
}

func (f *fakeRunner) DeleteStack(stack intmodel.Stack) error {
	if f.DeleteStackFn != nil {
		return f.DeleteStackFn(stack)
	}
	return errors.New("unexpected call to DeleteStack")
}

// Cell methods

func (f *fakeRunner) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	if f.GetCellFn != nil {
		return f.GetCellFn(cell)
	}
	return intmodel.Cell{}, errors.New("unexpected call to GetCell")
}

func (f *fakeRunner) ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error) {
	if f.ListCellsFn != nil {
		return f.ListCellsFn(realmName, spaceName, stackName)
	}
	return nil, errors.New("unexpected call to ListCells")
}

func (f *fakeRunner) CreateCell(cell intmodel.Cell) (intmodel.Cell, error) {
	if f.CreateCellFn != nil {
		return f.CreateCellFn(cell)
	}
	return intmodel.Cell{}, errors.New("unexpected call to CreateCell")
}

func (f *fakeRunner) EnsureCell(cell intmodel.Cell) (intmodel.Cell, error) {
	if f.EnsureCellFn != nil {
		return f.EnsureCellFn(cell)
	}
	return intmodel.Cell{}, errors.New("unexpected call to EnsureCell")
}

func (f *fakeRunner) StartCell(cell intmodel.Cell) error {
	if f.StartCellFn != nil {
		return f.StartCellFn(cell)
	}
	return errors.New("unexpected call to StartCell")
}

func (f *fakeRunner) StopCell(cell intmodel.Cell) error {
	if f.StopCellFn != nil {
		return f.StopCellFn(cell)
	}
	return errors.New("unexpected call to StopCell")
}

func (f *fakeRunner) KillCell(cell intmodel.Cell) error {
	if f.KillCellFn != nil {
		return f.KillCellFn(cell)
	}
	return errors.New("unexpected call to KillCell")
}

func (f *fakeRunner) DeleteCell(cell intmodel.Cell) error {
	if f.DeleteCellFn != nil {
		return f.DeleteCellFn(cell)
	}
	return errors.New("unexpected call to DeleteCell")
}

func (f *fakeRunner) ExistsCellRootContainer(cell intmodel.Cell) (bool, error) {
	if f.ExistsCellRootContainerFn != nil {
		return f.ExistsCellRootContainerFn(cell)
	}
	return false, errors.New("unexpected call to ExistsCellRootContainer")
}

func (f *fakeRunner) UpdateCellMetadata(cell intmodel.Cell) error {
	if f.UpdateCellMetadataFn != nil {
		return f.UpdateCellMetadataFn(cell)
	}
	return errors.New("unexpected call to UpdateCellMetadata")
}

// Container methods

func (f *fakeRunner) ListContainers(
	realmName, spaceName, stackName, cellName string,
) ([]intmodel.ContainerSpec, error) {
	if f.ListContainersFn != nil {
		return f.ListContainersFn(realmName, spaceName, stackName, cellName)
	}
	return nil, errors.New("unexpected call to ListContainers")
}

func (f *fakeRunner) CreateContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error) {
	if f.CreateContainerFn != nil {
		return f.CreateContainerFn(cell, container)
	}
	return intmodel.Cell{}, errors.New("unexpected call to CreateContainer")
}

func (f *fakeRunner) EnsureContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error) {
	if f.EnsureContainerFn != nil {
		return f.EnsureContainerFn(cell, container)
	}
	return intmodel.Cell{}, errors.New("unexpected call to EnsureContainer")
}

func (f *fakeRunner) StartContainer(cell intmodel.Cell, containerID string) error {
	if f.StartContainerFn != nil {
		return f.StartContainerFn(cell, containerID)
	}
	return errors.New("unexpected call to StartContainer")
}

func (f *fakeRunner) StopContainer(cell intmodel.Cell, containerID string) error {
	if f.StopContainerFn != nil {
		return f.StopContainerFn(cell, containerID)
	}
	return errors.New("unexpected call to StopContainer")
}

func (f *fakeRunner) KillContainer(cell intmodel.Cell, containerID string) error {
	if f.KillContainerFn != nil {
		return f.KillContainerFn(cell, containerID)
	}
	return errors.New("unexpected call to KillContainer")
}

func (f *fakeRunner) DeleteContainer(cell intmodel.Cell, containerID string) error {
	if f.DeleteContainerFn != nil {
		return f.DeleteContainerFn(cell, containerID)
	}
	return errors.New("unexpected call to DeleteContainer")
}

// Utility methods

func (f *fakeRunner) ExistsCgroup(doc any) (bool, error) {
	if f.ExistsCgroupFn != nil {
		return f.ExistsCgroupFn(doc)
	}
	return false, errors.New("unexpected call to ExistsCgroup")
}

// Purge methods

func (f *fakeRunner) PurgeRealm(realm intmodel.Realm) error {
	if f.PurgeRealmFn != nil {
		return f.PurgeRealmFn(realm)
	}
	return errors.New("unexpected call to PurgeRealm")
}

func (f *fakeRunner) PurgeSpace(space intmodel.Space) error {
	if f.PurgeSpaceFn != nil {
		return f.PurgeSpaceFn(space)
	}
	return errors.New("unexpected call to PurgeSpace")
}

func (f *fakeRunner) PurgeStack(stack intmodel.Stack) error {
	if f.PurgeStackFn != nil {
		return f.PurgeStackFn(stack)
	}
	return errors.New("unexpected call to PurgeStack")
}

func (f *fakeRunner) PurgeCell(cell intmodel.Cell) error {
	if f.PurgeCellFn != nil {
		return f.PurgeCellFn(cell)
	}
	return errors.New("unexpected call to PurgeCell")
}

func (f *fakeRunner) PurgeContainer(realm intmodel.Realm, containerID string) error {
	if f.PurgeContainerFn != nil {
		return f.PurgeContainerFn(realm, containerID)
	}
	return errors.New("unexpected call to PurgeContainer")
}

// Bootstrap

func (f *fakeRunner) BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error) {
	if f.BootstrapCNIFn != nil {
		return f.BootstrapCNIFn(cfgDir, cacheDir, binDir)
	}
	return cni.BootstrapReport{}, errors.New("unexpected call to BootstrapCNI")
}

// Close

func (f *fakeRunner) Close() error {
	if f.CloseFn != nil {
		return f.CloseFn()
	}
	return nil
}

// Test helper functions

// setupTestLogger creates a test logger that discards output.
func setupTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupTestContext creates a context with test logger.
func setupTestContext(t *testing.T) context.Context {
	t.Helper()
	logger := setupTestLogger(t)
	return context.WithValue(context.Background(), "logger", logger)
}

// setupTestController creates a test controller instance with injected mock runner.
func setupTestController(t *testing.T, mockRunner runner.Runner) *controller.Exec {
	t.Helper()
	ctx := context.Background()
	logger := setupTestLogger(t)
	opts := controller.Options{
		RunPath:          "/test/run/path",
		ContainerdSocket: "/test/containerd.sock",
	}
	return controller.NewControllerExecForTesting(ctx, logger, opts, mockRunner)
}

// Resource builder helpers

// buildTestRealm creates a test realm with the specified name and namespace.
func buildTestRealm(name, namespace string) intmodel.Realm {
	if namespace == "" {
		namespace = name
	}
	return intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: name,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: namespace,
			},
		},
		Spec: intmodel.RealmSpec{
			Namespace: namespace,
		},
		Status: intmodel.RealmStatus{
			State: intmodel.RealmStateReady,
		},
	}
}

// buildTestSpace creates a test space with the specified name and realm name.
func buildTestSpace(name, realmName string) intmodel.Space {
	return intmodel.Space{
		Metadata: intmodel.SpaceMetadata{
			Name: name,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmName,
				consts.KukeonSpaceLabelKey: name,
			},
		},
		Spec: intmodel.SpaceSpec{
			RealmName: realmName,
		},
		Status: intmodel.SpaceStatus{
			State: intmodel.SpaceStateReady,
		},
	}
}

// buildTestStack creates a test stack with the specified name, realm name, and space name.
func buildTestStack(name, realmName, spaceName string) intmodel.Stack {
	return intmodel.Stack{
		Metadata: intmodel.StackMetadata{
			Name: name,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmName,
				consts.KukeonSpaceLabelKey: spaceName,
				consts.KukeonStackLabelKey: name,
			},
		},
		Spec: intmodel.StackSpec{
			ID:        name,
			RealmName: realmName,
			SpaceName: spaceName,
		},
		Status: intmodel.StackStatus{
			State: intmodel.StackStateReady,
		},
	}
}

// buildTestCell creates a test cell with the specified name, realm name, space name, and stack name.
func buildTestCell(name, realmName, spaceName, stackName string) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: name,
			Labels: map[string]string{
				consts.KukeonRealmLabelKey: realmName,
				consts.KukeonSpaceLabelKey: spaceName,
				consts.KukeonStackLabelKey: stackName,
				consts.KukeonCellLabelKey:  name,
			},
		},
		Spec: intmodel.CellSpec{
			ID:        name,
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
		Status: intmodel.CellStatus{
			State: intmodel.CellStateReady,
		},
	}
}

// buildTestContainer creates a test container with the specified parameters.
func buildTestContainer(name, realmName, spaceName, stackName, cellName, image string) intmodel.Container {
	return intmodel.Container{
		Metadata: intmodel.ContainerMetadata{
			Name: name,
		},
		Spec: intmodel.ContainerSpec{
			ID:        name,
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
			CellName:  cellName,
			Image:     image,
		},
		Status: intmodel.ContainerStatus{
			State: intmodel.ContainerStateReady,
		},
	}
}

// Basic test to verify mock runner infrastructure works

func TestFakeRunner_ImplementsInterface(t *testing.T) {
	f := &fakeRunner{}
	var _ runner.Runner = f
}

func TestSetupTestController_CreatesControllerWithMockRunner(t *testing.T) {
	mockRunner := &fakeRunner{
		GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) {
			return buildTestRealm("test-realm", "test-ns"), nil
		},
	}

	ctrl := setupTestController(t, mockRunner)
	if ctrl == nil {
		t.Fatal("expected controller to be created, got nil")
	}
}
