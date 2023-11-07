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
	assert "github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kvcorev1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

const (
	testNamespace = "target-ns"
)

var (
	testScName = "test-sc"
)

func TestCheckupShouldSucceed(t *testing.T) {
	testClient := newClientStub()
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
	actualResults := testCheckup.Results()
	assert.Equal(t, expectedResults, actualResults)
}

// FIXME: fill relevant results
func successfulRunResults(vmiUnderTestName string) status.Results {
	return status.Results{
		DefaultStorageClass:                       testScName,
		StorageProfilesWithEmptyClaimPropertySets: "",
		StorageProfilesWithSpecClaimPropertySets:  "",
		StorageWithRWX:                            "",
		StorageMissingVolumeSnapshotClass:         "",
		GoldenImagesNotUpToDate:                   "",
		VMsWithNonVirtRbdStorageClass:             "",
		VMsWithUnsetEfsStorageClass:               "",
		VMBootFromGoldenImage:                     fmt.Sprintf("VMI %q successfully booted", vmiUnderTestName),
		VMVolumeClone:                             "DV cloneType: \"\"",
		VMLiveMigration:                           fmt.Sprintf("VMI %q migration completed", vmiUnderTestName),
		VMHotplugVolume: fmt.Sprintf("VMI %q hotplug volume ready\nVMI %q hotplug volume removed",
			vmiUnderTestName, vmiUnderTestName),
	}
}

type clientStub struct {
	createdVMs        map[string]*kvcorev1.VirtualMachine
	createdVMIs       map[string]*kvcorev1.VirtualMachineInstance
	vmCreationFailure error
	vmDeletionFailure error
	vmiGetFailure     error
	skipDeletion      bool
}

func newClientStub() *clientStub {
	return &clientStub{
		createdVMs:  map[string]*kvcorev1.VirtualMachine{},
		createdVMIs: map[string]*kvcorev1.VirtualMachineInstance{},
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

// FIXME: if migrationFailure...
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
					Name: "test-ns",
				},
			},
		},
	}
	return nsList, nil
}

// FIXME: if noDefaultStorageClassFailure...
func (cs *clientStub) ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error) {
	scList := &storagev1.StorageClassList{
		Items: []storagev1.StorageClass{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        testScName,
					Annotations: map[string]string{checkup.AnnDefaultStorageClass: "true"},
				},
			},
		},
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
			},
		},
	}
	return spList, nil
}

func (cs *clientStub) ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error) {
	return nil, nil
}

func (cs *clientStub) ListDataImportCrons(ctx context.Context, namespace string) (*cdiv1.DataImportCronList, error) {
	dicList := &cdiv1.DataImportCronList{
		Items: []cdiv1.DataImportCron{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-dic",
					Namespace: "test-ns",
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
	return nil, nil
}

func (cs *clientStub) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	blockMode := corev1.PersistentVolumeBlock
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-ns",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeMode:       &blockMode,
			StorageClassName: &testScName,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		},
	}

	return pvc, nil
}

func (cs *clientStub) GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	return nil, nil
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
			Namespace: "test-ns",
		},
		Spec: cdiv1.DataSourceSpec{
			Source: cdiv1.DataSourceSource{
				PVC: &cdiv1.DataVolumeSourcePVC{
					Name:      "test-pvc",
					Namespace: "test-ns",
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
	return das, nil
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
	return config.Config{}
}

func objectFullName(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
