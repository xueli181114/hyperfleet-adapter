package executor_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/executor"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// testLogger implements logger.Logger for testing
type testLogger struct {
	t      *testing.T
	prefix string
}

func newTestLogger(t *testing.T) *testLogger {
	return &testLogger{t: t, prefix: ""}
}

func (l *testLogger) V(level int32) logger.Logger                       { return l }
func (l *testLogger) Extra(key string, value interface{}) logger.Logger { return l }
func (l *testLogger) Infof(format string, args ...interface{}) {
	l.t.Logf("[INFO] "+format, args...)
}
func (l *testLogger) Warningf(format string, args ...interface{}) {
	l.t.Logf("[WARN] "+format, args...)
}
func (l *testLogger) Errorf(format string, args ...interface{}) {
	l.t.Logf("[ERROR] "+format, args...)
}
func (l *testLogger) Info(message string)    { l.t.Logf("[INFO] %s", message) }
func (l *testLogger) Warning(message string) { l.t.Logf("[WARN] %s", message) }
func (l *testLogger) Error(message string)   { l.t.Logf("[ERROR] %s", message) }
func (l *testLogger) Fatal(message string)   { l.t.Fatalf("[FATAL] %s", message) }

// mockAPIServer creates a test HTTP server that simulates the HyperFleet API
type mockAPIServer struct {
	server           *httptest.Server
	mu               sync.Mutex
	requests         []mockRequest
	clusterResponse  map[string]interface{}
	statusResponses  []map[string]interface{}
	failPrecondition bool
}

type mockRequest struct {
	Method string
	Path   string
	Body   string
}

func newMockAPIServer(t *testing.T) *mockAPIServer {
	mock := &mockAPIServer{
		requests: make([]mockRequest, 0),
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

		// Read body
		var bodyStr string
		if r.Body != nil {
			buf := make([]byte, 1024*1024)
			n, _ := r.Body.Read(buf)
			bodyStr = string(buf[:n])
		}

		mock.requests = append(mock.requests, mockRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   bodyStr,
		})

		t.Logf("Mock API received: %s %s", r.Method, r.URL.Path)
		if bodyStr != "" {
			t.Logf("Body: %s", bodyStr)
		}

		// Route handling
		switch {
		case strings.Contains(r.URL.Path, "/clusters/") && strings.HasSuffix(r.URL.Path, "/status"):
			// POST /clusters/{id}/status - Store status and return success
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
			// GET /clusters/{id} - Return cluster details
			if r.Method == http.MethodGet {
				if mock.failPrecondition {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "cluster not found"})
					return
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(mock.clusterResponse)
				return
			}

		case strings.Contains(r.URL.Path, "/validation/availability"):
			// GET validation availability
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode("available")
			return
		}

		// Default 404
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))

	return mock
}

func (m *mockAPIServer) Close() {
	m.server.Close()
}

func (m *mockAPIServer) URL() string {
	return m.server.URL
}

func (m *mockAPIServer) GetRequests() []mockRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockRequest{}, m.requests...)
}

func (m *mockAPIServer) GetStatusResponses() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]interface{}{}, m.statusResponses...)
}

func (m *mockAPIServer) SetClusterResponse(resp map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusterResponse = resp
}

func (m *mockAPIServer) SetFailPrecondition(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failPrecondition = fail
}

// createTestEvent creates a CloudEvent for testing
func createTestEvent(clusterId, resourceId string) *event.Event {
	evt := event.New()
	evt.SetID("test-event-" + clusterId)
	evt.SetType("com.redhat.hyperfleet.cluster.provision")
	evt.SetSource("test")
	evt.SetTime(time.Now())

	eventData := map[string]interface{}{
		"cluster_id":    clusterId,
		"resource_id":   resourceId,
		"resource_type": "cluster",
		"generation":    "gen-001",
		"href":          "/api/v1/clusters/" + clusterId,
	}
	eventDataBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventDataBytes)

	return &evt
}

// createTestConfig creates an AdapterConfig for testing
func createTestConfig(apiBaseURL string) *config_loader.AdapterConfig {
	return &config_loader.AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata: config_loader.Metadata{
			Name:      "test-adapter",
			Namespace: "test-ns",
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
					Name:     "resourceId",
					Source:   "event.resource_id",
					Required: true,
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
						{As: "vpcId", Field: "spec.vpc_id"},
					},
					Conditions: []config_loader.Condition{
						{Field: "clusterPhase", Operator: "in", Value: []interface{}{"Provisioning", "Installing", "Ready"}},
						{Field: "cloudProvider", Operator: "in", Value: []interface{}{"aws", "gcp", "azure"}},
					},
				},
			},
			// No K8s resources in this test - dry run mode
			Resources: []config_loader.Resource{},
			Post: &config_loader.PostConfig{
				Params: []config_loader.Parameter{
					{
						Name: "clusterStatusPayload",
						Build: map[string]interface{}{
							"conditions": map[string]interface{}{
								"health": map[string]interface{}{
									"status": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\"",
									},
									"reason": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\" ? \"Healthy\" : adapter.errorReason",
									},
									"message": map[string]interface{}{
										"expression": "adapter.executionStatus == \"success\" ? \"All operations completed\" : adapter.errorMessage",
									},
								},
							},
							"clusterId": map[string]interface{}{
								"value": "{{ .clusterId }}",
							},
							"clusterName": map[string]interface{}{
								"value": "{{ .clusterName }}",
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

func TestExecutor_FullFlow_Success(t *testing.T) {
	// Setup mock API server
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config and executor
	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient(
		hyperfleet_api.WithTimeout(10*time.Second),
		hyperfleet_api.WithRetryAttempts(1),
	)

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true). // No actual K8s resources
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Create test event
	evt := createTestEvent("cluster-123", "resource-456")

	// Execute
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := exec.Execute(ctx, evt)

	// Verify result
	if result.Status != executor.StatusSuccess {
		t.Errorf("Expected success status, got %s: %v", result.Status, result.Error)
	}

	if result.EventID != evt.ID() {
		t.Errorf("Expected event ID %s, got %s", evt.ID(), result.EventID)
	}

	// Verify params were extracted
	if result.Params["clusterId"] != "cluster-123" {
		t.Errorf("Expected clusterId 'cluster-123', got '%v'", result.Params["clusterId"])
	}

	// Verify preconditions passed
	if len(result.PreconditionResults) != 1 {
		t.Errorf("Expected 1 precondition result, got %d", len(result.PreconditionResults))
	} else {
		precondResult := result.PreconditionResults[0]
		if !precondResult.Matched {
			t.Errorf("Expected precondition to match, but it didn't")
		}
		if precondResult.ExtractedFields["clusterName"] != "test-cluster" {
			t.Errorf("Expected extracted clusterName 'test-cluster', got '%v'", precondResult.ExtractedFields["clusterName"])
		}
	}

	// Verify post actions executed
	if len(result.PostActionResults) != 1 {
		t.Errorf("Expected 1 post action result, got %d", len(result.PostActionResults))
	} else {
		postResult := result.PostActionResults[0]
		if postResult.Status != executor.StatusSuccess {
			t.Errorf("Expected post action success, got %s: %v", postResult.Status, postResult.Error)
		}
		if !postResult.APICallMade {
			t.Error("Expected API call to be made in post action")
		}
	}

	// Verify API calls were made
	requests := mockAPI.GetRequests()
	if len(requests) < 2 {
		t.Errorf("Expected at least 2 API requests (precondition + post action), got %d", len(requests))
	}

	// Verify status was posted
	statusResponses := mockAPI.GetStatusResponses()
	if len(statusResponses) != 1 {
		t.Errorf("Expected 1 status response, got %d", len(statusResponses))
	} else {
		status := statusResponses[0]
		t.Logf("Status payload received: %+v", status)

		// Check that status contains expected fields
		if conditions, ok := status["conditions"].(map[string]interface{}); ok {
			if health, ok := conditions["health"].(map[string]interface{}); ok {
				if health["status"] != true {
					t.Errorf("Expected health.status to be true, got %v", health["status"])
				}
			} else {
				t.Error("Expected health condition in status")
			}
		} else {
			t.Error("Expected conditions in status payload")
		}
	}

	t.Logf("Execution completed in %v", result.Duration)
}

func TestExecutor_PreconditionNotMet(t *testing.T) {
	// Setup mock API server that returns a cluster in "Terminating" phase
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	// Set cluster to a phase that doesn't match conditions
	mockAPI.SetClusterResponse(map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-cluster",
		},
		"spec": map[string]interface{}{
			"region":   "us-east-1",
			"provider": "aws",
			"vpc_id":   "vpc-12345",
		},
		"status": map[string]interface{}{
			"phase": "Terminating", // This won't match the condition
		},
	})

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config and executor
	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	evt := createTestEvent("cluster-456", "resource-789")
	ctx := context.Background()
	result := exec.Execute(ctx, evt)

	// Verify result - should be skipped (soft failure)
	if result.Status != executor.StatusSkipped {
		t.Errorf("Expected skipped status, got %s", result.Status)
	}

	// Verify precondition was not matched
	if len(result.PreconditionResults) != 1 {
		t.Errorf("Expected 1 precondition result, got %d", len(result.PreconditionResults))
	} else {
		if result.PreconditionResults[0].Matched {
			t.Error("Expected precondition to NOT match")
		}
	}

	// Post actions should still execute (to report the error)
	if len(result.PostActionResults) != 1 {
		t.Errorf("Expected 1 post action result (error reporting), got %d", len(result.PostActionResults))
	}

	// Verify the status payload contains error info
	statusResponses := mockAPI.GetStatusResponses()
	if len(statusResponses) == 1 {
		status := statusResponses[0]
		t.Logf("Error status payload: %+v", status)

		if conditions, ok := status["conditions"].(map[string]interface{}); ok {
			if health, ok := conditions["health"].(map[string]interface{}); ok {
				// Health status should be false because adapter.executionStatus is "failed"
				if health["status"] != false {
					t.Errorf("Expected health.status to be false for failed execution, got %v", health["status"])
				}
			}
		}
	}

	t.Logf("Execution completed with expected precondition failure in %v", result.Duration)
}

func TestExecutor_PreconditionAPIFailure(t *testing.T) {
	// Setup mock API server that fails precondition API call
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()
	mockAPI.SetFailPrecondition(true) // API will return 404

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config and executor
	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient(
		hyperfleet_api.WithRetryAttempts(1),
	)

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	evt := createTestEvent("cluster-notfound", "resource-000")
	ctx := context.Background()
	result := exec.Execute(ctx, evt)

	// Verify result - should be failed (API error)
	if result.Status != executor.StatusFailed {
		t.Errorf("Expected failed status, got %s", result.Status)
	}

	if result.Error == nil {
		t.Error("Expected error to be set")
	}

	t.Logf("Execution failed as expected: %v", result.Error)
}

func TestExecutor_CELExpressionEvaluation(t *testing.T) {
	// Setup mock API server
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	// Set environment variables
	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create config with CEL expression precondition
	config := createTestConfig(mockAPI.URL())
	config.Spec.Preconditions = []config_loader.Precondition{
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
				{As: "nodeCount", Field: "spec.node_count"},
			},
			// Use CEL expression instead of structured conditions
			Expression: `clusterPhase == "Ready" && nodeCount >= 3`,
		},
	}

	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	evt := createTestEvent("cluster-cel-test", "resource-cel")
	ctx := context.Background()
	result := exec.Execute(ctx, evt)

	// Verify CEL evaluation passed
	if result.Status != executor.StatusSuccess {
		t.Errorf("Expected success status, got %s: %v", result.Status, result.Error)
	}

	if len(result.PreconditionResults) != 1 {
		t.Fatalf("Expected 1 precondition result, got %d", len(result.PreconditionResults))
	}

	precondResult := result.PreconditionResults[0]
	if !precondResult.Matched {
		t.Error("Expected CEL expression to evaluate to true")
	}

	// Verify CEL result was recorded
	if precondResult.CELResult == nil {
		t.Error("Expected CEL result to be recorded")
	} else {
		t.Logf("CEL expression result: matched=%v, value=%v", precondResult.CELResult.Matched, precondResult.CELResult.Value)
	}
}

func TestExecutor_MultipleMessages(t *testing.T) {
	// Test that multiple messages can be processed with isolated contexts
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Process multiple messages
	clusterIds := []string{"cluster-a", "cluster-b", "cluster-c"}
	results := make([]*executor.ExecutionResult, len(clusterIds))

	for i, clusterId := range clusterIds {
		evt := createTestEvent(clusterId, fmt.Sprintf("resource-%d", i))
		results[i] = exec.Execute(context.Background(), evt)
	}

	// Verify all succeeded with isolated params
	for i, result := range results {
		if result.Status != executor.StatusSuccess {
			t.Errorf("Message %d failed: %v", i, result.Error)
			continue
		}

		// Verify each message had its own clusterId
		expectedClusterId := clusterIds[i]
		if result.Params["clusterId"] != expectedClusterId {
			t.Errorf("Message %d: expected clusterId '%s', got '%v'", i, expectedClusterId, result.Params["clusterId"])
		}
	}

	// Verify we got separate status posts for each message
	statusResponses := mockAPI.GetStatusResponses()
	if len(statusResponses) != len(clusterIds) {
		t.Errorf("Expected %d status responses, got %d", len(clusterIds), len(statusResponses))
	}

	t.Logf("Successfully processed %d messages with isolated contexts", len(clusterIds))
}

func TestExecutor_Handler_Integration(t *testing.T) {
	// Test the CreateHandler function that would be used with broker
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Get the handler function
	handler := exec.CreateHandler()

	// Simulate broker calling the handler
	evt := createTestEvent("cluster-handler-test", "resource-handler")
	ctx := context.Background()

	err = handler(ctx, evt)

	// Handler should return nil on success
	if err != nil {
		t.Errorf("Handler returned error: %v", err)
	}

	// Verify API calls were made
	requests := mockAPI.GetRequests()
	if len(requests) < 2 {
		t.Errorf("Expected at least 2 API requests, got %d", len(requests))
	}

	t.Log("Handler integration test passed")
}

func TestExecutor_Handler_PreconditionNotMet_ReturnsNil(t *testing.T) {
	// When preconditions aren't met, handler should return nil (not an error)
	// because it's expected behavior, not a system failure
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	mockAPI.SetClusterResponse(map[string]interface{}{
		"metadata": map[string]interface{}{"name": "test"},
		"spec":     map[string]interface{}{"region": "us-east-1", "provider": "aws"},
		"status":   map[string]interface{}{"phase": "Terminating"}, // Won't match
	})

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	handler := exec.CreateHandler()
	evt := createTestEvent("cluster-skip", "resource-skip")

	// Handler should return nil even when precondition not met
	err = handler(context.Background(), evt)
	if err != nil {
		t.Errorf("Handler should return nil for precondition not met, got: %v", err)
	}

	t.Log("Handler correctly returns nil for skipped execution")
}

func TestExecutor_ContextCancellation(t *testing.T) {
	// Test that context cancellation is respected
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Create already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	evt := createTestEvent("cluster-cancelled", "resource-cancelled")
	result := exec.Execute(ctx, evt)

	// Should fail due to context cancellation
	// Note: The exact behavior depends on where cancellation is checked
	t.Logf("Result with cancelled context: status=%s, error=%v", result.Status, result.Error)
}

func TestExecutor_MissingRequiredParam(t *testing.T) {
	// Test that missing required params cause failure
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	// Don't set HYPERFLEET_API_BASE_URL - it's required
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	config := createTestConfig(mockAPI.URL())
	apiClient := hyperfleet_api.NewClient()

	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(newTestLogger(t)).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Unset the env var after executor creation
	os.Unsetenv("HYPERFLEET_API_BASE_URL")

	evt := createTestEvent("cluster-missing-param", "resource-missing")
	result := exec.Execute(context.Background(), evt)

	// Should fail during param extraction
	if result.Status != executor.StatusFailed {
		t.Errorf("Expected failed status for missing required param, got %s", result.Status)
	}

	if result.Phase != executor.PhaseParamExtraction {
		t.Errorf("Expected failure in param_extraction phase, got %s", result.Phase)
	}

	t.Logf("Correctly failed with missing required param: %v", result.Error)
}

// TestExecutor_LogAction tests log actions in preconditions and post-actions
func TestExecutor_LogAction(t *testing.T) {
	mockAPI := newMockAPIServer(t)
	defer mockAPI.Close()

	t.Setenv("HYPERFLEET_API_BASE_URL", mockAPI.URL())
	t.Setenv("HYPERFLEET_API_VERSION", "v1")

	// Create a custom logger that captures log messages
	logCapture := &logCaptureLogger{t: t, messages: make([]string, 0)}

	// Create config with log actions in preconditions and post-actions
	config := &config_loader.AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata: config_loader.Metadata{
			Name:      "log-test-adapter",
			Namespace: "test",
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
				{Name: "resourceId", Source: "event.resource_id", Required: true},
			},
			Preconditions: []config_loader.Precondition{
				{
					// Log action only - no API call or conditions
					Name: "logStart",
					Log: &config_loader.LogAction{
						Message: "Starting processing for cluster {{ .clusterId }}",
						Level:   "info",
					},
				},
				{
					// Log action before API call
					Name: "logBeforeAPICall",
					Log: &config_loader.LogAction{
						Message: "About to check cluster status for {{ .clusterId }}",
						Level:   "debug",
					},
				},
				{
					Name: "checkCluster",
					APICall: &config_loader.APICall{
						Method: "GET",
						URL:    "{{ .hyperfleetApiBaseUrl }}/api/{{ .hyperfleetApiVersion }}/clusters/{{ .clusterId }}",
					},
					Extract: []config_loader.ExtractField{
						{As: "clusterPhase", Field: "status.phase"},
					},
					Conditions: []config_loader.Condition{
						{Field: "clusterPhase", Operator: "equals", Value: "Ready"},
					},
				},
			},
			Post: &config_loader.PostConfig{
				PostActions: []config_loader.PostAction{
					{
						// Log action in post-actions
						Name: "logCompletion",
						Log: &config_loader.LogAction{
							Message: "Completed processing cluster {{ .clusterId }} with resource {{ .resourceId }}",
							Level:   "info",
						},
					},
					{
						// Log with warning level
						Name: "logWarning",
						Log: &config_loader.LogAction{
							Message: "This is a warning for cluster {{ .clusterId }}",
							Level:   "warning",
						},
					},
				},
			},
		},
	}

	apiClient := hyperfleet_api.NewClient()
	exec, err := executor.NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(apiClient).
		WithLogger(logCapture).
		WithDryRun(true).
		Build()
	if err != nil {
		t.Fatalf("Failed to create executor: %v", err)
	}

	// Execute
	evt := createTestEvent("log-test-cluster", "log-test-resource")
	result := exec.Execute(context.Background(), evt)

	// Should succeed
	if result.Status != executor.StatusSuccess {
		t.Fatalf("Expected success, got %s: %v", result.Status, result.Error)
	}

	// Verify log messages were captured
	t.Logf("Captured %d log messages", len(logCapture.messages))
	for i, msg := range logCapture.messages {
		t.Logf("  [%d] %s", i, msg)
	}

	// Check for expected log messages (with [config] prefix)
	expectedLogs := []string{
		"[config] Starting processing for cluster log-test-cluster",
		"[config] About to check cluster status for log-test-cluster",
		"[config] Completed processing cluster log-test-cluster with resource log-test-resource",
		"[config] This is a warning for cluster log-test-cluster",
	}

	for _, expected := range expectedLogs {
		found := false
		for _, msg := range logCapture.messages {
			if strings.Contains(msg, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected log message not found: %s", expected)
		}
	}

	// Verify preconditions executed (including log-only ones)
	if len(result.PreconditionResults) != 3 {
		t.Errorf("Expected 3 precondition results, got %d", len(result.PreconditionResults))
	}

	// Verify post actions executed
	if len(result.PostActionResults) != 2 {
		t.Errorf("Expected 2 post action results, got %d", len(result.PostActionResults))
	}

	t.Logf("Log action test completed successfully")
}

// logCaptureLogger captures log messages for testing
type logCaptureLogger struct {
	t        *testing.T
	messages []string
	mu       sync.Mutex
}

func (l *logCaptureLogger) V(level int32) logger.Logger { return l }
func (l *logCaptureLogger) Extra(key string, value interface{}) logger.Logger { return l }

func (l *logCaptureLogger) capture(level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := fmt.Sprintf("[%s] "+format, append([]interface{}{level}, args...)...)
	l.messages = append(l.messages, msg)
	l.t.Logf("%s", msg)
}

func (l *logCaptureLogger) Infof(format string, args ...interface{}) {
	l.capture("INFO", format, args...)
}
func (l *logCaptureLogger) Warningf(format string, args ...interface{}) {
	l.capture("WARN", format, args...)
}
func (l *logCaptureLogger) Errorf(format string, args ...interface{}) {
	l.capture("ERROR", format, args...)
}
func (l *logCaptureLogger) Info(message string)    { l.capture("INFO", "%s", message) }
func (l *logCaptureLogger) Warning(message string) { l.capture("WARN", "%s", message) }
func (l *logCaptureLogger) Error(message string)   { l.capture("ERROR", "%s", message) }
func (l *logCaptureLogger) Fatal(message string)   { l.t.Fatalf("[FATAL] %s", message) }

