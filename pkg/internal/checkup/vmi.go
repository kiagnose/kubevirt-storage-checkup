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
	"fmt"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/vmi"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"

	corev1 "k8s.io/api/core/v1"

	kvcorev1 "kubevirt.io/api/core/v1"
)

const (
	guestMemory                   = "2Gi"
	terminationGracePeriodSeconds = 0
)

func newVMUnderTest(name string, pvc *corev1.PersistentVolumeClaim, snap *snapshotv1.VolumeSnapshot,
	checkupConfig config.Config, addBlankDataVolume bool) *kvcorev1.VirtualMachine {
	dvName := getVMDvName(name)
	dvOpts := []vmi.DataVolumeOption{}

	if pvc != nil {
		dvOpts = append(dvOpts, vmi.WithDataVolumePvcSource(pvc))
	} else if snap != nil {
		dvOpts = append(dvOpts, vmi.WithDataVolumeSnapshotSource(snap))
	}

	if checkupConfig.StorageClass != "" {
		dvOpts = append(dvOpts, vmi.WithDataVolumeStorageClass(checkupConfig.StorageClass))
	}

	optionsToApply := []vmi.Option{
		vmi.WithDataVolume(dvName, dvOpts...),
		vmi.WithMemory(guestMemory),
		vmi.WithTerminationGracePeriodSeconds(terminationGracePeriodSeconds),
		vmi.WithOwnerReference(checkupConfig.PodName, checkupConfig.PodUID),
	}

	if addBlankDataVolume {
		blankDvName := fmt.Sprintf("%s-blank", dvName)
		dvOpts := []vmi.DataVolumeOption{vmi.WithDataVolumeBlankSource()}
		if checkupConfig.StorageClass != "" {
			dvOpts = append(dvOpts, vmi.WithDataVolumeStorageClass(checkupConfig.StorageClass))
		}
		optionsToApply = append(optionsToApply, vmi.WithDataVolume(blankDvName, dvOpts...))
	}

	return vmi.NewVM(name, optionsToApply...)
}

func getVMDvName(vmName string) string {
	return fmt.Sprintf("%s-dv", vmName)
}
