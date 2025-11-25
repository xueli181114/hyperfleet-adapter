# Config Loader

The `config_loader` package provides functionality for loading and parsing HyperFleet Adapter configuration files in YAML format.

## Overview

This package handles the complete adapter configuration structure as defined in `configs/adapter-config-template.yaml`. It provides type-safe parsing, validation, and querying capabilities for adapter configurations.

## Features

- **YAML Parsing**: Load adapter configurations from YAML files
- **Validation**: Comprehensive validation of required fields and structure
- **Type Safety**: Strongly-typed Go structs for all configuration elements
- **Helper Methods**: Convenient methods for querying configuration data

## Usage

### Loading a Configuration

```go
import "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"

// Load from file
config, err := config_loader.Load("path/to/adapter-config.yaml")
if err != nil {
    log.Fatal(err)
}

// Parse from bytes
data, _ := os.ReadFile("config.yaml")
config, err := config_loader.Parse(data)
if err != nil {
    log.Fatal(err)
}
```

### Accessing Configuration

```go
// Access metadata
fmt.Println("Adapter name:", config.Metadata.Name)
fmt.Println("Adapter version:", config.Spec.Adapter.Version)

// Access HyperFleet API config
timeout, _ := config.Spec.HyperfleetAPI.ParseTimeout()
fmt.Println("API timeout:", timeout)

// Get required parameters
requiredParams := config.GetRequiredParams()
for _, param := range requiredParams {
    fmt.Printf("Required param: %s from %s\n", param.Name, param.Source)
}

// Find specific resource
resource := config.GetResourceByName("clusterNamespace")
if resource != nil {
    fmt.Println("Found resource:", resource.Name)
}

// Find specific precondition
precond := config.GetPreconditionByName("clusterStatus")
if precond != nil {
    fmt.Println("Found precondition:", precond.Name)
}
```

## Configuration Structure

### Top Level

```yaml
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: example-adapter
  namespace: hyperfleet-system
spec:
  # ... (see below)
```

### Spec Section

#### Adapter Info

```yaml
spec:
  adapter:
    version: "0.0.1"
```

#### HyperFleet API Configuration

```yaml
spec:
  hyperfleetApi:
    timeout: 2s
    retryAttempts: 3
    retryBackoff: exponential
```

#### Parameters

Parameters are extracted from CloudEvents or environment variables:

```yaml
spec:
  params:
    - name: "clusterId"
      source: "event.cluster_id"
      type: "string"
      required: true
    
    - name: "hyperfleetApiToken"
      source: "env.HYPERFLEET_API_TOKEN"
      type: "string"
      required: true
```

#### Preconditions

Preconditions validate state before resource creation:

```yaml
spec:
  preconditions:
    - name: "clusterStatus"
      apiCall:
        method: "GET"
        url: "{{ .hyperfleetApiBaseUrl }}/api/v1/clusters/{{ .clusterId }}"
      storeResponseAs: "clusterDetails"
      extract:
        - as: "clusterPhase"
          field: "status.phase"
      conditions:
        - field: "clusterPhase"
          operator: "in"
          value: ["Provisioning", "Installing", "Ready"]
```

#### Resources

Resources define Kubernetes objects to create:

```yaml
spec:
  resources:
    - name: "clusterNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "cluster-{{ .clusterId }}"
      discovery:
        namespace: ""
        bySelectors:
          labelSelector:
            hyperfleet.io/cluster-id: "{{ .clusterId }}"
```

#### Post-Processing

Post-processing actions after resource creation:

```yaml
spec:
  post:
    params:
      - name: "clusterStatusPayload"
        build:
          conditions:
            applied:
              status:
                expression: |
                  resources.clusterNamespace.status.phase == "Active"
    postActions:
      - name: "reportStatus"
        apiCall:
          method: "POST"
          url: "{{ .hyperfleetApiBaseUrl }}/api/v1/status"
```

## Validation

The package performs comprehensive validation:

- **Required Fields**: Ensures all required fields are present
- **API Call Validation**: Validates HTTP methods and required parameters
- **Parameter Validation**: Ensures parameters have source, build, or buildRef
- **Precondition Validation**: Validates precondition structure
- **Resource Validation**: Ensures resources have manifests

### Validation Errors

```go
config, err := config_loader.Load("invalid-config.yaml")
if err != nil {
    // Error messages are descriptive:
    // "spec.params[0].name is required"
    // "spec.preconditions[1].apiCall.method must be one of: GET, POST, PUT, PATCH, DELETE"
    fmt.Println(err)
}
```

## Types

### Key Types

- `AdapterConfig`: Top-level configuration structure
- `AdapterConfigSpec`: Specification section
- `Parameter`: Parameter extraction configuration
- `Precondition`: Precondition check configuration
- `Resource`: Kubernetes resource configuration
- `PostConfig`: Post-processing configuration
- `APICall`: HTTP API call configuration
- `Condition`: Structured condition definition

See `types.go` for complete type definitions.

## Examples

### Loading Template Config

```go
config, err := config_loader.Load("configs/adapter-config-template.yaml")
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Loaded config for adapter: %s\n", config.Metadata.Name)
fmt.Printf("Adapter version: %s\n", config.Spec.Adapter.Version)
fmt.Printf("Parameters: %d\n", len(config.Spec.Params))
fmt.Printf("Preconditions: %d\n", len(config.Spec.Preconditions))
fmt.Printf("Resources: %d\n", len(config.Spec.Resources))
```

### Iterating Over Resources

```go
for i, resource := range config.Spec.Resources {
    fmt.Printf("Resource %d: %s\n", i, resource.Name)
    if resource.Discovery != nil {
        if resource.Discovery.ByName != "" {
            fmt.Printf("  Discovery by name: %s\n", resource.Discovery.ByName)
        }
        if resource.Discovery.BySelectors != nil {
            fmt.Printf("  Discovery by selectors: %+v\n", 
                resource.Discovery.BySelectors.LabelSelector)
        }
    }
}
```

### Checking Precondition Conditions

```go
precond := config.GetPreconditionByName("clusterStatus")
if precond != nil {
    for _, cond := range precond.Conditions {
        fmt.Printf("Condition: %s %s %v\n", 
            cond.Field, cond.Operator, cond.Value)
    }
}
```

## Testing

The package includes comprehensive tests:

```bash
# Run all tests
go test ./internal/config_loader/...

# Run with coverage
go test -cover ./internal/config_loader/...

# Run specific test
go test -run TestLoad ./internal/config_loader/...
```

### Test Coverage

- Unit tests for all validation rules
- Integration tests with actual config templates
- Error handling tests
- Helper method tests

## Related Packages

- `internal/criteria`: Evaluates conditions defined in the config
- `internal/k8s-client`: Creates and manages Kubernetes resources

## Configuration Template

See `configs/adapter-config-template.yaml` for the complete configuration template with detailed documentation.

