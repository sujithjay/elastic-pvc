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

### Controller Flags

| Flag | Default | Description |
|---|---|---|
| `--interval` | `1m` | How often to check PVC usage |
| `--metrics-addr` | `:8080` | Prometheus metrics endpoint |
| `--health-addr` | `:8081` | Health/readiness probe endpoint |

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

## Development

```bash
make build        # build binary
make test         # run tests
make lint         # fmt + vet
make docker-build # build container image
```
