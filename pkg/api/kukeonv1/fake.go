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
	"context"
	"errors"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ErrUnexpectedCall is returned by FakeClient methods that have not been
// overridden by the test. Tests that want to assert "this method was not
// called" can match on errors.Is(err, ErrUnexpectedCall).
var ErrUnexpectedCall = errors.New("unexpected kukeonv1 client call")

// FakeClient is a Client with every method returning ErrUnexpectedCall.
// Embed it into a per-test fake and override only the methods under test.
// Close() is a no-op so deferred client.Close() calls never fail.
type FakeClient struct{}

var _ Client = (*FakeClient)(nil)

func (FakeClient) Close() error { return nil }

func (FakeClient) CreateRealm(context.Context, v1beta1.RealmDoc) (CreateRealmResult, error) {
	return CreateRealmResult{}, ErrUnexpectedCall
}

func (FakeClient) CreateSpace(context.Context, v1beta1.SpaceDoc) (CreateSpaceResult, error) {
	return CreateSpaceResult{}, ErrUnexpectedCall
}

func (FakeClient) CreateStack(context.Context, v1beta1.StackDoc) (CreateStackResult, error) {
	return CreateStackResult{}, ErrUnexpectedCall
}

func (FakeClient) CreateCell(context.Context, v1beta1.CellDoc) (CreateCellResult, error) {
	return CreateCellResult{}, ErrUnexpectedCall
}

func (FakeClient) CreateContainer(context.Context, v1beta1.ContainerDoc) (CreateContainerResult, error) {
	return CreateContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) GetRealm(context.Context, v1beta1.RealmDoc) (GetRealmResult, error) {
	return GetRealmResult{}, ErrUnexpectedCall
}

func (FakeClient) GetSpace(context.Context, v1beta1.SpaceDoc) (GetSpaceResult, error) {
	return GetSpaceResult{}, ErrUnexpectedCall
}

func (FakeClient) GetStack(context.Context, v1beta1.StackDoc) (GetStackResult, error) {
	return GetStackResult{}, ErrUnexpectedCall
}

func (FakeClient) GetCell(context.Context, v1beta1.CellDoc) (GetCellResult, error) {
	return GetCellResult{}, ErrUnexpectedCall
}

func (FakeClient) GetContainer(context.Context, v1beta1.ContainerDoc) (GetContainerResult, error) {
	return GetContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return nil, ErrUnexpectedCall
}

func (FakeClient) ListSpaces(context.Context, string) ([]v1beta1.SpaceDoc, error) {
	return nil, ErrUnexpectedCall
}

func (FakeClient) ListStacks(context.Context, string, string) ([]v1beta1.StackDoc, error) {
	return nil, ErrUnexpectedCall
}

func (FakeClient) ListCells(context.Context, string, string, string) ([]v1beta1.CellDoc, error) {
	return nil, ErrUnexpectedCall
}

func (FakeClient) ListContainers(context.Context, string, string, string, string) ([]v1beta1.ContainerSpec, error) {
	return nil, ErrUnexpectedCall
}

func (FakeClient) StartCell(context.Context, v1beta1.CellDoc) (StartCellResult, error) {
	return StartCellResult{}, ErrUnexpectedCall
}

func (FakeClient) StartContainer(context.Context, v1beta1.ContainerDoc) (StartContainerResult, error) {
	return StartContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) AttachContainer(context.Context, v1beta1.ContainerDoc) (AttachContainerResult, error) {
	return AttachContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) StopCell(context.Context, v1beta1.CellDoc) (StopCellResult, error) {
	return StopCellResult{}, ErrUnexpectedCall
}

func (FakeClient) StopContainer(context.Context, v1beta1.ContainerDoc) (StopContainerResult, error) {
	return StopContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) KillCell(context.Context, v1beta1.CellDoc) (KillCellResult, error) {
	return KillCellResult{}, ErrUnexpectedCall
}

func (FakeClient) KillContainer(context.Context, v1beta1.ContainerDoc) (KillContainerResult, error) {
	return KillContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) DeleteRealm(context.Context, v1beta1.RealmDoc, bool, bool) (DeleteRealmResult, error) {
	return DeleteRealmResult{}, ErrUnexpectedCall
}

func (FakeClient) DeleteSpace(context.Context, v1beta1.SpaceDoc, bool, bool) (DeleteSpaceResult, error) {
	return DeleteSpaceResult{}, ErrUnexpectedCall
}

func (FakeClient) DeleteStack(context.Context, v1beta1.StackDoc, bool, bool) (DeleteStackResult, error) {
	return DeleteStackResult{}, ErrUnexpectedCall
}

func (FakeClient) DeleteCell(context.Context, v1beta1.CellDoc) (DeleteCellResult, error) {
	return DeleteCellResult{}, ErrUnexpectedCall
}

func (FakeClient) DeleteContainer(context.Context, v1beta1.ContainerDoc) (DeleteContainerResult, error) {
	return DeleteContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) PurgeRealm(context.Context, v1beta1.RealmDoc, bool, bool) (PurgeRealmResult, error) {
	return PurgeRealmResult{}, ErrUnexpectedCall
}

func (FakeClient) PurgeSpace(context.Context, v1beta1.SpaceDoc, bool, bool) (PurgeSpaceResult, error) {
	return PurgeSpaceResult{}, ErrUnexpectedCall
}

func (FakeClient) PurgeStack(context.Context, v1beta1.StackDoc, bool, bool) (PurgeStackResult, error) {
	return PurgeStackResult{}, ErrUnexpectedCall
}

func (FakeClient) PurgeCell(context.Context, v1beta1.CellDoc, bool, bool) (PurgeCellResult, error) {
	return PurgeCellResult{}, ErrUnexpectedCall
}

func (FakeClient) PurgeContainer(context.Context, v1beta1.ContainerDoc) (PurgeContainerResult, error) {
	return PurgeContainerResult{}, ErrUnexpectedCall
}

func (FakeClient) RefreshAll(context.Context) (RefreshAllResult, error) {
	return RefreshAllResult{}, ErrUnexpectedCall
}

func (FakeClient) ApplyDocuments(context.Context, []byte) (ApplyDocumentsResult, error) {
	return ApplyDocumentsResult{}, ErrUnexpectedCall
}

func (FakeClient) Ping(context.Context) error {
	return ErrUnexpectedCall
}
