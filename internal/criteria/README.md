# Criteria

The `criteria` package provides a flexible evaluation engine for evaluating conditions and criteria against dynamic data. It supports multiple operators and nested field access, making it suitable for complex condition evaluation in adapter configurations.

## Overview

This package is used to evaluate preconditions, post-conditions, and other criteria defined in the adapter configuration. It provides a type-safe, expression-based evaluation system with support for various comparison operators.

## Features

- **Multiple Operators**: equals, notEquals, in, notIn, contains, greaterThan, lessThan, exists
- **Nested Field Access**: Evaluate deeply nested fields using dot notation (e.g., `status.phase`)
- **Type Flexibility**: Handles strings, numbers, arrays, maps, and complex nested structures
- **Context Management**: Maintain evaluation context with variable storage and retrieval
- **Error Handling**: Descriptive error messages for debugging

## Supported Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `equals` | Field equals value | `clusterPhase == "Ready"` |
| `notEquals` | Field does not equal value | `status != "Failed"` |
| `in` | Field is in a list of values | `provider in ["aws", "gcp", "azure"]` |
| `notIn` | Field is not in a list of values | `phase notIn ["Terminating", "Failed"]` |
| `contains` | String/array contains value | `"hello world" contains "world"` |
| `greaterThan` | Numeric field is greater than value | `nodeCount > 3` |
| `lessThan` | Numeric field is less than value | `replicas < 10` |
| `exists` | Field exists and is not empty | `vpcId exists` |

## Usage

### Basic Evaluation

```go
import "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"

// Create evaluation context
ctx := criteria.NewEvaluationContext()
ctx.Set("clusterPhase", "Ready")
ctx.Set("provider", "aws")
ctx.Set("nodeCount", 5)

// Create evaluator
evaluator := criteria.NewEvaluator(ctx)

// Evaluate a single condition
result, err := evaluator.EvaluateCondition(
    "clusterPhase",
    criteria.OperatorEquals,
    "Ready",
)
if err != nil {
    log.Fatal(err)
}
fmt.Println("Cluster is ready:", result) // true
```

### Evaluating Multiple Conditions

```go
// Multiple conditions (AND logic)
// Use typed Operator constants for compile-time safety
conditions := []criteria.ConditionDef{
    {Field: "clusterPhase", Operator: criteria.OperatorIn, Value: []interface{}{"Provisioning", "Ready"}},
    {Field: "provider", Operator: criteria.OperatorIn, Value: []interface{}{"aws", "gcp", "azure"}},
    {Field: "nodeCount", Operator: criteria.OperatorGreaterThan, Value: 1},
}

result, err := evaluator.EvaluateConditions(conditions)
if err != nil {
    log.Fatal(err)
}
fmt.Println("All conditions pass:", result)
```

### Nested Field Access

```go
// Set nested data
ctx.Set("cluster", map[string]interface{}{
    "status": map[string]interface{}{
        "phase": "Ready",
        "conditions": []interface{}{
            map[string]interface{}{
                "type":   "Available",
                "status": "True",
            },
        },
    },
})

// Evaluate nested field
result, err := evaluator.EvaluateCondition(
    "cluster.status.phase",
    criteria.OperatorEquals,
    "Ready",
)
```

### Context Management

```go
// Create context
ctx := criteria.NewEvaluationContext()

// Set values
ctx.Set("key", "value")

// Get values
val, ok := ctx.Get("key")
if ok {
    fmt.Println("Value:", val)
}

// Get nested field
val, err := ctx.GetNestedField("cluster.status.phase")

// Merge contexts
ctx2 := criteria.NewEvaluationContext()
ctx2.Set("newKey", "newValue")
ctx.Merge(ctx2) // ctx now has both key and newKey
```

## Examples

### Example 1: Cluster Validation

```go
// Simulate cluster details from API
ctx := criteria.NewEvaluationContext()
ctx.Set("clusterPhase", "Ready")
ctx.Set("cloudProvider", "aws")
ctx.Set("vpcId", "vpc-12345")

evaluator := criteria.NewEvaluator(ctx)

// Validate cluster is in correct phase
phaseValid, _ := evaluator.EvaluateCondition(
    "clusterPhase",
    criteria.OperatorIn,
    []interface{}{"Provisioning", "Installing", "Ready"},
)

// Validate provider is allowed
providerValid, _ := evaluator.EvaluateCondition(
    "cloudProvider",
    criteria.OperatorIn,
    []interface{}{"aws", "gcp", "azure"},
)

// Validate VPC exists
vpcExists, _ := evaluator.EvaluateCondition(
    "vpcId",
    criteria.OperatorExists,
    nil,
)

if phaseValid && providerValid && vpcExists {
    fmt.Println("Cluster validation passed")
}
```

### Example 2: Resource Status Check

```go
// Simulate resource status
ctx := criteria.NewEvaluationContext()
ctx.Set("resources", map[string]interface{}{
    "clusterNamespace": map[string]interface{}{
        "status": map[string]interface{}{
            "phase": "Active",
        },
    },
    "clusterController": map[string]interface{}{
        "status": map[string]interface{}{
            "replicas":      3,
            "readyReplicas": 3,
        },
    },
})

evaluator := criteria.NewEvaluator(ctx)

// Check namespace is active
nsActive, _ := evaluator.EvaluateCondition(
    "resources.clusterNamespace.status.phase",
    criteria.OperatorEquals,
    "Active",
)

// Check all replicas are ready
allReady, _ := evaluator.EvaluateCondition(
    "resources.clusterController.status.readyReplicas",
    criteria.OperatorGreaterThan,
    0,
)

if nsActive && allReady {
    fmt.Println("Resources are healthy")
}
```

### Example 3: Array and String Contains

```go
ctx := criteria.NewEvaluationContext()
evaluator := criteria.NewEvaluator(ctx)

// String contains
ctx.Set("message", "Deployment ready and healthy")
result, _ := evaluator.EvaluateCondition(
    "message",
    criteria.OperatorContains,
    "ready",
)
fmt.Println("Message contains 'ready':", result) // true

// Array contains
ctx.Set("tags", []interface{}{"production", "us-east-1", "critical"})
result, _ = evaluator.EvaluateCondition(
    "tags",
    criteria.OperatorContains,
    "production",
)
fmt.Println("Tags contain 'production':", result) // true
```

### Example 4: Numeric Comparisons

```go
ctx := criteria.NewEvaluationContext()
ctx.Set("nodeCount", 5)
ctx.Set("minNodes", 1)
ctx.Set("maxNodes", 10)

evaluator := criteria.NewEvaluator(ctx)

// Check if within range
aboveMin, _ := evaluator.EvaluateCondition(
    "nodeCount",
    criteria.OperatorGreaterThan,
    0, // nodeCount > 0 means >= 1
)

belowMax, _ := evaluator.EvaluateCondition(
    "nodeCount",
    criteria.OperatorLessThan,
    11, // nodeCount < 11 means <= 10
)

if aboveMin && belowMax {
    fmt.Println("Node count is within valid range")
}
```

## Integration with Config Loader

The criteria package is designed to work seamlessly with conditions defined in adapter configurations:

```go
import (
    "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
    "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
)

// Load config
config, _ := config_loader.Load("adapter-config.yaml")

// Get precondition
precond := config.GetPreconditionByName("clusterStatus")

// Create evaluation context with API response data
ctx := criteria.NewEvaluationContext()
ctx.Set("clusterPhase", "Ready")
ctx.Set("cloudProvider", "aws")
ctx.Set("vpcId", "vpc-12345")

// Evaluate precondition conditions
evaluator := criteria.NewEvaluator(ctx)
conditions := make([]criteria.ConditionDef, len(precond.Conditions))
for i, cond := range precond.Conditions {
    conditions[i] = criteria.ConditionDef{
        Field:    cond.Field,
        Operator: criteria.Operator(cond.Operator), // Cast string to Operator type
        Value:    cond.Value,
    }
}

result, err := evaluator.EvaluateConditions(conditions)
if err != nil {
    log.Fatal(err)
}

if result {
    fmt.Println("Precondition passed - proceeding with resource creation")
} else {
    fmt.Println("Precondition failed - skipping resource creation")
}
```

## Error Handling

The package provides descriptive error messages:

```go
ctx := criteria.NewEvaluationContext()
ctx.Set("count", "not a number")

evaluator := criteria.NewEvaluator(ctx)
result, err := evaluator.EvaluateCondition(
    "count",
    criteria.OperatorGreaterThan,
    5,
)

if err != nil {
    // Error message: "evaluation error for field 'count': failed to convert field value to number: ..."
    fmt.Println("Evaluation error:", err)
}
```

## Testing

The package includes comprehensive tests:

```bash
# Run all tests
go test ./internal/criteria/...

# Run with coverage
go test -cover ./internal/criteria/...

# Run integration tests
go test -v ./internal/criteria/... -run Integration
```

### Test Coverage

- Unit tests for each operator
- Nested field access tests
- Type conversion tests
- Error handling tests
- Real-world scenario tests

## Performance Considerations

- **Field Access**: Nested field lookups use reflection for struct fields
- **Type Conversions**: Numeric comparisons automatically convert between int, float, etc.
- **Context Reuse**: Reuse `EvaluationContext` and `Evaluator` for multiple evaluations
- **Memory**: Context stores references to data, not copies (where possible)

## Best Practices

1. **Reuse Contexts**: Create one context and update values between evaluations
2. **Validate Types**: Ensure values are of expected types for operators
3. **Check Errors**: Always check errors from evaluation methods
4. **Use Typed Values**: Prefer typed values over string conversions
5. **Descriptive Field Names**: Use clear, descriptive field names for debugging

## Related Packages

- `internal/config_loader`: Parses adapter configurations with condition definitions
- `internal/k8s_client`: Uses criteria for resource status evaluation

## Configuration Template Examples

See `configs/adapter-config-template.yaml` for examples of condition usage:

```yaml
preconditions:
  - name: "clusterStatus"
    conditions:
      - field: "clusterPhase"
        operator: "in"
        value: ["Provisioning", "Installing", "Ready"]
      - field: "cloudProvider"
        operator: "in"
        value: ["aws", "gcp", "azure"]
      - field: "vpcId"
        operator: "exists"
```

