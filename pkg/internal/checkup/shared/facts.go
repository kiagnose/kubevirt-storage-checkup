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
 * Copyright 2026 Red Hat, Inc.
 *
 */

package shared

import (
	"fmt"

	storagev1 "k8s.io/api/storage/v1"
)

const (
	ErrNoDefaultStorageClass = "No default storage class found. " +
		"Set a default StorageClass on the cluster or provide one via spec.param.storageClass"
	ErrMultipleDefaultStorageClasses = "Multiple default storage classes found. Ensure only one StorageClass is annotated as default"
)

// State holds facts shared between the validation phases during one checkup run.
type State struct {
	StorageClasses       *storagev1.StorageClassList
	SelectedStorageClass *storagev1.StorageClass
	DefaultStorageClass  string
	CSIDriver            *storagev1.CSIDriver
}

// FindStorageClass returns the named class from the supplied list.
func FindStorageClass(scs *storagev1.StorageClassList, name string) (*storagev1.StorageClass, error) {
	if scs != nil {
		for i := range scs.Items {
			if scs.Items[i].Name == name {
				return &scs.Items[i], nil
			}
		}
	}
	return nil, fmt.Errorf("storage class %q not found", name)
}

// FindDefaultStorageClass prefers the virtualization default over the Kubernetes default.
// It rejects duplicate defaults of either kind and returns nil, nil when no default exists.
func FindDefaultStorageClass(scs *storagev1.StorageClassList) (*storagev1.StorageClass, error) {
	if scs == nil {
		return nil, nil
	}
	const (
		defaultAnnotation     = "storageclass.kubernetes.io/is-default-class"
		defaultVirtAnnotation = "storageclass.kubevirt.io/is-default-virt-class"
	)
	var defaultClass, defaultVirtClass *storagev1.StorageClass
	for i := range scs.Items {
		sc := &scs.Items[i]
		if sc.Annotations[defaultVirtAnnotation] == "true" {
			if defaultVirtClass != nil {
				return nil, fmt.Errorf("default StorageClass validation failed: %s", ErrMultipleDefaultStorageClasses)
			}
			defaultVirtClass = sc
		}
		if sc.Annotations[defaultAnnotation] == "true" {
			if defaultClass != nil {
				return nil, fmt.Errorf("default StorageClass validation failed: %s", ErrMultipleDefaultStorageClasses)
			}
			defaultClass = sc
		}
	}
	if defaultVirtClass != nil {
		return defaultVirtClass, nil
	}
	return defaultClass, nil
}
