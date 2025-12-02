package executor_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// k8sTestAPIServer creates a mock API server for K8s integration tests
type k8sTestAPIServer struct {
	server           *httptest.Server
	mu               sync.Mutex
	requests         []k8sTestRequest
	clusterResponse  map[string]interface{}
	statusResponses  []map[string]interface{}
}

type k8sTestRequest struct {
	Method string
	Path   string
	Body   string
}

func newK8sTestAPIServer(t *testing.T) *k8sTestAPIServer {
	mock := &k8sTestAPIServer{
		requests: make([]k8sTestRequest, 0),
		clusterResponse: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "test-cluster",
			},
			"spec": map[string]interface{}{
				"region":     "us-east-1",
				"provider":   "aws",
				"vpc_id":     "vpc-12345",
				"node_count": 3,
			},
			"status": map[string]interface{}{
				"phase": "Ready",
			},
		},
		statusResponses: make([]map[string]interface{}, 0),
	}

	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		var bodyStr string
		if r.Body != nil {
			buf := make([]byte, 1024*1024)
			n, _ := r.Body.Read(buf)
			bodyStr = string(buf[:n])
		}

		mock.requests = append(mock.requests, k8sTestRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   bodyStr,
		})

		t.Logf("Mock API: %s %s", r.Method, r.URL.Path)

		switch {
		case strings.Contains(r.URL.Path, "/clusters/") && strings.HasSuffix(r.URL.Path, "/status"):
			if r.Method == http.MethodPost {
				var statusBody map[string]interface{}
				if err := json.Unmarshal([]byte(bodyStr), &statusBody); err == nil {
					mock.statusResponses = append(mock.statusResponses, statusBody)
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
				return
			}
		case strings.Contains(r.URL.Path, "/clusters/"):
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(mock.clusterResponse)
				return
			}
		}

		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))

	return mock
}

func (m *k8sTestAPIServer) Close() {
	m.server.Close()
}

func (m *k8sTestAPIServer) URL() string {
	return m.server.URL
}

func (m *k8sTestAPIServer) GetStatusResponses() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]interface{}{}, m.statusResponses...)
}

// createK8sTestEvent creates a CloudEvent for K8s integration testing
func createK8sTestEvent(clusterId string) *event.Event {
	evt := event.New()
	evt.SetID("k8s-test-event-" + clusterId)
	evt.SetType("com.redhat.hyperfleet.cluster.provision")
	evt.SetSource("k8s-integration-test")
	evt.SetTime(time.Now())

	eventData := map[string]interface{}{
		"cluster_id":    clusterId,
		"resource_id":   "resource-" + clusterId,
		"resource_type": "cluster",
		"generation":    "gen-001",
		"href":          "/api/v1/clusters/" + clusterId,
	}
	eventDataBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventDataBytes)

	return &evt
}

// createK8sTestConfig creates an AdapterConfig with K8s resources
func createK8sTestConfig(apiBaseURL, testNamespace string) *config_loader.AdapterConfig {
	return &config_loader.AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata: config_loader.Metadata{
			Name:      "k8s-test-adapter",
			Namespace: testNamespace,
		},
		Spec: config_loader.AdapterConfigSpec{
			Adapter: config_loader.AdapterInfo{
				Version: "1.0.0",
			},
			HyperfleetAPI: config_loader.HyperfleetAPIConfig{
				Timeout:       "10s",
				RetryAttempts: 1,
				RetryBackoff:  "constant",
			},
			Params: []config_loader.Parameter{
				{
					Name:     "hyperfleetApiBaseUrl",
					Source:   "env.HYPERFLEET_API_BASE_URL",
					Required: true,
				},
				{
					Name:     "hyperfleetApiVersion",
					Source:   "env.HYPERFLEET_API_VERSION",
					Default:  "v1",
					Required: false,
				},
				{
					Name:     "clusterId",
					Source:   "event.cluster_id",
					Required: true,
				},
				{
					Name:     "testNamespace",
					Default:  testNamespace,
					Required: false,
				},
			},
			Preconditions: []config_loader.Precondition{
				{
					Name: "clusterStatus",
					APICall: &config_loader.APICall{
						Method:  "GET",
						URL:     "{{ .hyperfleetApiBaseUrl }}/api/{{ .hyperfleetApiVersion }}/clusters/{{ .clusterId }}",
						Timeout: "5s",
					},
					StoreResponseAs: "clusterDetails",
					Extract: []config_loader.ExtractField{
						{As: "clusterName", Field: "metadata.name"},
						{As: "clusterPhase", Field: "status.phase"},
						{As: "region", Field: "spec.region"},
						{As: "cloudProvider", Field: "spec.provider"},
					},
					Conditions: []config_loader.Condition{
						{Field: "clusterPhase", Operator: "in", Value: []interface{}{"Provisioning", "Installing", "Ready"}},
					},
				},
			},
			// K8s Resources to create
			Resources: []config_loader.Resource{
				{
					Name: "clusterConfigMap",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "cluster-config-{{ .clusterId }}",
							"namespace": testNamespace,
							"labels": map[string]interface{}{
								"hyperfleet.io/cluster-id": "{{ .clusterId }}",
								"hyperfleet.io/managed-by": "{{ .metadata.name }}",
								"test":                     "executor-integration",
							},
						},
						"data": map[string]interface{}{
							"cluster-id":   "{{ .clusterId }}",
							"cluster-name": "{{ .clusterName }}",
							"region":       "{{ .region }}",
							"provider":     "{{ .cloudProvider }}",
							"phase":        "{{ .clusterPhase }}",
						},
					},
					Discovery: &config_loader.DiscoveryConfig{
						Namespace: testNamespace,
						ByName:    "cluster-config-{{ .clusterId }}",
					},
				},
				{
					Name: "clusterSecret",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Secret",
						"metadata": map[string]interface{}{
							"name":      "cluster-secret-{{ .clusterId }}",
							"namespace": testNamespace,
							"labels": map[string]interface{}{
								"hyperfleet.io/cluster-id": "{{ .clusterId }}",
								"hyperfleet.io/managed-by": "{{ .metadata.name }}",
								"test":                     "executor-integration",
							},
						},
						"type": "Opaque",
						"stringData": map[string]interface{}{
							"cluster-id": "{{ .clusterId }}",
							"api-token":  "test-token-{{ .clusterId }}",
						},
					},
					Discovery: &config_loader.DiscoveryConfig{
						Namespace: testNamespace,
						ByName:    "cluster-secret-{{ .clusterId }}",
					},
				},
			},
			Post: &config_loader.PostConfig{
				Params: []config_loader.Parameter{
					{
						Name: "clusterStatusPayload",
						Build: map[string]interface{}{
							"conditions": map[string]interface{}{
								"applied": map[string]interface{}{
									"status": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\"",
									},
									"reason": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\" ? \"ResourcesCreated\" : adapter.errorReason",
									},
									"message": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\" ? \"ConfigMap and Secret created\" : adapter.errorMessage",
									},
								},
							},
							"clusterId": map[string]interface{}{
								"value": "{{ .clusterId }}",
							},
							"resourcesCreated": map[string]interface{}{
								"value": "2",
							},
						},
					},
				},
				PostActions: []config_loader.PostAction{
					{
						Name: "reportClusterStatus",
						APICall: &config_loader.APICall{
							Method:  "POST",
							URL:     "{{ .hyperfleetApiBaseUrl }}/api/{{ .hyperfleetApiVersion }}/clusters/{{ .clusterId }}/status",
							Body:    "{{ .clusterStatusPayload }}",
							Timeout: "5s",
						},
					},
				},
			},
		},
	}
}

// TestExecutor_K8s_CreateResources tests the full flow with real K8s resource creation
func TestExecutor_K8s_CreateResources(t *testing.T) {
	// Setup K8s test environment
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	// Create test namespace
	testNamespace := fmt.Sprintf("executor-test-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	// Setup mock API server
	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config with K8s resources
	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	apiClient := hyperfleet_api.NewClient(
		hyperfleet_api.WithTimeout(10*time.Second),
		hyperfleet_api.WithRetryAttempts(1),
	)

	// Create executor with real K8s client
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		WithDryRun(false). // Actually create resources
		Build()
	require.NoError(t, err)

	// Create test event
	clusterId := fmt.Sprintf("cluster-%d", time.Now().UnixNano())
	evt := createK8sTestEvent(clusterId)

	// Execute
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result := exec.Execute(ctx, evt)

	// Verify execution succeeded
	if result.Status != executor.StatusSuccess {
		t.Fatalf("Expected success status, got %s: %v (phase: %s)", result.Status, result.Error, result.Phase)
	}

	t.Logf("Execution completed in %v", result.Duration)

	// Verify resource results
	require.Len(t, result.ResourceResults, 2, "Expected 2 resource results")

	// Check ConfigMap was created
	cmResult := result.ResourceResults[0]
	assert.Equal(t, "clusterConfigMap", cmResult.Name)
	assert.Equal(t, executor.StatusSuccess, cmResult.Status, "ConfigMap creation should succeed")
	assert.Equal(t, executor.OperationCreate, cmResult.Operation, "Should be create operation")
	assert.Equal(t, "ConfigMap", cmResult.Kind)
	t.Logf("ConfigMap created: %s/%s (operation: %s)", cmResult.Namespace, cmResult.ResourceName, cmResult.Operation)

	// Check Secret was created
	secretResult := result.ResourceResults[1]
	assert.Equal(t, "clusterSecret", secretResult.Name)
	assert.Equal(t, executor.StatusSuccess, secretResult.Status, "Secret creation should succeed")
	assert.Equal(t, executor.OperationCreate, secretResult.Operation)
	assert.Equal(t, "Secret", secretResult.Kind)
	t.Logf("Secret created: %s/%s (operation: %s)", secretResult.Namespace, secretResult.ResourceName, secretResult.Operation)

	// Verify ConfigMap exists in K8s
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	cmName := fmt.Sprintf("cluster-config-%s", clusterId)
	cm, err := k8sEnv.Client.GetResource(ctx, cmGVK, testNamespace, cmName)
	require.NoError(t, err, "ConfigMap should exist in K8s")
	assert.Equal(t, cmName, cm.GetName())

	// Verify ConfigMap data
	cmData, found, err := unstructured.NestedStringMap(cm.Object, "data")
	require.NoError(t, err)
	require.True(t, found, "ConfigMap should have data")
	assert.Equal(t, clusterId, cmData["cluster-id"])
	assert.Equal(t, "test-cluster", cmData["cluster-name"])
	assert.Equal(t, "us-east-1", cmData["region"])
	assert.Equal(t, "aws", cmData["provider"])
	assert.Equal(t, "Ready", cmData["phase"])
	t.Logf("ConfigMap data verified: %+v", cmData)

	// Verify ConfigMap labels
	cmLabels := cm.GetLabels()
	assert.Equal(t, clusterId, cmLabels["hyperfleet.io/cluster-id"])
	assert.Equal(t, "k8s-test-adapter", cmLabels["hyperfleet.io/managed-by"])

	// Verify Secret exists in K8s
	secretGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
	secretName := fmt.Sprintf("cluster-secret-%s", clusterId)
	secret, err := k8sEnv.Client.GetResource(ctx, secretGVK, testNamespace, secretName)
	require.NoError(t, err, "Secret should exist in K8s")
	assert.Equal(t, secretName, secret.GetName())
	t.Logf("Secret verified: %s", secretName)

	// Verify post action reported status
	statusResponses := mockAPI.GetStatusResponses()
	require.Len(t, statusResponses, 1, "Should have 1 status response")
	status := statusResponses[0]
	t.Logf("Status reported: %+v", status)

	if conditions, ok := status["conditions"].(map[string]interface{}); ok {
		if applied, ok := conditions["applied"].(map[string]interface{}); ok {
			assert.Equal(t, true, applied["status"], "Applied status should be true")
			assert.Equal(t, "ResourcesCreated", applied["reason"])
		}
	}
}

// TestExecutor_K8s_UpdateExistingResource tests updating an existing resource
func TestExecutor_K8s_UpdateExistingResource(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-update-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	clusterId := fmt.Sprintf("update-cluster-%d", time.Now().UnixNano())

	// Pre-create the ConfigMap
	existingCM := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("cluster-config-%s", clusterId),
				"namespace": testNamespace,
				"labels": map[string]interface{}{
					"hyperfleet.io/cluster-id": clusterId,
					"hyperfleet.io/managed-by": "k8s-test-adapter",
					"test":                     "executor-integration",
				},
			},
			"data": map[string]interface{}{
				"cluster-id": clusterId,
				"phase":      "Provisioning", // Old value
			},
		},
	}
	existingCM.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})

	ctx := context.Background()
	_, err := k8sEnv.Client.CreateResource(ctx, existingCM)
	require.NoError(t, err, "Failed to pre-create ConfigMap")
	t.Logf("Pre-created ConfigMap with phase=Provisioning")

	// Create executor
	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	// Only include ConfigMap resource for this test
	config.Spec.Resources = config.Spec.Resources[:1]

	apiClient := hyperfleet_api.NewClient()
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	// Execute - should update existing resource
	evt := createK8sTestEvent(clusterId)
	result := exec.Execute(ctx, evt)

	require.Equal(t, executor.StatusSuccess, result.Status, "Execution should succeed: %v", result.Error)

	// Verify it was an update operation
	require.Len(t, result.ResourceResults, 1)
	cmResult := result.ResourceResults[0]
	assert.Equal(t, executor.OperationUpdate, cmResult.Operation, "Should be update operation")
	t.Logf("Resource operation: %s", cmResult.Operation)

	// Verify ConfigMap was updated with new data
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	cmName := fmt.Sprintf("cluster-config-%s", clusterId)
	updatedCM, err := k8sEnv.Client.GetResource(ctx, cmGVK, testNamespace, cmName)
	require.NoError(t, err)

	cmData, _, _ := unstructured.NestedStringMap(updatedCM.Object, "data")
	assert.Equal(t, "Ready", cmData["phase"], "Phase should be updated to Ready")
	assert.Equal(t, "test-cluster", cmData["cluster-name"], "Should have new cluster-name field")
	t.Logf("Updated ConfigMap data: %+v", cmData)
}

// TestExecutor_K8s_DiscoveryByLabels tests resource discovery using label selectors
func TestExecutor_K8s_DiscoveryByLabels(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-discovery-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	clusterId := fmt.Sprintf("discovery-cluster-%d", time.Now().UnixNano())

	// Create config with label-based discovery
	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	// Modify to use label selector instead of byName
	config.Spec.Resources = []config_loader.Resource{
		{
			Name: "clusterConfigMap",
			Manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "cluster-config-{{ .clusterId }}",
					"namespace": testNamespace,
					"labels": map[string]interface{}{
						"hyperfleet.io/cluster-id": "{{ .clusterId }}",
						"hyperfleet.io/managed-by": "{{ .metadata.name }}",
						"app":                      "cluster-config",
					},
				},
				"data": map[string]interface{}{
					"cluster-id": "{{ .clusterId }}",
				},
			},
			Discovery: &config_loader.DiscoveryConfig{
				Namespace: testNamespace,
				BySelectors: &config_loader.SelectorConfig{
					LabelSelector: map[string]string{
						"hyperfleet.io/cluster-id": "{{ .clusterId }}",
						"app":                      "cluster-config",
					},
				},
			},
		},
	}

	apiClient := hyperfleet_api.NewClient()
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	ctx := context.Background()

	// First execution - should create
	evt := createK8sTestEvent(clusterId)
	result1 := exec.Execute(ctx, evt)
	require.Equal(t, executor.StatusSuccess, result1.Status)
	assert.Equal(t, executor.OperationCreate, result1.ResourceResults[0].Operation)
	t.Logf("First execution: %s", result1.ResourceResults[0].Operation)

	// Second execution - should find by labels and update
	evt2 := createK8sTestEvent(clusterId)
	result2 := exec.Execute(ctx, evt2)
	require.Equal(t, executor.StatusSuccess, result2.Status)
	assert.Equal(t, executor.OperationUpdate, result2.ResourceResults[0].Operation)
	t.Logf("Second execution: %s (discovered by labels)", result2.ResourceResults[0].Operation)
}

// TestExecutor_K8s_RecreateOnChange tests the recreateOnChange behavior
func TestExecutor_K8s_RecreateOnChange(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-recreate-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	clusterId := fmt.Sprintf("recreate-cluster-%d", time.Now().UnixNano())

	// Create config with recreateOnChange
	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	config.Spec.Resources = []config_loader.Resource{
		{
			Name:             "clusterConfigMap",
			RecreateOnChange: true, // Enable recreate
			Manifest: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "cluster-config-{{ .clusterId }}",
					"namespace": testNamespace,
					"labels": map[string]interface{}{
						"hyperfleet.io/cluster-id": "{{ .clusterId }}",
					},
				},
				"data": map[string]interface{}{
					"cluster-id": "{{ .clusterId }}",
				},
			},
			Discovery: &config_loader.DiscoveryConfig{
				Namespace: testNamespace,
				ByName:    "cluster-config-{{ .clusterId }}",
			},
		},
	}

	apiClient := hyperfleet_api.NewClient()
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	ctx := context.Background()

	// First execution - create
	evt := createK8sTestEvent(clusterId)
	result1 := exec.Execute(ctx, evt)
	require.Equal(t, executor.StatusSuccess, result1.Status)
	assert.Equal(t, executor.OperationCreate, result1.ResourceResults[0].Operation)

	// Get the original UID
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	cmName := fmt.Sprintf("cluster-config-%s", clusterId)
	originalCM, err := k8sEnv.Client.GetResource(ctx, cmGVK, testNamespace, cmName)
	require.NoError(t, err)
	originalUID := originalCM.GetUID()
	t.Logf("Original ConfigMap UID: %s", originalUID)

	// Second execution - should recreate (delete + create)
	evt2 := createK8sTestEvent(clusterId)
	result2 := exec.Execute(ctx, evt2)
	require.Equal(t, executor.StatusSuccess, result2.Status)
	assert.Equal(t, executor.OperationRecreate, result2.ResourceResults[0].Operation)
	t.Logf("Second execution: %s", result2.ResourceResults[0].Operation)

	// Verify it's a new resource (different UID)
	recreatedCM, err := k8sEnv.Client.GetResource(ctx, cmGVK, testNamespace, cmName)
	require.NoError(t, err)
	newUID := recreatedCM.GetUID()
	assert.NotEqual(t, originalUID, newUID, "Resource should have new UID after recreate")
	t.Logf("Recreated ConfigMap UID: %s (different from %s)", newUID, originalUID)
}

// TestExecutor_K8s_MultipleResourceTypes tests creating different resource types
func TestExecutor_K8s_MultipleResourceTypes(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-multi-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Execute with default config (ConfigMap + Secret)
	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	clusterId := fmt.Sprintf("multi-cluster-%d", time.Now().UnixNano())
	evt := createK8sTestEvent(clusterId)

	result := exec.Execute(context.Background(), evt)

	require.Equal(t, executor.StatusSuccess, result.Status)
	require.Len(t, result.ResourceResults, 2)

	// Verify both resources created
	for _, rr := range result.ResourceResults {
		assert.Equal(t, executor.StatusSuccess, rr.Status, "Resource %s should succeed", rr.Name)
		assert.Equal(t, executor.OperationCreate, rr.Operation)
		t.Logf("Created %s: %s/%s", rr.Kind, rr.Namespace, rr.ResourceName)
	}

	// Verify we can list resources by labels
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	selector := fmt.Sprintf("hyperfleet.io/cluster-id=%s", clusterId)
	list, err := k8sEnv.Client.ListResources(context.Background(), cmGVK, testNamespace, selector)
	require.NoError(t, err)
	assert.Len(t, list.Items, 1, "Should find 1 ConfigMap with cluster label")
}

// TestExecutor_K8s_ResourceCreationFailure tests handling of K8s API failures
func TestExecutor_K8s_ResourceCreationFailure(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	// Use a namespace that doesn't exist (should fail)
	nonExistentNamespace := "non-existent-namespace-12345"

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createK8sTestConfig(mockAPI.URL(), nonExistentNamespace)
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	evt := createK8sTestEvent("failure-test")
	result := exec.Execute(context.Background(), evt)

	// Should fail during resource creation
	assert.Equal(t, executor.StatusFailed, result.Status)
	// Phase will be post_actions because executor continues to post-actions after resource failure
	// This is correct behavior - we want to report errors even when resources fail
	assert.Equal(t, executor.PhasePostActions, result.Phase)
	assert.NotNil(t, result.Error)
	t.Logf("Expected failure: %v", result.Error)

	// Post actions should still execute to report error
	assert.NotEmpty(t, result.PostActionResults, "Post actions should still execute")
}

// TestExecutor_K8s_MultipleMatchingResources tests behavior when multiple resources match label selector
// Expected behavior: returns the first matching resource (order is not guaranteed by K8s API)
// TestExecutor_K8s_MultipleMatchingResources tests resource creation with multiple labeled resources.
// Note: Discovery-based update logic is not yet implemented. This test currently only verifies
// that creating a new resource works when other resources with similar labels exist.
// TODO: Implement proper discovery-based update logic and update this test accordingly.
func TestExecutor_K8s_MultipleMatchingResources(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-multi-match-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	clusterId := fmt.Sprintf("multi-match-%d", time.Now().UnixNano())
	ctx := context.Background()

	// Pre-create multiple ConfigMaps with the same labels but different names
	for i := 1; i <= 3; i++ {
		cm := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      fmt.Sprintf("config-%s-%d", clusterId, i),
					"namespace": testNamespace,
					"labels": map[string]interface{}{
						"hyperfleet.io/cluster-id": clusterId,
						"app":                      "multi-match-test",
					},
				},
				"data": map[string]interface{}{
					"index": fmt.Sprintf("%d", i),
				},
			},
		}
		cm.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
		_, err := k8sEnv.Client.CreateResource(ctx, cm)
		require.NoError(t, err, "Failed to pre-create ConfigMap %d", i)
	}
	t.Logf("Pre-created 3 ConfigMaps with same labels")

	// Create config WITHOUT discovery - just create a new resource
	// Discovery-based update logic is not yet implemented
	config := &config_loader.AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata: config_loader.Metadata{
			Name:      "multi-match-test",
			Namespace: testNamespace,
		},
		Spec: config_loader.AdapterConfigSpec{
			Adapter: config_loader.AdapterInfo{Version: "1.0.0"},
			HyperfleetAPI: config_loader.HyperfleetAPIConfig{
				Timeout: "10s", RetryAttempts: 1,
			},
			Params: []config_loader.Parameter{
				{Name: "hyperfleetApiBaseUrl", Source: "env.HYPERFLEET_API_BASE_URL", Required: true},
				{Name: "hyperfleetApiVersion", Default: "v1"},
				{Name: "clusterId", Source: "event.cluster_id", Required: true},
			},
			// No preconditions - this test focuses on resource creation
			Resources: []config_loader.Resource{
				{
					Name: "clusterConfig",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "config-{{ .clusterId }}-new",
							"namespace": testNamespace,
							"labels": map[string]interface{}{
								"hyperfleet.io/cluster-id": "{{ .clusterId }}",
								"app":                      "multi-match-test",
							},
						},
						"data": map[string]interface{}{
							"cluster-id": "{{ .clusterId }}",
							"created":    "true",
						},
					},
					// No Discovery - just create the resource
				},
			},
		},
	}

	apiClient := hyperfleet_api.NewClient()
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	evt := createK8sTestEvent(clusterId)
	result := exec.Execute(ctx, evt)

	require.Equal(t, executor.StatusSuccess, result.Status, "Execution should succeed: %v", result.Error)
	require.Len(t, result.ResourceResults, 1)

	// Should create a new resource (no discovery configured)
	rr := result.ResourceResults[0]
	assert.Equal(t, executor.OperationCreate, rr.Operation,
		"Should create new resource (no discovery configured)")
	t.Logf("Operation: %s on resource: %s/%s", rr.Operation, rr.Namespace, rr.ResourceName)

	// Verify we now have 4 ConfigMaps (3 pre-created + 1 new)
	cmGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	selector := fmt.Sprintf("hyperfleet.io/cluster-id=%s,app=multi-match-test", clusterId)
	list, err := k8sEnv.Client.ListResources(ctx, cmGVK, testNamespace, selector)
	require.NoError(t, err)
	assert.Len(t, list.Items, 4, "Should have 4 ConfigMaps (3 pre-created + 1 new)")

	// Verify the new one has the "created" field
	createdCount := 0
	for _, item := range list.Items {
		data, _, _ := unstructured.NestedStringMap(item.Object, "data")
		if data["created"] == "true" {
			createdCount++
			t.Logf("Created ConfigMap: %s", item.GetName())
		}
	}
	assert.Equal(t, 1, createdCount, "Exactly one ConfigMap should be created")
}

// TestExecutor_K8s_PostActionsAfterPreconditionNotMet tests that post actions execute even when preconditions don't match
func TestExecutor_K8s_PostActionsAfterPreconditionNotMet(t *testing.T) {
	k8sEnv := SetupK8sTestEnv(t)
	defer k8sEnv.Cleanup(t)

	testNamespace := fmt.Sprintf("executor-precond-fail-%d", time.Now().Unix())
	k8sEnv.CreateTestNamespace(t, testNamespace)
	defer k8sEnv.CleanupTestNamespace(t, testNamespace)

	mockAPI := newK8sTestAPIServer(t)
	defer mockAPI.Close()

	// Set cluster to Terminating phase (won't match condition)
	mockAPI.clusterResponse = map[string]interface{}{
		"metadata": map[string]interface{}{"name": "test-cluster"},
		"spec":     map[string]interface{}{"region": "us-east-1"},
		"status":   map[string]interface{}{"phase": "Terminating"}, // Won't match
	}

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createK8sTestConfig(mockAPI.URL(), testNamespace)
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithK8sClient(k8sEnv.Client).
		WithLogger(k8sEnv.Log).
		Build()
	require.NoError(t, err)

	clusterId := fmt.Sprintf("precond-fail-%d", time.Now().UnixNano())
	evt := createK8sTestEvent(clusterId)

	result := exec.Execute(context.Background(), evt)

	// Should be skipped (precondition not met)
	assert.Equal(t, executor.StatusSkipped, result.Status, "Should be skipped when precondition not met")
	assert.Contains(t, result.ErrorReason, "precondition", "Error should mention precondition")

	// Resources should NOT be created (skipped)
	assert.Empty(t, result.ResourceResults, "Resources should be skipped when precondition not met")

	// Post actions SHOULD still execute
	assert.NotEmpty(t, result.PostActionResults, "Post actions should execute even when precondition not met")
	t.Logf("Post action executed: %s (status: %s)", 
		result.PostActionResults[0].Name, result.PostActionResults[0].Status)

	// Verify status was reported with error info
	statusResponses := mockAPI.GetStatusResponses()
	require.Len(t, statusResponses, 1, "Should have reported status")
	status := statusResponses[0]
	t.Logf("Status reported after precondition failure: %+v", status)

	// Check that error info is in the status payload
	if conditions, ok := status["conditions"].(map[string]interface{}); ok {
		if applied, ok := conditions["applied"].(map[string]interface{}); ok {
			// Status should be false because execution failed
			assert.Equal(t, false, applied["status"], "Applied status should be false")
			assert.Equal(t, "PreconditionNotMet", applied["reason"], "Reason should indicate precondition failure")
		}
	}
}


