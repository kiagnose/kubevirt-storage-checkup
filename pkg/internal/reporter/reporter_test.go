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

package reporter_test

import (
	"strconv"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	kconfigmap "github.com/kiagnose/kiagnose/kiagnose/configmap"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/reporter"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

const (
	testNamespace     = "target-ns"
	testConfigMapName = "storage-checkup-config"
)

func TestReportShouldSucceed(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(newConfigMap())
	testReporter := reporter.New(fakeClient, testNamespace, testConfigMapName)

	assert.NoError(t, testReporter.Report(status.Status{}))
}

func TestReportShouldSuccessfullyReportResults(t *testing.T) {
	const (
		failureReason1 = "some reason"
		failureReason2 = "some other reason"
	)

	t.Run("on checkup success", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset(newConfigMap())
		testReporter := reporter.New(fakeClient, testNamespace, testConfigMapName)

		var checkupStatus status.Status
		checkupStatus.StartTimestamp = time.Now()
		assert.NoError(t, testReporter.Report(checkupStatus))

		checkupStatus.FailureReason = []string{}
		checkupStatus.CompletionTimestamp = time.Now()
		checkupStatus.Results = status.Results{
			DefaultStorageClass:                       "test_sc",
			StorageProfilesWithEmptyClaimPropertySets: "sc1, sc2",
			StorageProfilesWithSpecClaimPropertySets:  "sc3, sc4",
			StorageWithRWX:                            "sc5, sc6",
			StorageMissingVolumeSnapshotClass:         "sc7, sc8",
			GoldenImagesNotUpToDate:                   "dic1, dic2",
			GoldenImagesNoDataSource:                  "dic3",
			VMsWithNonVirtRbdStorageClass:             "vm1,vm2",
			VMsWithUnsetEfsStorageClass:               "vm3,vm4",
			VMBootFromGoldenImage:                     "ok",
			VMVolumeClone:                             "snapshot",
			VMLiveMigration:                           "success",
			VMHotplugVolume:                           "fail",
		}

		assert.NoError(t, testReporter.Report(checkupStatus))

		expectedReportData := map[string]string{
			"status.succeeded":                                        strconv.FormatBool(true),
			"status.failureReason":                                    "",
			"status.startTimestamp":                                   timestamp(checkupStatus.StartTimestamp),
			"status.completionTimestamp":                              timestamp(checkupStatus.CompletionTimestamp),
			"status.result.defaultStorageClass":                       checkupStatus.Results.DefaultStorageClass,
			"status.result.storageProfilesWithEmptyClaimPropertySets": checkupStatus.Results.StorageProfilesWithEmptyClaimPropertySets,
			"status.result.storageProfilesWithSpecClaimPropertySets":  checkupStatus.Results.StorageProfilesWithSpecClaimPropertySets,
			"status.result.storageWithRWX":                            checkupStatus.Results.StorageWithRWX,
			"status.result.storageMissingVolumeSnapshotClass":         checkupStatus.Results.StorageMissingVolumeSnapshotClass,
			"status.result.goldenImagesNotUpToDate":                   checkupStatus.Results.GoldenImagesNotUpToDate,
			"status.result.goldenImagesNoDataSource":                  checkupStatus.Results.GoldenImagesNoDataSource,
			"status.result.vmsWithNonVirtRbdStorageClass":             checkupStatus.Results.VMsWithNonVirtRbdStorageClass,
			"status.result.vmsWithUnsetEfsStorageClass":               checkupStatus.Results.VMsWithUnsetEfsStorageClass,
			"status.result.vmBootFromGoldenImage":                     checkupStatus.Results.VMBootFromGoldenImage,
			"status.result.vmVolumeClone":                             checkupStatus.Results.VMVolumeClone,
			"status.result.vmLiveMigration":                           checkupStatus.Results.VMLiveMigration,
			"status.result.vmHotplugVolume":                           checkupStatus.Results.VMHotplugVolume,
		}

		assert.Equal(t, expectedReportData, getCheckupData(t, fakeClient, testNamespace, testConfigMapName))
	})

	t.Run("on checkup failure", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset(newConfigMap())
		testReporter := reporter.New(fakeClient, testNamespace, testConfigMapName)

		var checkupStatus status.Status
		checkupStatus.StartTimestamp = time.Now()
		assert.NoError(t, testReporter.Report(checkupStatus))

		checkupStatus.FailureReason = []string{failureReason1}
		checkupStatus.CompletionTimestamp = time.Now()
		assert.NoError(t, testReporter.Report(checkupStatus))

		expectedReportData := map[string]string{
			"status.succeeded":           strconv.FormatBool(false),
			"status.failureReason":       failureReason1,
			"status.startTimestamp":      timestamp(checkupStatus.StartTimestamp),
			"status.completionTimestamp": timestamp(checkupStatus.CompletionTimestamp),
		}

		assert.Equal(t, expectedReportData, getCheckupData(t, fakeClient, testNamespace, testConfigMapName))
	})

	t.Run("on checkup with multiple failures", func(t *testing.T) {
		fakeClient := fake.NewSimpleClientset(newConfigMap())
		testReporter := reporter.New(fakeClient, testNamespace, testConfigMapName)

		var checkupStatus status.Status
		checkupStatus.StartTimestamp = time.Now()
		checkupStatus.CompletionTimestamp = time.Now()
		assert.NoError(t, testReporter.Report(checkupStatus))

		checkupStatus.FailureReason = []string{failureReason1, failureReason2}
		assert.NoError(t, testReporter.Report(checkupStatus))

		expectedReportData := map[string]string{
			"status.succeeded":           strconv.FormatBool(false),
			"status.failureReason":       failureReason1 + "," + failureReason2,
			"status.startTimestamp":      timestamp(checkupStatus.StartTimestamp),
			"status.completionTimestamp": timestamp(checkupStatus.CompletionTimestamp),
		}

		assert.Equal(t, expectedReportData, getCheckupData(t, fakeClient, testNamespace, testConfigMapName))
	})
}

func TestReportShouldFailWhenCannotUpdateConfigMap(t *testing.T) {
	// ConfigMap does not exist
	fakeClient := fake.NewSimpleClientset()

	testReporter := reporter.New(fakeClient, testNamespace, testConfigMapName)

	assert.ErrorContains(t, testReporter.Report(status.Status{}), "not found")
}

func newConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{},
	}
}

func getCheckupData(t *testing.T, client kubernetes.Interface, configMapNamespace, configMapName string) map[string]string {
	configMap, err := kconfigmap.Get(client, configMapNamespace, configMapName)
	assert.NoError(t, err)

	return configMap.Data
}

func timestamp(t time.Time) string {
	return t.Format(time.RFC3339)
}
