// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0
package log_test

import (
	"errors"
	"io"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"testing"
	"time"

	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestLog_RPC_FollowRidesOutDaemonRestart pins that `kuke log -f` keeps
// following when kukeond goes away after the first chunk: it closes the dead
// client so the next poll dials again, and resumes from the same cursor.
func TestLog_RPC_FollowRidesOutDaemonRestart(t *testing.T) {
	var (
		mu            sync.Mutex
		calls         int
		resumeOffset  int64 = -1
		closesAtRetry int32
	)
	fc := &fakeClient{}
	fc.readContainerLogFn = func(_ v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		switch {
		case calls == 1:
			return kukeonv1.ContainerLogResult{Data: []byte("a"), Next: 1, Size: 1}, nil
		case calls <= 4:
			return kukeonv1.ContainerLogResult{}, io.ErrUnexpectedEOF
		case calls == 5:
			resumeOffset = req.Offset
			closesAtRetry = fc.closes.Load()
			return kukeonv1.ContainerLogResult{Data: []byte("b"), Next: 2, Size: 2}, nil
		default:
			return kukeonv1.ContainerLogResult{Next: req.Offset, Size: req.Offset}, nil
		}
	}
	out, cancel, wait := runFollow(t, fc)
	waitFor(t, "output after the outage", func() bool { return out.String() == "ab" })
	cancel()
	if err := wait(); err != nil {
		t.Fatalf("follow returned %v, want nil", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if closesAtRetry < 1 {
		t.Fatalf("Close calls before the reconnect = %d, want at least 1", closesAtRetry)
	}
	if resumeOffset != 1 {
		t.Fatalf("request after the outage carried offset %d, want the cursor 1", resumeOffset)
	}
}

func TestLog_RPC_FollowGivesUpAfterOutageLimit(t *testing.T) {
	t.Cleanup(logcmd.SetFollowOutageLimitForTesting(50 * time.Millisecond))
	var (
		mu    sync.Mutex
		calls int
	)
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return kukeonv1.ContainerLogResult{Data: []byte("a"), Next: 1, Size: 1}, nil
			}
			return kukeonv1.ContainerLogResult{}, rpc.ErrShutdown
		},
	}
	_, cancel, wait := runFollow(t, fc)
	defer cancel()
	err := wait()
	if err == nil || !strings.Contains(err.Error(), "kukeond unreachable") {
		t.Fatalf("error = %v, want kukeond unreachable", err)
	}
	if !errors.Is(err, rpc.ErrShutdown) {
		t.Fatalf("error %v does not unwrap to rpc.ErrShutdown", err)
	}
}

func TestLog_RPC_FollowFirstRequestTransportErrorIsNotRetried(t *testing.T) {
	calls := 0
	opErr := &net.OpError{Op: "dial", Net: "unix", Err: errors.New("connection refused")}
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			calls++
			return kukeonv1.ContainerLogResult{}, opErr
		},
	}
	_, cancel, wait := runFollow(t, fc)
	defer cancel()
	var got *net.OpError
	if err := wait(); !errors.As(err, &got) {
		t.Fatalf("error = %v, want the *net.OpError", err)
	}
	if calls != 1 {
		t.Fatalf("ReadContainerLog calls = %d, want 1 (no retry)", calls)
	}
}

func TestLog_RPC_NoFollowTransportErrorIsReturned(t *testing.T) {
	calls := 0
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			calls++
			if calls == 1 {
				return kukeonv1.ContainerLogResult{Data: []byte("a"), Next: 1, Size: 2, More: true}, nil
			}
			return kukeonv1.ContainerLogResult{}, io.EOF
		},
	}
	cmd, _ := newCmdWithCtx(t, fc, nil)
	t.Cleanup(logcmd.SetPollIntervalForTesting(5 * time.Millisecond))
	cmd.SetArgs(logArgs())
	if err := cmd.Execute(); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
	if calls != 2 {
		t.Fatalf("ReadContainerLog calls = %d, want 2 (no retry)", calls)
	}
}

func TestLog_RPC_FollowDaemonAnswerIsNotRetried(t *testing.T) {
	calls := 0
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			calls++
			if calls == 1 {
				return kukeonv1.ContainerLogResult{Data: []byte("a"), Next: 1, Size: 1}, nil
			}
			return kukeonv1.ContainerLogResult{}, errdefs.ErrContainerNotFound
		},
	}
	_, cancel, wait := runFollow(t, fc)
	defer cancel()
	if err := wait(); !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("error = %v, want ErrContainerNotFound", err)
	}
	if calls != 2 {
		t.Fatalf("ReadContainerLog calls = %d, want 2 (no retry)", calls)
	}
}
