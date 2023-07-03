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

	storagev1 "k8s.io/api/storage/v1"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

type kubeVirtStorageClient interface {
	ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error)
}

const (
	AnnDefaultStorageClass = "storageclass.kubernetes.io/is-default-class"
)

type Checkup struct {
	client    kubeVirtStorageClient
	namespace string
	results   status.Results
}

func New(client kubeVirtStorageClient, namespace string, checkupConfig config.Config) *Checkup {
	return &Checkup{
		client:    client,
		namespace: namespace,
	}
}

func (c *Checkup) Setup(ctx context.Context) error {
	return nil
}

func (c *Checkup) Run(ctx context.Context) error {
	scs, err := c.client.ListStorageClasses(ctx)
	if err != nil {
		return err
	}
	for _, sc := range scs.Items {
		if sc.Annotations[AnnDefaultStorageClass] == "true" {
			c.results.HasDefaultStorageClass = true
			break
		}
	}
	return nil
}

func (c *Checkup) Teardown(ctx context.Context) error {
	return nil
}

func (c *Checkup) Results() status.Results {
	return c.results
}
