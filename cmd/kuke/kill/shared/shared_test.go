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

package shared_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	sharedpkg "github.com/eminwux/kukeon/cmd/kuke/kill/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestControllerFromCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name        string
		setupCtx    func(*cobra.Command)
		viperConfig map[string]string
		wantErr     string
	}{
		{
			name: "success delegates to create/shared",
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
			viperConfig: map[string]string{
				config.KUKEON_ROOT_RUN_PATH.ViperKey:          "/test/run",
				config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey: "/test/socket",
			},
		},
		{
			name: "error when logger missing",
			setupCtx: func(cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := &cobra.Command{Use: "test"}
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
			}

			for k, v := range tt.viperConfig {
				viper.Set(k, v)
			}

			ctrl, err := sharedpkg.ControllerFromCmd(cmd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if ctrl != nil {
					t.Errorf("expected nil controller on error, got %v", ctrl)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ctrl == nil {
				t.Fatal("expected controller, got nil")
			}
		})
	}
}

func TestLoggerFromCmd(t *testing.T) {
	tests := []struct {
		name     string
		setupCtx func(*cobra.Command)
		wantErr  string
	}{
		{
			name: "success delegates to create/shared",
			setupCtx: func(cmd *cobra.Command) {
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
				cmd.SetContext(ctx)
			},
		},
		{
			name: "error when logger missing",
			setupCtx: func(cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			wantErr: errdefs.ErrLoggerNotFound.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			if tt.setupCtx != nil {
				tt.setupCtx(cmd)
			}

			logger, err := sharedpkg.LoggerFromCmd(cmd)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				if logger != nil {
					t.Errorf("expected nil logger on error, got %v", logger)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if logger == nil {
				t.Fatal("expected logger, got nil")
			}
		})
	}
}
