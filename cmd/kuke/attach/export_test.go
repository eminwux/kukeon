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

package attach

// ExecFn is the test-visible alias of the unexported execFn type. Tests
// build values of this type and store them under MockExecKey to bypass
// the real syscall.Exec, which would replace the test binary on success.
type ExecFn = execFn

// ResolveSbBinaryForTest exposes resolveSbBinary to the external _test
// package so the PATH-lookup vs absolute-path branches can be exercised
// without going through the full cobra entry point.
func ResolveSbBinaryForTest(name string) (string, error) {
	return resolveSbBinary(name)
}
