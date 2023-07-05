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
	"fmt"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"

	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

type kubeVirtStorageClient interface {
	ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error)
	ListStorageProfiles(ctx context.Context) (*cdiv1.StorageProfileList, error)
	ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error)
}

const (
	annDefaultStorageClass = "storageclass.kubernetes.io/is-default-class"

	errNoDefaultStorageClass      = "No default storage class."
	errMultipleStorageClasses     = "There are multiple default storage classes."
	errEmptyClaimPropertySets     = "There are StorageProfiles with empty ClaimPropertySets (unknown provisioners): %s."
	errMissingVolumeSnapshotClass = "There are StorageProfiles missing VolumeSnapshotClass: %s"
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
	errStr := ""

	scs, err := c.client.ListStorageClasses(ctx)
	if err != nil {
		return err
	}
	c.checkDefaultStorageClass(scs, &errStr)

	sps, err := c.client.ListStorageProfiles(ctx)
	if err != nil {
		return err
	}
	c.checkStorageProfilesWithEmptyClaimPropertySets(sps, &errStr)
	c.checkStorageProfilesWithSpecClaimPropertySets(sps)
	c.checkStorageProfilesWithRWX(sps)
	vscs, err := c.client.ListVolumeSnapshotClasses(ctx)
	if err != nil {
		return err
	}
	c.checkVolumeSnapShotClasses(sps, vscs, &errStr)

	if errStr != "" {
		return errors.New(errStr)
	}
	return nil
}

func (c *Checkup) checkDefaultStorageClass(scs *storagev1.StorageClassList, errStr *string) {
	for i := range scs.Items {
		sc := &scs.Items[i]
		if sc.Annotations[annDefaultStorageClass] == "true" {
			if c.results.DefaultStorageClass != "" {
				c.results.DefaultStorageClass = errMultipleStorageClasses
				appendSep(errStr, errMultipleStorageClasses, "\n")
				break
			}
			c.results.DefaultStorageClass = sc.Name
		}
	}
	if c.results.DefaultStorageClass == "" {
		c.results.DefaultStorageClass = errNoDefaultStorageClass
		appendSep(errStr, errNoDefaultStorageClass, "\n")
	}
}

func (c *Checkup) checkStorageProfilesWithEmptyClaimPropertySets(sps *cdiv1.StorageProfileList, errStr *string) {
	spNames := ""
	for i := range sps.Items {
		sp := &sps.Items[i]
		cpSets := sp.Status.ClaimPropertySets
		if len(cpSets) == 0 {
			appendSep(&spNames, sp.Name, ", ")
		}
	}
	if spNames != "" {
		c.results.StorageProfilesWithEmptyClaimPropertySets = spNames
		appendSep(errStr, fmt.Sprintf(errEmptyClaimPropertySets, spNames), "\n")
	}
}

func (c *Checkup) checkStorageProfilesWithSpecClaimPropertySets(sps *cdiv1.StorageProfileList) {
	spNames := ""
	for i := range sps.Items {
		sp := &sps.Items[i]
		cpSets := sp.Status.ClaimPropertySets
		if len(cpSets) != 0 {
			appendSep(&spNames, sp.Name, ", ")
		}
	}
	if spNames != "" {
		c.results.StorageProfilesWithSpecClaimPropertySets = spNames
	}
}

func (c *Checkup) checkStorageProfilesWithRWX(sps *cdiv1.StorageProfileList) {
	spNames := ""
	for i := range sps.Items {
		sp := &sps.Items[i]
		if hasRWX(sp.Status.ClaimPropertySets) {
			appendSep(&spNames, sp.Name, ", ")
		}
	}
	if spNames != "" {
		c.results.StorageWithRWX = spNames
	}
}

func hasRWX(cpSets []cdiv1.ClaimPropertySet) bool {
	for _, cpSet := range cpSets {
		for _, am := range cpSet.AccessModes {
			if am == v1.ReadWriteMany {
				return true
			}
		}
	}
	return false
}

func (c *Checkup) checkVolumeSnapShotClasses(sps *cdiv1.StorageProfileList, vscs *snapshotv1.VolumeSnapshotClassList, errStr *string) {
	spNames := ""
	for i := range sps.Items {
		sp := &sps.Items[i]
		if sp.Status.CloneStrategy != nil &&
			*sp.Status.CloneStrategy == cdiv1.CloneStrategySnapshot &&
			sp.Status.Provisioner != nil {
			if !hasDriver(vscs, *sp.Status.Provisioner) {
				appendSep(&spNames, sp.Name, ", ")
			}
		}
	}
	if spNames != "" {
		c.results.StorageMissingVolumeSnapshotClass = spNames
		appendSep(errStr, fmt.Sprintf(errMissingVolumeSnapshotClass, spNames), "\n")
	}
}

func hasDriver(vscs *snapshotv1.VolumeSnapshotClassList, driver string) bool {
	for i := range vscs.Items {
		if vscs.Items[i].Driver == driver {
			return true
		}
	}
	return false
}

func (c *Checkup) Teardown(ctx context.Context) error {
	return nil
}

func (c *Checkup) Results() status.Results {
	return c.results
}

func appendSep(s *string, appended, sep string) {
	if s == nil {
		return
	}
	if *s == "" {
		*s = appended
		return
	}
	*s = *s + sep + appended
}
