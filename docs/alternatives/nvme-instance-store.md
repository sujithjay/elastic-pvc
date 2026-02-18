# NVMe Instance Store Alternative

For clusters where NVMe-equipped instance types are cost-effective, using instance store volumes for Spark shuffle/spill storage offers better I/O performance than EBS.

## When to Use

- Your workloads need very high I/O throughput (local NVMe >> EBS gp3)
- The maximum spill size per node is predictable and fits within instance store capacity
- You are already using storage-optimized instances (r6id, c6id, i3, etc.)

## When NOT to Use

- Cluster size makes NVMe instances too expensive
- Spill size is unpredictable and may exceed instance store capacity
- You need storage that survives node replacement (instance store is ephemeral)

## Setup with Karpenter

### EC2NodeClass

```yaml
apiVersion: karpenter.k8s.aws/v1beta1
kind: EC2NodeClass
metadata:
  name: spark-nvme
spec:
  instanceStorePolicy: RAID0
  # ... other config
```

`instanceStorePolicy: RAID0` tells Karpenter to format all NVMe disks into a single RAID0 array and mount it.

### NodePool

```yaml
apiVersion: karpenter.sh/v1beta1
kind: NodePool
metadata:
  name: spark-executors
spec:
  template:
    spec:
      requirements:
        - key: karpenter.k8s.aws/instance-family
          operator: In
          values: ["r6id", "c6id", "i3"]
      nodeClassRef:
        name: spark-nvme
```

### Spark Configuration

Use `emptyDir` volumes. When kubelet's pod directory is on NVMe (via `instanceStorePolicy`), all `emptyDir` data goes to NVMe automatically:

```properties
spark.kubernetes.executor.volumes.emptyDir.spark-local.mount.path=/tmp/spark
spark.local.dir=/tmp/spark
```

No additional storage configuration needed.

## Hybrid Approach

Combine NVMe (fast, fixed) with elastic-pvc (slower, elastic) using multiple Spark local dirs:

```properties
spark.local.dir=/tmp/spark-nvme,/tmp/spark-ebs
```

- `/tmp/spark-nvme` -- `emptyDir` on NVMe instance store
- `/tmp/spark-ebs` -- EBS PVC managed by elastic-pvc

Spark round-robins writes across local dirs. NVMe handles the bulk; EBS PVC provides overflow capacity that auto-expands.

## Trade-offs

| Aspect | NVMe Instance Store | EBS via elastic-pvc |
|---|---|---|
| I/O performance | Very high (local NVMe) | Moderate (network EBS gp3) |
| Capacity | Fixed per instance type | Elastic, auto-expanding |
| Cost model | Included in instance price | Per-GB EBS pricing |
| Data durability | Ephemeral (lost on stop/terminate) | Persistent until PVC deleted |
| Configuration | Instance type selection | Annotations on PVC |
