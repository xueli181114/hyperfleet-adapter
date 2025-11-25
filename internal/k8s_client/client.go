package k8s_client

import (
	"context"
	"encoding/json"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client is the Kubernetes client for managing resources using controller-runtime
type Client struct {
	client client.Client
	log    logger.Logger
}

// ClientConfig holds configuration for creating a Kubernetes client
type ClientConfig struct {
	// KubeConfigPath is the path to kubeconfig file
	// Leave empty ("") to use in-cluster ServiceAccount authentication
	// Set to a path for local development or external cluster access
	KubeConfigPath string
	// QPS is the queries per second rate limiter
	QPS float32
	// Burst is the burst rate limiter
	Burst int
}

// NewClient creates a new Kubernetes client with automatic authentication detection
//
// Authentication Methods:
//   1. In-Cluster (ServiceAccount) - When KubeConfigPath is empty ("")
//      - Uses ServiceAccount token mounted at /var/run/secrets/kubernetes.io/serviceaccount/token
//      - Uses CA certificate at /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
//      - Automatically configured when running in a Kubernetes pod
//      - Requires appropriate RBAC permissions for the ServiceAccount
//
//   2. Kubeconfig - When KubeConfigPath is set
//      - Uses the specified kubeconfig file for authentication
//      - Suitable for local development or accessing remote clusters
//
// Example Usage:
//   // For production deployment in K8s cluster (uses ServiceAccount)
//   config := ClientConfig{KubeConfigPath: "", QPS: 100.0, Burst: 200}
//   client, err := NewClient(ctx, config, log)
//
//   // For local development (uses kubeconfig)
//   config := ClientConfig{KubeConfigPath: "/home/user/.kube/config"}
//   client, err := NewClient(ctx, config, log)
func NewClient(ctx context.Context, config ClientConfig, log logger.Logger) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if config.KubeConfigPath == "" {
		// Use in-cluster config with ServiceAccount
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.KubernetesError("failed to create in-cluster config: %v", err)
		}
		log.Info("Using in-cluster Kubernetes configuration (ServiceAccount)")
	} else {
		// Use kubeconfig file for local development or remote access
		restConfig, err = clientcmd.BuildConfigFromFlags("", config.KubeConfigPath)
		if err != nil {
			return nil, errors.KubernetesError("failed to load kubeconfig from %s: %v", config.KubeConfigPath, err)
		}
		log.Infof("Using kubeconfig from: %s", config.KubeConfigPath)
	}

	// Set rate limits
	if config.QPS == 0 {
		restConfig.QPS = 100.0
	} else {
		restConfig.QPS = config.QPS
	}
	if config.Burst == 0 {
		restConfig.Burst = 200
	} else {
		restConfig.Burst = config.Burst
	}

	// Create controller-runtime client
	// This provides automatic caching, better performance, and cleaner API
	k8sClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, errors.KubernetesError("failed to create kubernetes client: %v", err)
	}

	return &Client{
		client: k8sClient,
		log:    log,
	}, nil
}

// NewClientFromConfig creates a client from an existing rest.Config
// This is useful for testing with envtest
func NewClientFromConfig(ctx context.Context, restConfig *rest.Config, log logger.Logger) (*Client, error) {
	k8sClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, errors.KubernetesError("failed to create kubernetes client: %v", err)
	}

	return &Client{
		client: k8sClient,
		log:    log,
	}, nil
}

// CreateResource creates a Kubernetes resource from an unstructured object
func (c *Client) CreateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()
	name := obj.GetName()

	c.log.Infof("Creating resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	err := c.client.Create(ctx, obj)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
		return nil, err
	}
		return nil, errors.KubernetesError("failed to create resource %s/%s (namespace: %s): %v", gvk.Kind, name, namespace, err)
	}

	c.log.Infof("Successfully created resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// GetResource retrieves a specific Kubernetes resource by GVK, namespace, and name
func (c *Client) GetResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	c.log.Infof("Getting resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	key := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}

	err := c.client.Get(ctx, key, obj)
	if err != nil {
		// Don't wrap NotFound errors so callers can check for them
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.KubernetesError("failed to get resource %s/%s (namespace: %s): %v", gvk.Kind, name, namespace, err)
	}

	c.log.Infof("Successfully retrieved resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// ListResources lists Kubernetes resources by GVK, namespace, and label selector.
//
// Parameters:
//   - gvk: GroupVersionKind of the resources to list
//   - namespace: namespace to list resources in (empty string for cluster-scoped or all namespaces)
//   - labelSelector: label selector string (e.g., "app=myapp,env=prod") - empty to skip
//
// For more flexible discovery (including by-name lookup), use DiscoverResources() instead.
func (c *Client) ListResources(ctx context.Context, gvk schema.GroupVersionKind, namespace string, labelSelector string) (*unstructured.UnstructuredList, error) {
	c.log.Infof("Listing resources: %s (namespace: %s, labelSelector: %s)", gvk.Kind, namespace, labelSelector)

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)

	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if labelSelector != "" {
		selector, err := metav1.ParseToLabelSelector(labelSelector)
		if err != nil {
			return nil, errors.KubernetesError("invalid label selector %s: %v", labelSelector, err)
		}
		labelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, errors.KubernetesError("failed to convert label selector: %v", err)
		}
		opts = append(opts, client.MatchingLabelsSelector{Selector: labelSelector})
	}

	err := c.client.List(ctx, list, opts...)
	if err != nil {
		return nil, errors.KubernetesError("failed to list resources %s (namespace: %s, labelSelector: %s): %v", gvk.Kind, namespace, labelSelector, err)
	}

	c.log.Infof("Successfully listed resources: %s (found %d items)", gvk.Kind, len(list.Items))
	return list, nil
}

// UpdateResource updates an existing Kubernetes resource by replacing it entirely.
//
// This performs a full resource replacement - all fields in the provided object
// will replace the existing resource. Any fields not included will be reset to
// their default values. Requires the object to have a valid resourceVersion.
//
// Use UpdateResource when:
//   - You have the complete, current resource (e.g., from GetResource)
//   - You want to replace the entire resource
//   - You're making multiple changes across the object
//
// Use PatchResource instead when:
//   - You only want to modify specific fields
//   - You don't have the current resource
//   - You want to avoid conflicts with concurrent updates
//
// Example:
//   resource, _ := client.GetResource(ctx, gvk, "default", "my-cm")
//   resource.SetLabels(map[string]string{"app": "myapp"})
//   updated, err := client.UpdateResource(ctx, resource)
func (c *Client) UpdateResource(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()
	name := obj.GetName()

	c.log.Infof("Updating resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	err := c.client.Update(ctx, obj)
	if err != nil {
		if apierrors.IsConflict(err) {
			return nil, errors.KubernetesError("update conflict for %s/%s (namespace: %s): resource version mismatch. Get the latest version and retry", gvk.Kind, name, namespace)
	}
		return nil, errors.KubernetesError("failed to update resource %s/%s (namespace: %s): %v", gvk.Kind, name, namespace, err)
	}

	c.log.Infof("Successfully updated resource: %s/%s", gvk.Kind, name)
	return obj, nil
}

// DeleteResource deletes a Kubernetes resource
func (c *Client) DeleteResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	c.log.Infof("Deleting resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)

	err := c.client.Delete(ctx, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.log.Infof("Resource already deleted: %s/%s", gvk.Kind, name)
			return nil
	}
		return errors.KubernetesError("failed to delete resource %s/%s (namespace: %s): %v", gvk.Kind, name, namespace, err)
	}

	c.log.Infof("Successfully deleted resource: %s/%s", gvk.Kind, name)
	return nil
}

// PatchResource applies a patch to a Kubernetes resource
//
// This performs a strategic merge patch, updating only the specified fields
// while preserving other fields. This is safer than UpdateResource for
// concurrent modifications.
//
// Patch Types:
//   - Strategic Merge Patch: Merges the patch with existing resource intelligently
//   - Preserves fields not specified in the patch
//   - No need for resourceVersion (optimistic concurrency)
//
// Use PatchResource when:
//   - You only want to update specific fields
//   - You don't have the complete current resource
//   - You want to avoid conflicts from concurrent updates
//   - You're updating labels, annotations, or specific spec fields
//
// Use UpdateResource instead when:
//   - You have the complete resource and want to replace it entirely
//   - You're making complex multi-field changes
//
// Example:
//   patchData := []byte(`{"metadata":{"labels":{"new-label":"value"}}}`)
//   patched, err := client.PatchResource(ctx, gvk, "default", "my-cm", patchData)
func (c *Client) PatchResource(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string, patchData []byte) (*unstructured.Unstructured, error) {
	c.log.Infof("Patching resource: %s/%s (namespace: %s)", gvk.Kind, name, namespace)

	// Parse patch data to validate JSON
	var patchObj map[string]interface{}
	if err := json.Unmarshal(patchData, &patchObj); err != nil {
		return nil, errors.KubernetesError("invalid patch data: %v", err)
	}

	// Create the resource reference
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)

	// Apply the patch using strategic merge patch type
	// This is equivalent to kubectl patch with --type=merge
	patch := client.RawPatch(types.MergePatchType, patchData)
	
	err := c.client.Patch(ctx, obj, patch)
	if err != nil {
		// Don't wrap NotFound errors so callers can check for them
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, errors.KubernetesError("failed to patch resource %s/%s (namespace: %s): %v", gvk.Kind, name, namespace, err)
	}

	c.log.Infof("Successfully patched resource: %s/%s", gvk.Kind, name)
	
	// Get the updated resource to return
	return c.GetResource(ctx, gvk, namespace, name)
}
