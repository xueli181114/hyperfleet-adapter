package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResourceExecutor creates and updates Kubernetes resources
type ResourceExecutor struct {
	k8sClient *k8s_client.Client
	log       logger.Logger
	dryRun    bool
}

// NewResourceExecutor creates a new resource executor
func NewResourceExecutor(k8sClient *k8s_client.Client, log logger.Logger, dryRun bool) *ResourceExecutor {
	return &ResourceExecutor{
		k8sClient: k8sClient,
		log:       log,
		dryRun:    dryRun,
	}
}

// ExecuteAll creates/updates all resources in sequence
// Returns results for each resource and updates the execution context
func (re *ResourceExecutor) ExecuteAll(ctx context.Context, resources []config_loader.Resource, execCtx *ExecutionContext) ([]ResourceResult, error) {
	results := make([]ResourceResult, 0, len(resources))

	for _, resource := range resources {
		result, err := re.executeResource(ctx, resource, execCtx)
		results = append(results, result)

		if err != nil {
			return results, err
		}
	}

	return results, nil
}

// executeResource creates or updates a single Kubernetes resource
func (re *ResourceExecutor) executeResource(ctx context.Context, resource config_loader.Resource, execCtx *ExecutionContext) (ResourceResult, error) {
	startTime := time.Now()
	result := ResourceResult{
		Name:   resource.Name,
		Status: StatusSuccess,
	}

	re.log.Infof("Processing resource: %s", resource.Name)

	// Step 1: Build the manifest
	manifest, err := re.buildManifest(resource, execCtx)
	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		result.Duration = time.Since(startTime)
		return result, NewExecutorError(PhaseResources, resource.Name, "failed to build manifest", err)
	}

	// Extract resource info
	gvk := manifest.GroupVersionKind()
	result.Kind = gvk.Kind
	result.Namespace = manifest.GetNamespace()
	result.ResourceName = manifest.GetName()

	re.log.Infof("Manifest built: %s %s/%s (namespace: %s)",
		gvk.Kind, gvk.Group, manifest.GetName(), manifest.GetNamespace())

	// Step 2: Check for existing resource using discovery
	var existingResource *unstructured.Unstructured
	if resource.Discovery != nil {
		existingResource, err = re.discoverExistingResource(ctx, gvk, resource.Discovery, execCtx)
		if err != nil && !apierrors.IsNotFound(err) {
			if isRetryableDiscoveryError(err) {
				// Transient/network error - log and continue, we'll try to create
				re.log.Warningf("Transient discovery error (continuing): %v", err)
			} else {
				// Fatal error (auth, permission, validation) - fail fast
				result.Status = StatusFailed
				result.Error = err
				result.Duration = time.Since(startTime)
				return result, NewExecutorError(PhaseResources, resource.Name, "failed to discover existing resource", err)
			}
		}
	}

	// Step 3: Perform the appropriate operation
	if re.dryRun {
		result.Operation = OperationDryRun
		result.Resource = manifest
		re.log.Infof("Dry run: would %s resource %s/%s",
			re.determineOperation(existingResource, resource.RecreateOnChange),
			gvk.Kind, manifest.GetName())
	} else if existingResource != nil {
		// Resource exists - update or recreate
		if resource.RecreateOnChange {
			result.Operation = OperationRecreate
			result.Resource, err = re.recreateResource(ctx, existingResource, manifest)
		} else {
			result.Operation = OperationUpdate
			result.Resource, err = re.updateResource(ctx, existingResource, manifest)
		}
	} else {
		// Create new resource
		result.Operation = OperationCreate
		result.Resource, err = re.createResource(ctx, manifest)
	}

	if err != nil {
		result.Status = StatusFailed
		result.Error = err
		result.Duration = time.Since(startTime)
		return result, NewExecutorError(PhaseResources, resource.Name,
			fmt.Sprintf("failed to %s resource", result.Operation), err)
	}

	// Store resource in execution context
	if result.Resource != nil {
		execCtx.Resources[resource.Name] = result.Resource
	}

	result.Duration = time.Since(startTime)
	re.log.Infof("Resource %s completed: %s %s/%s (operation: %s, duration: %v)",
		resource.Name, result.Kind, result.Namespace, result.ResourceName, result.Operation, result.Duration)

	return result, nil
}

// buildManifest builds an unstructured manifest from the resource configuration
func (re *ResourceExecutor) buildManifest(resource config_loader.Resource, execCtx *ExecutionContext) (*unstructured.Unstructured, error) {
	var manifestData map[string]interface{}

	// Check if manifest is inline or from ManifestItems (loaded from ref)
	if len(resource.ManifestItems) > 0 {
		// Use first manifest item (loaded from ref file)
		manifestData = resource.ManifestItems[0]
	} else if resource.Manifest != nil {
		// Use inline manifest
		switch m := resource.Manifest.(type) {
		case map[string]interface{}:
			manifestData = m
		case map[interface{}]interface{}:
			manifestData = convertToStringKeyMap(m)
		default:
			return nil, fmt.Errorf("unsupported manifest type: %T", resource.Manifest)
		}
	} else {
		return nil, fmt.Errorf("no manifest specified for resource %s", resource.Name)
	}

	// Deep copy to avoid modifying the original
	manifestData = deepCopyMap(manifestData)

	// Render all template strings in the manifest
	renderedData, err := renderManifestTemplates(manifestData, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render manifest templates: %w", err)
	}

	// Convert to unstructured
	obj := &unstructured.Unstructured{Object: renderedData}

	// Validate required fields
	if obj.GetAPIVersion() == "" {
		return nil, fmt.Errorf("manifest missing apiVersion")
	}
	if obj.GetKind() == "" {
		return nil, fmt.Errorf("manifest missing kind")
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("manifest missing metadata.name")
	}

	return obj, nil
}

// discoverExistingResource discovers an existing resource using the discovery config
func (re *ResourceExecutor) discoverExistingResource(ctx context.Context, gvk schema.GroupVersionKind, discovery *config_loader.DiscoveryConfig, execCtx *ExecutionContext) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	// Render discovery config templates
	namespace, err := renderTemplate(discovery.Namespace, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render namespace template: %w", err)
	}

	// Check if discovering by name
	if discovery.ByName != "" {
		name, err := renderTemplate(discovery.ByName, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to render byName template: %w", err)
		}
		return re.k8sClient.GetResource(ctx, gvk, namespace, name)
	}

	// Discover by label selector
	if discovery.BySelectors != nil && len(discovery.BySelectors.LabelSelector) > 0 {
		// Render label selector templates
		renderedLabels := make(map[string]string)
		for k, v := range discovery.BySelectors.LabelSelector {
			renderedK, err := renderTemplate(k, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label key template: %w", err)
			}
			renderedV, err := renderTemplate(v, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render label value template: %w", err)
			}
			renderedLabels[renderedK] = renderedV
		}

		labelSelector := k8s_client.BuildLabelSelector(renderedLabels)

		discoveryConfig := &k8s_client.DiscoveryConfig{
			Namespace:     namespace,
			LabelSelector: labelSelector,
		}

		list, err := re.k8sClient.DiscoverResources(ctx, gvk, discoveryConfig)
		if err != nil {
			return nil, err
		}

		if len(list.Items) == 0 {
			return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, "")
		}

		// Sort by generation annotation (descending) to return the one with the latest generation
		// This ensures deterministic behavior when multiple resources match the label selector
		// Secondary sort by metadata.name for consistency when generations are equal
		sort.Slice(list.Items, func(i, j int) bool {
			genI := getGenerationAnnotationValue(&list.Items[i])
			genJ := getGenerationAnnotationValue(&list.Items[j])
			if genI != genJ {
				return genI > genJ // Descending order - latest generation first
			}
			// Fall back to metadata.name for deterministic ordering when generations are equal
			return list.Items[i].GetName() < list.Items[j].GetName()
		})

		return &list.Items[0], nil
	}

	return nil, fmt.Errorf("discovery config must specify byName or bySelectors")
}

// createResource creates a new Kubernetes resource
func (re *ResourceExecutor) createResource(ctx context.Context, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	return re.k8sClient.CreateResource(ctx, manifest)
}

// updateResource updates an existing Kubernetes resource
func (re *ResourceExecutor) updateResource(ctx context.Context, existing, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	// Preserve resourceVersion from existing for update
	manifest.SetResourceVersion(existing.GetResourceVersion())
	manifest.SetUID(existing.GetUID())

	return re.k8sClient.UpdateResource(ctx, manifest)
}

// recreateResource deletes and recreates a Kubernetes resource
// It waits for the resource to be fully deleted before creating the new one
// to avoid race conditions with Kubernetes asynchronous deletion
func (re *ResourceExecutor) recreateResource(ctx context.Context, existing, manifest *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if re.k8sClient == nil {
		return nil, fmt.Errorf("kubernetes client not configured")
	}

	gvk := existing.GroupVersionKind()
	namespace := existing.GetNamespace()
	name := existing.GetName()

	// Delete the existing resource
	re.log.Infof("Deleting resource for recreation: %s/%s", gvk.Kind, name)
	if err := re.k8sClient.DeleteResource(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to delete resource for recreation: %w", err)
	}

	// Wait for the resource to be fully deleted
	re.log.Infof("Waiting for resource deletion to complete: %s/%s", gvk.Kind, name)
	if err := re.waitForDeletion(ctx, gvk, namespace, name); err != nil {
		return nil, fmt.Errorf("failed waiting for resource deletion: %w", err)
	}

	// Create the new resource
	re.log.Infof("Creating new resource after deletion confirmed: %s/%s", gvk.Kind, manifest.GetName())
	return re.k8sClient.CreateResource(ctx, manifest)
}

// waitForDeletion polls until the resource is confirmed deleted or context times out
// Returns nil when the resource is confirmed gone (NotFound), or an error otherwise
func (re *ResourceExecutor) waitForDeletion(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) error {
	const pollInterval = 100 * time.Millisecond

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			re.log.Warningf("Context cancelled/timed out while waiting for deletion of %s/%s", gvk.Kind, name)
			return fmt.Errorf("context cancelled while waiting for resource deletion: %w", ctx.Err())
		case <-ticker.C:
			_, err := re.k8sClient.GetResource(ctx, gvk, namespace, name)
			if err != nil {
				// NotFound means the resource is deleted - this is success
				if apierrors.IsNotFound(err) {
					re.log.Infof("Resource deletion confirmed: %s/%s", gvk.Kind, name)
					return nil
				}
				// Any other error is unexpected
			re.log.Errorf("Error checking resource deletion status for %s/%s: %v", gvk.Kind, name, err)
				return fmt.Errorf("error checking deletion status: %w", err)
			}
			// Resource still exists, continue polling
			re.log.V(2).Infof("Resource %s/%s still exists, waiting for deletion...", gvk.Kind, name)
		}
	}
}

// determineOperation determines what operation would be performed
func (re *ResourceExecutor) determineOperation(existing *unstructured.Unstructured, recreateOnChange bool) string {
	if existing == nil {
		return "create"
	}
	if recreateOnChange {
		return "recreate"
	}
	return "update"
}

// convertToStringKeyMap converts map[interface{}]interface{} to map[string]interface{}
func convertToStringKeyMap(m map[interface{}]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		strKey := fmt.Sprintf("%v", k)
		switch val := v.(type) {
		case map[interface{}]interface{}:
			result[strKey] = convertToStringKeyMap(val)
		case []interface{}:
			result[strKey] = convertSlice(val)
		default:
			result[strKey] = v
		}
	}
	return result
}

// convertSlice converts slice elements recursively
func convertSlice(s []interface{}) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		switch val := v.(type) {
		case map[interface{}]interface{}:
			result[i] = convertToStringKeyMap(val)
		case []interface{}:
			result[i] = convertSlice(val)
		default:
			result[i] = v
		}
	}
	return result
}

// deepCopyMap creates a deep copy of a map
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	// Use JSON marshaling for a simple deep copy
	data, err := json.Marshal(m)
	if err != nil {
		// Fallback to shallow copy
		result := make(map[string]interface{})
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		// Fallback to shallow copy
		result := make(map[string]interface{})
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	return result
}

// renderManifestTemplates recursively renders all template strings in a manifest
func renderManifestTemplates(data map[string]interface{}, params map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for k, v := range data {
		renderedKey, err := renderTemplate(k, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render key '%s': %w", k, err)
		}

		renderedValue, err := renderValue(v, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render value for key '%s': %w", k, err)
		}

		result[renderedKey] = renderedValue
	}

	return result, nil
}

// renderValue renders a value recursively
func renderValue(v interface{}, params map[string]interface{}) (interface{}, error) {
	switch val := v.(type) {
	case string:
		return renderTemplate(val, params)
	case map[string]interface{}:
		return renderManifestTemplates(val, params)
	case map[interface{}]interface{}:
		converted := convertToStringKeyMap(val)
		return renderManifestTemplates(converted, params)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			rendered, err := renderValue(item, params)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil
	default:
		return v, nil
	}
}

// getGenerationAnnotationValue extracts the generation annotation value from a resource
// Returns 0 if the resource is nil, has no annotations, or the annotation cannot be parsed
func getGenerationAnnotationValue(obj *unstructured.Unstructured) int64 {
	if obj == nil {
		return 0
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return 0
	}
	genStr, ok := annotations[AnnotationGeneration]
	if !ok || genStr == "" {
		return 0
	}
	// Try to parse as integer directly
	gen, err := strconv.ParseInt(genStr, 10, 64)
	if err != nil {
		// Generation value is not a valid integer, return 0
		return 0
	}
	return gen
}

// GetResourceAsMap converts an unstructured resource to a map for CEL evaluation
func GetResourceAsMap(resource *unstructured.Unstructured) map[string]interface{} {
	if resource == nil {
		return nil
	}
	return resource.Object
}

// toCamelCase converts a snake_case string to camelCase.
// For example: "my_resource" -> "myResource", "some_long_name" -> "someLongName"
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return s
	}

	// First segment stays lowercase
	result := strings.ToLower(parts[0])

	// Subsequent segments: capitalize the first letter
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) == 0 {
			continue
		}
		result += strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
	}

	return result
}

// BuildResourcesMap builds a map of all resources for CEL evaluation.
// Resource names are converted from snake_case to camelCase for CEL access.
// Returns an error if key collision is detected after camelCase conversion.
func BuildResourcesMap(resources map[string]*unstructured.Unstructured) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	// Track original names for collision detection
	keyOrigins := make(map[string]string)

	for name, resource := range resources {
		if resource != nil {
			// Convert snake_case to camelCase
			key := toCamelCase(name)

			// Check for collision
			if existingName, exists := keyOrigins[key]; exists {
				return nil, fmt.Errorf("resource key collision: both %q and %q convert to camelCase key %q", existingName, name, key)
			}

			keyOrigins[key] = name
			result[key] = resource.Object
		}
	}
	return result, nil
}

// isRetryableDiscoveryError determines if a discovery error is transient/retryable
// (and thus safe to ignore and proceed with create) or fatal (and should fail fast).
//
// Retryable errors (log and continue):
//   - Timeouts (request/server timeouts)
//   - Server errors (5xx status codes)
//   - Network/connection errors (connection refused, reset, etc.)
//   - Service unavailable
//
// Non-retryable/fatal errors (fail fast):
//   - Forbidden (403) - permission denied
//   - Unauthorized (401) - authentication failure
//   - Bad request (400) - invalid request
//   - Invalid/validation errors
//   - Gone (410) - resource no longer exists
//   - Other clearly fatal API errors
func isRetryableDiscoveryError(err error) bool {
	if err == nil {
		return false
	}

	// Check for transient Kubernetes API errors (retryable)
	if apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) ||
		apierrors.IsTooManyRequests(err) {
		return true
	}

	// Check for fatal Kubernetes API errors (non-retryable)
	if apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) ||
		apierrors.IsBadRequest(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsGone(err) ||
		apierrors.IsMethodNotSupported(err) ||
		apierrors.IsNotAcceptable(err) {
		return false
	}

	// Check for network-level errors (retryable)
	if isNetworkError(err) {
		return true
	}

	// Default: treat unknown errors as non-retryable to surface issues early
	return false
}

// isNetworkError checks if the error is a network-level error (connection issues, DNS, etc.)
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Check the error chain for network errors
	var netErr net.Error
	var opErr *net.OpError
	var urlErr *url.Error

	// Check for net.Error (includes timeouts)
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	// Check for net.OpError (connection refused, reset, etc.)
	if errors.As(err, &opErr) {
		return true
	}

	// Check for url.Error (URL-related network issues)
	if errors.As(err, &urlErr) {
		return urlErr.Timeout() || isNetworkError(urlErr.Err)
	}

	// Check error message for common network error patterns
	errMsg := strings.ToLower(err.Error())
	networkPatterns := []string{
		"connection refused",
		"connection reset",
		"connection timed out",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"eof",
		"broken pipe",
	}
	for _, pattern := range networkPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

