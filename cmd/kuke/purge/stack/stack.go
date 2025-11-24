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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/purge/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type stackController interface {
	PurgeStack(doc *v1beta1.StackDoc, force, cascade bool) (controller.PurgeStackResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Short:         "Purge a stack with comprehensive cleanup",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl stackController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(stackController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_STACK_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_PURGE_STACK_SPACE.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}

			force := shared.ParseForceFlag(cmd)
			cascade := shared.ParseCascadeFlag(cmd)

			doc := &v1beta1.StackDoc{
				Metadata: v1beta1.StackMetadata{
					Name: name,
				},
				Spec: v1beta1.StackSpec{
					RealmID: realm,
					SpaceID: space,
				},
			}

			result, err := ctrl.PurgeStack(doc, force, cascade)
			if err != nil {
				return err
			}

			stackName := name
			spaceName := space
			if result.StackDoc != nil {
				if trimmed := strings.TrimSpace(result.StackDoc.Metadata.Name); trimmed != "" {
					stackName = trimmed
				}
				if trimmed := strings.TrimSpace(result.StackDoc.Spec.SpaceID); trimmed != "" {
					spaceName = trimmed
				}
			}

			cmd.Printf("Purged stack %q from space %q\n", stackName, spaceName)
			if len(result.Purged) > 0 {
				cmd.Printf("Additional resources purged: %v\n", result.Purged)
			}
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_PURGE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_PURGE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	// Register autocomplete functions
	cmd.ValidArgsFunction = config.CompleteStackNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) PurgeStack(
	doc *v1beta1.StackDoc,
	force, cascade bool,
) (controller.PurgeStackResult, error) {
	var zero controller.PurgeStackResult
	if w == nil || w.ctrl == nil {
		return zero, errors.New("controller not initialized")
	}
	if doc == nil {
		return zero, errdefs.ErrStackNameRequired
	}

	return w.ctrl.PurgeStack(doc, force, cascade)
}
