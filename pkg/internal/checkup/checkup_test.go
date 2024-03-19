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

func TestCheckupShouldReturnErrorWhen(t *testing.T) {
	tests := map[string]struct {
		clientConfig    clientConfig
		expectedResults map[string]string
		expectedErr     string
	}{
		"noStorageClasses": {
			clientConfig:    clientConfig{noStorageClasses: true},
			expectedResults: map[string]string{reporter.DefaultStorageClassKey: checkup.ErrNoDefaultStorageClass},
			expectedErr:     checkup.ErrNoDefaultStorageClass,
		},
		"noDefaultStorageClass": {
			clientConfig:    clientConfig{noDefaultStorageClass: true},
			expectedResults: map[string]string{reporter.DefaultStorageClassKey: checkup.ErrNoDefaultStorageClass},
			expectedErr:     checkup.ErrNoDefaultStorageClass,
		},
		"multipleDefaultStorageClasses": {
			clientConfig:    clientConfig{multipleDefaultStorageClasses: true},
			expectedResults: map[string]string{reporter.DefaultStorageClassKey: checkup.ErrMultipleDefaultStorageClasses},
			expectedErr:     checkup.ErrMultipleDefaultStorageClasses,
		},
		"storageProfileIncomplete": {
			clientConfig: clientConfig{spIncomplete: true},
			expectedResults: map[string]string{reporter.StorageProfilesWithEmptyClaimPropertySetsKey: testScName,
				reporter.StorageProfilesWithSpecClaimPropertySetsKey: testScName, reporter.StorageWithRWXKey: ""},
			expectedErr: checkup.ErrEmptyClaimPropertySets,
		},
		"noVolumeSnapshotClasses": {
			clientConfig:    clientConfig{noVolumeSnapshotClasses: true},
			expectedResults: map[string]string{reporter.StorageMissingVolumeSnapshotClassKey: testScName},
			expectedErr:     "",
		},
		"dataSourceNotReady": {
			clientConfig: clientConfig{dataSourceNotReady: true, expectNoVMI: true},
			expectedResults: map[string]string{reporter.GoldenImagesNotUpToDateKey: testNamespace + "/" + testDIC,
				reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoGoldenImage},
			expectedErr: checkup.ErrGoldenImagesNotUpToDate,
		},
		"dicNoDataSource": {
			clientConfig: clientConfig{dicNoDataSource: true, expectNoVMI: true},
			expectedResults: map[string]string{reporter.GoldenImagesNoDataSourceKey: testNamespace + "/" + testDIC,
				reporter.VMBootFromGoldenImageKey: checkup.MessageSkipNoGoldenImage},
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
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testClient := newClientStub(tc.clientConfig)
			testConfig := newTestConfig()

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

			expectedResults := fullExpectedResults(vmiUnderTestName, tc.expectedResults)
			actualResults := reporter.FormatResults(testCheckup.Results())

			assert.Equal(t, expectedResults, actualResults)
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
		if strings.Contains(expectedResult, "%s") {
			expectedResult = fmt.Sprintf(expectedResult, vmiUnderTestName)
		}
		fullResults[key] = expectedResult
	}
	return fullResults
}

func expectedResultsNoVMI(expectedResults map[string]string) {
	expectedResults[reporter.VMHotplugVolumeKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMLiveMigrationKey] = checkup.MessageSkipNoVMI
	expectedResults[reporter.VMVolumeCloneKey] = ""
}

// FIXME: fill relevant results
func successfulRunResults(vmiUnderTestName string) map[string]string {
	return map[string]string{
		reporter.OCPVersionKey:                                testOCPVersion,
		reporter.CNVVersionKey:                                testCNVVersion,
		reporter.DefaultStorageClassKey:                       testScName,
		reporter.StorageProfilesWithEmptyClaimPropertySetsKey: "",
		reporter.StorageProfilesWithSpecClaimPropertySetsKey:  "",
		reporter.StorageWithRWXKey:                            testScName,
		reporter.StorageMissingVolumeSnapshotClassKey:         "",
		reporter.GoldenImagesNotUpToDateKey:                   "",
		reporter.GoldenImagesNoDataSourceKey:                  "",
		reporter.VMsWithNonVirtRbdStorageClassKey:             "",
		reporter.VMsWithUnsetEfsStorageClassKey:               "",
		reporter.VMBootFromGoldenImageKey:                     fmt.Sprintf("VMI %q successfully booted", vmiUnderTestName),
		reporter.VMVolumeCloneKey:                             "DV cloneType: \"\"",
		reporter.VMLiveMigrationKey:                           fmt.Sprintf("VMI %q migration completed", vmiUnderTestName),
		reporter.VMHotplugVolumeKey: fmt.Sprintf("VMI %q hotplug volume ready\nVMI %q hotplug volume removed",
			vmiUnderTestName, vmiUnderTestName),
	}
}

type clientConfig struct {
	skipDeletion                  bool
	noStorageClasses              bool
	noDefaultStorageClass         bool
	multipleDefaultStorageClasses bool
	unsetEfsStorageClass          bool
	spIncomplete                  bool
	noVolumeSnapshotClasses       bool
	dicNoDataSource               bool
	dataSourceNotReady            bool
	expectNoVMI                   bool
	cloneFallback                 bool
	failMigration                 bool
}

type clientStub struct {
	createdVMs        map[string]*kvcorev1.VirtualMachine
	createdVMIs       map[string]*kvcorev1.VirtualMachineInstance
	vmCreationFailure error
	vmDeletionFailure error
	vmiGetFailure     error
	clientConfig
}

func newClientStub(clientConfig clientConfig) *clientStub {
	return &clientStub{
		createdVMs:   map[string]*kvcorev1.VirtualMachine{},
		createdVMIs:  map[string]*kvcorev1.VirtualMachineInstance{},
		clientConfig: clientConfig,
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
			},
		},
	}

	vmi.Name = vm.Name
	vmi.Namespace = namespace
	cs.createdVMIs[vmFullName] = vmi

	return vm, nil
}

func (cs *clientStub) DeleteVirtualMachine(ctx context.Context, namespace, name string) error {
	if cs.vmDeletionFailure != nil {
		return cs.vmDeletionFailure
	}

	vmFullName := objectFullName(namespace, name)
	if _, exist := cs.createdVMs[vmFullName]; !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachines"}, name)
	}
	if _, exist := cs.createdVMIs[vmFullName]; !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}, name)
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

func (cs *clientStub) AddVirtualMachineInstanceVolume(ctx context.Context, namespace, name string,
	addVolumeOptions *kvcorev1.AddVolumeOptions) error {
	vmiFullName := objectFullName(namespace, name)
	vmi, exist := cs.createdVMIs[vmiFullName]
	if !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}, name)
	}
	vmi.Status.VolumeStatus = append(vmi.Status.VolumeStatus, kvcorev1.VolumeStatus{
		Name:          addVolumeOptions.Name,
		HotplugVolume: &kvcorev1.HotplugVolumeStatus{},
		Phase:         kvcorev1.VolumeReady,
	})

	return nil
}

func (cs *clientStub) RemoveVirtualMachineInstanceVolume(ctx context.Context, namespace, name string,
	removeVolumeOptions *kvcorev1.RemoveVolumeOptions) error {
	vmiFullName := objectFullName(namespace, name)
	vmi, exist := cs.createdVMIs[vmiFullName]
	if !exist {
		return errors.NewNotFound(schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}, name)
	}
	volStat := vmi.Status.VolumeStatus
	for i, vs := range volStat {
		if vs.Name == removeVolumeOptions.Name {
			volStat = append(volStat[:i], volStat[i+1:]...)
			break
		}
	}
	vmi.Status.VolumeStatus = volStat

	return nil
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
					Name:        testScName,
					Annotations: map[string]string{checkup.AnnDefaultStorageClass: "true"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        testScName2,
					Annotations: map[string]string{checkup.AnnDefaultStorageClass: "false"},
				},
			},
		},
	}
	if cs.noDefaultStorageClass {
		scList.Items[0].Annotations[checkup.AnnDefaultStorageClass] = "false"
	}
	if cs.multipleDefaultStorageClasses {
		scList.Items[1].Annotations[checkup.AnnDefaultStorageClass] = "true"
	}
	if cs.unsetEfsStorageClass {
		scList.Items = append(scList.Items, storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-sc-unset-efs",
				Annotations: map[string]string{checkup.AnnDefaultStorageClass: "false"},
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

func (cs *clientStub) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	blockMode := corev1.PersistentVolumeBlock
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: testNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeMode:       &blockMode,
			StorageClassName: &testScName,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		},
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
