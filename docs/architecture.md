# Architecture

## Overview

elastic-pvc is a Kubernetes controller that runs as a single Deployment. It periodically:

1. Discovers which PVCs to manage (via StorageClass and PVC annotations)
2. Collects filesystem usage from kubelet on every node
3. Expands PVCs that are running low on space

The actual volume expansion is handled by the AWS EBS CSI driver, which calls `ec2:ModifyVolume` and performs an online filesystem resize.

## Components

### cmd/main.go

Entry point. Sets up a `controller-runtime` Manager with:
- Health/readiness probes
- Metrics endpoint
- The Autoscaler as a Runnable

### internal/controller/reconciler.go

The core loop. Implements `manager.Runnable` with a ticker:
- Lists StorageClasses with `elastic-pvc.io/enabled: "true"` (via field index)
- Lists PVCs in those StorageClasses (via field index)
- Filters to target PVCs (bound, filesystem mode, has storage-limit annotation)
- Compares kubelet-reported available bytes against the threshold
- Patches PVC `spec.resources.requests.storage` to trigger expansion

### internal/controller/indexer.go

Field indexers for efficient lookups:
- StorageClass indexed by `elastic-pvc.io/enabled` annotation value
- PVC indexed by `spec.storageClassName`

### internal/kubelet/stats_client.go

Queries each node's kubelet `/stats/summary` endpoint via the Kubernetes API proxy:
```
GET /api/v1/nodes/{node}/proxy/stats/summary
```

Nodes are queried in parallel using `errgroup`. For each pod volume with a `pvcRef`, extracts `availableBytes` and `capacityBytes`.

### internal/resize/calculator.go

Pure functions for:
- Parsing percentage or absolute values (e.g., "20%" or "10Gi")
- Calculating new PVC size with GiB rounding and limit capping

## Data Flow

```
kubelet /stats/summary (per node)
        │
        ▼
  VolumeStats map[PVC] → {availableBytes, capacityBytes}
        │
        ▼
  For each managed PVC:
    threshold = parse(annotation or default "20%", capacityBytes)
    if availableBytes < threshold:
      increase = parse(annotation or default "50%", currentCapacity)
      newSize = ceil_gib(currentCapacity + increase)
      newSize = min(newSize, storageLimit)
      PATCH PVC
```

## RBAC

The controller needs:
- `get/list/watch/update/patch` on PVCs
- `get/list/watch` on StorageClasses
- `get/list` on Nodes
- `get` on Nodes/proxy (for kubelet stats)
- `create/patch` on Events

## Annotation Contract

| Annotation | On | Description |
|---|---|---|
| `elastic-pvc.io/enabled` | StorageClass | Opt-in for the StorageClass |
| `elastic-pvc.io/storage-limit` | PVC | Max size (required to be managed) |
| `elastic-pvc.io/threshold` | PVC | Free-space trigger (default: 20%) |
| `elastic-pvc.io/increase` | PVC | Growth amount (default: 50%) |
| `elastic-pvc.io/prev-capacity` | PVC | Internal: tracks in-progress resize |
