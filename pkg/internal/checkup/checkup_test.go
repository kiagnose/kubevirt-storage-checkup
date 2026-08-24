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

package checkup_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	configv1 "github.com/openshift/api/config/v1"
	assert "github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	kvcorev1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/reporter"

	snapshotv1alpha1 "kubevirt.io/api/snapshot/v1alpha1"
)

const (
	testNamespace = "target-ns"
)

var (
	testVMIName    = "test-vmi"
	testScName     = "test-sc"
	testScName2    = "test-sc2"
	efsSc          = "efs.csi.aws.com"
	testDIC        = "test-dic"
	testPodName    = "test-pod"
	testPodUID     = "test-uid"
	testOCPVersion = "1.2.3"
	testCNVVersion = "4.5.6"
)

func TestCheckupShouldSucceed(t *testing.T) {
	testClient := newClientStub(clientConfig{})
	testConfig := newTestConfig()

	testCheckup := checkup.New(testClient, testNamespace, testConfig)

	assert.NoError(t, testCheckup.Setup(context.Background()))
	assert.NoError(t, testCheckup.Run(context.Background()))

	vmiUnderTestName := testClient.VMIName(checkup.VMIUnderTestNamePrefix)
	assert.NotEmpty(t, vmiUnderTestName)

	assert.NoError(t, testCheckup.Teardown(context.Background()))

	assert.Empty(t, testClient.createdVMs)
	assert.Empty(t, testClient.createdVMIs)

	expectedResults := successfulRunResults(vmiUnderTestName)
	actualResults := reporter.FormatResults(testCheckup.Results())
	assert.Equal(t, expectedResults, actualResults)
}

var tests = map[string]struct {
	clientConfig    clientConfig
	expectedResults map[string]string
	expectedErr     string
	resultsContains bool
	vmiTimeout      time.Duration
}{
	"noStorageClasses": {
		clientConfig: clientConfig{noStorageClasses: true, expectNoVMI: true},
		expectedResults: map[string]string{
			reporter.DefaultStorageClassKey:   checkup.ErrNoDefaultStorageClass,
			reporter.PVCBoundKey:              checkup.MessageSkipNoDefaultStorageClass,
			reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoDefaultStorageClass,
			reporter.ConcurrentVMBootKey:      checkup.MessageSkipNoDefaultStorageClass,
		},
		expectedErr: checkup.ErrNoDefaultStorageClass,
	},
	"noDefaultStorageClass": {
		clientConfig: clientConfig{noDefaultStorageClass: true, expectNoVMI: true},
		expectedResults: map[string]string{
			reporter.DefaultStorageClassKey:   checkup.ErrNoDefaultStorageClass,
			reporter.PVCBoundKey:              checkup.MessageSkipNoDefaultStorageClass,
			reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoDefaultStorageClass,
			reporter.ConcurrentVMBootKey:      checkup.MessageSkipNoDefaultStorageClass,
		},
		expectedErr: checkup.ErrNoDefaultStorageClass,
	},
	"onlyDefaultStorageClass": {
		clientConfig:    clientConfig{onlyDefaultStorageClass: true},
		expectedResults: map[string]string{reporter.DefaultStorageClassKey: testScName2},
		expectedErr:     "",
	},
	"onlyDefaultVirtStorageClass": {
		clientConfig:    clientConfig{onlyDefaultVirtStorageClass: true},
		expectedResults: map[string]string{reporter.DefaultStorageClassKey: testScName},
		expectedErr:     "",
	},
	"bothDefaultStorageClasses": {
		clientConfig:    clientConfig{bothDefaultStorageClasses: true},
		expectedResults: map[string]string{reporter.DefaultStorageClassKey: testScName},
		expectedErr:     "",
	},
	"multipleDefaultStorageClasses": {
		clientConfig:    clientConfig{multipleDefaultStorageClasses: true},
		expectedResults: map[string]string{reporter.DefaultStorageClassKey: checkup.ErrMultipleDefaultStorageClasses},
		expectedErr:     checkup.ErrMultipleDefaultStorageClasses,
	},
	"multipleDefaultVirtStorageClasses": {
		clientConfig:    clientConfig{multipleDefaultVirtStorageClasses: true},
		expectedResults: map[string]string{reporter.DefaultStorageClassKey: checkup.ErrMultipleDefaultStorageClasses},
		expectedErr:     checkup.ErrMultipleDefaultStorageClasses,
	},
	"failPvcBound": {
		clientConfig:    clientConfig{failPvcBound: true},
		expectedResults: map[string]string{reporter.PVCBoundKey: checkup.ErrPvcNotBound},
		expectedErr:     checkup.ErrPvcNotBound,
	},
	"storageProfileIncomplete": {
		clientConfig: clientConfig{spIncomplete: true},
		expectedResults: map[string]string{reporter.StorageProfilesWithEmptyClaimPropertySetsKey: testScName,
			reporter.StorageProfilesWithSpecClaimPropertySetsKey: testScName, reporter.StorageProfilesWithRWXKey: ""},
		expectedErr: checkup.ErrEmptyClaimPropertySets,
	},
	"noVolumeSnapshotClasses": {
		clientConfig: clientConfig{noVolumeSnapshotClasses: true},
		expectedResults: map[string]string{reporter.StorageProfileMissingVolumeSnapshotClassKey: testScName,
			reporter.StorageProfilesWithSmartCloneKey: ""},
		expectedErr: "",
	},
	"dataSourceNotReady": {
		clientConfig: clientConfig{dataSourceNotReady: true, expectNoVMI: true},
		expectedResults: map[string]string{reporter.GoldenImagesNotUpToDateKey: testNamespace + "/" + testDIC,
			reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoGoldenImage,
			reporter.ConcurrentVMBootKey:      checkup.MessageSkipNoGoldenImage,
		},
		expectedErr: checkup.ErrGoldenImagesNotUpToDate,
	},
	"dicNoDataSource": {
		clientConfig: clientConfig{dicNoDataSource: true, expectNoVMI: true},
		expectedResults: map[string]string{reporter.GoldenImagesNoDataSourceKey: testNamespace + "/" + testDIC,
			reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoGoldenImage,
			reporter.ConcurrentVMBootKey:      checkup.MessageSkipNoGoldenImage,
		},
		expectedErr: checkup.ErrGoldenImageNoDataSource,
	},
	"vmisWithUnsetEfsSC": {
		clientConfig:    clientConfig{unsetEfsStorageClass: true},
		expectedResults: map[string]string{reporter.VMsWithUnsetEfsStorageClassKey: testNamespace + "/" + testVMIName},
		expectedErr:     checkup.ErrVMsWithUnsetEfsStorageClass,
	},
	"dvCloneFallback": {
		clientConfig:    clientConfig{cloneFallback: true},
		expectedResults: map[string]string{reporter.VMVolumeCloneKey: "DV cloneType: \"host-assisted\"\nDV clone fallback reason: reason"},
		expectedErr:     "DV clone fallback reason: reason",
	},
	"migrationFails": {
		clientConfig:    clientConfig{failMigration: true},
		expectedResults: map[string]string{reporter.VMLiveMigrationKey: "failed waiting for VMI \"%s\" migration completed: migration failed"},
		expectedErr:     "migration failed",
	},
	"snapshotFails": {
		clientConfig: clientConfig{failSnapshot: true},
		expectedResults: map[string]string{
			reporter.VMSnapshotKey: `failed waiting for VMSnapshot "snapshot-%s" after 0s: snapshot failed`,
			reporter.VMRestoreKey:  checkup.MessageSkipNoSnapshot,
		},
		expectedErr: `snapshot failed`,
	},
	"restoreFails": {
		clientConfig: clientConfig{failRestore: true},
		expectedResults: map[string]string{
			reporter.VMRestoreKey: `VMRestore "restore-%s" has no volume restores`,
		},
		expectedErr: `has no volume restores`,
	},
	"stopFails": {
		clientConfig: clientConfig{failStop: true},
		expectedResults: map[string]string{
			reporter.VMSnapshotKey: fmt.Sprintf("VMSnapshot name: snapshot-%%s"),
		},
		resultsContains: true,
		expectedErr:     "failed to stop VM",
	},
	"skipMigrationOnSingleNode": {
		clientConfig:    clientConfig{singleNode: true},
		expectedResults: map[string]string{reporter.VMLiveMigrationKey: "Skip check - single node"},
		expectedErr:     "",
	},
}

func TestCheckupShouldReturnErrorWhen(t *testing.T) {
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testClient := newClientStub(tc.clientConfig)
			testConfig := newTestConfig()
			if tc.vmiTimeout > 0 {
				testConfig.VMITimeout = tc.vmiTimeout
			}

			testCheckup := checkup.New(testClient, testNamespace, testConfig)

			assert.NoError(t, testCheckup.Setup(context.Background()))
			err := testCheckup.Run(context.Background())

			vmiUnderTestName := testClient.VMIName(checkup.VMIUnderTestNamePrefix)
			if testClient.expectNoVMI {
				assert.Empty(t, vmiUnderTestName)
			} else {
				assert.NotEmpty(t, vmiUnderTestName)
				checkOwnerRef(t, testClient)
			}

			actualResults := reporter.FormatResults(testCheckup.Results())
			if tc.resultsContains {
				for key, substr := range tc.expectedResults {
					substr = strings.ReplaceAll(substr, "%s", vmiUnderTestName)
					assert.Contains(t, actualResults[key], substr, "key %s", key)
				}
			} else {
				expectedResults := fullExpectedResults(vmiUnderTestName, tc.expectedResults)
				assert.Equal(t, expectedResults, actualResults)
			}
			if tc.expectedErr != "" {
				assert.ErrorContains(t, err, tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func checkOwnerRef(t *testing.T, testClient *clientStub) {
	vmiUnderTestName := testClient.VMIName(checkup.VMIUnderTestNamePrefix)
	vmFullName := objectFullName(testNamespace, vmiUnderTestName)
	vm, exists := testClient.createdVMs[vmFullName]
	assert.True(t, exists)
	assert.Len(t, vm.OwnerReferences, 1)
	ownerRef := vm.OwnerReferences[0]
	assert.Equal(t, ownerRef.Name, testPodName)
	assert.Equal(t, ownerRef.UID, types.UID(testPodUID))
}

func fullExpectedResults(vmiUnderTestName string, expectedResults map[string]string) map[string]string {
	fullResults := successfulRunResults(vmiUnderTestName)
	if vmiUnderTestName == "" {
		expectedResultsNoVMI(fullResults)
	}
	for key, expectedResult := range expectedResults {
		expectedResult = strings.ReplaceAll(expectedResult, "%s", vmiUnderTestName)
		fullResults[key] = expectedResult
	}
	return fullResults
}

func expectedResultsNoVMI(expectedResults map[string]string) {
	expectedResults[reporter.VMHotplugVolumeKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMLiveMigrationKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMSnapshotKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMRestoreKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMVolumeCloneKey] = ""
}

// FIXME: fill relevant results
func successfulRunResults(vmiUnderTestName string) map[string]string {
	return map[string]string{
		reporter.OCPVersionKey:                                testOCPVersion,
		reporter.CNVVersionKey:                                testCNVVersion,
		reporter.DefaultStorageClassKey:                       testScName,
		reporter.PVCBoundKey:                                  "PVC \"checkup-pvc\" bound",
		reporter.StorageProfilesWithEmptyClaimPropertySetsKey: "",
		reporter.StorageProfilesWithSpecClaimPropertySetsKey:  "",
		reporter.StorageProfilesWithSmartCloneKey:             testScName,
		reporter.StorageProfilesWithRWXKey:                    testScName,
		reporter.StorageProfileMissingVolumeSnapshotClassKey:  "",
		reporter.GoldenImagesNotUpToDateKey:                   "",
		reporter.GoldenImagesNoDataSourceKey:                  "",
		reporter.VMsWithNonVirtRbdStorageClassKey:             "",
		reporter.VMsWithUnsetEfsStorageClassKey:               "",
		reporter.VMBootFromGoldenImageKey:                     fmt.Sprintf("VMI %q successfully booted", vmiUnderTestName),
		reporter.VMVolumeCloneKey:                             "DV cloneType: \"\"",
		reporter.VMLiveMigrationKey:                           fmt.Sprintf("VMI %q migration completed", vmiUnderTestName),
		reporter.VMHotplugVolumeKey: fmt.Sprintf("VMI %q hotplug volume ready\nVMI %q hotplug volume removed",
			vmiUnderTestName, vmiUnderTestName),
		reporter.ConcurrentVMBootKey: "Boot completed on all VMs on time",
		reporter.VMSnapshotKey: strings.Join([]string{
			fmt.Sprintf("VMSnapshot name: %s", "snapshot-"+vmiUnderTestName),
			fmt.Sprintf("VMSnapshot for VM %q succeeded", vmiUnderTestName),
			"Creation time: 03:04:05 UTC",
			"Creation duration: 0s",
			"Ready duration: 0s",
			"ReadyToUse=true",
			"Indications: [Online GuestAgent]",
			fmt.Sprintf("Included volumes: [%s-dv]", vmiUnderTestName),
		}, "\n"),
		reporter.VMRestoreKey: strings.Join([]string{
			fmt.Sprintf("VMRestore name: %s", "restore-"+vmiUnderTestName),
			fmt.Sprintf("VMRestore for VM %q succeeded", vmiUnderTestName),
			"Restore time: 03:05:06 UTC",
			"Complete duration: 0s",
			"Complete=true",
		}, "\n"),
	}
}

type clientConfig struct {
	skipDeletion                      bool
	noStorageClasses                  bool
	noDefaultStorageClass             bool
	onlyDefaultStorageClass           bool
	onlyDefaultVirtStorageClass       bool
	bothDefaultStorageClasses         bool
	multipleDefaultStorageClasses     bool
	multipleDefaultVirtStorageClasses bool
	failPvcBound                      bool
	unsetEfsStorageClass              bool
	spIncomplete                      bool
	noVolumeSnapshotClasses           bool
	dicNoDataSource                   bool
	dataSourceNotReady                bool
	expectNoVMI                       bool
	cloneFallback                     bool
	failMigration                     bool
	failSnapshot                      bool
	failRestore                       bool
	failStop                          bool
	singleNode                        bool
}

type clientStub struct {
	createdVMs        map[string]*kvcorev1.VirtualMachine
	createdVMIs       map[string]*kvcorev1.VirtualMachineInstance
	createdSnapshots  map[string]*snapshotv1alpha1.VirtualMachineSnapshot
	createdRestores   map[string]*snapshotv1alpha1.VirtualMachineRestore
	vmCreationFailure error
	vmDeletionFailure error
	vmiGetFailure     error
	clientConfig
}

func newClientStub(clientConfig clientConfig) *clientStub {
	return &clientStub{
		createdVMs:       map[string]*kvcorev1.VirtualMachine{},
		createdVMIs:      map[string]*kvcorev1.VirtualMachineInstance{},
		createdSnapshots: map[string]*snapshotv1alpha1.VirtualMachineSnapshot{},
		createdRestores:  map[string]*snapshotv1alpha1.VirtualMachineRestore{},
		clientConfig:     clientConfig,
	}
}

func (cs *clientStub) CreateVirtualMachine(ctx context.Context, namespace string, vm *kvcorev1.VirtualMachine) (
	*kvcorev1.VirtualMachine, error) {
	if cs.vmCreationFailure != nil {
		return nil, cs.vmCreationFailure
	}

	vm.Namespace = namespace
	vmFullName := objectFullName(vm.Namespace, vm.Name)
	cs.createdVMs[vmFullName] = vm

	vmi := &kvcorev1.VirtualMachineInstance{
		ObjectMeta: vm.Spec.Template.ObjectMeta,
		Spec:       vm.Spec.Template.Spec,
		Status: kvcorev1.VirtualMachineInstanceStatus{
			Conditions: []kvcorev1.VirtualMachineInstanceCondition{
				{
					Type:   kvcorev1.VirtualMachineInstanceReady,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   kvcorev1.VirtualMachineInstanceIsMigratable,
					Status: corev1.ConditionTrue,
				},
				{
					Type:   kvcorev1.VirtualMachineInstanceAgentConnected,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	vmi.Name = vm.Name
	vmi.Namespace = namespace
	cs.createdVMIs[vmFullName] = vmi

	return vm, nil
}

func (cs *clientStub) StopVirtualMachine(ctx context.Context, namespace, name string, stopOptions *kvcorev1.StopOptions) error {
	if cs.failStop {
		return fmt.Errorf("failed to stop VM")
	}
	vmFullName := objectFullName(namespace, name)
	vm, exist := cs.createdVMs[vmFullName]
	if !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, name)
	}
	// Mimic Stop on Always: KubeVirt moves to Halted and removes the VMI.
	runStrategy := kvcorev1.RunStrategyHalted
	vm.Spec.RunStrategy = &runStrategy
	vm.Spec.Running = nil
	delete(cs.createdVMIs, vmFullName)
	return nil
}

func (cs *clientStub) DeleteVirtualMachine(ctx context.Context, namespace, name string) error {
	if cs.vmDeletionFailure != nil {
		return cs.vmDeletionFailure
	}

	vmFullName := objectFullName(namespace, name)
	if _, exist := cs.createdVMs[vmFullName]; !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, name)
	}

	if !cs.skipDeletion {
		delete(cs.createdVMs, vmFullName)
		delete(cs.createdVMIs, vmFullName)
	}

	return nil
}

func (cs *clientStub) GetVirtualMachineInstance(ctx context.Context, namespace, name string) (*kvcorev1.VirtualMachineInstance, error) {
	if cs.vmiGetFailure != nil {
		return nil, cs.vmiGetFailure
	}

	vmiFullName := objectFullName(namespace, name)
	vmi, exist := cs.createdVMIs[vmiFullName]
	if !exist {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}, name)
	}

	return vmi, nil
}

func (cs *clientStub) CreateVirtualMachineInstanceMigration(ctx context.Context, namespace string,
	vmim *kvcorev1.VirtualMachineInstanceMigration) (*kvcorev1.VirtualMachineInstanceMigration, error) {
	name := vmim.Spec.VMIName
	vmiFullName := objectFullName(namespace, name)
	vmi, exist := cs.createdVMIs[vmiFullName]
	if !exist {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}, name)
	}
	vmi.Status.MigrationState = &kvcorev1.VirtualMachineInstanceMigrationState{
		Completed: true,
	}
	if cs.failMigration {
		vmi.Status.MigrationState = &kvcorev1.VirtualMachineInstanceMigrationState{
			Completed: false,
			Failed:    true,
		}
	}

	return vmim, nil
}

func (cs *clientStub) GetVirtualMachine(ctx context.Context, namespace, name string) (*kvcorev1.VirtualMachine, error) {
	vmFullName := objectFullName(namespace, name)
	vm, exist := cs.createdVMs[vmFullName]
	if !exist {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, name)
	}
	return vm.DeepCopy(), nil
}

func (cs *clientStub) PatchVirtualMachine(ctx context.Context, namespace string, _, modified *kvcorev1.VirtualMachine) (
	*kvcorev1.VirtualMachine, error) {
	vmFullName := objectFullName(namespace, modified.Name)
	if _, exist := cs.createdVMs[vmFullName]; !exist {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, modified.Name)
	}
	cs.createdVMs[vmFullName] = modified

	if vmi, exist := cs.createdVMIs[vmFullName]; exist {
		syncHotplugVolumeStatus(vmi, modified)
	}

	return modified, nil
}

func syncHotplugVolumeStatus(vmi *kvcorev1.VirtualMachineInstance, vm *kvcorev1.VirtualMachine) {
	hotplugNames := map[string]struct{}{}
	for _, vol := range vm.Spec.Template.Spec.Volumes {
		if vol.DataVolume != nil && vol.DataVolume.Hotpluggable {
			hotplugNames[vol.Name] = struct{}{}
		}
	}

	var volumeStatus []kvcorev1.VolumeStatus
	existingHotplug := map[string]struct{}{}
	for _, vs := range vmi.Status.VolumeStatus {
		if vs.HotplugVolume == nil {
			volumeStatus = append(volumeStatus, vs)
			continue
		}
		if _, ok := hotplugNames[vs.Name]; ok {
			volumeStatus = append(volumeStatus, vs)
			existingHotplug[vs.Name] = struct{}{}
		}
	}
	for name := range hotplugNames {
		if _, ok := existingHotplug[name]; ok {
			continue
		}
		volumeStatus = append(volumeStatus, kvcorev1.VolumeStatus{
			Name:          name,
			HotplugVolume: &kvcorev1.HotplugVolumeStatus{},
			Phase:         kvcorev1.VolumeReady,
		})
	}
	vmi.Status.VolumeStatus = volumeStatus
}

func (cs *clientStub) CreateDataVolume(ctx context.Context, namespace string, dv *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	return nil, nil
}

func (cs *clientStub) DeleteDataVolume(ctx context.Context, namespace, name string) error {
	return nil
}

func (cs *clientStub) DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error {
	return nil
}

func (cs *clientStub) ListNodes(ctx context.Context) (*corev1.NodeList, error) {
	nodeList := &corev1.NodeList{}
	itemCount := 2
	if cs.singleNode {
		itemCount = 1
	}
	for i := 0; i < itemCount; i++ {
		nodeList.Items = append(nodeList.Items, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
		})
	}

	return nodeList, nil
}

func (cs *clientStub) ListNamespaces(ctx context.Context) (*corev1.NamespaceList, error) {
	nsList := &corev1.NamespaceList{
		Items: []corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			},
		},
	}
	return nsList, nil
}

func (cs *clientStub) ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error) {
	if cs.noStorageClasses {
		return &storagev1.StorageClassList{}, nil
	}
	scList := &storagev1.StorageClassList{
		Items: []storagev1.StorageClass{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: testScName,
					Annotations: map[string]string{
						checkup.AnnDefaultVirtStorageClass: checkup.StrTrue,
						checkup.AnnDefaultStorageClass:     checkup.StrFalse,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: testScName2,
					Annotations: map[string]string{
						checkup.AnnDefaultVirtStorageClass: checkup.StrFalse,
						checkup.AnnDefaultStorageClass:     checkup.StrTrue,
					},
				},
			},
		},
	}
	if cs.onlyDefaultStorageClass || cs.noDefaultStorageClass {
		scList.Items[0].Annotations[checkup.AnnDefaultVirtStorageClass] = checkup.StrFalse
	}
	if cs.onlyDefaultVirtStorageClass || cs.noDefaultStorageClass {
		scList.Items[1].Annotations[checkup.AnnDefaultStorageClass] = checkup.StrFalse
	}
	if cs.multipleDefaultStorageClasses {
		scList.Items[0].Annotations[checkup.AnnDefaultStorageClass] = checkup.StrTrue
	}
	if cs.multipleDefaultVirtStorageClasses {
		scList.Items[1].Annotations[checkup.AnnDefaultVirtStorageClass] = checkup.StrTrue
	}
	if cs.unsetEfsStorageClass {
		scList.Items = append(scList.Items, storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-sc-unset-efs",
				Annotations: map[string]string{checkup.AnnDefaultStorageClass: checkup.StrFalse},
			},
			Provisioner: efsSc,
			Parameters:  map[string]string{"uid": ""},
		})
	}
	return scList, nil
}

func (cs *clientStub) ListStorageProfiles(ctx context.Context) (*cdiv1.StorageProfileList, error) {
	spList := &cdiv1.StorageProfileList{
		Items: []cdiv1.StorageProfile{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: testScName,
				},
				Status: cdiv1.StorageProfileStatus{
					Provisioner:       &testScName,
					ClaimPropertySets: []cdiv1.ClaimPropertySet{{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}}},
				},
			},
		},
	}
	if cs.spIncomplete {
		spList.Items[0].Status.ClaimPropertySets = []cdiv1.ClaimPropertySet{}
		spList.Items[0].Spec.ClaimPropertySets = []cdiv1.ClaimPropertySet{
			{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}},
		}
	}
	return spList, nil
}

func (cs *clientStub) ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error) {
	if cs.noVolumeSnapshotClasses {
		return &snapshotv1.VolumeSnapshotClassList{}, nil
	}
	vscList := &snapshotv1.VolumeSnapshotClassList{
		Items: []snapshotv1.VolumeSnapshotClass{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: testScName,
				},
				Driver: testScName,
			},
		},
	}
	return vscList, nil
}

func (cs *clientStub) ListDataImportCrons(ctx context.Context, namespace string) (*cdiv1.DataImportCronList, error) {
	if namespace != testNamespace {
		return &cdiv1.DataImportCronList{}, nil
	}

	dicList := &cdiv1.DataImportCronList{
		Items: []cdiv1.DataImportCron{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testDIC,
					Namespace: testNamespace,
				},
				Status: cdiv1.DataImportCronStatus{
					Conditions: []cdiv1.DataImportCronCondition{
						{
							Type: cdiv1.DataImportCronUpToDate,
							ConditionState: cdiv1.ConditionState{
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
		},
	}
	return dicList, nil
}

func (cs *clientStub) ListVirtualMachinesInstances(ctx context.Context, namespace string) (*kvcorev1.VirtualMachineInstanceList, error) {
	vmiList := &kvcorev1.VirtualMachineInstanceList{
		Items: []kvcorev1.VirtualMachineInstance{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testVMIName,
					Namespace: testNamespace,
				},
				Spec: kvcorev1.VirtualMachineInstanceSpec{
					Volumes: []kvcorev1.Volume{
						{
							Name: "test-vol",
							VolumeSource: kvcorev1.VolumeSource{
								PersistentVolumeClaim: &kvcorev1.PersistentVolumeClaimVolumeSource{
									PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "test-pvc",
									},
								},
							},
						},
					},
				},
				Status: kvcorev1.VirtualMachineInstanceStatus{
					Phase: kvcorev1.Running,
					Conditions: []kvcorev1.VirtualMachineInstanceCondition{
						{
							Type:   kvcorev1.VirtualMachineInstanceReady,
							Status: corev1.ConditionTrue,
						},
						{
							Type:   kvcorev1.VirtualMachineInstanceIsMigratable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
		},
	}

	return vmiList, nil
}

func (cs *clientStub) GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	return ns, nil
}

func (cs *clientStub) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	blockMode := corev1.PersistentVolumeBlock
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeMode:       &blockMode,
			StorageClassName: &testScName,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		},
	}

	if cs.failPvcBound {
		pvc.Status.Phase = corev1.ClaimPending
	} else {
		pvc.Status.Phase = corev1.ClaimBound
	}

	if cs.cloneFallback {
		pvc.Annotations = map[string]string{
			"cdi.kubevirt.io/cloneType":           "host-assisted",
			"cdi.kubevirt.io/cloneFallbackReason": "reason",
		}
	}

	return pvc, nil
}

func (cs *clientStub) GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pv",
			Namespace: testNamespace,
		},
		Spec: corev1.PersistentVolumeSpec{},
	}
	if cs.unsetEfsStorageClass {
		pv.Spec.PersistentVolumeSource = corev1.PersistentVolumeSource{
			CSI: &corev1.CSIPersistentVolumeSource{
				Driver: efsSc,
			},
		}
		pv.Spec.StorageClassName = "test-sc-unset-efs"
	}

	return pv, nil
}

func (cs *clientStub) GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	return nil, nil
}

func (cs *clientStub) GetCSIDriver(ctx context.Context, name string) (*storagev1.CSIDriver, error) {
	return nil, nil
}

func (cs *clientStub) GetDataSource(ctx context.Context, namespace, name string) (*cdiv1.DataSource, error) {
	das := &cdiv1.DataSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-das",
			Namespace: testNamespace,
		},
		Spec: cdiv1.DataSourceSpec{
			Source: cdiv1.DataSourceSource{
				PVC: &cdiv1.DataVolumeSourcePVC{
					Name:      "test-pvc",
					Namespace: testNamespace,
				},
			},
		},
		Status: cdiv1.DataSourceStatus{
			Conditions: []cdiv1.DataSourceCondition{
				{
					Type: cdiv1.DataSourceReady,
					ConditionState: cdiv1.ConditionState{
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}
	if cs.dicNoDataSource {
		das.Spec.Source = cdiv1.DataSourceSource{}
	}
	if cs.dataSourceNotReady {
		das.Status.Conditions[0].Status = corev1.ConditionFalse
	}
	return das, nil
}

func (cs *clientStub) GetClusterVersion(ctx context.Context, name string) (*configv1.ClusterVersion, error) {
	ver := &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{
					State:   configv1.PartialUpdate,
					Version: "partial-version",
				},
				{
					State:   configv1.CompletedUpdate,
					Version: testOCPVersion,
				},
				{
					State:   configv1.CompletedUpdate,
					Version: "old-version",
				},
			},
		},
	}

	return ver, nil
}

func (cs *clientStub) CreateVirtualMachineSnapshot(ctx context.Context, namespace string,
	snapshot *snapshotv1alpha1.VirtualMachineSnapshot) (*snapshotv1alpha1.VirtualMachineSnapshot, error) {
	snapshot.Namespace = namespace
	snapshotFullName := objectFullName(snapshot.Namespace, snapshot.Name)
	cs.createdSnapshots[snapshotFullName] = snapshot
	vmFullName := objectFullName(namespace, snapshot.Spec.Source.Name)
	vm, exists := cs.createdVMs[vmFullName]
	if !exists {
		return nil, fmt.Errorf("virtual machine %s not found", vmFullName)
	}
	var includedVolumes []string
	for _, volume := range vm.Spec.Template.Spec.Volumes {
		includedVolumes = append(includedVolumes, volume.Name)
	}

	readyToUse := true
	creationTime := metav1.Time{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	snapshot.CreationTimestamp = creationTime

	phase := snapshotv1alpha1.Succeeded
	if cs.failSnapshot {
		phase = snapshotv1alpha1.Failed
		readyToUse = false
	}

	indications := []snapshotv1alpha1.Indication{
		snapshotv1alpha1.VMSnapshotOnlineSnapshotIndication,
		snapshotv1alpha1.VMSnapshotGuestAgentIndication,
	}

	snapshotVolumes := &snapshotv1alpha1.SnapshotVolumesLists{
		IncludedVolumes: includedVolumes,
		ExcludedVolumes: []string{},
	}

	snapshot.Status = &snapshotv1alpha1.VirtualMachineSnapshotStatus{
		Phase:           phase,
		ReadyToUse:      &readyToUse,
		CreationTime:    &creationTime,
		Indications:     indications,
		SnapshotVolumes: snapshotVolumes,
	}
	return snapshot, nil
}

func (cs *clientStub) GetVirtualMachineSnapshot(ctx context.Context, namespace,
	name string) (*snapshotv1alpha1.VirtualMachineSnapshot, error) {
	snapshotFullName := objectFullName(namespace, name)
	snapshot, exists := cs.createdSnapshots[snapshotFullName]
	if !exists {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachinesnapshots"}, name)
	}
	return snapshot, nil
}

func (cs *clientStub) DeleteVirtualMachineSnapshot(ctx context.Context, namespace, name string) error {
	snapshotFullName := objectFullName(namespace, name)
	delete(cs.createdSnapshots, snapshotFullName)
	return nil
}

func (cs *clientStub) CreateVirtualMachineRestore(ctx context.Context, namespace string,
	restore *snapshotv1alpha1.VirtualMachineRestore) (*snapshotv1alpha1.VirtualMachineRestore, error) {
	restore.Namespace = namespace
	// 1. snapshot
	snapshotFullName := objectFullName(namespace, restore.Spec.VirtualMachineSnapshotName)
	snapshot, exists := cs.createdSnapshots[snapshotFullName]
	if !exists {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "snapshot.kubevirt.io", Resource: "virtualmachinesnapshots"},
			restore.Spec.VirtualMachineSnapshotName)
	}
	// 2. VM from snapshot source
	vmFullName := objectFullName(namespace, snapshot.Spec.Source.Name)
	vm, exists := cs.createdVMs[vmFullName]
	if !exists {
		return nil, fmt.Errorf("virtual machine %s not found", vmFullName)
	}
	// 3. build status from VM disks
	var restores []snapshotv1alpha1.VolumeRestore
	for _, vol := range vm.Spec.Template.Spec.Volumes {
		vsName := fmt.Sprintf("vs-%s", vol.Name)
		restores = append(restores, snapshotv1alpha1.VolumeRestore{
			VolumeName:                vol.Name,
			PersistentVolumeClaimName: vol.Name,
			VolumeSnapshotName:        vsName,
		})
	}
	var deletedDVs []string
	for _, dvt := range vm.Spec.DataVolumeTemplates {
		deletedDVs = append(deletedDVs, dvt.Name)
	}

	complete := true
	restoreTime := metav1.Time{Time: time.Date(2026, 1, 2, 3, 5, 6, 0, time.UTC)}
	if cs.failRestore {
		restores = nil
	}

	restore.Status = &snapshotv1alpha1.VirtualMachineRestoreStatus{
		Complete:           &complete,
		RestoreTime:        &restoreTime,
		Restores:           restores,
		DeletedDataVolumes: deletedDVs,
		Conditions: []snapshotv1alpha1.Condition{
			{
				Type:   snapshotv1alpha1.ConditionReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   snapshotv1alpha1.ConditionProgressing,
				Status: corev1.ConditionFalse,
			},
		},
	}
	cs.createdRestores[objectFullName(namespace, restore.Name)] = restore
	return restore, nil
}

func (cs *clientStub) GetVirtualMachineRestore(ctx context.Context, namespace,
	name string) (*snapshotv1alpha1.VirtualMachineRestore, error) {
	restoreFullName := objectFullName(namespace, name)
	restore, exists := cs.createdRestores[restoreFullName]
	if !exists {
		return nil, errors.NewNotFound(schema.GroupResource{Group: "snapshot.kubevirt.io", Resource: "virtualmachinerestores"}, name)
	}
	return restore, nil
}

func (cs *clientStub) DeleteVirtualMachineRestore(ctx context.Context, namespace, name string) error {
	restoreFullName := objectFullName(namespace, name)
	delete(cs.createdRestores, restoreFullName)
	return nil
}

func (cs *clientStub) ListCDIs(ctx context.Context) (*cdiv1.CDIList, error) {
	cdis := &cdiv1.CDIList{
		Items: []cdiv1.CDI{
			{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/version": testCNVVersion,
					},
				},
			},
		},
	}

	return cdis, nil
}

func (cs *clientStub) VMIName(namePrefix string) string {
	for _, vmi := range cs.createdVMs {
		if strings.Contains(vmi.Name, namePrefix) {
			return vmi.Name
		}
	}

	return ""
}

func newTestConfig() config.Config {
	return config.Config{
		PodName: testPodName,
		PodUID:  testPodUID,
	}
}

func objectFullName(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
