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

package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Controller interface {
	Bootstrap() (BootstrapReport, error)
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options
	runner runner.Runner
}

type BootstrapReport struct {
	RealmName               string
	Namespace               string
	RunPath                 string
	RealmMetadataExistsPre  bool
	RealmMetadataExistsPost bool
	NamespaceExistsPre      bool
	NamespaceExistsPost     bool
	RealmCreated            bool
	NamespaceCreated        bool
}

type Options struct {
	RunPath          string
	ContainerdSocket string
}

func NewControllerExec(ctx context.Context, logger *slog.Logger, opts Options) *Exec {
	return &Exec{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		runner: runner.NewRunner(ctx, logger, runner.Options{
			ContainerdSocket: opts.ContainerdSocket,
			RunPath:          opts.RunPath,
		}),
	}
}

func (b *Exec) Bootstrap() (BootstrapReport, error) {
	b.logger.DebugContext(b.ctx, "bootstrapping kukeon", "options", b.opts)

	report := BootstrapReport{
		RealmName: consts.KukeonRealmName,
		Namespace: consts.KukeonRealmNamespace,
		RunPath:   b.opts.RunPath,
	}

	var err error

	// Pre-state
	_, err = b.runner.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: consts.KukeonRealmName},
		Spec:     v1beta1.RealmSpec{Namespace: consts.KukeonRealmNamespace},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			report.RealmMetadataExistsPre = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		report.RealmMetadataExistsPre = true
	}
	nsExistsPre, err := b.runner.NamespaceExists(consts.KukeonRealmNamespace)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	report.NamespaceExistsPre = nsExistsPre

	_, err = b.runner.CreateRealm(
		&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: consts.KukeonRealmName,
				Labels: map[string]string{
					consts.KukeonRealmLabelKey: consts.KukeonRealmNamespace,
				},
			},
			Spec: v1beta1.RealmSpec{
				Namespace: consts.KukeonRealmNamespace,
			},
		},
	)
	if err != nil && !errors.Is(err, errdefs.ErrNamespaceAlreadyExists) {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCreateRealm, err)
	}

	// Post-state
	_, err = b.runner.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{Name: consts.KukeonRealmName},
		Spec:     v1beta1.RealmSpec{Namespace: consts.KukeonRealmNamespace},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			report.RealmMetadataExistsPost = false
		} else {
			return report, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
		}
	} else {
		report.RealmMetadataExistsPost = true
	}
	nsExistsPost, err := b.runner.NamespaceExists(consts.KukeonRealmNamespace)
	if err != nil {
		return report, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	report.NamespaceExistsPost = nsExistsPost

	// Derived
	report.RealmCreated = !report.RealmMetadataExistsPre && report.RealmMetadataExistsPost
	report.NamespaceCreated = !report.NamespaceExistsPre && report.NamespaceExistsPost

	return report, nil
}
