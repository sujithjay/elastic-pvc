# elastic-pvc

elastic-pvc is a lean Kubernetes controller that expands PersistentVolumeClaims when filesystem usage crosses a threshold.

The primary use-case this project targets are Spark on EKS deployments, where we use `elastic-pvc` to handle large disk spills on Spark executors.

## How It Works

elastic-pvc polls kubelet stats on every node and checks PVC filesystem usage. When free space drops below a configurable threshold, it patches the PVC to request more storage. The AWS EBS CSI driver handles the actual volume resize (`ec2:ModifyVolume`) and online filesystem expansion.

No Prometheus or external metrics system required.

See [Architecture](docs/architecture.md) for more details.

## Quick Start

### Prerequisites

- EKS cluster with the [AWS EBS CSI Driver](https://docs.aws.amazon.com/eks/latest/userguide/ebs-csi.html) installed
- A StorageClass with `allowVolumeExpansion: true`

### Install with Helm

```bash
helm install elastic-pvc deploy/helm/elastic-pvc/ \
  --namespace elastic-pvc --create-namespace \
  --set storageClass.enabled=true
```

### Annotate Your PVCs

Add these annotations to PVCs you want elastic-pvc to manage:

```yaml
metadata:
  annotations:
    elastic-pvc.io/storage-limit: "500Gi"   # max size (required)
    elastic-pvc.io/threshold: "20%"          # expand when free space < 20% (optional, default: 20%)
    elastic-pvc.io/increase: "50%"           # grow by 50% each time (optional, default: 50%)
```

### Annotate Your StorageClass

The StorageClass must opt in:

```yaml
metadata:
  annotations:
    elastic-pvc.io/enabled: "true"
```

## Configuration

### PVC Annotations

| Annotation | Required | Default | Description |
|---|---|---|---|
| `elastic-pvc.io/storage-limit` | Yes | - | Maximum size the PVC can grow to (e.g., `500Gi`) |
| `elastic-pvc.io/threshold` | No | `20%` | Free-space threshold that triggers expansion. Percentage or absolute (e.g., `10Gi`) |
| `elastic-pvc.io/increase` | No | `50%` | How much to grow each time. Percentage of current capacity or absolute |
| `elastic-pvc.io/cooldown` | No | `5m` | Minimum interval between resizes for this PVC (e.g., `10m`, `1h`). Overrides global `--resize-cooldown` |

### Controller Flags

| Flag | Default | Description |
|---|---|---|
| `--interval` | `1m` | How often to check PVC usage |
| `--max-resizes-per-cycle` | `10` | Maximum resize operations per reconciliation cycle |
| `--resize-cooldown` | `5m` | Minimum interval between resizes for the same PVC |
| `--metrics-addr` | `:8080` | Prometheus metrics endpoint |
| `--health-addr` | `:8081` | Health/readiness probe endpoint |

### Rate Limiting

elastic-pvc includes rate limiting to prevent EBS API exhaustion during burst scenarios:

1. **Per-cycle limit**: Only `--max-resizes-per-cycle` PVCs are resized per reconciliation cycle. PVCs with the lowest available space are prioritized.

2. **Per-PVC cooldown**: After a resize, each PVC enters a cooldown period (default: 5 minutes) before it can be resized again. This can be overridden per-PVC with the `elastic-pvc.io/cooldown` annotation.

Rate limiting metrics are exposed at `/metrics`:
- `elastic_pvc_rate_limited_total{reason="cooldown"}` - resizes skipped due to cooldown
- `elastic_pvc_rate_limited_total{reason="per_cycle_limit"}` - resizes deferred due to per-cycle limit
- `elastic_pvc_resizes_total` - successful resize operations

## Example: Spark on Kubernetes

```properties
spark.kubernetes.executor.volumes.persistentVolumeClaim.spark-local.options.claimName=OnDemand
spark.kubernetes.executor.volumes.persistentVolumeClaim.spark-local.options.storageClass=spark-local-ebs
spark.kubernetes.executor.volumes.persistentVolumeClaim.spark-local.options.sizeLimit=150Gi
spark.kubernetes.executor.volumes.persistentVolumeClaim.spark-local.mount.path=/tmp/spark
spark.kubernetes.executor.volumes.persistentVolumeClaim.spark-local.mount.readOnly=false
spark.local.dir=/tmp/spark
```

Each executor gets a fresh EBS-backed PVC. elastic-pvc watches them and grows as Spark spills data to disk. When the executor terminates, `reclaimPolicy: Delete` cleans up the EBS volume.

## Limitations

### EBS Volume Modification Cooldown

AWS allows [up to four Elastic Volume modifications per volume in a rolling 24-hour window](https://aws.amazon.com/about-aws/whats-new/2026/01/amazon-ebs-up-to-four-volume-modifications/). Once a volume hits this limit, further resize attempts will fail until the window rolls over. The `--resize-cooldown` flag (and its per-PVC override `elastic-pvc.io/cooldown`) helps mitigate this by spacing out resize operations on the same volume, reducing the chance of exhausting the four-modification quota. However, if the threshold and increase values are set too aggressively, a single PVC can still hit the AWS limit within 24 hours.

### ModifyVolume API Rate Limits

The EC2 `ModifyVolume` API is subject to standard AWS API rate limits. In clusters with many PVCs resizing concurrently, API throttling can cause resize requests to fail or be delayed. The `--max-resizes-per-cycle` flag bounds concurrent modifications per reconciliation cycle, but operators should monitor for throttling errors in clusters with hundreds of managed PVCs.

### Cost Implications

Automatic storage expansion means EBS costs can grow without manual approval. In large clusters with many short-lived PVCs (e.g., Spark executors), rapid expansion across hundreds of volumes can lead to significant cost increases. The `elastic-pvc.io/storage-limit` annotation caps the maximum size each PVC can reach, providing an upper bound on per-volume cost. Set this annotation thoughtfully — it is the primary safeguard against unbounded storage growth.

## Alternatives

elastic-pvc is not the only way to handle storage for spill-heavy workloads. Depending on your instance types and workload patterns, these alternatives may be a better fit.

### NVMe Instance Store

For clusters already using storage-optimized instances (r6id, c6id, i3, etc.), local NVMe volumes offer significantly higher I/O throughput than EBS. Karpenter's `instanceStorePolicy: RAID0` makes setup straightforward. The trade-off is fixed capacity — if spill size is unpredictable, NVMe alone may not be enough. A hybrid approach combining NVMe with elastic-pvc is also possible.

See [NVMe Instance Store Alternative](docs/alternatives/nvme-instance-store.md) for setup details and trade-offs.

### Dynamic EBS Root Volume Sizing

A [Karpenter blueprint for dynamic EBS volume sizing](https://github.com/aws-samples/karpenter-blueprints/pull/34) uses a DaemonSet to detect instance type and resize root EBS volumes accordingly. This approach sizes volumes at node launch rather than reacting to usage at runtime. It is useful when different instance types need different root volume sizes, but does not handle PVC-level expansion for per-pod storage.

## Development

```bash
make build        # build binary
make test         # run tests
make lint         # fmt + vet
make docker-build # build container image
```
