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
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type stackController interface {
	CreateStack(stack intmodel.Stack) (controller.CreateStackResult, error)
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
		Aliases:       []string{"st"},
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

func (w *controllerWrapper) CreateStack(stack intmodel.Stack) (controller.CreateStackResult, error) {
	return w.ctrl.CreateStack(stack)
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
		realm = config.KUKE_CREATE_STACK_REALM.ValueOrDefault()
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_STACK_SPACE.ViperKey))
	if space == "" {
		space = config.KUKE_CREATE_STACK_SPACE.ValueOrDefault()
	}

	ctrl, err := getCtrl(cmd)
	if err != nil {
		return err
	}

	// Build v1beta1.StackDoc from command arguments
	doc := &v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: name,
		},
		Spec: v1beta1.StackSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
		},
	}

	// Convert at boundary before calling controller
	stack, version, err := apischeme.NormalizeStack(*doc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Call controller with internal type
	result, err := ctrl.CreateStack(stack)
	if err != nil {
		return err
	}

	printStackResult(cmd, result, printOutcome, version)
	return nil
}

func printStackResult(
	cmd *cobra.Command,
	result controller.CreateStackResult,
	printOutcome printOutcomeFunc,
	version v1beta1.Version,
) {
	// Convert result back to external for output
	resultDoc, err := apischeme.BuildStackExternalFromInternal(result.Stack, version)
	if err != nil {
		// Fallback to internal type if conversion fails
		cmd.Printf(
			"Stack %q (realm %q, space %q)\n",
			result.Stack.Metadata.Name,
			result.Stack.Spec.RealmName,
			result.Stack.Spec.SpaceName,
		)
		cmd.Printf("Warning: failed to convert result for output: %v\n", err)
	} else {
		cmd.Printf("Stack %q (realm %q, space %q)\n", resultDoc.Metadata.Name, resultDoc.Spec.RealmID, resultDoc.Spec.SpaceID)
	}
	printOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	printOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
}

// PrintStackResult is exported for testing purposes.
func PrintStackResult(
	cmd *cobra.Command,
	result controller.CreateStackResult,
	printOutcome printOutcomeFunc,
	version v1beta1.Version,
) {
	printStackResult(cmd, result, printOutcome, version)
}
