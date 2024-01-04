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

package reporter

import (
	"k8s.io/client-go/kubernetes"

	kreporter "github.com/kiagnose/kiagnose/kiagnose/reporter"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

const (
	OCPVersionKey                                = "ocpVersion"
	CNVVersionKey                                = "cnvVersion"
	DefaultStorageClassKey                       = "defaultStorageClass"
	StorageProfilesWithEmptyClaimPropertySetsKey = "storageProfilesWithEmptyClaimPropertySets"
	StorageProfilesWithSpecClaimPropertySetsKey  = "storageProfilesWithSpecClaimPropertySets"
	StorageWithRWXKey                            = "storageWithRWX"
	StorageMissingVolumeSnapshotClassKey         = "storageMissingVolumeSnapshotClass"
	GoldenImagesNotUpToDateKey                   = "goldenImagesNotUpToDate"
	GoldenImagesNoDataSourceKey                  = "goldenImagesNoDataSource"
	VMsWithNonVirtRbdStorageClassKey             = "vmsWithNonVirtRbdStorageClass"
	VMsWithUnsetEfsStorageClassKey               = "vmsWithUnsetEfsStorageClass"
	VMBootFromGoldenImageKey                     = "vmBootFromGoldenImage"
	VMVolumeCloneKey                             = "vmVolumeClone"
	VMLiveMigrationKey                           = "vmLiveMigration"
	VMHotplugVolumeKey                           = "vmHotplugVolume"
)

type Reporter struct {
	kreporter.Reporter
}

func New(c kubernetes.Interface, configMapNamespace, configMapName string) *Reporter {
	r := kreporter.New(c, configMapNamespace, configMapName)
	return &Reporter{*r}
}

func (r *Reporter) Report(checkupStatus status.Status) error {
	if !r.Reporter.HasData() {
		return r.Reporter.Report(checkupStatus.Status)
	}

	checkupStatus.Succeeded = len(checkupStatus.FailureReason) == 0

	checkupStatus.Status.Results = FormatResults(checkupStatus.Results)

	return r.Reporter.Report(checkupStatus.Status)
}

// FormatResults returns a map representing the checkup results
func FormatResults(checkupResults status.Results) map[string]string {
	var emptyResults status.Results
	if checkupResults == emptyResults {
		return map[string]string{}
	}

	formattedResults := map[string]string{
		OCPVersionKey:          checkupResults.OCPVersion,
		CNVVersionKey:          checkupResults.CNVVersion,
		DefaultStorageClassKey: checkupResults.DefaultStorageClass,
		StorageProfilesWithEmptyClaimPropertySetsKey: checkupResults.StorageProfilesWithEmptyClaimPropertySets,
		StorageProfilesWithSpecClaimPropertySetsKey:  checkupResults.StorageProfilesWithSpecClaimPropertySets,
		StorageWithRWXKey:                    checkupResults.StorageWithRWX,
		StorageMissingVolumeSnapshotClassKey: checkupResults.StorageMissingVolumeSnapshotClass,
		GoldenImagesNotUpToDateKey:           checkupResults.GoldenImagesNotUpToDate,
		GoldenImagesNoDataSourceKey:          checkupResults.GoldenImagesNoDataSource,
		VMsWithNonVirtRbdStorageClassKey:     checkupResults.VMsWithNonVirtRbdStorageClass,
		VMsWithUnsetEfsStorageClassKey:       checkupResults.VMsWithUnsetEfsStorageClass,
		VMBootFromGoldenImageKey:             checkupResults.VMBootFromGoldenImage,
		VMVolumeCloneKey:                     checkupResults.VMVolumeClone,
		VMLiveMigrationKey:                   checkupResults.VMLiveMigration,
		VMHotplugVolumeKey:                   checkupResults.VMHotplugVolume,
	}

	return formattedResults
}
