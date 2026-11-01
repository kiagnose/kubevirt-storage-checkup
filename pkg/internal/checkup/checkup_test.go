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

package checkup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kvcorev1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/reporter"
)

const (
	coordinatorStorageClass = "test-sc"
	coordinatorCSIDriver    = "test.csi.example.com"
)

type coordinatorClientStub struct {
	checkup.Client
	storageErr, vmiErr, cdiErr error
	noCDI                      bool
	vmiCalled, cdiCalled       bool
	csiName, vmiNamespace      string
}

func (c *coordinatorClientStub) ListStorageClasses(_ context.Context) (*storagev1.StorageClassList, error) {
	return &storagev1.StorageClassList{Items: []storagev1.StorageClass{{
		ObjectMeta: metav1.ObjectMeta{
			Name: coordinatorStorageClass, Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"},
		}, Provisioner: coordinatorCSIDriver,
	}}}, c.storageErr
}

func (c *coordinatorClientStub) GetCSIDriver(_ context.Context, name string) (*storagev1.CSIDriver, error) {
	c.csiName = name
	return &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: name}}, nil
}

func (c *coordinatorClientStub) ListVirtualMachinesInstances(
	_ context.Context, namespace string,
) (*kvcorev1.VirtualMachineInstanceList, error) {
	c.vmiCalled, c.vmiNamespace = true, namespace
	return &kvcorev1.VirtualMachineInstanceList{}, c.vmiErr
}

func (c *coordinatorClientStub) ListCDIs(_ context.Context) (*cdiv1.CDIList, error) {
	c.cdiCalled = true
	result := &cdiv1.CDIList{}
	if !c.noCDI {
		result.Items = []cdiv1.CDI{{}}
	}
	return result, c.cdiErr
}

func TestCoordinatorPhaseBoundaries(t *testing.T) {
	vmiResource := schema.GroupResource{Group: "kubevirt.io", Resource: "virtualmachineinstances"}
	cdiResource := schema.GroupResource{Group: "cdi.kubevirt.io", Resource: "cdis"}
	storageErr := errors.New("storage API unavailable")
	vmiForbidden := k8serrors.NewForbidden(vmiResource, "", errors.New("access denied"))
	cdiForbidden := k8serrors.NewForbidden(cdiResource, "", errors.New("access denied"))

	tests := []struct {
		name             string
		storageErr       error
		vmiErr, cdiErr   error
		noCDI            bool
		wantErr          error
		wantVMI, wantCDI bool
	}{
		{name: "Phase 1 failure prevents KubeVirt calls", storageErr: storageErr, wantErr: storageErr},
		{
			name: "missing KubeVirt API skips Phase 2", wantVMI: true,
			vmiErr: k8serrors.NewNotFound(vmiResource, ""),
		},
		{
			name: "missing CDI API skips Phase 2", wantVMI: true, wantCDI: true,
			cdiErr: k8serrors.NewNotFound(cdiResource, ""),
		},
		{name: "no CDI installation skips Phase 2", noCDI: true, wantVMI: true, wantCDI: true},
		{name: "KubeVirt permission error is propagated", vmiErr: vmiForbidden, wantErr: vmiForbidden, wantVMI: true},
		{
			name: "CDI permission error is propagated", cdiErr: cdiForbidden, wantErr: cdiForbidden,
			wantVMI: true, wantCDI: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &coordinatorClientStub{
				storageErr: tc.storageErr, vmiErr: tc.vmiErr, cdiErr: tc.cdiErr, noCDI: tc.noCDI,
			}
			cfg := config.Config{StorageClass: coordinatorStorageClass}
			runner := checkup.New(client, "target-ns", cfg)

			require.NoError(t, runner.Setup(context.Background()))

			err := runner.Run(context.Background())
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.wantVMI, client.vmiCalled)
			require.Equal(t, tc.wantCDI, client.cdiCalled)
			if tc.wantVMI {
				require.Equal(t, metav1.NamespaceAll, client.vmiNamespace)
			}

			results := runner.Results()
			formatted := reporter.FormatResults(results)
			require.Equal(t, coordinatorStorageClass, formatted[reporter.SelectedStorageClassKey])
			require.NotEmpty(t, results.StorageClassDiscovery)
			require.Equal(t, results.StorageClassDiscovery, formatted[reporter.StorageClassDiscoveryKey])

			if tc.storageErr != nil {
				require.Empty(t, client.csiName)
			} else {
				require.Equal(t, coordinatorCSIDriver, client.csiName)
				require.Equal(t, coordinatorStorageClass, formatted[reporter.DefaultStorageClassKey])
				require.Equal(t, coordinatorCSIDriver, formatted[reporter.StorageClassProvisionerKey])
				require.NotEmpty(t, results.CSIDriverDiscovery)
				require.Equal(t, results.CSIDriverDiscovery, formatted[reporter.CSIDriverDiscoveryKey])
			}

			require.NoError(t, runner.Teardown(context.Background()))
		})
	}
}
