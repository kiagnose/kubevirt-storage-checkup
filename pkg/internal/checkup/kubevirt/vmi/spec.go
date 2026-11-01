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
package vmi

import (
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kvcorev1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

type Option func(vm *kvcorev1.VirtualMachine)

func NewVM(name string, options ...Option) *kvcorev1.VirtualMachine {
	newVM := &kvcorev1.VirtualMachine{
		TypeMeta: metav1.TypeMeta{
			Kind:       kvcorev1.VirtualMachineInstanceGroupVersionKind.Kind,
			APIVersion: kvcorev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kvcorev1.VirtualMachineSpec{
			RunStrategy: Pointer(kvcorev1.RunStrategyAlways),
			Template:    &kvcorev1.VirtualMachineInstanceTemplateSpec{},
		},
	}

	for _, f := range options {
		f(newVM)
	}

	return newVM
}

type DataVolumeOption func(*cdiv1.DataVolumeSpec)

func WithDataVolume(dvName string, opts ...DataVolumeOption) Option {
	return func(vm *kvcorev1.VirtualMachine) {
		dvt := kvcorev1.DataVolumeTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Name: dvName,
			},
			Spec: cdiv1.DataVolumeSpec{
				Source:  &cdiv1.DataVolumeSource{},
				Storage: &cdiv1.StorageSpec{},
			},
		}

		for _, f := range opts {
			f(&dvt.Spec)
		}

		vm.Spec.DataVolumeTemplates = append(vm.Spec.DataVolumeTemplates, dvt)

		newVolume := kvcorev1.Volume{
			Name: dvName,
			VolumeSource: kvcorev1.VolumeSource{
				DataVolume: &kvcorev1.DataVolumeSource{
					Name: dvName,
				},
			},
		}
		vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, newVolume)
	}
}

func WithDataVolumePvcSource(pvc *corev1.PersistentVolumeClaim) DataVolumeOption {
	return func(dvSpec *cdiv1.DataVolumeSpec) {
		dvSpec.Source.PVC = &cdiv1.DataVolumeSourcePVC{
			Namespace: pvc.Namespace,
			Name:      pvc.Name,
		}

		if dvSpec.Storage.StorageClassName == nil {
			dvSpec.Storage.StorageClassName = pvc.Spec.StorageClassName
		}
	}
}

func WithDataVolumeSnapshotSource(snap *snapshotv1.VolumeSnapshot) DataVolumeOption {
	return func(dvSpec *cdiv1.DataVolumeSpec) {
		dvSpec.Source.Snapshot = &cdiv1.DataVolumeSourceSnapshot{
			Namespace: snap.Namespace,
			Name:      snap.Name,
		}
	}
}

func WithDataVolumeBlankSource() DataVolumeOption {
	return func(dvSpec *cdiv1.DataVolumeSpec) {
		dvSpec.Source.Blank = &cdiv1.DataVolumeBlankImage{}
		dvSpec.Storage.Resources.Requests = corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse("1Gi"),
		}
	}
}

func WithDataVolumeStorageClass(storageClass string) DataVolumeOption {
	return func(dvSpec *cdiv1.DataVolumeSpec) {
		dvSpec.Storage.StorageClassName = &storageClass
	}
}

func WithMemory(guestMemory string) Option {
	return func(vm *kvcorev1.VirtualMachine) {
		guestMemoryQuantity := resource.MustParse(guestMemory)
		vm.Spec.Template.Spec.Domain.Memory = &kvcorev1.Memory{
			Guest: &guestMemoryQuantity,
		}
	}
}

func WithTPM() Option {
	return func(vm *kvcorev1.VirtualMachine) {
		vm.Spec.Template.Spec.Domain.Devices.TPM = &kvcorev1.TPMDevice{
			Persistent: Pointer(true),
		}
	}
}

func WithTerminationGracePeriodSeconds(terminationGracePeriodSeconds int64) Option {
	return func(vm *kvcorev1.VirtualMachine) {
		vm.Spec.Template.Spec.TerminationGracePeriodSeconds = Pointer(terminationGracePeriodSeconds)
	}
}

func WithOwnerReference(ownerName, ownerUID string) Option {
	return func(vm *kvcorev1.VirtualMachine) {
		if ownerUID != "" && ownerName != "" {
			vm.ObjectMeta.OwnerReferences = append(vm.ObjectMeta.OwnerReferences, metav1.OwnerReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       ownerName,
				UID:        types.UID(ownerUID),
			})
		}
	}
}

func Pointer[T any](v T) *T {
	return &v
}
