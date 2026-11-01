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

package k8s_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/stretchr/testify/require"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/k8s"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/shared"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
)

const testProvisioner = "example.csi.test"

type storageClientStub struct {
	classes   *storagev1.StorageClassList
	listErr   error
	driver    *storagev1.CSIDriver
	driverErr error
	requested []string
}

func (s *storageClientStub) ListStorageClasses(context.Context) (*storagev1.StorageClassList, error) {
	return s.classes.DeepCopy(), s.listErr
}

func (s *storageClientStub) GetCSIDriver(_ context.Context, name string) (*storagev1.CSIDriver, error) {
	s.requested = append(s.requested, name)
	return s.driver.DeepCopy(), s.driverErr
}

func newClass(name, annotation string) storagev1.StorageClass {
	return storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: name, Annotations: map[string]string{annotation: "true"}},
		Provisioner: testProvisioner,
	}
}

type storageClassCase struct {
	name         string
	classes      []storagev1.StorageClass
	configured   string
	wantError    string
	wantSelected string
	wantDefault  string
}

func storageClassCases() []storageClassCase {
	const (
		standardDefault = "storageclass.kubernetes.io/is-default-class"
		virtDefault     = "storageclass.kubevirt.io/is-default-virt-class"
	)
	plain := newClass("selected", "")
	standard := newClass("standard", standardDefault)
	virt := newClass("virt", virtDefault)
	emptyProvisioner := plain.DeepCopy()
	emptyProvisioner.Provisioner = " "
	return []storageClassCase{
		{name: "no classes", wantError: shared.ErrNoDefaultStorageClass},
		{name: "no default", classes: []storagev1.StorageClass{plain}, wantError: shared.ErrNoDefaultStorageClass},
		{name: "duplicate standard defaults", classes: []storagev1.StorageClass{standard, newClass("other", standardDefault)},
			wantError: shared.ErrMultipleDefaultStorageClasses},
		{name: "duplicate virt defaults", classes: []storagev1.StorageClass{virt, newClass("other", virtDefault)},
			wantError: shared.ErrMultipleDefaultStorageClasses},
		{name: "explicit class without default", classes: []storagev1.StorageClass{plain},
			configured: plain.Name, wantSelected: plain.Name},
		{name: "standard fallback", classes: []storagev1.StorageClass{standard},
			wantSelected: standard.Name, wantDefault: standard.Name},
		{name: "virt fallback preferred", classes: []storagev1.StorageClass{standard, virt},
			wantSelected: virt.Name, wantDefault: virt.Name},
		{name: "explicit class preferred", classes: []storagev1.StorageClass{plain, standard, virt},
			configured: plain.Name, wantSelected: plain.Name, wantDefault: virt.Name},
		{name: "selected class missing", classes: []storagev1.StorageClass{standard}, configured: "missing",
			wantError: `storage class "missing" not found`, wantDefault: standard.Name},
		{name: "empty provisioner", classes: []storagev1.StorageClass{*emptyProvisioner}, configured: plain.Name,
			wantError: fmt.Sprintf(k8s.ErrEmptyProvisioner, plain.Name), wantSelected: plain.Name},
	}
}

func TestStorageClassDiscovery(t *testing.T) {
	for _, testCase := range storageClassCases() {
		t.Run(testCase.name, func(t *testing.T) {
			client := &storageClientStub{
				classes: &storagev1.StorageClassList{Items: testCase.classes},
				driver:  &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: testProvisioner}},
			}
			state := &shared.State{}
			checkup := k8s.New(client, "test", state, config.Config{StorageClass: testCase.configured})
			err := checkup.Run(context.Background())
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
				require.Contains(t, checkup.Results().StorageClassDiscovery, testCase.wantError)
				require.Equal(t, k8s.MessageSkipInvalidStorageClass, checkup.Results().CSIDriverDiscovery)
				require.Empty(t, client.requested)
			} else {
				require.NoError(t, err)
				require.Equal(t, []string{testProvisioner}, client.requested)
			}
			require.Equal(t, testCase.wantDefault, state.DefaultStorageClass)
			require.Equal(t, testCase.classes, state.StorageClasses.Items)
			if testCase.wantSelected == "" {
				require.Nil(t, state.SelectedStorageClass)
			} else {
				require.NotNil(t, state.SelectedStorageClass)
				require.Equal(t, testCase.wantSelected, state.SelectedStorageClass.Name)
				require.Equal(t, testCase.wantSelected, checkup.Results().SelectedStorageClass)
			}
		})
	}
}

func TestCSIDriverDiscovery(t *testing.T) {
	resource := schema.GroupResource{Group: "storage.k8s.io", Resource: "csidrivers"}
	forbidden := k8serrors.NewForbidden(resource, testProvisioner, errors.New("permission denied"))
	testCases := []struct {
		name        string
		provisioner string
		modes       []storagev1.VolumeLifecycleMode
		driverErr   error
		wantError   string
		wantMessage string
		wantLookup  bool
		wantDriver  bool
	}{
		{name: "Persistent declared", modes: []storagev1.VolumeLifecycleMode{storagev1.VolumeLifecyclePersistent},
			wantMessage: fmt.Sprintf(k8s.MessageCSIDriverFound, testProvisioner), wantLookup: true, wantDriver: true},
		{name: "Persistent defaulted", wantMessage: fmt.Sprintf(k8s.MessageCSIDriverFound, testProvisioner),
			wantLookup: true, wantDriver: true},
		{name: "Ephemeral only", modes: []storagev1.VolumeLifecycleMode{storagev1.VolumeLifecycleEphemeral},
			wantError: fmt.Sprintf(k8s.ErrPersistentLifecycleUnsupported, testProvisioner), wantLookup: true, wantDriver: true},
		{name: "missing metadata warning", driverErr: k8serrors.NewNotFound(resource, testProvisioner),
			wantMessage: fmt.Sprintf(k8s.MessageCSIDriverNotFound, testProvisioner), wantLookup: true},
		{name: "Forbidden fails", driverErr: forbidden, wantError: "forbidden", wantLookup: true},
		{name: "static provisioning", provisioner: "kubernetes.io/no-provisioner", wantMessage: k8s.MessageSkipStaticProvisioner},
		{name: "legacy provisioning", provisioner: "kubernetes.io/aws-ebs",
			wantMessage: fmt.Sprintf(k8s.MessageSkipLegacyProvisioner, "kubernetes.io/aws-ebs")},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sc := newClass("selected", "")
			if testCase.provisioner != "" {
				sc.Provisioner = testCase.provisioner
			}
			client := &storageClientStub{
				classes: &storagev1.StorageClassList{Items: []storagev1.StorageClass{sc}},
				driver: &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: testProvisioner},
					Spec: storagev1.CSIDriverSpec{VolumeLifecycleModes: testCase.modes}},
				driverErr: testCase.driverErr,
			}
			state := &shared.State{}
			checkup := k8s.New(client, "test", state, config.Config{StorageClass: sc.Name})
			err := checkup.Run(context.Background())
			if testCase.wantError == "" {
				require.NoError(t, err)
				require.Equal(t, testCase.wantMessage, checkup.Results().CSIDriverDiscovery)
			} else {
				require.ErrorContains(t, err, testCase.wantError)
				require.Contains(t, checkup.Results().CSIDriverDiscovery, testCase.wantError)
			}
			if testCase.driverErr == forbidden {
				require.ErrorIs(t, err, forbidden)
			}
			require.Equal(t, testCase.wantLookup, len(client.requested) > 0)
			if testCase.wantLookup {
				require.Equal(t, []string{testProvisioner}, client.requested)
			}
			require.Equal(t, testCase.wantDriver, state.CSIDriver != nil)
		})
	}
}

func TestStorageClassListError(t *testing.T) {
	listErr := errors.New("storage API unavailable")
	client := &storageClientStub{listErr: listErr}
	state := &shared.State{}
	checkup := k8s.New(client, "test", state, config.Config{})
	require.ErrorIs(t, checkup.Run(context.Background()), listErr)
	require.Contains(t, checkup.Results().StorageClassDiscovery, listErr.Error())
	require.Empty(t, client.requested)
	require.Nil(t, state.StorageClasses)
}
