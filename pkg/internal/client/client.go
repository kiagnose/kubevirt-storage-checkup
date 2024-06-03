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

package client

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	kvcorev1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

type Client struct {
	kubecli.KubevirtClient
	configv1client.ClusterVersionsGetter
}

func New() (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	client, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		return nil, err
	}

	oClient, err := configv1client.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Client{client, oClient}, nil
}

func (c *Client) CreateVirtualMachine(ctx context.Context, namespace string, vm *kvcorev1.VirtualMachine) (
	*kvcorev1.VirtualMachine, error) {
	return c.VirtualMachine(namespace).Create(ctx, vm)
}

func (c *Client) DeleteVirtualMachine(ctx context.Context, namespace, name string) error {
	return c.VirtualMachine(namespace).Delete(ctx, name, &metav1.DeleteOptions{})
}

func (c *Client) GetVirtualMachineInstance(ctx context.Context, namespace, name string) (*kvcorev1.VirtualMachineInstance, error) {
	return c.VirtualMachineInstance(namespace).Get(ctx, name, &metav1.GetOptions{})
}

func (c *Client) CreateVirtualMachineInstanceMigration(ctx context.Context, namespace string,
	vmim *kvcorev1.VirtualMachineInstanceMigration) (*kvcorev1.VirtualMachineInstanceMigration, error) {
	return c.VirtualMachineInstanceMigration(namespace).Create(vmim, &metav1.CreateOptions{})
}

func (c *Client) AddVirtualMachineInstanceVolume(ctx context.Context, namespace, name string,
	addVolumeOptions *kvcorev1.AddVolumeOptions) error {
	return c.VirtualMachineInstance(namespace).AddVolume(ctx, name, addVolumeOptions)
}

func (c *Client) RemoveVirtualMachineInstanceVolume(ctx context.Context, namespace, name string,
	removeVolumeOptions *kvcorev1.RemoveVolumeOptions) error {
	return c.VirtualMachineInstance(namespace).RemoveVolume(ctx, name, removeVolumeOptions)
}

func (c *Client) CreateDataVolume(ctx context.Context, namespace string, dv *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	return c.CdiClient().CdiV1beta1().DataVolumes(namespace).Create(ctx, dv, metav1.CreateOptions{})
}

func (c *Client) DeleteDataVolume(ctx context.Context, namespace, name string) error {
	return c.CdiClient().CdiV1beta1().DataVolumes(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error {
	return c.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) ListNodes(ctx context.Context) (*corev1.NodeList, error) {
	return c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
}

func (c *Client) ListNamespaces(ctx context.Context) (*corev1.NamespaceList, error) {
	return c.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
}

func (c *Client) ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error) {
	return c.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
}

func (c *Client) ListStorageProfiles(ctx context.Context) (*cdiv1.StorageProfileList, error) {
	return c.CdiClient().CdiV1beta1().StorageProfiles().List(ctx, metav1.ListOptions{})
}

func (c *Client) ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error) {
	return c.KubernetesSnapshotClient().SnapshotV1().VolumeSnapshotClasses().List(ctx, metav1.ListOptions{})
}

func (c *Client) ListDataImportCrons(ctx context.Context, namespace string) (*cdiv1.DataImportCronList, error) {
	return c.CdiClient().CdiV1beta1().DataImportCrons(namespace).List(ctx, metav1.ListOptions{})
}

func (c *Client) ListVirtualMachinesInstances(ctx context.Context, namespace string) (*kvcorev1.VirtualMachineInstanceList, error) {
	return c.VirtualMachineInstance(namespace).List(ctx, &metav1.ListOptions{})
}

func (c *Client) ListCDIs(ctx context.Context) (*cdiv1.CDIList, error) {
	return c.CdiClient().CdiV1beta1().CDIs().List(ctx, metav1.ListOptions{})
}

func (c *Client) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	return c.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	return c.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	return c.KubernetesSnapshotClient().SnapshotV1().VolumeSnapshots(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) GetCSIDriver(ctx context.Context, name string) (*storagev1.CSIDriver, error) {
	return c.StorageV1().CSIDrivers().Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) GetDataSource(ctx context.Context, namespace, name string) (*cdiv1.DataSource, error) {
	return c.CdiClient().CdiV1beta1().DataSources(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *Client) GetClusterVersion(ctx context.Context, name string) (*configv1.ClusterVersion, error) {
	return c.ClusterVersions().Get(ctx, name, metav1.GetOptions{})
}
