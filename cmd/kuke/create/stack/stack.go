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
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type stackController interface {
	CreateStack(opts controller.CreateStackOptions) (controller.CreateStackResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type (
	controllerGetter func(*cobra.Command) (stackController, error)
	printOutcomeFunc func(*cobra.Command, string, bool, bool)
)

func NewStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "stack [name]",
		Short:         "Create or reconcile a stack within a space",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateStack,
	}

	cmd.Flags().String("realm", "", "Realm that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the stack")
	_ = viper.BindPFlag(config.KUKE_CREATE_STACK_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)

	return cmd
}

func runCreateStack(cmd *cobra.Command, args []string) error {
	return runCreateStackWithDeps(
		cmd,
		args,
		getController,
		shared.PrintCreationOutcome,
	)
}

func getController(cmd *cobra.Command) (stackController, error) {
	// Check for mock controller in context (for testing)
	if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(stackController); ok {
		return mockCtrl, nil
	}

	ctrl, err := shared.ControllerFromCmd(cmd)
	if err != nil {
		return nil, err
	}
	return &controllerWrapper{ctrl: ctrl}, nil
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) CreateStack(opts controller.CreateStackOptions) (controller.CreateStackResult, error) {
	return w.ctrl.CreateStack(opts)
}

func runCreateStackWithDeps(
	cmd *cobra.Command,
	args []string,
	getCtrl controllerGetter,
	printOutcome printOutcomeFunc,
) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"stack",
		viper.GetString(config.KUKE_CREATE_STACK_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_REALM.ViperKey))
	if realm == "" {
		return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_SPACE.ViperKey))
	if space == "" {
		return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
	}

	ctrl, err := getCtrl(cmd)
	if err != nil {
		return err
	}

	result, err := ctrl.CreateStack(controller.CreateStackOptions{
		Name:      name,
		RealmName: realm,
		SpaceName: space,
	})
	if err != nil {
		return err
	}

	printStackResult(cmd, result, printOutcome)
	return nil
}

func printStackResult(cmd *cobra.Command, result controller.CreateStackResult, printOutcome printOutcomeFunc) {
	cmd.Printf("Stack %q (realm %q, space %q)\n", result.Name, result.RealmName, result.SpaceName)
	printOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	printOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintStackResult is exported for testing purposes.
func PrintStackResult(cmd *cobra.Command, result controller.CreateStackResult, printOutcome printOutcomeFunc) {
	printStackResult(cmd, result, printOutcome)
}
