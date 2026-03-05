# HyperFleet Adapter Metrics — Alerting and Monitoring

This document provides recommended alerting rules and monitoring queries for the hyperfleet-adapter.

For the canonical list of all metrics, labels, and descriptions, see [observability.md](observability.md). Metrics are served on port **9090** at `/metrics`.

---

## Health Endpoints

Health checks are served on port **8080** (separate from metrics).

| Endpoint | Purpose | Healthy Response |
|----------|---------|-----------------|
| `/healthz` | Liveness probe | Always `200 OK` |
| `/readyz` | Readiness probe | `200 OK` when config is loaded and broker is connected |

Readiness returns `503 Service Unavailable` when:
- The adapter is shutting down
- Config has not been loaded yet (`config` check)
- Broker is not connected (`broker` check)

---

## Recommended Alerts

### Adapter Down

```yaml
alert: HyperFleetAdapterDown
expr: hyperfleet_adapter_up == 0
for: 1m
labels:
  severity: critical
annotations:
  summary: "HyperFleet Adapter is down"
  description: "Adapter {{ $labels.component }} has been down for more than 1 minute."
```

### High Event Failure Rate

```yaml
alert: HyperFleetAdapterHighFailureRate
expr: |
  sum by (component, version) (rate(hyperfleet_adapter_events_processed_total{status="failed"}[5m]))
  /
  sum by (component, version) (rate(hyperfleet_adapter_events_processed_total[5m]))
  > 0.1
for: 5m
labels:
  severity: warning
annotations:
  summary: "High event failure rate"
  description: "More than 10% of events are failing for {{ $labels.component }}."
```

### No Events Processed (Dead Man's Switch)

```yaml
alert: HyperFleetAdapterNoEventsProcessed
expr: |
  sum by (component, version) (rate(hyperfleet_adapter_events_processed_total[15m])) == 0
  and on(component, version)
  hyperfleet_adapter_up == 1
for: 15m
labels:
  severity: warning
annotations:
  summary: "No events processed"
  description: "Adapter {{ $labels.component }} has not processed any events in 15 minutes."
```

### Slow Event Processing

```yaml
alert: HyperFleetAdapterSlowProcessing
expr: |
  histogram_quantile(0.95,
    rate(hyperfleet_adapter_event_processing_duration_seconds_bucket[5m])
  ) > 60
for: 5m
labels:
  severity: warning
annotations:
  summary: "Slow event processing"
  description: "P95 event processing time exceeds 60 seconds for {{ $labels.component }}."
```

### Broker Errors

```yaml
alert: HyperFleetBrokerErrors
expr: rate(hyperfleet_broker_errors_total[5m]) > 0
for: 5m
labels:
  severity: warning
annotations:
  summary: "Broker errors detected"
  description: "Broker errors occurring for {{ $labels.component }}: {{ $labels.error_type }}."
```

### Rising Error Count by Type

```yaml
alert: HyperFleetAdapterErrorsRising
expr: rate(hyperfleet_adapter_errors_total[5m]) > 0.5
for: 5m
labels:
  severity: warning
annotations:
  summary: "Adapter errors rising"
  description: "Error rate for {{ $labels.error_type }} exceeds 0.5/s on {{ $labels.component }}."
```

---

## Example PromQL Queries

### Event throughput

```promql
rate(hyperfleet_adapter_events_processed_total[5m])
```

### Event success rate (percentage)

```promql
sum by (component, version) (rate(hyperfleet_adapter_events_processed_total{status="success"}[5m]))
/
sum by (component, version) (rate(hyperfleet_adapter_events_processed_total[5m]))
* 100
```

### P50 / P95 / P99 event processing latency

```promql
histogram_quantile(0.50, rate(hyperfleet_adapter_event_processing_duration_seconds_bucket[5m]))
histogram_quantile(0.95, rate(hyperfleet_adapter_event_processing_duration_seconds_bucket[5m]))
histogram_quantile(0.99, rate(hyperfleet_adapter_event_processing_duration_seconds_bucket[5m]))
```

### Errors by type

```promql
sum by (error_type) (rate(hyperfleet_adapter_errors_total[5m]))
```

### Broker message consumption rate

```promql
rate(hyperfleet_broker_messages_consumed_total[5m])
```

### Currently running adapter version

```promql
hyperfleet_adapter_build_info
```
