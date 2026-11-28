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

package cloudhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/cloudlink"
)

type secretClient struct {
	kukeonv1.FakeClient

	result kukeonv1.GetSecretResult
	err    error
}

func (c secretClient) ReadSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	return c.result, c.err
}

func TestLoadCredentialErrors(t *testing.T) {
	daemonDown := errors.New("daemon down")
	cases := map[string]struct {
		client      secretClient
		notEnrolled bool
	}{
		"absent": {secretClient{}, true},
		"garbage": {
			secretClient{
				result: kukeonv1.GetSecretResult{
					MetadataExists: true,
					Secret:         v1beta1.SecretDoc{Spec: v1beta1.SecretSpec{Data: "x"}},
				},
			},
			true,
		},
		"empty": {
			secretClient{
				result: kukeonv1.GetSecretResult{
					MetadataExists: true,
					Secret:         v1beta1.SecretDoc{Spec: v1beta1.SecretSpec{Data: "{}"}},
				},
			},
			true,
		},
		"daemon": {secretClient{err: daemonDown}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadCredential(context.Background(), tc.client)
			if got := errors.Is(err, cloudlink.ErrNotEnrolled); got != tc.notEnrolled || err == nil {
				t.Fatalf("LoadCredential = %v, ErrNotEnrolled=%v want %v", err, got, tc.notEnrolled)
			}
		})
	}
}

func TestStatusFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", StatusFileName)
	if _, err := ReadStatus(path); !errors.Is(err, ErrNoStatus) {
		t.Fatalf("ReadStatus of a missing file = %v, want ErrNoStatus", err)
	}
	want := Status{
		PID:    os.Getpid(),
		URL:    "wss://r/v1/link",
		HostID: "h-1",
		State:  cloudlink.State{Status: cloudlink.StateConnecting},
	}
	if err := WriteStatus(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(path)
	if err != nil || got.HostID != "h-1" || got.Status != cloudlink.StateConnecting {
		t.Fatalf("ReadStatus = %+v, %v", got, err)
	}
	RemoveStatus(path, want.PID+1)
	if _, err = os.Stat(path); err != nil {
		t.Fatal("RemoveStatus removed another process's status file")
	}
	RemoveStatus(path, want.PID)
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status file not removed: %v", err)
	}
}
