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

package stack

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/delete/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// StackController defines the interface for stack deletion operations.
// It is exported for use in tests.
type StackController interface {
	DeleteStack(stack intmodel.Stack, force, cascade bool) (controller.DeleteStackResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Aliases:       []string{"st"},
		Short:         "Delete a stack",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl StackController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(StackController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_STACK_REALM.ViperKey))
			if realm == "" {
				realm = config.KUKE_DELETE_STACK_REALM.ValueOrDefault()
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_DELETE_STACK_SPACE.ViperKey))
			if space == "" {
				space = config.KUKE_DELETE_STACK_SPACE.ValueOrDefault()
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			stackDoc := &v1beta1.StackDoc{
				Metadata: v1beta1.StackMetadata{
					Name: name,
				},
				Spec: v1beta1.StackSpec{
					RealmID: realm,
					SpaceID: space,
				},
			}

			// Convert at boundary before calling controller
			stackInternal, _, err := apischeme.NormalizeStack(*stackDoc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			result, err := ctrl.DeleteStack(stackInternal, force, cascade)
			if err != nil {
				return err
			}

			cmd.Printf("Deleted stack %q from space %q\n", result.StackName, result.SpaceName)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_DELETE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_DELETE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	// Register autocomplete functions
	cmd.ValidArgsFunction = config.CompleteStackNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) DeleteStack(
	stack intmodel.Stack,
	force, cascade bool,
) (controller.DeleteStackResult, error) {
	return w.ctrl.DeleteStack(stack, force, cascade)
}
