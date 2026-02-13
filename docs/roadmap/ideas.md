## Future Improvements

### Tier 1: Quick Wins
#### 1.1 Node Filtering - Query Only Nodes with Target PVCs
Current: Query ALL nodes for kubelet stats.

Proposed: Build node set from mounted PVCs, query only those nodes.

Benefit: Could reduce queries, and thus load on API server.

#### 1.2 Concurrent PVC Patching
Current: Sequential pvc.Update() calls.

Proposed: Use worker pool (10-20 concurrent workers) for PVC patches.

Benefit: 10x faster reconciliation for large PVC counts.

#### 1.3 Beyond EBS
Current: Support for EBS only

Proposed: Since the controller depends on CSI, it can support more storage-classes beyond EBS.

### Tier 2: Medium-Term Improvements
#### 2.1 Adaptive Polling Interval
Current: Fixed 1-minute interval

Proposed: Configurable per-StorageClass or adaptive based on cluster size

#### 2.2 Kubelet Stats Caching
Current: Fresh stats every cycle, no caching

Proposed: In-memory caching with TTL. Only refresh nodes where PVCs are close to threshold.

Benefit: Reduce redundant kubelet queries for stable PVCs

#### 2.3 Prometheus Backend
Current: Direct kubelet /stats/summary queries.

Proposed: Option to use Prometheus for volume stats.

Trade-off: Adds a dependency, but solves the need to query O(n nodes) per cycle and also reduces API server load significantly.

#### 2.4 Watch-based PVC Discovery
Current: List all PVCs every cycle

Proposed: Use informer/watch for PVC changes, maintain in-memory state. Only process PVCs that changed or need periodic check.

Benefit: Could reduce queries, and thus load on API server.

#### 2.5 Leader Election for HA
Current: Single replica

Proposed: Multiple replicas with leader election.

Benefit: No downtime during pod restarts/upgrades.

#### 2.6 Mutating Webhook for Auto-Annotation
Current: Manual annotation

Proposed: Automatic annotation on PVC creation

### Tier 3: Spark-Specific Optimizations
#### 3.1 Executor Lifecycle Awareness
Spark executors are ephemeral. Ideas:
- Detect executor pod termination, skip resize for dying PVCs
- Use finalizers to ensure resize completes before PVC deletion
- Integration with Spark Operator annotations for smarter decisions

#### 3.2 Proactive Resizing Based on Shuffle Metrics
Current: React when threshold crossed

Proposed: Integrate with Spark metrics (shuffle spill, disk usage rate). Predict resize needs before threshold hit.

#### 3.3 Per-Application Resize Policies
Current: Single threshold/increase per PVC annotation

Proposed: Support application-level policies (e.g., per SparkApplication), StorageClass or namespace-level defaults

#### 3.4 Burst Mode for Concurrent Resizes
Current: Shuffle in Spark can multiple executors to hit threshold simultaneously.

Proposed: Detect "burst" patterns (>N PVCs need resize in one cycle), and prioritize by urgency (available bytes remaining). We should rate-limit to avoid storage-class API throttling.
