/*
 * This file is part of the kiagnose project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2023 Red Hat, Inc.
 *
 */

package checkup

import (
	"context"
	"errors"
	"log"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/k8s"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/kubevirt"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/shared"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

type Client interface {
	k8s.K8sStorageClient
	kubevirt.KubeVirtStorageClient
}

// Checkup delegates validation to the KubeVirt phase.
type Checkup struct {
	state  *shared.State
	phase1 *k8s.Checkup
	phase2 *kubevirt.Checkup
}

// New creates the checkup coordinator.
func New(client Client, namespace string, checkupConfig config.Config) *Checkup {
	state := &shared.State{}

	return &Checkup{
		state:  state,
		phase1: k8s.New(client, namespace, state, checkupConfig),
		phase2: kubevirt.New(client, namespace, state, checkupConfig),
	}
}

func (c *Checkup) Setup(ctx context.Context) error {
	if err := c.phase1.Setup(ctx); err != nil {
		return err
	}

	return c.phase2.Setup(ctx)
}

func (c *Checkup) Run(ctx context.Context) error {
	err := c.phase1.Run(ctx)
	if err != nil {
		return err
	}
	log.Print("Kubernetes/CSI validation passed")
	return c.phase2.Run(ctx)
}

func (c *Checkup) Teardown(ctx context.Context) error {
	phase2Err := c.phase2.Teardown(ctx)
	phase1Err := c.phase1.Teardown(ctx)

	return errors.Join(phase2Err, phase1Err)
}

func (c *Checkup) Results() status.Results {
	results := c.phase2.Results()
	results.K8sResults = c.phase1.Results().K8sResults
	return results
}

func (c *Checkup) Config() config.Config {
	return c.phase2.Config()
}
