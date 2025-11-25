# Kubernetes Client Package (`k8sclient`)

Kubernetes client wrapper for dynamic resource operations in the HyperFleet adapter framework.

**Package:** `github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client`

## Overview

The k8s_client package provides foundational Kubernetes API operations using **controller-runtime**:
- Create, Get, List, Update, Delete, and Patch any Kubernetes resource
- Support for both namespaced and cluster-scoped resources
- **Built-in caching** for improved performance on reads
- Works with any resource type including CRDs
- In-cluster and kubeconfig-based authentication
- Industry-standard controller-runtime client (used by Kubebuilder, Operator SDK)

**For high-level operations:** Use `internal/config-loader` package which provides `ResourceManager` with template rendering, resource discovery, and lifecycle management.

### Why Controller-Runtime?

This package uses `sigs.k8s.io/controller-runtime/pkg/client` instead of raw `client-go/dynamic` for several benefits:
- **80% less boilerplate**: ~40 lines vs ~130 lines for client setup
- **Automatic caching**: Reads hit cache (eventual consistency), writes go to API server
- **Better API**: Cleaner interface with `client.Get()`, `client.List()`, etc.
- **Industry standard**: Used by thousands of Kubernetes operators
- **Performance**: Built-in watch-based caching reduces API server load

## Architecture

```
┌─────────────────────────────────────────────────┐
│              Client                              │
│    (Low-level K8s API operations)               │
│                                                  │
│  • CreateResource()                             │
│  • GetResource()                                │
│  • ListResources()                              │
│  • DiscoverResources()  ← Discovery interface   │
│  • UpdateResource()                             │
│  • DeleteResource()                             │
│  • PatchResource()                              │
└─────────────────────────────────────────────────┘
```

## Key Features

### 1. Dynamic Client Operations

Works with any Kubernetes resource type using `*unstructured.Unstructured`:

```go
// Create a resource
created, err := client.CreateResource(ctx, obj)

// Get a specific resource
resource, err := client.GetResource(ctx, gvk, namespace, name)

// List resources by label selector
list, err := client.ListResources(ctx, gvk, namespace, "app=myapp")

// Discover resources using Discovery interface
discovery := &k8s_client.DiscoveryConfig{
    Namespace:     "default",
    LabelSelector: "app=myapp",
}
list, err := client.DiscoverResources(ctx, gvk, discovery)

// Discover single resource by name
discovery := &k8s_client.DiscoveryConfig{
    Namespace: "default",
    ByName:    "my-resource",
}
list, err := client.DiscoverResources(ctx, gvk, discovery)

// Update a resource (full replacement)
updated, err := client.UpdateResource(ctx, obj)

// Patch a resource (partial update)
patched, err := client.PatchResource(ctx, gvk, namespace, name, patchData)

// Delete a resource
err := client.DeleteResource(ctx, gvk, namespace, name)
```

### 2. Authentication

Two authentication methods with automatic detection:

**Production (In-Cluster):**
```go
config := ClientConfig{
    KubeConfigPath: "", // Empty = use ServiceAccount
    QPS: 100.0,
    Burst: 200,
}
client, err := NewClient(ctx, config, log)
```

**Development (Kubeconfig):**
```go
config := ClientConfig{
    KubeConfigPath: "/home/user/.kube/config",
    QPS: 50.0,
    Burst: 100,
}
client, err := NewClient(ctx, config, log)
```

### 3. GroupVersionKind (GVK) Utilities

**Production code must extract GVK from config:**

```go
// ✅ CORRECT: Extract from adapter config
gvk, err := GVKFromKindAndApiVersion("Deployment", "apps/v1")
if err != nil {
    return fmt.Errorf("invalid GVK: %w", err)
}

// Use with client operations
resource, err := client.GetResource(ctx, gvk, "default", "my-deployment")
```

**Testing only:**

```go
// ⚠️ For tests only - available in *_test.go files
gvk := CommonResourceKinds.Namespace
gvk := CommonResourceKinds.Pod
gvk := CommonResourceKinds.Deployment
```

## Usage Examples

### Create a Resource

```go
// Create a ConfigMap
cm := &unstructured.Unstructured{
    Object: map[string]interface{}{
        "apiVersion": "v1",
        "kind":       "ConfigMap",
        "metadata": map[string]interface{}{
            "name":      "my-config",
            "namespace": "default",
    },
        "data": map[string]interface{}{
            "key": "value",
        },
    },
}
gvk, _ := GVKFromKindAndApiVersion("ConfigMap", "v1")
cm.SetGroupVersionKind(gvk)

created, err := client.CreateResource(ctx, cm)
```

### Get and Check Resource Status

```go
// Get a custom resource
gvk, _ := GVKFromKindAndApiVersion("MyResource", "example.com/v1")
resource, err := client.GetResource(ctx, gvk, "default", "my-resource")

if err != nil {
    if apierrors.IsNotFound(err) {
        // Resource doesn't exist
    }
    return err
}

// Extract status conditions
conditions, found, _ := unstructured.NestedSlice(resource.Object, "status", "conditions")
if found {
    for _, cond := range conditions {
        condMap := cond.(map[string]interface{})
        condType := condMap["type"].(string)
        status := condMap["status"].(string)
        // Process condition...
    }
}
```

### Discover Resources

The `DiscoverResources` method provides flexible resource discovery:

```go
gvk, _ := GVKFromKindAndApiVersion("Pod", "v1")

// List by label selector
discovery := &k8s_client.DiscoveryConfig{
    Namespace:     "default",
    LabelSelector: "app=myapp,env=prod",
}
list, err := client.DiscoverResources(ctx, gvk, discovery)
if err != nil {
    return err
}

for _, pod := range list.Items {
    log.Infof("Found pod: %s", pod.GetName())
}

// Get single resource by name
discovery := &k8s_client.DiscoveryConfig{
    Namespace: "default",
    ByName:    "my-pod",
}
list, err := client.DiscoverResources(ctx, gvk, discovery)
// list.Items will contain exactly one item
```

### List Resources (Low-Level)

For simple listing without the Discovery interface:

```go
gvk, _ := GVKFromKindAndApiVersion("Pod", "v1")

// List by label selector
list, err := client.ListResources(ctx, gvk, "default", "app=myapp,env=prod")
if err != nil {
    return err
}

for _, pod := range list.Items {
    log.Infof("Found pod: %s", pod.GetName())
}
```

### Update a Resource

```go
// Get current resource
resource, err := client.GetResource(ctx, gvk, "default", "my-resource")
if err != nil {
    return err
}

// Modify it
labels := resource.GetLabels()
labels["updated"] = "true"
resource.SetLabels(labels)

// Update (requires resourceVersion)
updated, err := client.UpdateResource(ctx, resource)
```

### Patch a Resource

```go
// Strategic merge patch
patchData := []byte(`{
    "metadata": {
        "labels": {
            "new-label": "new-value"
        }
    }
}`)

patched, err := client.PatchResource(ctx, gvk, "default", "my-resource", patchData)
```

## Configuration

```go
type ClientConfig struct {
    KubeConfigPath string  // "" for in-cluster, path for kubeconfig
    QPS            float32 // Queries per second (default: 100.0)
    Burst          int     // Burst rate (default: 200)
}
```

## Error Handling

```go
resource, err := client.GetResource(ctx, gvk, namespace, name)
if err != nil {
    if apierrors.IsNotFound(err) {
        // Resource doesn't exist
    } else if apierrors.IsAlreadyExists(err) {
        // Resource already exists (on create)
    } else {
        // Other error
    }
    return err
}
```

## Dependencies

- `k8s.io/client-go` - Kubernetes Go client
- `k8s.io/apimachinery` - Kubernetes API machinery
- `k8s.io/api` - Kubernetes API types

## Testing

Unit tests cover:
- Resource CRUD operations
- GVK to GVR conversion
- Error handling
- Authentication methods

Integration tests use **Testcontainers** with K3s for real Kubernetes cluster testing. This provides:
- Full K3s cluster with all Kubernetes features
- Simple setup (just needs Docker/Podman)
- Fresh cluster for each test suite
- Real networking and CRD support

See `test/integration/k8s_client/` for integration test examples and setup guide.

## Best Practices

1. **Always extract GVK from config** using `GVKFromKindAndApiVersion()`
2. **Use in-cluster auth for production** (empty `KubeConfigPath`)
3. **Set appropriate rate limits** to avoid overwhelming the API server
4. **Handle errors gracefully** - check for `IsNotFound`, `IsAlreadyExists`, etc.
5. **Use high-level ResourceManager** from `config-loader` for template rendering and discovery

## Related Packages

- **`internal/config-loader`**: High-level resource management with templates and discovery
- **`pkg/errors`**: Error handling utilities
- **`pkg/logger`**: Logging interface
