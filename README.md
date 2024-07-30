# kubevirt-storage-checkup

This checkup performs storage checks, validating storage is working correctly for virtual machines. The checkup is using the Kiagnose engine.

## Permissions

The [following](manifests/storage_checkup_permissions.yaml) ServiceAccount, Role and RoleBinding should be applied on the test namespace.

```bash
kubectl apply -n <target-namespace> -f manifests/storage_checkup_permissions.yaml
```

Cluster admin should create the following cluster-reader permissions for dedicated `storage-checkup-sa` ServiceAccount and namespace:

```yaml
- apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRoleBinding
  metadata:
    name: kubevirt-storage-checkup-clustereader
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: cluster-reader
  subjects:
  - kind: ServiceAccount
    name: storage-checkup-sa
    namespace: <target-namespace>
```

## Configuration

|Key|Description|Is Mandatory|Remarks|
|---------------------------------------------|-------------------------------------------------------------------------------------------------------------------|--------------|-------------------------------------------------------------------------------------|
|spec.timeout|How much time before the checkup will try to close itself|False|Default is 10m|
|spec.param.storageClass|Optional storage class to be used instead of the default one|False||
|spec.param.vmiTimeout|Optional timeout for VMI operations|False|Default is 3m|
|spec.param.numOfVMs|Optional number of concurrent VMs to boot|False|Default is 10|

### Example

In order to execute the checkup, the [following](manifests/storage_checkup.yaml) ConfigMap and Job should be applied on the test namespace.

You can create the ConfigMap and Job with:
```bash
export CHECKUP_NAMESPACE=<target-namespace>

envsubst < manifests/storage_checkup.yaml|kubectl apply -f -
```

and cleanup them with:
```bash
envsubst < manifests/storage_checkup.yaml|kubectl delete -f -
```
## Checkup Results Retrieval

After the checkup Job had completed, the results are made available at the user-supplied ConfigMap object:

```bash
kubectl get configmap storage-checkup-config -n <target-namespace> -o yaml
```
|Key|Description|Remarks|
|--------------------------------------------------|-------------------------------------------------------------------|----------|
|status.succeeded|Has the checkup succeeded||
|status.failureReason|Failure reason in case of a failure||
|status.startTimestamp|Checkup start timestamp|RFC 3339|
|status.completionTimestamp|Checkup completion timestamp|RFC 3339|
|status.result.cnvVersion|OpenShift Virtualization version||
|status.result.ocpVersion|OpenShift Container Platform cluster version||
|status.result.defaultStorageClass|Indicates whether there is a default storage class||
|status.result.pvcBound|PVC of 10Mi created and bound by the provisioner||
|status.result.storageProfilesWithEmptyClaimPropertySets|StorageProfiles with empty claimPropertySets (unknown provisioners)||
|status.result.storageProfilesWithSpecClaimPropertySets|StorageProfiles with spec-overriden claimPropertySets||
|status.result.storageProfilesWithSmartClone|StorageProfiles with smart clone support (CSI/snapshot)||
|status.result.storageProfilesWithRWX|StorageProfiles with ReadWriteMany access mode||
|status.result.storageProfileMissingVolumeSnapshotClass|StorageProfiles using snapshot-based clone but missing VolumeSnapshotClass||
|status.result.goldenImagesNotUpToDate|Golden images whose DataImportCron is not up to date or DataSource is not ready||
|status.result.goldenImagesNoDataSource|Golden images with no DataSource||
|status.result.vmsWithNonVirtRbdStorageClass|VMs using the plain RBD storageclass when the virtualization storageclass exists||
|status.result.vmsWithUnsetEfsStorageClass|VMs using an EFS storageclass where the gid and uid are not set in the storageclass||
|status.result.vmBootFromGoldenImage|VM created and started from a golden image||
|status.result.vmVolumeClone|VM volume clone type used (efficient or host-assisted) and fallback reason||
|status.result.vmLiveMigration|VM live-migration||
|status.result.vmHotplugVolume|VM volume hotplug and unplug||
|status.result.concurrentVMBoot|Concurrent VM boot from a golden image||