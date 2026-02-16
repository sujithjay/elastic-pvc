# ADR: Observability Metrics

**Date:** 2026-02-16
**Status:** Accepted

## Context

elastic-pvc is a Kubernetes controller that automatically expands PVCs based on filesystem usage. As the controller operates autonomously, operators need visibility into its behavior: how many resizes occurred, whether rate limiting is active, and if node queries are failing.

The controller already depends on `controller-runtime`, which includes a built-in metrics server using `prometheus/client_golang`. This provides a `/metrics` endpoint out of the box.

## Decision

Expose Prometheus metrics for controller observability by registering custom metrics with controller-runtime's metrics registry.

### Current Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `elastic_pvc_resizes_total` | Counter | Total successful PVC resize operations |
| `elastic_pvc_rate_limited_total{reason}` | Counter | PVC resizes skipped due to rate limiting (reason: `cooldown` or `per_cycle_limit`) |
| `elastic_pvc_node_query_failures_total` | Counter | Total kubelet stats query failures |

### Future Metrics (see GitHub issues)

- PVC usage ratio gauge
- Resize latency histogram
- PVCs at storage limit gauge

## Consequences

**Positive:**
- Operators can monitor controller health and behavior via existing Prometheus/Grafana infrastructure
- No additional dependencies (prometheus/client_golang is already a transitive dependency of controller-runtime)
- Enables alerting on failures and rate limiting

**Negative:**
- Slightly increases code complexity
- Metrics must be maintained as the codebase evolves

## Alternatives Considered

1. **Logs only** - Rejected; logs are harder to aggregate and alert on at scale
2. **Custom metrics endpoint** - Rejected; reinventing what Prometheus already does well
3. **OpenTelemetry** - Deferred; Prometheus is simpler and sufficient for current needs
