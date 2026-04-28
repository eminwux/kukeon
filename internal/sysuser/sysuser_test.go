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

package sysuser_test

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/sysuser"
)

type fakeRunner struct {
	calls [][]string
	errs  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	if err, ok := f.errs[name]; ok {
		return err
	}
	return nil
}

func TestEnsureUserGroup_AlreadyExists(t *testing.T) {
	runner := &fakeRunner{}
	res, err := sysuser.EnsureUserGroup(context.Background(), "kukeon", "kukeon", sysuser.EnsureOptions{
		Runner: runner,
		LookupGroup: func(name string) (*user.Group, error) {
			return &user.Group{Name: name, Gid: "999"}, nil
		},
		LookupUser: func(name string) (*user.User, error) {
			return &user.User{Username: name, Uid: "998", Gid: "999"}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.UID != 998 || res.GID != 999 {
		t.Fatalf("unexpected ids: %+v", res)
	}
	if res.UserCreated || res.GroupCreated {
		t.Fatalf("expected no creation when both exist: %+v", res)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no exec calls, got: %v", runner.calls)
	}
}

func TestEnsureUserGroup_CreatesGroupAndUser(t *testing.T) {
	runner := &fakeRunner{}
	groupCreated := false
	userCreated := false

	opts := sysuser.EnsureOptions{
		NoLoginShell: "/usr/sbin/nologin",
		LookupGroup: func(name string) (*user.Group, error) {
			if !groupCreated {
				return nil, user.UnknownGroupError(name)
			}
			return &user.Group{Name: name, Gid: "12345"}, nil
		},
		LookupUser: func(name string) (*user.User, error) {
			if !userCreated {
				return nil, user.UnknownUserError(name)
			}
			return &user.User{Username: name, Uid: "23456", Gid: "12345"}, nil
		},
	}

	// Wrap the fake runner so the existence flags flip after each create.
	opts.Runner = runnerFunc(func(ctx context.Context, name string, args ...string) error {
		if err := runner.Run(ctx, name, args...); err != nil {
			return err
		}
		switch name {
		case "groupadd":
			groupCreated = true
		case "useradd":
			userCreated = true
		}
		return nil
	})

	res, err := sysuser.EnsureUserGroup(context.Background(), "kukeon", "kukeon", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.GroupCreated || !res.UserCreated {
		t.Fatalf("expected creation flags: %+v", res)
	}
	if res.GID != 12345 || res.UID != 23456 {
		t.Fatalf("unexpected ids: %+v", res)
	}

	want := [][]string{
		{"groupadd", "--system", "kukeon"},
		{"useradd", "--system", "--gid", "kukeon", "--no-create-home", "--shell", "/usr/sbin/nologin", "kukeon"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls: got %v want %v", runner.calls, want)
	}
}

func TestEnsureUserGroup_GroupAddFails(t *testing.T) {
	runner := &fakeRunner{errs: map[string]error{"groupadd": errors.New("permission denied")}}
	_, err := sysuser.EnsureUserGroup(context.Background(), "kukeon", "kukeon", sysuser.EnsureOptions{
		Runner: runner,
		LookupGroup: func(name string) (*user.Group, error) {
			return nil, user.UnknownGroupError(name)
		},
		LookupUser: func(_ string) (*user.User, error) {
			t.Fatal("should not be reached when groupadd fails")
			return nil, errors.New("unreachable")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission-denied error, got %v", err)
	}
}

func TestEnsureUserGroup_LookupNonNotFoundError(t *testing.T) {
	sentinel := errors.New("io error")
	_, err := sysuser.EnsureUserGroup(context.Background(), "kukeon", "kukeon", sysuser.EnsureOptions{
		Runner: &fakeRunner{},
		LookupGroup: func(_ string) (*user.Group, error) {
			return nil, sentinel
		},
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected wrapped sentinel, got %v", err)
	}
}

func TestChownAndChmod_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Use current uid/gid so the test doesn't need root.
	uid, gid := os.Getuid(), os.Getgid()
	if err := sysuser.ChownAndChmod(path, uid, gid, 0o640); err != nil {
		t.Fatalf("chown/chmod: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode: got %#o want 0o640", got)
	}
}

func TestChownAndChmod_MissingPath(t *testing.T) {
	err := sysuser.ChownAndChmod(filepath.Join(t.TempDir(), "missing"), 0, 0, 0o640)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestChownTreeAndChmod(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(subdir, "data.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	uid, gid := os.Getuid(), os.Getgid()
	if err := sysuser.ChownTreeAndChmod(root, uid, gid, 0o750, 0o640); err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, p := range []string{root, filepath.Join(root, "a"), subdir} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %q: %v", p, err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("%q dir mode: got %#o want 0o750", p, got)
		}
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("file mode: got %#o want 0o640", got)
	}
}

// runnerFunc adapts a function value to the CommandRunner interface for tests.
type runnerFunc func(ctx context.Context, name string, args ...string) error

func (f runnerFunc) Run(ctx context.Context, name string, args ...string) error {
	return f(ctx, name, args...)
}
