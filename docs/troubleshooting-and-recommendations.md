# Troubleshooting and Recommendations — kubevirt-storage-checkup

> **Purpose of this document:**
> This is source material for documentation of the
> kubevirt-storage-checkup. It lists everything the checkup can report, what each
> item means, and how to act on it. It is written to be accurate against the code,
> so the facts can be reshaped into user-facing docs.
>
> Items are grouped into three categories that the checkup treats differently:
> 1. **Failures** — the checkup reports `status.succeeded: false` and sets `status.failureReason`.
> 2. **Recommendations** — the checkup records a finding in `status.result.*` but
>    does **not** fail. These are advisories.
> 3. **Skips** — a check is intentionally not run; this is expected, not an error.

## How to read checkup output

After the checkup Job completes, read the result ConfigMap:

```bash
kubectl get configmap storage-checkup-config -n <target-namespace> -o yaml
```

The two fields to look at first:

| Field | Meaning |
|---|---|
| `status.succeeded` | `true` if all checks passed, `false` otherwise. |
| `status.failureReason` | When `succeeded: false`, a newline-separated list of the failure messages. Each message is one failed check. |

Per-check detail is under `status.result.*` (see [Result fields reference](#result-fields-reference)). When debugging, also read the checkup Pod logs and consider setting `spec.param.skipTeardown: onfailure` (see [Debugging tips](#debugging-tips)).

---

## 1. Failures (checkup reports `succeeded: false`)

Each row is a message that can appear in `status.failureReason`. Some messages are fixed strings; others are composed at runtime and are shown here as a template with `…` for the runtime detail.

| Failure message | What it means | How to fix |
|---|---|---|
| `No default storage class found. Set a default StorageClass on the cluster` | The cluster has no StorageClass annotated as default. This check inspects StorageClass annotations only. | Annotate one StorageClass as default: `storageclass.kubernetes.io/is-default-class: "true"`, or the virt-specific `storageclass.kubevirt.io/is-default-virt-class: "true"`. |
| `Multiple default storage classes found. Ensure only one StorageClass is annotated as default` | More than one StorageClass is annotated as default (for either the standard or the virt default annotation). | Remove the default annotation from all but one StorageClass. |
| `PVC binding check failed: a test PVC did not bind within the timeout. Check that the storage provisioner is healthy and the StorageClass is functional` | The checkup created a small (10Mi) test DataVolume/PVC and it did not reach `Bound` within ~1 minute. | Verify the storage provisioner/CSI driver is running and healthy; confirm the StorageClass can actually provision volumes (check events on the PVC). |
| `Some StorageProfiles have empty ClaimPropertySets (unknown provisioners). Check that all provisioners are properly configured` | One or more StorageProfiles have no `status.claimPropertySets` — CDI does not know how to provision for that provisioner. Affected profiles are listed in `status.result.storageProfilesWithEmptyClaimPropertySets`. | Configure the provisioner correctly, or populate the StorageProfile `spec.claimPropertySets` manually so CDI knows the access modes / volume modes to use. |
| `VMs are using an EFS StorageClass where uid/gid are not set. Configure uid and gid in the StorageClass parameters` | Running VMs use an AWS EFS StorageClass whose `uid`/`gid` parameters are empty. Affected VMs are listed in `status.result.vmsWithUnsetEfsStorageClass`. | Set the `uid` and `gid` parameters in the EFS StorageClass. |
| `Golden images are not up to date: DataImportCron is not current or DataSource is not ready` | At least one golden-image DataImportCron does not report the `UpToDate` condition, or its managed DataSource is not `Ready`. Affected items are in `status.result.goldenImagesNotUpToDate`. | Check the DataImportCron and its DataSource: confirm the source registry/image is reachable and the import succeeded; inspect the DataImportCron status and CDI import Pod logs. |
| `Golden image DataSource has no PVC or Snapshot source configured` | A golden-image DataSource is `Ready` but its spec references neither a PVC nor a VolumeSnapshot source. Affected items are in `status.result.goldenImagesNoDataSource`. | Fix the DataSource so its `spec.source` points to a valid PVC or Snapshot. |
| `DV clone fallback reason: …` | The test VM's disk was cloned from the golden image, but CDI could **not** use an efficient clone (CSI or snapshot) and fell back to host-assisted copy. The `…` is CDI's own reason string. | Review the fallback reason. Typically the StorageProfile lacks smart-clone support — ensure the provisioner supports CSI clone or a matching VolumeSnapshotClass exists (see `storageProfileMissingVolumeSnapshotClass`). |
| `Concurrent VM boot check failed: one or more VMs did not boot successfully. Check the logs and the concurrentVMBoot result for details: …` | Of the N concurrently booted VMs (`spec.param.numOfVMs`, default 10), at least one failed to boot in time. Per-VM reasons follow in the message and in `status.result.concurrentVMBoot`. | Check whether the failure is capacity/scheduling (insufficient nodes/resources), slow provisioning under load, or storage throughput limits. Lower `numOfVMs` to isolate, and inspect the named VMs' events. |
| `failed waiting for VMI "<name>" successfully booted: …` | The single test VM created from the golden image did not reach "agent connected" within `vmiTimeout` (default 3m). Reflected in `status.result.vmBootFromGoldenImage`. | Check the VMI/virt-launcher Pod events and logs: scheduling failure, image pull/clone slowness, or the guest agent not starting. Increase `spec.param.vmiTimeout` if provisioning is legitimately slow. |
| `failed waiting for VMI "<name>" hotplug volume ready: …` / `… hotplug volume removed: …` | Volume hotplug (attach) or unplug (detach) did not complete within `vmiTimeout`. Reflected in `status.result.vmHotplugVolume`. | Confirm the StorageClass supports hotplug (RWX / block as required), and check virt-launcher and CDI events for the hotplug DataVolume. |
| `failed waiting for VMI "<name>" migration completed: …` | Live migration of the test VM did not complete. The `…` detail is either `migration failed: <reasons>` (the migration reported a failed state; the reasons are KubeVirt's own condition messages) or a timeout (migration never completed within `vmiTimeout`). Reflected in `status.result.vmLiveMigration`. | Inspect the VirtualMachineInstanceMigration object and virt-handler logs. Common causes: RWX access not available on the disk, node selector/affinity constraints, or insufficient target-node resources. For large memory footprints, consider increasing `vmiTimeout`. |
| VM snapshot failure — one of: `failed waiting for VMSnapshot "<name>" after <d>: …` (timeout, or `snapshot failed: …`); `VMSnapshot "<name>" phase is <X>, expected Succeeded`; `VMSnapshot "<name>" indications … do not equal expected …`; `VMSnapshot "<name>" has no SnapshotVolumes` | Creating or validating the VM snapshot failed, timed out, or produced unexpected content. Reflected in `status.result.vmSnapshot`. | Verify the StorageClass has a working VolumeSnapshotClass and CSI snapshot support. Inspect the VirtualMachineSnapshot and VolumeSnapshot objects and the snapshot controller logs. |
| VM restore failure — one of: `failed waiting for VMRestore "<name>" after <d>: …` (timeout); `VMRestore "<name>" has no status`; `VMRestore "<name>" is not complete`; `VMRestore "<name>" has no volume restores`; `VMRestore "<name>" restored volumes … do not contain all expected …` | Restoring the VM from the snapshot failed, timed out, or did not restore all expected volumes. Reflected in `status.result.vmRestore`. | Inspect the VirtualMachineRestore object and the snapshot/restore controller logs. A restore failure often follows a snapshot problem — check `vmSnapshot` first. |

> **Hard errors (checkup aborts before producing results).** These are not in
> `failureReason` as advisories — they stop the run. Document them as prerequisite failures:
> - `no CDI deployed in cluster` / `expecting single CDI instance in cluster` — CDI (Containerized Data Importer) must be installed, exactly one instance.
> - Invalid config params: `invalid VMI timeout`, `invalid number of VMIs` (must be 1–100), `invalid number of data volumes` (must be 0–10), `invalid skip teardown mode`.

---

## 2. Recommendations (reported, but do **not** fail the checkup)

These are findings the checkup records for the user's attention. The checkup still reports `succeeded: true` if nothing else failed.

| Result field | What it reports | Recommendation |
|---|---|---|
| `status.result.vmsWithNonVirtRbdStorageClass` | Running VMs that use a plain Ceph RBD StorageClass while a virtualization-optimized RBD StorageClass exists on the cluster. | Migrate these VMs to the `*-ceph-rbd-virtualization` StorageClass (configured with `mounter: rbd` and `mapOptions: krbd:rxbounce`) for correct/efficient virt behavior. |
| `status.result.storageProfileMissingVolumeSnapshotClass` | StorageProfiles that use snapshot-based cloning but have no matching VolumeSnapshotClass for their provisioner. | Create a VolumeSnapshotClass for the provisioner so smart (snapshot) cloning works; otherwise CDI falls back to slower host-assisted cloning. |

---

## 3. Skip conditions (expected, not errors)

When a check is skipped, the corresponding `status.result.*` field contains the skip message below. This is normal.

| Skip message | Appears when | Affected checks |
|---|---|---|
| `Skipped - no default storage class` | No default StorageClass and no `storageClass` param. | PVC binding, VM boot, concurrent boot. |
| `Skipped - no golden image PVC or Snapshot` | No usable golden image was found in any scanned namespace. | VM boot from golden image, concurrent boot. |
| `Skipped - no VMI` | The test VM was never created (an earlier step skipped it). | Hotplug, live migration, snapshot, restore. |
| `Skipped - single node` | The cluster has only one node, so live migration cannot be tested. | Live migration. |
| `Skipped - no VM snapshot` | The snapshot step did not succeed, so there is nothing to restore. | Restore. |
| *(live-migration condition message)* | The VM is not migratable (e.g. non-RWX disk). The actual KubeVirt condition message is passed through verbatim. | Live migration. |

---

## Result fields reference

Every `status.result.*` field, as defined by the checkup. Fields can hold a value, a success line, a failure message, or a skip message depending on the run.

| Field | Description |
|---|---|
| `cnvVersion` | OpenShift Virtualization (CNV) version. |
| `ocpVersion` | OpenShift Container Platform version (empty on non-OpenShift clusters). |
| `defaultStorageClass` | The detected default StorageClass name, or a default-related error message. |
| `pvcBound` | Result of the 10Mi test PVC bind. |
| `storageProfilesWithEmptyClaimPropertySets` | StorageProfiles with empty claimPropertySets (unknown provisioners). |
| `storageProfilesWithSpecClaimPropertySets` | StorageProfiles whose claimPropertySets were overridden via spec (informational). |
| `storageProfilesWithSmartClone` | StorageProfiles that support smart clone (CSI or snapshot). |
| `storageProfilesWithRWX` | StorageProfiles that support ReadWriteMany. |
| `storageProfileMissingVolumeSnapshotClass` | StorageProfiles using snapshot clone but missing a VolumeSnapshotClass. |
| `goldenImagesNotUpToDate` | Golden images whose DataImportCron is not up to date or DataSource not ready. |
| `goldenImagesNoDataSource` | Golden images with no PVC/Snapshot source. |
| `vmsWithNonVirtRbdStorageClass` | VMs using plain RBD while a virt RBD StorageClass exists. |
| `vmsWithUnsetEfsStorageClass` | VMs using an EFS StorageClass with unset uid/gid. |
| `vmBootFromGoldenImage` | Result of creating and booting the test VM from a golden image. |
| `vmVolumeClone` | Clone type used (snapshot / csi-clone / host-assisted) and any fallback reason. |
| `vmLiveMigration` | Result of live-migrating the test VM. |
| `vmHotplugVolume` | Result of hotplugging and unplugging a volume. |
| `vmSnapshot` | Result and timing of VM snapshot creation and validation. |
| `vmRestore` | Result and timing of VM restore from snapshot. |
| `concurrentVMBoot` | Result of booting `numOfVMs` VMs concurrently from a golden image. |

---

## Configuration knobs (relevant to troubleshooting)

Set under `spec.param.*` (and `spec.timeout`) in the checkup ConfigMap.

| Param | Default | Notes |
|---|---|---|
| `spec.timeout` | `10m` | Overall checkup timeout. |
| `storageClass` | *(none)* | Force a specific StorageClass instead of the default. |
| `vmiTimeout` | `3m` | Per-VMI operation timeout (boot, migrate, hotplug, snapshot, restore). Increase when provisioning is legitimately slow. |
| `numOfVMs` | `10` | Concurrent VMs for the boot stress check. Valid range 1–100. Lower it to isolate concurrent-boot failures. |
| `numOfDataVolumes` | `0` | Extra data volumes attached to the test VM. Valid range 0–10. |
| `skipTeardown` | `never` | `always`/`true`, `onfailure`, or `never`/`false`. See Debugging tips. |

## Debugging tips

- **Keep resources after a failure:** set `spec.param.skipTeardown: onfailure`. The test VM, DataVolumes, snapshot, and restore are left in the namespace so you can inspect events and logs. Use `always` to always keep them, then clean up manually.
- **Read the Pod logs:** the checkup logs each stage (`checkVersions`, `checkDefaultStorageClass`, `checkVMIBoot`, …) and the exact object names it creates, which map directly to the objects to inspect.
- **Trace a failure to its object:** failure messages include the object name (e.g. `vmi-under-test-xxxxx`). Use that name to inspect the VMI, VirtualMachineInstanceMigration, VirtualMachineSnapshot, or VirtualMachineRestore and its controller logs.
