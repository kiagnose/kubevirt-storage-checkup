# Running kubevirt-storage-checkup on Non-OpenShift Kubernetes

This guide covers the additional setup required to run the storage checkup
on vanilla Kubernetes clusters (i.e., clusters without OpenShift / CNV).

On OpenShift, the CNV operator handles RBAC, feature gates, networking
defaults, and golden image provisioning automatically. On vanilla Kubernetes
these must be configured manually.

## Prerequisites

- Kubernetes cluster with [KubeVirt](https://kubevirt.io/user-guide/cluster_admin/installation/) installed
- [CDI](https://github.com/kubevirt/containerized-data-importer) (Containerized Data Importer) installed
- A CSI-based StorageClass with a default set (e.g., Ceph, Longhorn, etc.)
- [VolumeSnapshot CRDs and controller](https://github.com/kubernetes-csi/external-snapshotter) installed (for snapshot-based clone tests)

## 1. KubeVirt Configuration

The checkup tests volume hotplug and live migration, which require specific
KubeVirt settings that are not enabled by default.

### Feature Gates

Enable the `HotplugVolumes` feature gate:

```bash
kubectl patch kubevirt kubevirt -n kubevirt --type=merge \
  -p '{"spec":{"configuration":{"developerConfiguration":{"featureGates":["HotplugVolumes"]}}}}'
```

Without this, the hotplug volume check will fail with:
`Enable DeclarativeHotplugVolumes or HotplugVolumes feature gate to use this API.`

### Default Network Interface

Set masquerade as the default network interface to allow live migration:

```bash
kubectl patch kubevirt kubevirt -n kubevirt --type=merge \
  -p '{"spec":{"configuration":{"network":{"defaultNetworkInterface":"masquerade"}}}}'
```

Without this, KubeVirt creates VMs with bridge networking by default, which
is not live-migratable. The live migration check will be skipped with:
`cannot migrate VMI which does not use masquerade [...]`

Both settings can be applied in a single patch:

```bash
kubectl patch kubevirt kubevirt -n kubevirt --type=merge -p '{
  "spec": {
    "configuration": {
      "developerConfiguration": {
        "featureGates": ["HotplugVolumes"]
      },
      "network": {
        "defaultNetworkInterface": "masquerade"
      }
    }
  }
}'
```

## 2. RBAC

The upstream instructions reference `cluster-reader`, an OpenShift-only
ClusterRole. On vanilla Kubernetes, use the provided ClusterRole manifest
instead.

Apply the namespace-scoped RBAC:

```bash
kubectl apply -n <target-namespace> -f manifests/storage_checkup_permissions.yaml
```

Apply the ClusterRole and bind it to the checkup ServiceAccount:

```bash
kubectl apply -f manifests/storage_checkup_cluster_role.yaml

kubectl create clusterrolebinding kubevirt-storage-checkup-clustereader \
  --clusterrole=kubevirt-storage-checkup-reader \
  --serviceaccount=<target-namespace>:storage-checkup-sa
```

## 3. Golden Images

On OpenShift, the CNV operator automatically creates DataImportCrons that
import OS images (Fedora, CentOS, RHEL, etc.) for use as VM boot disks. On
vanilla Kubernetes, you must create one manually.

Apply the provided DataImportCron manifest:

```bash
kubectl apply -n <target-namespace> -f manifests/golden_image_dataimportcron.yaml
```

Wait for the initial image import to complete (typically 2-5 minutes):

```bash
kubectl wait -n <target-namespace> dataimportcron/fedora-golden \
  --for=condition=UpToDate --timeout=600s
```

Without a golden image, the following checks are silently skipped:
- VM boot from golden image
- VM live migration
- VM volume hotplug
- Concurrent VM boot

The golden image persists across checkup runs. You only need to create it
once per namespace.

## 4. Running the Checkup

Once the prerequisites above are in place, create the ConfigMap and Job:

```bash
export CHECKUP_NAMESPACE=<target-namespace>

envsubst < manifests/storage_checkup.yaml | kubectl apply -f -
```

### Tuning for Smaller Clusters

The default configuration boots 10 VMs concurrently with a 3-minute per-VM
timeout. On smaller clusters (2-3 nodes), this may not be enough. Adjust the
ConfigMap before deploying:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: storage-checkup-config
data:
  spec.timeout: 20m
  spec.param.vmiTimeout: 8m
  spec.param.numOfVMs: "3"
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `spec.timeout` | 10m | Overall checkup job timeout |
| `spec.param.vmiTimeout` | 3m | Timeout per individual VM operation (boot, migration, hotplug) |
| `spec.param.numOfVMs` | 10 | Number of VMs for the concurrent boot test |
| `spec.param.storageClass` | (default SC) | Override the StorageClass used for tests |

## 5. Checking Results

```bash
kubectl get configmap storage-checkup-config -n <target-namespace> -o yaml
```

Key result fields:

| Field | Description |
|-------|-------------|
| `status.succeeded` | `true` if all checks passed |
| `status.failureReason` | Error details if any check failed |
| `status.result.ocpVersion` | Empty on non-OpenShift clusters (expected) |
| `status.result.cnvVersion` | May be empty if CDI lacks the `app.kubernetes.io/version` label |
| `status.result.pvcBound` | PVC creation and binding test result |
| `status.result.vmBootFromGoldenImage` | VM boot from cloned golden image |
| `status.result.vmLiveMigration` | VM live migration result |
| `status.result.vmHotplugVolume` | Volume hotplug attach/detach result |
| `status.result.concurrentVMBoot` | Concurrent VM boot result |

## 6. Cleanup

```bash
export CHECKUP_NAMESPACE=<target-namespace>

envsubst < manifests/storage_checkup.yaml | kubectl delete -f -
kubectl delete -n $CHECKUP_NAMESPACE -f manifests/golden_image_dataimportcron.yaml
kubectl delete clusterrolebinding kubevirt-storage-checkup-clustereader
kubectl delete -f manifests/storage_checkup_cluster_role.yaml
kubectl delete -n $CHECKUP_NAMESPACE -f manifests/storage_checkup_permissions.yaml
```

