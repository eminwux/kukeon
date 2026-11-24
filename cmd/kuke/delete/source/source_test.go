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

package source_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/delete/source"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

type fakeClient struct {
	kukeonv1.FakeClient

	result kukeonv1.DeleteSourceResult
	err    error
	gotRef kukeonv1.SourceRef
}

func (f *fakeClient) DeleteSource(_ context.Context, ref kukeonv1.SourceRef) (kukeonv1.DeleteSourceResult, error) {
	f.gotRef = ref
	return f.result, f.err
}

func run(t *testing.T, fake *fakeClient, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := source.NewSourceCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), source.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestDeleteSource(t *testing.T) {
	fake := &fakeClient{result: kukeonv1.DeleteSourceResult{Deleted: true}}
	out, err := run(t, fake, "agents")
	if err != nil || !strings.Contains(out, "Deleted source agents") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if fake.gotRef != (kukeonv1.SourceRef{Name: "agents", Realm: "default"}) {
		t.Fatalf("ref = %+v", fake.gotRef)
	}
}

// TestDeleteSourceInUse is AC7: kukeond's refusal, naming the references,
// reaches the operator as it is.
func TestDeleteSourceInUse(t *testing.T) {
	refusal := fmt.Errorf("%w: source agents is referenced by team kukeon (declared agents)", errdefs.ErrSourceInUse)
	_, err := run(t, &fakeClient{err: refusal}, "agents")
	if !errors.Is(err, errdefs.ErrSourceInUse) || !strings.Contains(err.Error(), "team kukeon") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteSourceNotStored(t *testing.T) {
	_, err := run(t, &fakeClient{}, "nothing", "--realm", "other")
	if !errors.Is(err, errdefs.ErrSourceNotFound) || !strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v", err)
	}
}
