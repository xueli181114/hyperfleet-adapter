# Executor Package

The `executor` package is the core event processing engine for the HyperFleet Adapter. It orchestrates the execution of CloudEvents according to the adapter configuration, coordinating parameter extraction, precondition evaluation, Kubernetes resource management, and post-action execution.

## Overview

The executor implements a four-phase execution pipeline:

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Event Processing Pipeline                     │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  CloudEvent ──► Phase 1 ──► Phase 2 ──► Phase 3 ──► Phase 4 ──► Done │
│                Extract    Precond.   Resources   Post-Act.           │
│                Params     Eval.      Create      Execute             │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

## Components

### Main Components

| Component | File | Description |
|-----------|------|-------------|
| `Executor` | `executor.go` | Main orchestrator that coordinates all phases |
| `ParamExtractor` | `param_extractor.go` | Extracts parameters from events and environment |
| `PreconditionExecutor` | `precondition_executor.go` | Evaluates preconditions with API calls and CEL |
| `ResourceExecutor` | `resource_executor.go` | Creates/updates Kubernetes resources |
| `PostActionExecutor` | `post_action_executor.go` | Executes post-processing actions |

### Type Definitions

| Type | Description |
|------|-------------|
| `ExecutionResult` | Contains the result of processing an event |
| `PreconditionResult` | Result of a single precondition evaluation |
| `ResourceResult` | Result of a single resource operation |
| `PostActionResult` | Result of a single post-action execution |
| `ExecutionContext` | Runtime context during execution |

## Usage

### Basic Usage

```go
import (
    "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
)

// Create executor using builder
exec, err := executor.NewBuilder().
    WithAdapterConfig(adapterConfig).
    WithAPIClient(apiClient).
    WithK8sClient(k8sClient).
    WithLogger(log).
    Build()
if err != nil {
    return err
}

// Create handler for broker subscription
handler := exec.CreateHandler()

// Or execute directly
result := exec.Execute(ctx, cloudEvent)
if result.Status == executor.StatusFailed {
    log.Errorf("Execution failed: %v", result.Error)
}
```

### Dry Run Mode

Enable dry run mode to test execution without actually creating Kubernetes resources:

```go
exec, err := executor.NewBuilder().
    WithAdapterConfig(adapterConfig).
    WithAPIClient(apiClient).
    WithLogger(log).
    WithDryRun(true).
    Build()
```

## Execution Phases

### Phase 1: Parameter Extraction

Extracts parameters from various sources:

- **Environment Variables**: `source: "env.VARIABLE_NAME"`
- **Event Data**: `source: "event.field.path"`
- **Secrets** (planned): `source: "secret.name.key"`
- **ConfigMaps** (planned): `source: "configmap.name.key"`

```yaml
params:
  - name: "clusterId"
    source: "event.cluster_id"
    type: "string"
    required: true
  - name: "apiToken"
    source: "env.API_TOKEN"
    required: true
```

### Phase 2: Precondition Evaluation

Executes preconditions with optional API calls and condition evaluation:

```yaml
preconditions:
  - name: "checkClusterStatus"
    apiCall:
      method: "GET"
      url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}"
    storeResponseAs: "clusterDetails"
    extract:
      - as: "clusterPhase"
        field: "status.phase"
    conditions:
      - field: "clusterPhase"
        operator: "in"
        value: ["Ready", "Provisioning"]
```

#### Supported Condition Operators

| Operator | Description |
|----------|-------------|
| `equals` | Exact equality |
| `notEquals` | Not equal |
| `in` | Value in list |
| `notIn` | Value not in list |
| `contains` | String/array contains |
| `greaterThan` | Numeric comparison |
| `lessThan` | Numeric comparison |
| `exists` | Field exists and is not empty |

#### CEL Expressions

For complex conditions, use CEL expressions:

```yaml
preconditions:
  - name: "complexCheck"
    expression: |
      clusterPhase == "Ready" && nodeCount >= 3
```

### Phase 3: Resource Management

Creates or updates Kubernetes resources from manifests:

```yaml
resources:
  - name: "clusterNamespace"
    manifest:
      apiVersion: v1
      kind: Namespace
      metadata:
        name: "cluster-{{ .clusterId }}"
    discovery:
      byName: "cluster-{{ .clusterId }}"
  
  - name: "externalTemplate"
    manifest:
      ref: "templates/deployment.yaml"
    discovery:
      namespace: "cluster-{{ .clusterId }}"
      bySelectors:
        labelSelector:
          app: "myapp"
```

#### Resource Operations

| Operation | When | Description |
|-----------|------|-------------|
| `create` | Resource doesn't exist | Creates new resource |
| `update` | Resource exists | Updates existing resource |
| `recreate` | `recreateOnChange: true` | Deletes and recreates |
| `skip` | No changes needed | No operation performed |
| `dry_run` | Dry run mode | Simulated operation |

### Phase 4: Post-Actions

Executes post-processing actions like status reporting:

```yaml
post:
  params:
    - name: "statusPayload"
      build:
        status:
          expression: |
            resources.clusterController.status.readyReplicas > 0
        message:
          value: "Deployment successful"
  
  postActions:
    - name: "reportStatus"
      apiCall:
        method: "POST"
        url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}/status"
        body: "{{ .statusPayload }}"
```

## Execution Results

### ExecutionResult

```go
type ExecutionResult struct {
    EventID             string
    Status              ExecutionStatus  // success, failed, skipped
    Phase               ExecutionPhase   // where execution ended
    Duration            time.Duration
    Params              map[string]interface{}
    PreconditionResults []PreconditionResult
    ResourceResults     []ResourceResult
    PostActionResults   []PostActionResult
    Error               error
    ErrorReason         string
}
```

### Status Values

| Status | Description |
|--------|-------------|
| `success` | All phases completed successfully |
| `failed` | Execution failed with an error |
| `skipped` | Preconditions not met (soft failure) |

## Error Handling

### Precondition Not Met

When preconditions are not satisfied, the executor:
1. Sets status to `skipped`
2. Skips resource creation phase
3. Still executes post-actions (for error reporting)

### Execution Errors

Errors are captured in `ExecutionResult` with:
- `Error`: The actual error
- `ErrorReason`: Human-readable reason
- `Phase`: Phase where error occurred

### Error Recovery

Post-actions always execute (even on failure) to allow error reporting:

```yaml
post:
  params:
    - name: "errorPayload"
      build:
        status:
          expression: |
            adapter.executionStatus == "success"
        errorReason:
          expression: |
            adapter.errorReason
  postActions:
    - name: "reportError"
      apiCall:
        method: "POST"
        url: "{{ .apiBaseUrl }}/clusters/{{ .clusterId }}/status"
        body: "{{ .errorPayload }}"
```

## Template Rendering

All string values in the configuration support Go templates:

```yaml
url: "{{ .apiBaseUrl }}/api/{{ .apiVersion }}/clusters/{{ .clusterId }}"
```

### Available Template Variables

| Source | Example |
|--------|---------|
| Extracted params | `{{ .clusterId }}` |
| API responses | `{{ .clusterDetails.status.phase }}` |
| Adapter metadata | `{{ .metadata.name }}` |
| Event metadata | `{{ .eventMetadata.id }}` |

## Integration

### With Broker Consumer

```go
// Create executor
exec, _ := executor.NewBuilder().
    WithAdapterConfig(config).
    WithAPIClient(apiClient).
    WithK8sClient(k8sClient).
    WithLogger(log).
    Build()

// Subscribe with executor handler
broker_consumer.Subscribe(ctx, subscriber, topic, exec.CreateHandler())
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `DRY_RUN=true` | Enable dry run mode |
| `KUBECONFIG` | Path to kubeconfig (for local dev) |

## Testing

The executor can be tested with dry run mode and mock clients:

```go
// Create mock API client
mockClient := &MockAPIClient{...}

// Create executor in dry run mode
exec, _ := executor.NewBuilder().
    WithAdapterConfig(config).
    WithAPIClient(mockClient).
    WithLogger(testLogger).
    WithDryRun(true).
    Build()

// Execute test event
result := exec.Execute(ctx, testEvent)
assert.Equal(t, executor.StatusSuccess, result.Status)
```

