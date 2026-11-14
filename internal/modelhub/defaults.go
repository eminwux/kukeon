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

package modelhub

// ScopeDefaults mirrors v1beta1.ScopeDefaults: the CellConfig fill a Realm,
// Space or Stack contributes to cells rendered from a CellConfig (#1418).
type ScopeDefaults struct {
	Values  map[string]string
	Repos   map[string]RepoFill
	Secrets map[string]SecretFill
}

// RepoFill mirrors v1beta1.CellConfigRepoFill.
type RepoFill struct {
	URL    string
	Branch string
	Ref    string
}

// SecretFill mirrors v1beta1.CellConfigSecretFill.
type SecretFill struct {
	SecretRef *ContainerSecretRef
}

// RealmImage mirrors v1beta1.RealmImage.
type RealmImage struct {
	Name  string
	Ref   string
	Build *RealmImageBuild
}

// RealmImageBuild mirrors v1beta1.RealmImageBuild.
type RealmImageBuild struct {
	Context    string
	Dockerfile string
	Args       map[string]string
	Tag        string
}
