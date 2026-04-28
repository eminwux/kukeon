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

package run

import (
	"fmt"

	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

// runResult is the structured shape emitted under -o json|yaml. It mirrors the
// kukeonv1.CreateCellResult fields the human formatter consumes so the three
// formats agree.
type runResult struct {
	Cell                    cellRef           `json:"cell"                    yaml:"cell"`
	Created                 bool              `json:"created"                 yaml:"created"`
	MetadataExistsPost      bool              `json:"metadataExistsPost"      yaml:"metadataExistsPost"`
	CgroupCreated           bool              `json:"cgroupCreated"           yaml:"cgroupCreated"`
	CgroupExistsPost        bool              `json:"cgroupExistsPost"        yaml:"cgroupExistsPost"`
	RootContainerCreated    bool              `json:"rootContainerCreated"    yaml:"rootContainerCreated"`
	RootContainerExistsPost bool              `json:"rootContainerExistsPost" yaml:"rootContainerExistsPost"`
	Started                 bool              `json:"started"                 yaml:"started"`
	Containers              []containerOutput `json:"containers,omitempty"    yaml:"containers,omitempty"`
}

type cellRef struct {
	Name    string `json:"name"    yaml:"name"`
	RealmID string `json:"realmId" yaml:"realmId"`
	SpaceID string `json:"spaceId" yaml:"spaceId"`
	StackID string `json:"stackId" yaml:"stackId"`
}

type containerOutput struct {
	Name       string `json:"name"       yaml:"name"`
	Created    bool   `json:"created"    yaml:"created"`
	ExistsPost bool   `json:"existsPost" yaml:"existsPost"`
}

func printRunResult(cmd *cobra.Command, result kukeonv1.CreateCellResult, format string) error {
	if format == "json" || format == "yaml" {
		return kukshared.PrintJSONOrYAML(cmd, toRunResult(result), format)
	}
	printRunResultHuman(cmd, result)
	return nil
}

func toRunResult(result kukeonv1.CreateCellResult) runResult {
	out := runResult{
		Cell: cellRef{
			Name:    result.Cell.Metadata.Name,
			RealmID: result.Cell.Spec.RealmID,
			SpaceID: result.Cell.Spec.SpaceID,
			StackID: result.Cell.Spec.StackID,
		},
		Created:                 result.Created,
		MetadataExistsPost:      result.MetadataExistsPost,
		CgroupCreated:           result.CgroupCreated,
		CgroupExistsPost:        result.CgroupExistsPost,
		RootContainerCreated:    result.RootContainerCreated,
		RootContainerExistsPost: result.RootContainerExistsPost,
		Started:                 result.Started,
	}
	if len(result.Containers) > 0 {
		out.Containers = make([]containerOutput, 0, len(result.Containers))
		for _, c := range result.Containers {
			out.Containers = append(out.Containers, containerOutput{
				Name:       c.Name,
				Created:    c.Created,
				ExistsPost: c.ExistsPost,
			})
		}
	}
	return out
}

// printRunResultHuman matches the layout of `kuke create cell` so operators
// see the same lifecycle rollup regardless of which verb produced it.
func printRunResultHuman(cmd *cobra.Command, result kukeonv1.CreateCellResult) {
	cmd.Printf(
		"Cell %q (realm %q, space %q, stack %q)\n",
		result.Cell.Metadata.Name,
		result.Cell.Spec.RealmID,
		result.Cell.Spec.SpaceID,
		result.Cell.Spec.StackID,
	)
	printOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	printOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
	printOutcome(cmd, "root container", result.RootContainerExistsPost, result.RootContainerCreated)

	if len(result.Containers) == 0 {
		cmd.Println("  - containers: none defined")
	} else {
		for _, c := range result.Containers {
			printOutcome(cmd, fmt.Sprintf("container %q", c.Name), c.ExistsPost, c.Created)
		}
	}

	if result.Started {
		cmd.Println("  - containers: started")
	} else {
		cmd.Println("  - containers: not started")
	}
}

func printOutcome(cmd *cobra.Command, label string, existsPost, created bool) {
	switch {
	case created:
		cmd.Printf("  - %s: created\n", label)
	case existsPost:
		cmd.Printf("  - %s: already existed\n", label)
	default:
		cmd.Printf("  - %s: missing\n", label)
	}
}

// PrintRunResult is exported for testing.
func PrintRunResult(cmd *cobra.Command, result kukeonv1.CreateCellResult, format string) error {
	return printRunResult(cmd, result, format)
}

// noOpResultFromGet shapes a CreateCellResult for the
// matching-spec + already-Ready short-circuit so the printer paths emit the
// same fields whether the cell was just created or pre-existing.
func noOpResultFromGet(pre kukeonv1.GetCellResult) kukeonv1.CreateCellResult {
	res := kukeonv1.CreateCellResult{
		Cell:                    pre.Cell,
		Created:                 false,
		MetadataExistsPost:      pre.MetadataExists,
		CgroupExistsPost:        pre.CgroupExists,
		RootContainerExistsPost: pre.RootContainerExists,
		Started:                 false,
	}
	for _, c := range pre.Cell.Spec.Containers {
		res.Containers = append(res.Containers, kukeonv1.ContainerCreationOutcome{
			Name:       c.ID,
			ExistsPost: true,
			Created:    false,
		})
	}
	return res
}
