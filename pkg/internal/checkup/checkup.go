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

package checkup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"

	vmispec "github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/vmi"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"

	kvcorev1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

type kubeVirtStorageClient interface {
	CreateVirtualMachine(ctx context.Context, namespace string, vm *kvcorev1.VirtualMachine) (*kvcorev1.VirtualMachine, error)
	DeleteVirtualMachine(ctx context.Context, namespace, name string) error
	GetVirtualMachineInstance(ctx context.Context, namespace, name string) (*kvcorev1.VirtualMachineInstance, error)
	CreateVirtualMachineInstanceMigration(ctx context.Context, namespace string,
		vmim *kvcorev1.VirtualMachineInstanceMigration) (*kvcorev1.VirtualMachineInstanceMigration, error)
	AddVirtualMachineInstanceVolume(ctx context.Context, namespace, name string, addVolumeOptions *kvcorev1.AddVolumeOptions) error
	RemoveVirtualMachineInstanceVolume(ctx context.Context, namespace, name string,
		removeVolumeOptions *kvcorev1.RemoveVolumeOptions) error
	CreatePersistentVolumeClaim(ctx context.Context, namespace string, pvc *corev1.PersistentVolumeClaim) (
		*corev1.PersistentVolumeClaim, error)
	DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error
	ListNamespaces(ctx context.Context) (*corev1.NamespaceList, error)
	ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error)
	ListStorageProfiles(ctx context.Context) (*cdiv1.StorageProfileList, error)
	ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error)
	ListDataImportCrons(ctx context.Context, namespace string) (*cdiv1.DataImportCronList, error)
	ListVirtualMachinesInstances(ctx context.Context, namespace string) (*kvcorev1.VirtualMachineInstanceList, error)
	GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error)
	GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error)
	GetDataSource(ctx context.Context, namespace, name string) (*cdiv1.DataSource, error)
}

const (
	VMIUnderTestNamePrefix = "vmi-under-test"
	hotplugVolumeName      = "hotplug-volume"

	annDefaultStorageClass = "storageclass.kubernetes.io/is-default-class"

	errNoDefaultStorageClass         = "No default storage class."
	errMultipleDefaultStorageClasses = "There are multiple default storage classes."
	errEmptyClaimPropertySets        = "There are StorageProfiles with empty ClaimPropertySets (unknown provisioners)."
	errMissingVolumeSnapshotClass    = "There are StorageProfiles missing VolumeSnapshotClass."
	errVMsWithNonVirtRbdStorageClass = "There are VMs using the plain RBD storageclass when the virtualization storageclass exists."
	errVMsWithUnsetEfsStorageClass   = "There are VMs using an EFS storageclass where the gid and uid are not set in the storageclass."
	errGoldenImagesNotUpToDate       = "There are golden images whose DataImportCron is not up to date or DataSource is not ready."
)

type Checkup struct {
	client         kubeVirtStorageClient
	namespace      string
	goldenImagePvc *corev1.PersistentVolumeClaim
	vmUnderTest    *kvcorev1.VirtualMachine
	results        status.Results
}

func New(client kubeVirtStorageClient, namespace string, checkupConfig config.Config) *Checkup {
	return &Checkup{
		client:    client,
		namespace: namespace,
	}
}

func (c *Checkup) Setup(ctx context.Context) error {
	return nil
}

func (c *Checkup) Run(ctx context.Context) error {
	errStr := ""

	scs, err := c.client.ListStorageClasses(ctx)
	if err != nil {
		return err
	}

	c.checkDefaultStorageClass(scs, &errStr)

	sps, err := c.client.ListStorageProfiles(ctx)
	if err != nil {
		return err
	}

	c.checkStorageProfiles(sps, &errStr)

	vscs, err := c.client.ListVolumeSnapshotClasses(ctx)
	if err != nil {
		return err
	}

	c.checkVolumeSnapShotClasses(sps, vscs, &errStr)

	nss, err := c.client.ListNamespaces(ctx)
	if err != nil {
		return err
	}

	if err := c.checkGoldenImages(ctx, nss, &errStr); err != nil {
		return err
	}
	if err := c.checkVMIs(ctx, nss, scs, &errStr); err != nil {
		return err
	}

	if err := c.checkVMICreation(ctx, &errStr); err != nil {
		return err
	}
	if err := c.checkVMILiveMigration(ctx, &errStr); err != nil {
		return err
	}
	if err := c.checkVMIHotplugVolume(ctx, &errStr); err != nil {
		return err
	}

	if errStr != "" {
		return errors.New(errStr)
	}

	return nil
}

func (c *Checkup) checkGoldenImages(ctx context.Context, namespaces *corev1.NamespaceList, errStr *string) error {
	log.Print("checkGoldenImages")
	dicNames := ""
	for i := range namespaces.Items {
		ns := namespaces.Items[i]
		dics, err := c.client.ListDataImportCrons(ctx, ns.Name)
		if err != nil {
			return err
		}
		for i := range dics.Items {
			dic := dics.Items[i]
			if !isDataImportCronUpToDate(dic.Status.Conditions) {
				appendSep(&dicNames, dic.Namespace+"/"+dic.Name)
				continue
			}
			das, err := c.client.GetDataSource(ctx, ns.Name, dic.Spec.ManagedDataSource)
			if err != nil {
				return err
			}
			if !isDataSourceReady(das.Status.Conditions) {
				appendSep(&dicNames, das.Namespace+"/"+das.Name)
				continue
			}

			if c.goldenImagePvc != nil {
				continue
			}
			// FIXME: check pvc - RWX, smart clone
			srcPvc := das.Spec.Source.PVC
			pvc, err := c.client.GetPersistentVolumeClaim(ctx, srcPvc.Namespace, srcPvc.Name)
			if err != nil {
				return err
			}
			c.goldenImagePvc = pvc
			log.Printf("Selected golden image PVC: %s/%s %s %s %s", pvc.Namespace, pvc.Name, *pvc.Spec.VolumeMode,
				pvc.Status.AccessModes[0], *pvc.Spec.StorageClassName)
		}
	}

	if dicNames != "" {
		c.results.GoldenImagesNotUpToDate = dicNames
		appendSep(errStr, errGoldenImagesNotUpToDate)
	}
	return nil
}

func isDataImportCronUpToDate(conditions []cdiv1.DataImportCronCondition) bool {
	for i := range conditions {
		cond := conditions[i]
		if cond.Type == cdiv1.DataImportCronUpToDate && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isDataSourceReady(conditions []cdiv1.DataSourceCondition) bool {
	for i := range conditions {
		cond := conditions[i]
		if cond.Type == cdiv1.DataSourceReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (c *Checkup) checkDefaultStorageClass(scs *storagev1.StorageClassList, errStr *string) {
	log.Print("checkDefaultStorageClass")
	for i := range scs.Items {
		sc := scs.Items[i]
		if sc.Annotations[annDefaultStorageClass] == "true" {
			if c.results.DefaultStorageClass != "" {
				c.results.DefaultStorageClass = errMultipleDefaultStorageClasses
				appendSep(errStr, errMultipleDefaultStorageClasses)
				break
			}
			c.results.DefaultStorageClass = sc.Name
		}
	}
	if c.results.DefaultStorageClass == "" {
		c.results.DefaultStorageClass = errNoDefaultStorageClass
		appendSep(errStr, errNoDefaultStorageClass)
	}
}

// FIXME: set default sc
func (c *Checkup) checkStorageProfiles(sps *cdiv1.StorageProfileList, errStr *string) {
	log.Print("checkStorageProfiles")
	var spWithEmptyClaimPropertySets, spWithSpecClaimPropertySets, spWithRWX string

	for i := range sps.Items {
		sp := sps.Items[i]
		provisioner := sp.Status.Provisioner
		if provisioner == nil ||
			*provisioner == "kubernetes.io/no-provisioner" ||
			*provisioner == "openshift-storage.ceph.rook.io/bucket" ||
			*provisioner == "openshift-storage.noobaa.io/obc" {
			continue
		}
		if len(sp.Status.ClaimPropertySets) == 0 {
			appendSep(&spWithEmptyClaimPropertySets, sp.Name)
		}
		if len(sp.Spec.ClaimPropertySets) != 0 {
			appendSep(&spWithSpecClaimPropertySets, sp.Name)
		}
		if hasRWX(sp.Status.ClaimPropertySets) {
			appendSep(&spWithRWX, sp.Name)
		}
	}

	if spWithEmptyClaimPropertySets != "" {
		c.results.StorageProfilesWithEmptyClaimPropertySets = spWithEmptyClaimPropertySets
		appendSep(errStr, errEmptyClaimPropertySets)
	}
	if spWithSpecClaimPropertySets != "" {
		c.results.StorageProfilesWithSpecClaimPropertySets = spWithSpecClaimPropertySets
	}
	if spWithRWX != "" {
		c.results.StorageWithRWX = spWithRWX
	}
}

func hasRWX(cpSets []cdiv1.ClaimPropertySet) bool {
	for i := range cpSets {
		cpSet := cpSets[i]
		for i := range cpSet.AccessModes {
			am := cpSet.AccessModes[i]
			if am == corev1.ReadWriteMany {
				return true
			}
		}
	}
	return false
}

func (c *Checkup) checkVolumeSnapShotClasses(sps *cdiv1.StorageProfileList, vscs *snapshotv1.VolumeSnapshotClassList, errStr *string) {
	log.Print("checkVolumeSnapShotClasses")
	spNames := ""
	for i := range sps.Items {
		sp := sps.Items[i]
		if sp.Status.CloneStrategy != nil &&
			*sp.Status.CloneStrategy == cdiv1.CloneStrategySnapshot &&
			sp.Status.Provisioner != nil {
			if !hasDriver(vscs, *sp.Status.Provisioner) {
				appendSep(&spNames, sp.Name)
			}
		}
	}
	if spNames != "" {
		c.results.StorageMissingVolumeSnapshotClass = spNames
		appendSep(errStr, errMissingVolumeSnapshotClass)
	}
}

func hasDriver(vscs *snapshotv1.VolumeSnapshotClassList, driver string) bool {
	for i := range vscs.Items {
		vsc := vscs.Items[i]
		if vsc.Driver == driver {
			return true
		}
	}
	return false
}

func (c *Checkup) checkVMIs(ctx context.Context, namespaces *corev1.NamespaceList, scs *storagev1.StorageClassList, errStr *string) error {
	var vmisWithNonVirtRbdSC, vmisWithUnsetEfsSC string

	log.Print("checkVMIs")
	virtSC, err := c.getVirtStorageClass(scs)
	if err != nil {
		return err
	}
	unsetEfsSC, err := c.getUnsetEfsStorageClass(scs)
	if err != nil {
		return err
	}
	if virtSC == nil && unsetEfsSC == nil {
		return nil
	}

	for i := range namespaces.Items {
		ns := namespaces.Items[i]
		vmis, err := c.client.ListVirtualMachinesInstances(ctx, ns.Name)
		if err != nil {
			return fmt.Errorf("failed ListVirtualMachinesInstances: %s", err)
		}
		for i := range vmis.Items {
			vmi := vmis.Items[i]
			if vmi.Status.Phase != kvcorev1.Running {
				continue
			}
			hasNonVirtRbdSC, hasUnsetEfsSC, err := c.checkVMIVolumes(ctx, &vmi, virtSC, unsetEfsSC)
			if err != nil {
				return err
			}
			if hasNonVirtRbdSC {
				appendSep(&vmisWithNonVirtRbdSC, vmi.Namespace+"/"+vmi.Name)
			}
			if hasUnsetEfsSC {
				appendSep(&vmisWithUnsetEfsSC, vmi.Namespace+"/"+vmi.Name)
			}
		}
	}

	if vmisWithNonVirtRbdSC != "" {
		c.results.VMsWithNonVirtRbdStorageClass = vmisWithNonVirtRbdSC
		// FIXME: not sure the checkup should fail on this one
		// appendSep(errStr, errVMsWithNonVirtRbdStorageClass)
	}
	if vmisWithUnsetEfsSC != "" {
		c.results.VMsWithUnsetEfsStorageClass = vmisWithUnsetEfsSC
		appendSep(errStr, errVMsWithUnsetEfsStorageClass)
	}

	return nil
}

// FIXME: check SC name suffix (-ceph-rbd-virtualization) or virt annotation (?). Only one SC should be found and returned.
func (c *Checkup) getVirtStorageClass(scs *storagev1.StorageClassList) (*string, error) {
	var virtSC *string
	for i := range scs.Items {
		sc := scs.Items[i]
		if strings.Contains(sc.Provisioner, "rbd.csi.ceph.com") &&
			sc.Parameters["mounter"] == "rbd" &&
			sc.Parameters["mapOptions"] == "krbd:rxbounce" {
			if virtSC != nil {
				return nil, errors.New("multiple virt StorageClasses")
			}
			virtSC = &sc.Name
		}
	}
	return virtSC, nil
}

func (c *Checkup) getUnsetEfsStorageClass(scs *storagev1.StorageClassList) (*string, error) {
	var unsetEfsSC *string
	for i := range scs.Items {
		sc := scs.Items[i]
		if strings.Contains(sc.Provisioner, "efs.csi.aws.com") &&
			(sc.Parameters["uid"] == "" || sc.Parameters["gid"] == "") {
			if unsetEfsSC != nil {
				return nil, errors.New("multiple unset EFS StorageClasses")
			}
			unsetEfsSC = &sc.Name
		}
	}
	return unsetEfsSC, nil
}

func (c *Checkup) checkVMIVolumes(ctx context.Context, vmi *kvcorev1.VirtualMachineInstance, virtSC, unsetEfsSC *string) (
	hasNonVirtRbdSC bool, hasUnsetEfsSC bool, err error) {
	vols := vmi.Spec.Volumes
	for i := range vols {
		var pv *corev1.PersistentVolume
		pv, err = c.getVolumePV(ctx, vols[i], vmi.Namespace)
		if err != nil || pv == nil {
			return
		}
		if !hasNonVirtRbdSC && pvHasNonVirtRbdStorageClass(pv, virtSC) {
			hasNonVirtRbdSC = true
		}
		if !hasUnsetEfsSC && pvHasUnsetEfsStorageClass(pv, unsetEfsSC) {
			hasUnsetEfsSC = true
		}
	}
	return
}

func (c *Checkup) getVolumePV(ctx context.Context, vol kvcorev1.Volume, namespace string) (*corev1.PersistentVolume, error) {
	pvcName := ""
	if pvc := vol.PersistentVolumeClaim; pvc != nil {
		pvcName = pvc.ClaimName
	} else if dv := vol.DataVolume; dv != nil {
		pvcName = dv.Name
	} else {
		return nil, nil
	}
	pvc, err := c.client.GetPersistentVolumeClaim(ctx, namespace, pvcName)
	if err != nil {
		return nil, fmt.Errorf("failed GetPersistentVolumeClaim: %s", err)
	}
	pv, err := c.client.GetPersistentVolume(ctx, pvc.Spec.VolumeName)
	if err != nil {
		return nil, fmt.Errorf("failed GetPersistentVolume: %s", err)
	}
	return pv, nil
}

func pvHasNonVirtRbdStorageClass(pv *corev1.PersistentVolume, virtSC *string) bool {
	return virtSC != nil && pv.Spec.CSI != nil &&
		strings.Contains(pv.Spec.CSI.Driver, "rbd.csi.ceph.com") &&
		pv.Spec.StorageClassName != *virtSC
}

func pvHasUnsetEfsStorageClass(pv *corev1.PersistentVolume, unsetEfsSC *string) bool {
	return unsetEfsSC != nil && pv.Spec.CSI != nil &&
		pv.Spec.CSI.Driver == "efs.csi.aws.com" &&
		pv.Spec.StorageClassName == *unsetEfsSC
}

func (c *Checkup) Teardown(ctx context.Context) error {
	if c.vmUnderTest == nil {
		return nil
	}

	err := c.client.DeleteVirtualMachine(ctx, c.namespace, c.vmUnderTest.Name)
	if ignoreNotFound(err) != nil {
		return fmt.Errorf("teardown: %v", err)
	}

	err = c.client.DeletePersistentVolumeClaim(ctx, c.namespace, hotplugVolumeName)
	if ignoreNotFound(err) != nil {
		return fmt.Errorf("teardown: %v", err)
	}

	return nil
}

func (c *Checkup) Results() status.Results {
	return c.results
}

func (c *Checkup) checkVMICreation(ctx context.Context, errStr *string) error {
	log.Print("checkVMICreation")
	const randomStringLen = 5
	vmName := fmt.Sprintf("%s-%s", VMIUnderTestNamePrefix, rand.String(randomStringLen))
	c.vmUnderTest = newVMUnderTest(vmName, c.goldenImagePvc)
	log.Printf("Creating VM %q", vmName)
	if _, err := c.client.CreateVirtualMachine(ctx, c.namespace, c.vmUnderTest); err != nil {
		return fmt.Errorf("failed to create VM: %w", err)
	}

	if err := c.waitForVMIStatus(ctx, "successfully booted", &c.results.VMBootFromGoldenImage, errStr,
		func(vmi *kvcorev1.VirtualMachineInstance) (done bool, err error) {
			for i := range vmi.Status.Conditions {
				condition := vmi.Status.Conditions[i]
				if condition.Type == kvcorev1.VirtualMachineInstanceAgentConnected && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
		return err
	}

	pvc, err := c.client.GetPersistentVolumeClaim(ctx, c.namespace, vmispec.OSDataVolumName)
	if err != nil {
		return err
	}
	cloneType := pvc.Annotations["cdi.kubevirt.io/cloneType"]
	c.results.VMVolumeClone = fmt.Sprintf("DV cloneType: %q", cloneType)
	log.Printf(c.results.VMVolumeClone)
	if cloneType != "snapshot" && cloneType != "csi-clone" {
		if reason := pvc.Annotations["cdi.kubevirt.io/cloneFallbackReason"]; reason != "" {
			cloneFallbackReason := fmt.Sprintf("DV clone fallback reason: %s", reason)
			log.Print(cloneFallbackReason)
			appendSep(&c.results.VMVolumeClone, cloneFallbackReason)
			appendSep(errStr, cloneFallbackReason)
		}
	}

	return nil
}

func (c *Checkup) checkVMILiveMigration(ctx context.Context, errStr *string) error {
	log.Print("checkVMILiveMigration")

	vmim := &kvcorev1.VirtualMachineInstanceMigration{
		TypeMeta: metav1.TypeMeta{
			Kind:       kvcorev1.VirtualMachineInstanceGroupVersionKind.Kind,
			APIVersion: kvcorev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "vmim",
		},
		Spec: kvcorev1.VirtualMachineInstanceMigrationSpec{
			VMIName: c.vmUnderTest.Name,
		},
	}

	if _, err := c.client.CreateVirtualMachineInstanceMigration(ctx, c.namespace, vmim); err != nil {
		return fmt.Errorf("failed to create VMI LiveMigration: %w", err)
	}

	if err := c.waitForVMIStatus(ctx, "migration completed", &c.results.VMLiveMigration, errStr,
		func(vmi *kvcorev1.VirtualMachineInstance) (done bool, err error) {
			if ms := vmi.Status.MigrationState; ms != nil {
				if ms.Completed {
					return true, nil
				}
				if ms.Failed {
					return false, errors.New("migration failed")
				}
			}
			return false, nil
		}); err != nil {
		return err
	}

	return nil
}

func (c *Checkup) checkVMIHotplugVolume(ctx context.Context, errStr *string) error {
	log.Print("checkVMIHotplugVolume")

	// FIXME: use default SC instead
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: hotplugVolumeName,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: c.goldenImagePvc.Spec.StorageClassName,
			VolumeMode:       c.goldenImagePvc.Spec.VolumeMode,
			AccessModes:      c.goldenImagePvc.Spec.AccessModes,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Mi"),
				},
			},
		},
	}

	if _, err := c.client.CreatePersistentVolumeClaim(ctx, c.namespace, pvc); err != nil {
		return err
	}

	addVolumeOpts := &kvcorev1.AddVolumeOptions{
		Name: hotplugVolumeName,
		Disk: &kvcorev1.Disk{
			DiskDevice: kvcorev1.DiskDevice{
				Disk: &kvcorev1.DiskTarget{
					Bus: "scsi",
				},
			},
		},
		VolumeSource: &kvcorev1.HotplugVolumeSource{
			PersistentVolumeClaim: &kvcorev1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: hotplugVolumeName,
				},
				Hotpluggable: true,
			},
		},
	}

	if err := c.client.AddVirtualMachineInstanceVolume(ctx, c.namespace, c.vmUnderTest.Name, addVolumeOpts); err != nil {
		return err
	}

	if err := c.waitForVMIStatus(ctx, "hotplug volume ready", &c.results.VMHotplugVolume, errStr,
		func(vmi *kvcorev1.VirtualMachineInstance) (done bool, err error) {
			for i := range vmi.Status.VolumeStatus {
				vs := vmi.Status.VolumeStatus[i]
				// Assuming single HotplugVolume
				if vs.HotplugVolume != nil {
					return vs.Phase == kvcorev1.VolumeReady, nil
				}
			}
			return false, nil
		}); err != nil {
		return err
	}

	removeVolumeOpts := &kvcorev1.RemoveVolumeOptions{
		Name: hotplugVolumeName,
	}

	if err := c.client.RemoveVirtualMachineInstanceVolume(ctx, c.namespace, c.vmUnderTest.Name, removeVolumeOpts); err != nil {
		return err
	}

	if err := c.waitForVMIStatus(ctx, "hotplug volume removed", &c.results.VMHotplugVolume, errStr,
		func(vmi *kvcorev1.VirtualMachineInstance) (done bool, err error) {
			for i := range vmi.Status.VolumeStatus {
				vs := vmi.Status.VolumeStatus[i]
				if vs.HotplugVolume != nil {
					return false, nil
				}
			}
			return true, nil
		}); err != nil {
		return err
	}

	return nil
}

type checkVMIStatusFn func(*kvcorev1.VirtualMachineInstance) (done bool, err error)

func (c *Checkup) waitForVMIStatus(ctx context.Context, checkMsg string, result, errStr *string, checkVMIStatus checkVMIStatusFn) error {
	const pollInterval = 5 * time.Second
	vmName := c.vmUnderTest.Name

	conditionFn := func(ctx context.Context) (bool, error) {
		vmi, err := c.client.GetVirtualMachineInstance(ctx, c.namespace, vmName)
		if err != nil {
			return false, ignoreNotFound(err)
		}
		return checkVMIStatus(vmi)
	}

	log.Printf("Waiting for VMI %q %s", vmName, checkMsg)
	if err := wait.PollImmediateUntilWithContext(ctx, pollInterval, conditionFn); err != nil {
		res := fmt.Sprintf("failed waiting for VMI %q %s: %v", vmName, checkMsg, err)
		log.Print(res)
		appendSep(result, res)
		appendSep(errStr, res)
		return nil
	}
	res := fmt.Sprintf("VMI %q %s", vmName, checkMsg)
	log.Print(res)
	appendSep(result, res)

	return nil
}

func appendSep(s *string, appended string) {
	if s == nil {
		return
	}
	if *s == "" {
		*s = appended
		return
	}
	*s = *s + "\n" + appended
}

func ignoreNotFound(err error) error {
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}
