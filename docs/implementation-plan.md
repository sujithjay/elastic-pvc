# Implementation Plan

## Context

In Spark in standalone mode, we use `amazon-ebs-autoscale` to handle disk spills by automatically expanding EBS storage. As we migrated to AWS EKS, we needed an equivalent capability. `elastic-pvc` is a purpose-built Kubernetes controller that monitors PVC filesystem usage via kubelet stats and automatically expands EBS-backed PVCs.

## Architecture

```
Controller (Deployment, 1 replica)
  │
  ├─ every 60s ─────────────────────────────────┐
  │                                              │
  ▼                                              ▼
  List StorageClasses                   Query kubelet /stats/summary
  (where elastic-pvc.io/enabled=true)   (per node, in parallel)
  │                                              │
  ▼                                              │
  List PVCs per StorageClass ◄───────────────────┘
  (where elastic-pvc.io/storage-limit set, phase=Bound)
  │
  ▼
  For each PVC:
    if availableBytes < threshold:
      newSize = currentCap + increase (rounded up to GiB, capped at limit)
      PATCH PVC spec.resources.requests.storage = newSize
      │
      ▼
      EBS CSI Driver: ec2:ModifyVolume + online fs resize
```

## Key Design Decisions

1. **Kubelet /stats/summary over /metrics** -- simpler JSON parsing. This might NOT be suitable for large clusters because of the additional load on the API server. Future development may add support for scraping kubelet /metrics endpoint as an alternative.

2. **Ticker-based loop over watch/reconcile** -- we need to periodically poll kubelet stats which are not Kubernetes events. A simple ticker in a `manager.Runnable` integrates cleanly with controller-runtime.

3. **Annotations over CRD** -- annotations are simpler and don't require CRD installation.

4. **GiB rounding** -- EBS volumes are sized in whole GiB. New sizes are rounded up to avoid sub-GiB requests that EBS would reject.

5. **In-progress detection via prev-capacity annotation** -- before patching a PVC, we record the current kubelet-reported capacity. On the next loop, if kubelet still reports the old capacity, we skip (resize still in flight).

## Dependencies

- `sigs.k8s.io/controller-runtime` -- manager, leader election, health probes
- `k8s.io/client-go` -- Kubernetes API client
- `github.com/spf13/cobra` -- CLI
- `golang.org/x/sync/errgroup` -- concurrent node queries

## Roadmap
See [Roadmap Ideas](roadmap/ideas.md)