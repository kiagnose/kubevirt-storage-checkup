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
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	configv1 "github.com/openshift/api/config/v1"

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
	CreateDataVolume(ctx context.Context, namespace string, dv *cdiv1.DataVolume) (*cdiv1.DataVolume, error)
	DeleteDataVolume(ctx context.Context, namespace, name string) error
	DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error
	ListNodes(ctx context.Context) (*corev1.NodeList, error)
	ListNamespaces(ctx context.Context) (*corev1.NamespaceList, error)
	ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error)
	ListStorageProfiles(ctx context.Context) (*cdiv1.StorageProfileList, error)
	ListVolumeSnapshotClasses(ctx context.Context) (*snapshotv1.VolumeSnapshotClassList, error)
	ListDataImportCrons(ctx context.Context, namespace string) (*cdiv1.DataImportCronList, error)
	ListVirtualMachinesInstances(ctx context.Context, namespace string) (*kvcorev1.VirtualMachineInstanceList, error)
	ListCDIs(ctx context.Context) (*cdiv1.CDIList, error)
	GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error)
	GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error)
	GetPersistentVolume(ctx context.Context, name string) (*corev1.PersistentVolume, error)
	GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error)
	GetCSIDriver(ctx context.Context, name string) (*storagev1.CSIDriver, error)
	GetDataSource(ctx context.Context, namespace, name string) (*cdiv1.DataSource, error)
	GetClusterVersion(ctx context.Context, name string) (*configv1.ClusterVersion, error)
}

const (
	VMIUnderTestNamePrefix = "vmi-under-test"
	hotplugVolumeName      = "hotplug-volume"
	pvcName                = "checkup-pvc"
	StrTrue                = "true"
	StrFalse               = "false"

	AnnDefaultVirtStorageClass = "storageclass.kubevirt.io/is-default-virt-class"
	AnnDefaultStorageClass     = "storageclass.kubernetes.io/is-default-class"

	ErrNoDefaultStorageClass         = "no default storage class"
	ErrPvcNotBound                   = "pvc failed to bound"
	ErrMultipleDefaultStorageClasses = "there are multiple default storage classes"
	ErrEmptyClaimPropertySets        = "there are StorageProfiles with empty ClaimPropertySets (unknown provisioners)"
	// FIXME: need to decide of we want to return errors in this cases
	// errMissingVolumeSnapshotClass    = "there are StorageProfiles missing VolumeSnapshotClass"
	// errVMsWithNonVirtRbdStorageClass = "there are VMs using the plain RBD storageclass when the virtualization storageclass exists"
	ErrVMsWithUnsetEfsStorageClass   = "there are VMs using an EFS storageclass where the gid and uid are not set in the storageclass"
	ErrGoldenImagesNotUpToDate       = "there are golden images whose DataImportCron is not up to date or DataSource is not ready"
	ErrGoldenImageNoDataSource       = "dataSource has no PVC or Snapshot source"
	ErrBootFailedOnSomeVMs           = "some of the VMs failed to complete boot on time"
	MessageBootCompletedOnAllVMs     = "Boot completed on all VMs on time"
	MessageSkipNoDefaultStorageClass = "Skip check - no default storage class"
	MessageSkipNoGoldenImage         = "Skip check - no golden image PVC or Snapshot"
	MessageSkipNoVMI                 = "Skip check - no VMI"
	MessageSkipSingleNode            = "Skip check - single node"

	pollInterval = 5 * time.Second
)

// UnsupportedProvisioners is a hash of provisioners which are known not to work with CDI
var UnsupportedProvisioners = map[string]struct{}{
	"kubernetes.io/no-provisioner": {},
	// The following provisioners may be found in Rook/Ceph deployments and are related to object storage
	"openshift-storage.ceph.rook.io/bucket": {},

	"openshift-storage.noobaa.io/obc": {},
}

type Checkup struct {
	client              kubeVirtStorageClient
	namespace           string
	checkupConfig       config.Config
	defaultStorageClass string
	goldenImageScs      []string
	goldenImagePvc      *corev1.PersistentVolumeClaim
	goldenImageSnap     *snapshotv1.VolumeSnapshot
	vmUnderTest         *kvcorev1.VirtualMachine
	results             status.Results
}

type goldenImagesCheckState struct {
	notReadyDicNames     string
	noDataSourceDicNames string
	fallbackPvcDefaultSC *corev1.PersistentVolumeClaim
	fallbackPvc          *corev1.PersistentVolumeClaim
}

func New(client kubeVirtStorageClient, namespace string, checkupConfig config.Config) *Checkup {
	return &Checkup{
		client:        client,
		namespace:     namespace,
		checkupConfig: checkupConfig,
	}
}

func (c *Checkup) Setup(ctx context.Context) error {
	return nil
}

func (c *Checkup) Run(ctx context.Context) error {
	errStr := ""

	if err := c.checkVersions(ctx); err != nil {
		return err
	}

	scs, err := c.client.ListStorageClasses(ctx)
	if err != nil {
		return err
	}

	c.checkDefaultStorageClass(scs, &errStr)
	err = c.checkPVCCreationAndBinding(ctx, &errStr)
	if err != nil {
		return err
	}

	sps, err := c.client.ListStorageProfiles(ctx)
	if err != nil {
		return err
	}

	vscs, err := c.client.ListVolumeSnapshotClasses(ctx)
	if err != nil {
		return err
	}

	c.checkStorageProfiles(ctx, sps, vscs, &errStr)
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

	if err := c.checkVMIBoot(ctx, &errStr); err != nil {
		return err
	}
	if err := c.checkVMILiveMigration(ctx, &errStr); err != nil {
		return err
	}
	if err := c.checkVMIHotplugVolume(ctx, &errStr); err != nil {
		return err
	}

	if err := c.checkConcurrentVMIBoot(ctx, &errStr); err != nil {
		return err
	}

	if errStr != "" {
		return errors.New(errStr)
	}

	return nil
}

func (c *Checkup) checkVersions(ctx context.Context) error {
	log.Print("checkVersions")

	ver, err := c.client.GetClusterVersion(ctx, "version")
	if err != nil {
		return err
	}
	ocpVersion := ""
	for _, update := range ver.Status.History {
		if update.State == configv1.CompletedUpdate {
			// obtain the version from the last completed update
			ocpVersion = update.Version
			break
		}
	}

	cdis, err := c.client.ListCDIs(ctx)
	if err != nil {
		return err
	}
	if len(cdis.Items) == 0 {
		return errors.New("no CDI deployed in cluster")
	}
	if len(cdis.Items) != 1 {
		return errors.New("expecting single CDI instance in cluster")
	}
	cnvVersion := cdis.Items[0].Labels["app.kubernetes.io/version"]

	log.Printf("OCP version: %s, CNV version: %s", ocpVersion, cnvVersion)
	c.results.OCPVersion = ocpVersion
	c.results.CNVVersion = cnvVersion

	return nil
}

// FIXME: allow providing specific golden image namespace in the config, instead of scanning all namespaces
func (c *Checkup) checkGoldenImages(ctx context.Context, namespaces *corev1.NamespaceList, errStr *string) error {
	log.Print("checkGoldenImages")

	const defaultGoldenImagesNamespace = "openshift-virtualization-os-images"
	var cs goldenImagesCheckState

	if ns, err := c.client.GetNamespace(ctx, defaultGoldenImagesNamespace); err == nil {
		if err := c.checkDataImportCrons(ctx, ns.Name, &cs); err != nil {
			return err
		}
	}

	for i := range namespaces.Items {
		if ns := namespaces.Items[i].Name; ns != defaultGoldenImagesNamespace {
			if err := c.checkDataImportCrons(ctx, ns, &cs); err != nil {
				return err
			}
		}
	}

	if c.goldenImagePvc == nil {
		if cs.fallbackPvcDefaultSC != nil {
			c.goldenImagePvc = cs.fallbackPvcDefaultSC
		} else if cs.fallbackPvc != nil {
			c.goldenImagePvc = cs.fallbackPvc
		}
	}

	if pvc := c.goldenImagePvc; pvc != nil {
		log.Printf("Selected golden image PVC: %s/%s %s %s %s", pvc.Namespace, pvc.Name, *pvc.Spec.VolumeMode,
			pvc.Status.AccessModes[0], *pvc.Spec.StorageClassName)
	} else if snap := c.goldenImageSnap; snap != nil {
		log.Printf("Selected golden image Snapshot: %s/%s", snap.Namespace, snap.Name)
	} else {
		log.Print("No golden image PVC or Snapshot found")
	}

	if cs.notReadyDicNames != "" {
		c.results.GoldenImagesNotUpToDate = cs.notReadyDicNames
		appendSep(errStr, ErrGoldenImagesNotUpToDate)
	}
	if cs.noDataSourceDicNames != "" {
		c.results.GoldenImagesNoDataSource = cs.noDataSourceDicNames
		appendSep(errStr, ErrGoldenImageNoDataSource)
	}
	return nil
}

func (c *Checkup) checkDataImportCrons(ctx context.Context, namespace string, cs *goldenImagesCheckState) error {
	dics, err := c.client.ListDataImportCrons(ctx, namespace)
	if err != nil {
		return err
	}

	sort.Slice(dics.Items, func(i, j int) bool {
		iTS := dics.Items[i].Status.LastImportTimestamp
		jTS := dics.Items[j].Status.LastImportTimestamp
		if iTS != nil && jTS != nil {
			return iTS.After(jTS.Time)
		}
		return iTS != nil && jTS == nil
	})

	for i := range dics.Items {
		dic := &dics.Items[i]
		pvc, snap, err := c.getGoldenImage(ctx, dic)
		if err != nil {
			if err.Error() == ErrGoldenImageNoDataSource {
				appendSep(&cs.noDataSourceDicNames, dic.Namespace+"/"+dic.Name)
				continue
			} else if err.Error() == ErrGoldenImagesNotUpToDate {
				appendSep(&cs.notReadyDicNames, dic.Namespace+"/"+dic.Name)
				continue
			}
			return err
		}

		c.updateGoldenImageSnapshot(snap)
		c.updateGoldenImagePvc(pvc, cs)
	}

	return nil
}

func (c *Checkup) getGoldenImage(ctx context.Context, dic *cdiv1.DataImportCron) (
	*corev1.PersistentVolumeClaim, *snapshotv1.VolumeSnapshot, error) {
	if !isDataImportCronUpToDate(dic.Status.Conditions) {
		return nil, nil, errors.New(ErrGoldenImagesNotUpToDate)
	}
	das, err := c.client.GetDataSource(ctx, dic.Namespace, dic.Spec.ManagedDataSource)
	if err != nil {
		return nil, nil, err
	}
	if !isDataSourceReady(das.Status.Conditions) {
		return nil, nil, errors.New(ErrGoldenImagesNotUpToDate)
	}

	if srcPvc := das.Spec.Source.PVC; srcPvc != nil {
		pvc, err := c.client.GetPersistentVolumeClaim(ctx, srcPvc.Namespace, srcPvc.Name)
		if err != nil {
			return nil, nil, err
		}
		return pvc, nil, nil
	}
	if srcSnap := das.Spec.Source.Snapshot; srcSnap != nil {
		snap, err := c.client.GetVolumeSnapshot(ctx, srcSnap.Namespace, srcSnap.Name)
		if err != nil {
			return nil, nil, err
		}
		return nil, snap, nil
	}

	return nil, nil, errors.New(ErrGoldenImageNoDataSource)
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

func (c *Checkup) updateGoldenImagePvc(pvc *corev1.PersistentVolumeClaim, cs *goldenImagesCheckState) {
	if pvc == nil {
		return
	}

	// Prefer golden image with configured/default storage class
	if c.goldenImagePvc != nil {
		sc := c.goldenImagePvc.Spec.StorageClassName
		if sc == nil {
			return
		}
		if *sc == c.results.DefaultStorageClass ||
			(c.checkupConfig.StorageClass != "" && *sc == c.checkupConfig.StorageClass) {
			return
		}
	}

	sc := pvc.Spec.StorageClassName
	if sc != nil && contains(c.goldenImageScs, *sc) {
		c.goldenImagePvc = pvc
	} else if cs.fallbackPvcDefaultSC == nil && (sc == nil || *sc == c.results.DefaultStorageClass) {
		cs.fallbackPvcDefaultSC = pvc
	} else if cs.fallbackPvc == nil {
		cs.fallbackPvc = pvc
	}
}

func (c *Checkup) updateGoldenImageSnapshot(snap *snapshotv1.VolumeSnapshot) {
	if snap != nil && c.goldenImageSnap == nil {
		c.goldenImageSnap = snap
	}
}

func (c *Checkup) checkDefaultStorageClass(scs *storagev1.StorageClassList, errStr *string) {
	log.Print("checkDefaultStorageClass")

	var multipleDefaultStorageClasses, hasDefaultVirtStorageClass, hasDefaultStorageClass bool
	for i := range scs.Items {
		sc := scs.Items[i]
		if sc.Annotations[AnnDefaultVirtStorageClass] == StrTrue {
			if !hasDefaultVirtStorageClass {
				hasDefaultVirtStorageClass = true
				c.defaultStorageClass = sc.Name
			} else {
				multipleDefaultStorageClasses = true
			}
		}
		if sc.Annotations[AnnDefaultStorageClass] == StrTrue {
			if !hasDefaultStorageClass {
				hasDefaultStorageClass = true
				if !hasDefaultVirtStorageClass {
					c.defaultStorageClass = sc.Name
				}
			} else {
				multipleDefaultStorageClasses = true
			}
		}
	}

	if multipleDefaultStorageClasses {
		c.results.DefaultStorageClass = ErrMultipleDefaultStorageClasses
		appendSep(errStr, ErrMultipleDefaultStorageClasses)
	} else if c.defaultStorageClass != "" {
		c.results.DefaultStorageClass = c.defaultStorageClass
	} else {
		c.results.DefaultStorageClass = ErrNoDefaultStorageClass
		appendSep(errStr, ErrNoDefaultStorageClass)
	}
}

func (c *Checkup) checkPVCCreationAndBinding(ctx context.Context, errStr *string) error {
	log.Print("checkPVCCreationAndBinding")

	if c.defaultStorageClass == "" && c.checkupConfig.StorageClass == "" {
		log.Print(MessageSkipNoDefaultStorageClass)
		c.results.PVCBound = MessageSkipNoDefaultStorageClass
		return nil
	}

	dv := &cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvcName,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       c.checkupConfig.PodName,
				UID:        types.UID(c.checkupConfig.PodUID),
			}},
			Annotations: map[string]string{
				"cdi.kubevirt.io/storage.bind.immediate.requested": StrTrue,
			},
		},
		Spec: cdiv1.DataVolumeSpec{
			Storage: &cdiv1.StorageSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Mi"),
					},
				},
			},
			Source: &cdiv1.DataVolumeSource{
				Blank: &cdiv1.DataVolumeBlankImage{},
			},
		},
	}

	if sc := c.checkupConfig.StorageClass; sc != "" {
		log.Printf("PVC storage class %q", sc)
		dv.Spec.Storage.StorageClassName = &sc
	}

	if _, err := c.client.CreateDataVolume(ctx, c.namespace, dv); err != nil {
		return err
	}

	c.waitForPVCBound(ctx, &c.results.PVCBound, errStr)

	return c.client.DeleteDataVolume(ctx, c.namespace, pvcName)
}

func (c *Checkup) waitForPVCBound(ctx context.Context, result, errStr *string) {
	conditionFn := func(ctx context.Context) (bool, error) {
		pvc, err := c.client.GetPersistentVolumeClaim(ctx, c.namespace, pvcName)
		if err != nil {
			return false, ignoreNotFound(err)
		}
		return pvc.Status.Phase == corev1.ClaimBound, nil
	}

	log.Printf("Waiting for PVC %q bound", pvcName)
	if err := wait.PollImmediateWithContext(ctx, pollInterval, time.Minute, conditionFn); err != nil {
		log.Printf("PVC %q failed to bound", pvcName)
		appendSep(result, ErrPvcNotBound)
		appendSep(errStr, ErrPvcNotBound)
		return
	}

	res := fmt.Sprintf("PVC %q bound", pvcName)
	log.Print(res)
	appendSep(result, res)
}

func (c *Checkup) hasSmartClone(ctx context.Context, sp *cdiv1.StorageProfile, vscs *snapshotv1.VolumeSnapshotClassList) bool {
	strategy := sp.Status.CloneStrategy
	provisioner := sp.Status.Provisioner

	if strategy != nil {
		if *strategy == cdiv1.CloneStrategyHostAssisted {
			return false
		}
		if *strategy == cdiv1.CloneStrategyCsiClone && provisioner != nil {
			_, err := c.client.GetCSIDriver(ctx, *provisioner)
			return err == nil
		}
	}

	if (strategy == nil || *strategy == cdiv1.CloneStrategySnapshot) && provisioner != nil {
		return hasDriver(vscs, *provisioner)
	}

	return false
}

func (c *Checkup) checkStorageProfiles(ctx context.Context, sps *cdiv1.StorageProfileList,
	vscs *snapshotv1.VolumeSnapshotClassList, errStr *string) {
	spWithEmptyClaimPropertySets := ""
	spWithSpecClaimPropertySets := ""
	spWithSmartClone := ""
	spWithRWX := ""

	log.Print("checkStorageProfiles")
	for i := range sps.Items {
		sp := &sps.Items[i]
		provisioner := sp.Status.Provisioner
		if provisioner == nil || unsupportedProvisioner(*provisioner) {
			continue
		}

		sc := sp.Status.StorageClass
		hasSmartClone := c.hasSmartClone(ctx, sp, vscs)
		hasRWX := hasRWX(sp.Status.ClaimPropertySets)
		if sc != nil && hasSmartClone && hasRWX {
			c.goldenImageScs = append(c.goldenImageScs, *sc)
		}

		if len(sp.Status.ClaimPropertySets) == 0 {
			appendSep(&spWithEmptyClaimPropertySets, sp.Name)
		}
		if len(sp.Spec.ClaimPropertySets) != 0 {
			appendSep(&spWithSpecClaimPropertySets, sp.Name)
		}
		if hasSmartClone {
			appendSep(&spWithSmartClone, sp.Name)
		}
		if hasRWX {
			appendSep(&spWithRWX, sp.Name)
		}
	}

	if spWithEmptyClaimPropertySets != "" {
		c.results.StorageProfilesWithEmptyClaimPropertySets = spWithEmptyClaimPropertySets
		appendSep(errStr, ErrEmptyClaimPropertySets)
	}
	if spWithSpecClaimPropertySets != "" {
		c.results.StorageProfilesWithSpecClaimPropertySets = spWithSpecClaimPropertySets
	}
	if spWithSmartClone != "" {
		c.results.StorageProfilesWithSmartClone = spWithSmartClone
	}
	if spWithRWX != "" {
		c.results.StorageProfilesWithRWX = spWithRWX
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

func (c *Checkup) checkVolumeSnapShotClasses(sps *cdiv1.StorageProfileList, vscs *snapshotv1.VolumeSnapshotClassList, _ *string) {
	log.Print("checkVolumeSnapShotClasses")

	spNames := ""
	for i := range sps.Items {
		sp := sps.Items[i]
		strategy := sp.Status.CloneStrategy
		provisioner := sp.Status.Provisioner
		if (strategy == nil || *strategy == cdiv1.CloneStrategySnapshot) &&
			provisioner != nil && !unsupportedProvisioner(*provisioner) &&
			!hasDriver(vscs, *provisioner) {
			appendSep(&spNames, sp.Name)
		}
	}
	if spNames != "" {
		c.results.StorageProfileMissingVolumeSnapshotClass = spNames
		// FIXME: not sure the checkup should fail on this one
		// appendSep(errStr, errMissingVolumeSnapshotClass)
	}
}

func unsupportedProvisioner(provisioner string) bool {
	_, ok := UnsupportedProvisioners[provisioner]
	return ok
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
		appendSep(errStr, ErrVMsWithUnsetEfsStorageClass)
	}

	return nil
}

func (c *Checkup) getVirtStorageClass(scs *storagev1.StorageClassList) (*string, error) {
	var virtSC *string

	hasVirtParams := func(sc *storagev1.StorageClass) bool {
		return strings.Contains(sc.Provisioner, "rbd.csi.ceph.com") &&
			sc.Parameters["mounter"] == "rbd" &&
			sc.Parameters["mapOptions"] == "krbd:rxbounce"
	}

	// First look for SC named with virtualization suffix
	for i := range scs.Items {
		sc := &scs.Items[i]
		if strings.HasSuffix(sc.Name, "-ceph-rbd-virtualization") {
			if virtSC != nil {
				return nil, errors.New("multiple virt StorageClasses")
			}
			if hasVirtParams(sc) {
				virtSC = &sc.Name
			}
		}
	}

	if virtSC != nil {
		return virtSC, nil
	}

	// If virt SC not found, look for one with virt params
	for i := range scs.Items {
		sc := &scs.Items[i]
		if hasVirtParams(sc) {
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

	if err := c.client.DeleteVirtualMachine(ctx, c.namespace, c.vmUnderTest.Name); ignoreNotFound(err) != nil {
		return fmt.Errorf("teardown: %v", err)
	}

	if err := c.client.DeleteDataVolume(ctx, c.namespace, hotplugVolumeName); ignoreNotFound(err) != nil {
		return fmt.Errorf("teardown: %v", err)
	}

	if err := c.client.DeletePersistentVolumeClaim(ctx, c.namespace, hotplugVolumeName); ignoreNotFound(err) != nil {
		return fmt.Errorf("teardown: %v", err)
	}

	return nil
}

func (c *Checkup) Results() status.Results {
	return c.results
}

func (c *Checkup) Config() config.Config {
	return c.checkupConfig
}

func (c *Checkup) checkVMIBoot(ctx context.Context, errStr *string) error {
	log.Print("checkVMIBoot")

	if c.defaultStorageClass == "" && c.checkupConfig.StorageClass == "" {
		log.Print(MessageSkipNoDefaultStorageClass)
		c.results.VMBootFromGoldenImage = MessageSkipNoDefaultStorageClass
		return nil
	}

	if c.goldenImagePvc == nil && c.goldenImageSnap == nil {
		log.Print(MessageSkipNoGoldenImage)
		c.results.VMBootFromGoldenImage = MessageSkipNoGoldenImage
		return nil
	}

	vmName := uniqueVMName()
	c.vmUnderTest = newVMUnderTest(vmName, c.goldenImagePvc, c.goldenImageSnap, c.checkupConfig, false)
	log.Printf("Creating VM %q", vmName)
	if _, err := c.client.CreateVirtualMachine(ctx, c.namespace, c.vmUnderTest); err != nil {
		return fmt.Errorf("failed to create VM: %w", err)
	}

	if err := c.waitForVMIBoot(ctx, vmName, &c.results.VMBootFromGoldenImage, errStr); err != nil {
		return err
	}

	if c.goldenImageSnap != nil {
		c.results.VMVolumeClone = "DV cloneType: snapshot"
		return nil
	}

	pvc, err := c.client.GetPersistentVolumeClaim(ctx, c.namespace, getVMDvName(vmName))
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

	if c.vmUnderTest == nil {
		log.Print(MessageSkipNoVMI)
		c.results.VMLiveMigration = MessageSkipNoVMI
		return nil
	}

	nodes, err := c.client.ListNodes(ctx)
	if err != nil {
		return err
	}
	if len(nodes.Items) == 1 {
		log.Print(MessageSkipSingleNode)
		c.results.VMLiveMigration = MessageSkipSingleNode
		return nil
	}

	vmName := c.vmUnderTest.Name
	vmi, err := c.client.GetVirtualMachineInstance(ctx, c.namespace, vmName)
	if err != nil {
		return err
	}
	for i := range vmi.Status.Conditions {
		condition := vmi.Status.Conditions[i]
		if condition.Type == kvcorev1.VirtualMachineInstanceIsMigratable && condition.Status == corev1.ConditionFalse {
			log.Print(condition.Message)
			c.results.VMLiveMigration = condition.Message
			return nil
		}
	}

	vmim := &kvcorev1.VirtualMachineInstanceMigration{
		TypeMeta: metav1.TypeMeta{
			Kind:       kvcorev1.VirtualMachineInstanceGroupVersionKind.Kind,
			APIVersion: kvcorev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "vmim",
		},
		Spec: kvcorev1.VirtualMachineInstanceMigrationSpec{
			VMIName: vmName,
		},
	}

	if _, err := c.client.CreateVirtualMachineInstanceMigration(ctx, c.namespace, vmim); err != nil {
		return fmt.Errorf("failed to create VMI LiveMigration: %w", err)
	}

	if err := c.waitForVMIStatus(ctx, vmName, "migration completed", &c.results.VMLiveMigration, errStr,
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

	if c.vmUnderTest == nil {
		log.Print(MessageSkipNoVMI)
		c.results.VMHotplugVolume = MessageSkipNoVMI
		return nil
	}

	dv := &cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: hotplugVolumeName,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       c.checkupConfig.PodName,
				UID:        types.UID(c.checkupConfig.PodUID),
			}},
		},
		Spec: c.vmUnderTest.Spec.DataVolumeTemplates[0].Spec,
	}

	if _, err := c.client.CreateDataVolume(ctx, c.namespace, dv); err != nil {
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
			DataVolume: &kvcorev1.DataVolumeSource{
				Name:         hotplugVolumeName,
				Hotpluggable: true,
			},
		},
	}

	vmName := c.vmUnderTest.Name
	if err := c.client.AddVirtualMachineInstanceVolume(ctx, c.namespace, vmName, addVolumeOpts); err != nil {
		return err
	}

	if err := c.waitForVMIStatus(ctx, vmName, "hotplug volume ready", &c.results.VMHotplugVolume, errStr,
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

	if err := c.client.RemoveVirtualMachineInstanceVolume(ctx, c.namespace, vmName, removeVolumeOpts); err != nil {
		return err
	}

	if err := c.waitForVMIStatus(ctx, vmName, "hotplug volume removed", &c.results.VMHotplugVolume, errStr,
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

func (c *Checkup) checkConcurrentVMIBoot(ctx context.Context, errStr *string) error {
	numOfVMs := c.checkupConfig.NumOfVMs
	log.Printf("checkConcurrentVMIBoot numOfVMs:%d", numOfVMs)

	if c.defaultStorageClass == "" && c.checkupConfig.StorageClass == "" {
		log.Print(MessageSkipNoDefaultStorageClass)
		c.results.ConcurrentVMBoot = MessageSkipNoDefaultStorageClass
		return nil
	}

	if c.goldenImagePvc == nil && c.goldenImageSnap == nil {
		log.Print(MessageSkipNoGoldenImage)
		c.results.ConcurrentVMBoot = MessageSkipNoGoldenImage
		return nil
	}

	var wg sync.WaitGroup
	isBootOk := true

	for i := 0; i < numOfVMs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			vmName := uniqueVMName()
			log.Printf("Creating VM %q", vmName)
			vm := newVMUnderTest(vmName, c.goldenImagePvc, c.goldenImageSnap, c.checkupConfig, true)
			if _, err := c.client.CreateVirtualMachine(ctx, c.namespace, vm); err != nil {
				log.Printf("failed to create VM %q: %s", vmName, err)
				isBootOk = false
				return
			}

			defer func() {
				if err := c.client.DeleteVirtualMachine(ctx, c.namespace, vmName); err != nil {
					log.Printf("failed to delete VM %q: %s", vmName, err)
				}
			}()

			var result, errs string
			if err := c.waitForVMIBoot(ctx, vmName, &result, &errs); err != nil || errs != "" {
				log.Printf("failed waiting for VM boot %q", vmName)
				isBootOk = false
			}
		}()
	}

	wg.Wait()
	if !isBootOk {
		log.Print(ErrBootFailedOnSomeVMs)
		c.results.ConcurrentVMBoot = ErrBootFailedOnSomeVMs
		appendSep(errStr, ErrBootFailedOnSomeVMs)
		return nil
	}

	log.Print(MessageBootCompletedOnAllVMs)
	c.results.ConcurrentVMBoot = MessageBootCompletedOnAllVMs

	return nil
}

func (c *Checkup) waitForVMIBoot(ctx context.Context, vmName string, result, errStr *string) error {
	return c.waitForVMIStatus(ctx, vmName, "successfully booted", result, errStr,
		func(vmi *kvcorev1.VirtualMachineInstance) (done bool, err error) {
			for i := range vmi.Status.Conditions {
				condition := vmi.Status.Conditions[i]
				if condition.Type == kvcorev1.VirtualMachineInstanceAgentConnected && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		})
}

func uniqueVMName() string {
	const randomStringLen = 5
	return fmt.Sprintf("%s-%s", VMIUnderTestNamePrefix, rand.String(randomStringLen))
}

type checkVMIStatusFn func(*kvcorev1.VirtualMachineInstance) (done bool, err error)

func (c *Checkup) waitForVMIStatus(ctx context.Context, vmName, checkMsg string, result, errStr *string,
	checkVMIStatus checkVMIStatusFn) error {
	conditionFn := func(ctx context.Context) (bool, error) {
		vmi, err := c.client.GetVirtualMachineInstance(ctx, c.namespace, vmName)
		if err != nil {
			return false, ignoreNotFound(err)
		}
		return checkVMIStatus(vmi)
	}

	log.Printf("Waiting for VMI %q %s", vmName, checkMsg)
	if err := wait.PollImmediateWithContext(ctx, pollInterval, c.checkupConfig.VMITimeout, conditionFn); err != nil {
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

// FIXME: use slices.contains instead
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func ignoreNotFound(err error) error {
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return err
}
