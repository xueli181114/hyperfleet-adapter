package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockAPIClient creates a new mock API client for convenience
func newMockAPIClient() *hyperfleet_api.MockClient {
	return hyperfleet_api.NewMockClient()
}

// TestNewExecutor tests the NewExecutor function
func TestNewExecutor(t *testing.T) {
	tests := []struct {
		name        string
		config      *ExecutorConfig
		expectError bool
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
		},
		{
			name: "missing adapter config",
			config: &ExecutorConfig{
				APIClient: newMockAPIClient(),
				Logger:    logger.NewTestLogger(),
			},
			expectError: true,
		},
		{
			name: "missing API client",
			config: &ExecutorConfig{
				Config: &config_loader.Config{},
				Logger: logger.NewTestLogger(),
			},
			expectError: true,
		},
		{
			name: "missing logger",
			config: &ExecutorConfig{
				Config:    &config_loader.Config{},
				APIClient: newMockAPIClient(),
			},
			expectError: true,
		},
		{
			name: "valid config",
			config: &ExecutorConfig{
				Config:          &config_loader.Config{},
				APIClient:       newMockAPIClient(),
				TransportClient: k8s_client.NewMockK8sClient(),
				Logger:          logger.NewTestLogger(),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewExecutor(tt.config)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExecutorBuilder(t *testing.T) {
	config := &config_loader.Config{
		Adapter: config_loader.AdapterInfo{
			Name:    "test-adapter",
			Version: "1.0.0",
		},
	}

	exec, err := NewBuilder().
		WithConfig(config).
		WithAPIClient(newMockAPIClient()).
		WithTransportClient(k8s_client.NewMockK8sClient()).
		WithLogger(logger.NewTestLogger()).
		Build()

	require.NoError(t, err)
	require.NotNil(t, exec)
}

func TestExecutionContext(t *testing.T) {
	ctx := context.Background()
	eventData := map[string]interface{}{
		"id": "test-cluster",
	}

	execCtx := NewExecutionContext(ctx, eventData, nil)

	assert.Equal(t, "test-cluster", execCtx.EventData["id"])
	assert.Empty(t, execCtx.Params)
	assert.Empty(t, execCtx.Resources)
	assert.Equal(t, string(StatusSuccess), execCtx.Adapter.ExecutionStatus)
}

func TestExecutionContext_SetError(t *testing.T) {
	ctx := context.Background()
	execCtx := NewExecutionContext(ctx, map[string]interface{}{}, nil)
	execCtx.SetError("TestReason", "Test message")

	assert.Equal(t, string(StatusFailed), execCtx.Adapter.ExecutionStatus)
	assert.Equal(t, "TestReason", execCtx.Adapter.ErrorReason)
	assert.Equal(t, "Test message", execCtx.Adapter.ErrorMessage)
}

func TestExecutionContext_EvaluationTracking(t *testing.T) {
	ctx := context.Background()
	execCtx := NewExecutionContext(ctx, map[string]interface{}{}, nil)

	// Verify evaluations are empty initially
	assert.Empty(t, execCtx.Evaluations, "expected empty evaluations initially")

	// Add a CEL evaluation
	execCtx.AddCELEvaluation(PhasePreconditions, "check-status", "status == 'active'", true)

	require.Len(t, execCtx.Evaluations, 1, "evaluation")

	eval := execCtx.Evaluations[0]
	assert.Equal(t, PhasePreconditions, eval.Phase)
	assert.Equal(t, "check-status", eval.Name)
	assert.Equal(t, EvaluationTypeCEL, eval.EvaluationType)
	assert.Equal(t, "status == 'active'", eval.Expression)
	assert.True(t, eval.Matched)

	// Add a conditions evaluation with field results (using criteria.EvaluationResult)
	fieldResults := map[string]criteria.EvaluationResult{
		"status.phase": {
			Field:         "status.phase",
			Operator:      criteria.OperatorEquals,
			ExpectedValue: "Running",
			FieldValue:    "Running",
			Matched:       true,
		},
		"replicas": {
			Field:         "replicas",
			Operator:      criteria.OperatorGreaterThan,
			ExpectedValue: 0,
			FieldValue:    3,
			Matched:       true,
		},
	}
	execCtx.AddConditionsEvaluation(PhasePreconditions, "check-replicas", true, fieldResults)

	require.Len(t, execCtx.Evaluations, 2, "evaluations")

	condEval := execCtx.Evaluations[1]
	assert.Equal(t, EvaluationTypeConditions, condEval.EvaluationType)
	assert.Len(t, condEval.FieldResults, 2)

	// Verify lookup by field name works
	assert.Contains(t, condEval.FieldResults, "status.phase")
	assert.Equal(t, "Running", condEval.FieldResults["status.phase"].FieldValue)

	assert.Contains(t, condEval.FieldResults, "replicas")
	assert.Equal(t, 3, condEval.FieldResults["replicas"].FieldValue)
}

func TestExecutionContext_GetEvaluationsByPhase(t *testing.T) {
	ctx := context.Background()
	execCtx := NewExecutionContext(ctx, map[string]interface{}{}, nil)

	// Add evaluations in different phases
	execCtx.AddCELEvaluation(PhasePreconditions, "precond-1", "true", true)
	execCtx.AddCELEvaluation(PhasePreconditions, "precond-2", "false", false)
	execCtx.AddCELEvaluation(PhasePostActions, "post-1", "true", true)

	// Get preconditions evaluations
	precondEvals := execCtx.GetEvaluationsByPhase(PhasePreconditions)
	require.Len(t, precondEvals, 2, "precondition evaluations")

	// Get post actions evaluations
	postEvals := execCtx.GetEvaluationsByPhase(PhasePostActions)
	require.Len(t, postEvals, 1, "post action evaluation")

	// Get resources evaluations (none)
	resourceEvals := execCtx.GetEvaluationsByPhase(PhaseResources)
	require.Len(t, resourceEvals, 0, "resource evaluations")
}

func TestExecutionContext_GetFailedEvaluations(t *testing.T) {
	ctx := context.Background()
	execCtx := NewExecutionContext(ctx, map[string]interface{}{}, nil)

	// Add mixed evaluations
	execCtx.AddCELEvaluation(PhasePreconditions, "passed-1", "true", true)
	execCtx.AddCELEvaluation(PhasePreconditions, "failed-1", "false", false)
	execCtx.AddCELEvaluation(PhasePreconditions, "passed-2", "true", true)
	execCtx.AddCELEvaluation(PhasePostActions, "failed-2", "false", false)

	failedEvals := execCtx.GetFailedEvaluations()
	require.Len(t, failedEvals, 2, "failed evaluations")

	// Verify the failed ones are correct
	names := make(map[string]bool)
	for _, eval := range failedEvals {
		names[eval.Name] = true
	}
	assert.True(t, names["failed-1"], "failed-1")
	assert.True(t, names["failed-2"], "failed-2")
}

func TestExecutorError(t *testing.T) {
	err := NewExecutorError(PhasePreconditions, "test-step", "test message", nil)

	expected := "[preconditions] test-step: test message"
	if err.Error() != expected {
		t.Errorf("expected '%s', got '%s'", expected, err.Error())
	}

	// With wrapped error
	wrappedErr := NewExecutorError(PhaseResources, "create", "failed to create", context.Canceled)
	assert.Equal(t, context.Canceled, wrappedErr.Unwrap())
}

func TestExecute_ParamExtraction(t *testing.T) {
	// Set up environment variable for test
	t.Setenv("TEST_VAR", "test-value")

	config := &config_loader.Config{
		Adapter: config_loader.AdapterInfo{
			Name:    "test-adapter",
			Version: "1.0.0",
		},
		Params: []config_loader.Parameter{
			{
				Name:     "testParam",
				Source:   "env.TEST_VAR",
				Required: true,
			},
			{
				Name:     "eventParam",
				Source:   "event.id",
				Required: true,
			},
		},
	}

	exec, err := NewBuilder().
		WithConfig(config).
		WithAPIClient(newMockAPIClient()).
		WithTransportClient(k8s_client.NewMockK8sClient()).
		WithLogger(logger.NewTestLogger()).
		Build()
	if err != nil {
		t.Fatalf("unexpected error creating executor: %v", err)
	}

	// Create event data
	eventData := map[string]interface{}{
		"id": "cluster-456",
	}

	// Execute with event ID in context
	ctx := logger.WithEventID(context.Background(), "test-event-123")
	result := exec.Execute(ctx, eventData)

	// Check result

	// Check extracted params
	if result.Params["testParam"] != "test-value" {
		t.Errorf("expected testParam to be 'test-value', got '%v'", result.Params["testParam"])
	}

	if result.Params["eventParam"] != "cluster-456" {
		t.Errorf("expected eventParam to be 'cluster-456', got '%v'", result.Params["eventParam"])
	}
}

func TestParamExtractor(t *testing.T) {
	t.Setenv("TEST_ENV", "env-value")

	evt := event.New()
	eventData := map[string]interface{}{
		"id": "test-cluster",
		"nested": map[string]interface{}{
			"value": "nested-value",
		},
	}
	_ = evt.SetData(event.ApplicationJSON, eventData)

	tests := []struct {
		name        string
		params      []config_loader.Parameter
		expectKey   string
		expectValue interface{}
		expectError bool
	}{
		{
			name: "extract from env",
			params: []config_loader.Parameter{
				{Name: "envVar", Source: "env.TEST_ENV"},
			},
			expectKey:   "envVar",
			expectValue: "env-value",
		},
		{
			name: "extract from event",
			params: []config_loader.Parameter{
				{Name: "clusterId", Source: "event.id"},
			},
			expectKey:   "clusterId",
			expectValue: "test-cluster",
		},
		{
			name: "extract nested from event",
			params: []config_loader.Parameter{
				{Name: "nestedVal", Source: "event.nested.value"},
			},
			expectKey:   "nestedVal",
			expectValue: "nested-value",
		},
		{
			name: "use default for missing optional",
			params: []config_loader.Parameter{
				{Name: "optional", Source: "env.MISSING", Default: "default-val"},
			},
			expectKey:   "optional",
			expectValue: "default-val",
		},
		{
			name: "fail on missing required",
			params: []config_loader.Parameter{
				{Name: "required", Source: "env.MISSING", Required: true},
			},
			expectError: true,
		},
		{
			name: "extract from config",
			params: []config_loader.Parameter{
				{Name: "adapterName", Source: "config.adapter.name"},
			},
			expectKey:   "adapterName",
			expectValue: "test",
		},
		{
			name: "extract nested from config",
			params: []config_loader.Parameter{
				{Name: "adapterVersion", Source: "config.adapter.version"},
			},
			expectKey:   "adapterVersion",
			expectValue: "1.0.0",
		},
		{
			name: "use default for missing optional config field",
			params: []config_loader.Parameter{
				{Name: "optional", Source: "config.nonexistent", Default: "fallback"},
			},
			expectKey:   "optional",
			expectValue: "fallback",
		},
		{
			name: "fail on missing required config field",
			params: []config_loader.Parameter{
				{Name: "required", Source: "config.nonexistent", Required: true},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fresh context for each test
			execCtx := NewExecutionContext(context.Background(), eventData, nil)

			// Create config with test params
			config := &config_loader.Config{
				Adapter: config_loader.AdapterInfo{
					Name:    "test",
					Version: "1.0.0",
				},
				Params: tt.params,
			}

			// Extract params using pure function
			configMap, err := configToMap(config)
			require.NoError(t, err)
			err = extractConfigParams(config, execCtx, configMap)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.expectKey != "" {
				if execCtx.Params[tt.expectKey] != tt.expectValue {
					t.Errorf("expected %s=%v, got %v", tt.expectKey, tt.expectValue, execCtx.Params[tt.expectKey])
				}
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		data        map[string]interface{}
		expected    string
		expectError bool
	}{
		{
			name:     "simple variable",
			template: "Hello {{ .name }}!",
			data:     map[string]interface{}{"name": "World"},
			expected: "Hello World!",
		},
		{
			name:     "no template",
			template: "plain text",
			data:     map[string]interface{}{},
			expected: "plain text",
		},
		{
			name:     "nested variable",
			template: "{{ .cluster.id }}",
			data: map[string]interface{}{
				"cluster": map[string]interface{}{"id": "test-123"},
			},
			expected: "test-123",
		},
		{
			name:        "missing variable",
			template:    "{{ .missing }}",
			data:        map[string]interface{}{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate(tt.template, tt.data)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

// TestSequentialExecution_Preconditions tests that preconditions stop on first failure
func TestSequentialExecution_Preconditions(t *testing.T) {
	tests := []struct {
		name             string
		preconditions    []config_loader.Precondition
		expectedResults  int // number of results before stopping
		expectError      bool
		expectNotMet     bool
		expectedLastName string
	}{
		{
			name: "all pass - all executed",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "precond1"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond2"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond3"}, Expression: "true"},
			},
			expectedResults:  3,
			expectError:      false,
			expectNotMet:     false,
			expectedLastName: "precond3",
		},
		{
			name: "first fails - stops immediately",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "precond1"}, Expression: "false"},
				{ActionBase: config_loader.ActionBase{Name: "precond2"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond3"}, Expression: "true"},
			},
			expectedResults:  1,
			expectError:      false,
			expectNotMet:     true,
			expectedLastName: "precond1",
		},
		{
			name: "second fails - first executes, stops at second",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "precond1"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond2"}, Expression: "false"},
				{ActionBase: config_loader.ActionBase{Name: "precond3"}, Expression: "true"},
			},
			expectedResults:  2,
			expectError:      false,
			expectNotMet:     true,
			expectedLastName: "precond2",
		},
		{
			name: "third fails - first two execute, stops at third",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "precond1"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond2"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "precond3"}, Expression: "false"},
			},
			expectedResults:  3,
			expectError:      false,
			expectNotMet:     true,
			expectedLastName: "precond3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_loader.Config{
				Adapter: config_loader.AdapterInfo{
					Name:    "test-adapter",
					Version: "1.0.0",
				},
				Preconditions: tt.preconditions,
			}

			exec, err := NewBuilder().
				WithConfig(config).
				WithAPIClient(newMockAPIClient()).
				WithTransportClient(k8s_client.NewMockK8sClient()).
				WithLogger(logger.NewTestLogger()).
				Build()
			if err != nil {
				t.Fatalf("unexpected error creating executor: %v", err)
			}

			ctx := logger.WithEventID(context.Background(), "test-event-seq")
			result := exec.Execute(ctx, map[string]interface{}{})

			// Verify number of precondition results
			assert.Equal(t, tt.expectedResults, len(result.PreconditionResults),
				"unexpected precondition result count")

			// Verify last executed precondition name
			if len(result.PreconditionResults) > 0 {
				lastResult := result.PreconditionResults[len(result.PreconditionResults)-1]
				if lastResult.Name != tt.expectedLastName {
					t.Errorf("expected last precondition to be '%s', got '%s'",
						tt.expectedLastName, lastResult.Name)
				}
			}

			// Verify error/not met status
			if tt.expectNotMet {
				// Precondition not met is a successful execution, just with resources skipped
				assert.Equal(t, StatusSuccess, result.Status, "expected status Success (precondition not met is valid outcome)")
				assert.True(t, result.ResourcesSkipped, "ResourcesSkipped")
				assert.NotEmpty(t, result.SkipReason, "expected SkipReason to be set")
			}

			if !tt.expectNotMet && !tt.expectError {
				assert.Equal(t, StatusSuccess, result.Status, "expected status Success")
			}
		})
	}
}

// TestSequentialExecution_Resources tests that resources stop on first failure
func TestSequentialExecution_Resources(t *testing.T) {
	// Note: This test uses dry-run mode and focuses on the sequential logic
	// without requiring a real K8s cluster. Resource sequential execution is better
	// tested in integration tests with real K8s API.

	tests := []struct {
		name            string
		resources       []config_loader.Resource
		expectedResults int
		expectFailure   bool
	}{
		{
			name: "single resource with valid manifest",
			resources: []config_loader.Resource{
				{
					Name: "resource1",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "test-cm",
						},
					},
				},
			},
			expectedResults: 1,
			expectFailure:   false,
		},
		{
			name: "first resource has no manifest - stops immediately",
			resources: []config_loader.Resource{
				{Name: "resource1"}, // No manifest at all
				{
					Name: "resource2",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name": "test-cm2",
						},
					},
				},
			},
			expectedResults: 1, // Stops at first failure
			expectFailure:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_loader.Config{
				Adapter: config_loader.AdapterInfo{
					Name:    "test-adapter",
					Version: "1.0.0",
				},
				Resources: tt.resources,
			}

			exec, err := NewBuilder().
				WithConfig(config).
				WithAPIClient(newMockAPIClient()).
				WithTransportClient(k8s_client.NewMockK8sClient()).
				WithLogger(logger.NewTestLogger()).
				Build()
			if err != nil {
				t.Fatalf("unexpected error creating executor: %v", err)
			}

			ctx := logger.WithEventID(context.Background(), "test-event-resources")
			result := exec.Execute(ctx, map[string]interface{}{})

			// Verify sequential stop-on-failure: number of results should match expected
			assert.Equal(t, tt.expectedResults, len(result.ResourceResults),
				"sequential execution should stop at failure")

			// Verify failure status
			if tt.expectFailure {
				if result.Status == StatusSuccess {
					t.Error("expected execution to fail but got success")
				}
			}
		})
	}
}

// TestSequentialExecution_PostActions tests that post actions stop on first failure
func TestSequentialExecution_PostActions(t *testing.T) {
	tests := []struct {
		name            string
		postActions     []config_loader.PostAction
		mockResponse    *hyperfleet_api.Response
		mockError       error
		expectedResults int
		expectError     bool
	}{
		{
			name: "all log actions succeed",
			postActions: []config_loader.PostAction{
				{ActionBase: config_loader.ActionBase{Name: "log1", Log: &config_loader.LogAction{Message: "msg1"}}},
				{ActionBase: config_loader.ActionBase{Name: "log2", Log: &config_loader.LogAction{Message: "msg2"}}},
				{ActionBase: config_loader.ActionBase{Name: "log3", Log: &config_loader.LogAction{Message: "msg3"}}},
			},
			expectedResults: 3,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			postConfig := &config_loader.PostConfig{
				PostActions: tt.postActions,
			}

			config := &config_loader.Config{
				Adapter: config_loader.AdapterInfo{
					Name:    "test-adapter",
					Version: "1.0.0",
				},
				Post: postConfig,
			}

			mockClient := newMockAPIClient()
			mockClient.GetResponse = tt.mockResponse
			mockClient.GetError = tt.mockError
			mockClient.PostResponse = tt.mockResponse
			mockClient.PostError = tt.mockError

			exec, err := NewBuilder().
				WithConfig(config).
				WithAPIClient(mockClient).
				WithTransportClient(k8s_client.NewMockK8sClient()).
				WithLogger(logger.NewTestLogger()).
				Build()
			if err != nil {
				t.Fatalf("unexpected error creating executor: %v", err)
			}

			ctx := logger.WithEventID(context.Background(), "test-event-post")
			result := exec.Execute(ctx, map[string]interface{}{})

			// Verify number of post action results
			assert.Equal(t, tt.expectedResults, len(result.PostActionResults),
				"unexpected post action result count")

			// Verify error expectation
			if tt.expectError {
				assert.NotEmpty(t, result.Errors, "expected errors, got none")
				assert.NotNil(t, result.Errors[PhasePostActions], "expected post_actions error, got %#v", result.Errors)
			} else {
				assert.Empty(t, result.Errors, "expected no errors, got %#v", result.Errors)
			}
		})
	}
}

// TestSequentialExecution_SkipReasonCapture tests that SkipReason captures which precondition wasn't met
func TestSequentialExecution_SkipReasonCapture(t *testing.T) {
	tests := []struct {
		name           string
		preconditions  []config_loader.Precondition
		expectedStatus ExecutionStatus
		expectSkipped  bool
	}{
		{
			name: "first precondition not met",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "check1"}, Expression: "false"},
				{ActionBase: config_loader.ActionBase{Name: "check2"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "check3"}, Expression: "true"},
			},
			expectedStatus: StatusSuccess, // Successful execution, just resources skipped
			expectSkipped:  true,
		},
		{
			name: "second precondition not met",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "check1"}, Expression: "true"},
				{ActionBase: config_loader.ActionBase{Name: "check2"}, Expression: "false"},
				{ActionBase: config_loader.ActionBase{Name: "check3"}, Expression: "true"},
			},
			expectedStatus: StatusSuccess, // Successful execution, just resources skipped
			expectSkipped:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &config_loader.Config{
				Adapter: config_loader.AdapterInfo{
					Name:    "test-adapter",
					Version: "1.0.0",
				},
				Preconditions: tt.preconditions,
			}

			exec, err := NewBuilder().
				WithConfig(config).
				WithAPIClient(newMockAPIClient()).
				WithTransportClient(k8s_client.NewMockK8sClient()).
				WithLogger(logger.NewTestLogger()).
				Build()
			if err != nil {
				t.Fatalf("unexpected error creating executor: %v", err)
			}

			ctx := logger.WithEventID(context.Background(), "test-event-skip")
			result := exec.Execute(ctx, map[string]interface{}{})

			// Verify execution status is success (adapter executed successfully)
			if result.Status != tt.expectedStatus {
				t.Errorf("expected status %s, got %s", tt.expectedStatus, result.Status)
			}

			// Verify resources were skipped
			if tt.expectSkipped {
				assert.True(t, result.ResourcesSkipped, "ResourcesSkipped")
				assert.NotEmpty(t, result.SkipReason, "expected SkipReason to be set")
				// Verify execution context captures skip information
				if result.ExecutionContext != nil {
					assert.True(t, result.ExecutionContext.Adapter.ResourcesSkipped, "adapter.ResourcesSkipped")
				}
			}
		})
	}
}

// TestCreateHandler_MetricsRecording verifies that CreateHandler records Prometheus metrics
func TestCreateHandler_MetricsRecording(t *testing.T) {
	tests := []struct {
		name           string
		preconditions  []config_loader.Precondition
		expectedStatus string // "success", "skipped", or "failed"
		expectedErrors []string
	}{
		{
			name:           "success records success metric",
			preconditions:  []config_loader.Precondition{},
			expectedStatus: "success",
		},
		{
			name: "skipped records skipped metric",
			preconditions: []config_loader.Precondition{
				{ActionBase: config_loader.ActionBase{Name: "check"}, Expression: "false"},
			},
			expectedStatus: "skipped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			recorder := metrics.NewRecorder("test-adapter", "v0.1.0", registry)

			config := &config_loader.Config{
				Adapter:       config_loader.AdapterInfo{Name: "test-adapter", Version: "v0.1.0"},
				Preconditions: tt.preconditions,
			}

			exec, err := NewBuilder().
				WithConfig(config).
				WithAPIClient(newMockAPIClient()).
				WithTransportClient(k8s_client.NewMockK8sClient()).
				WithLogger(logger.NewTestLogger()).
				WithMetricsRecorder(recorder).
				Build()
			require.NoError(t, err)

			handler := exec.CreateHandler()

			evt := event.New()
			evt.SetID("test-event-1")
			evt.SetType("com.hyperfleet.test")
			evt.SetSource("test")
			eventData := map[string]interface{}{"id": "cluster-1"}
			eventBytes, _ := json.Marshal(eventData)
			_ = evt.SetData(event.ApplicationJSON, eventBytes)

			err = handler(context.Background(), &evt)
			require.NoError(t, err, "handler should always return nil")

			// Verify events_processed_total
			families, err := registry.Gather()
			require.NoError(t, err)

			eventsCount := getCounterValue(t, families, "hyperfleet_adapter_events_processed_total", "status", tt.expectedStatus)
			assert.Equal(t, float64(1), eventsCount, "expected 1 event with status %s", tt.expectedStatus)

			// Verify duration was recorded
			durationFamily := findFamily(families, "hyperfleet_adapter_event_processing_duration_seconds")
			require.NotNil(t, durationFamily, "duration metric should exist")
			histogram := durationFamily.GetMetric()[0].GetHistogram()
			assert.Equal(t, uint64(1), histogram.GetSampleCount(), "expected 1 duration sample")
		})
	}
}

// TestCreateHandler_MetricsRecording_Failed verifies error metrics are recorded on failure
func TestCreateHandler_MetricsRecording_Failed(t *testing.T) {
	registry := prometheus.NewRegistry()
	recorder := metrics.NewRecorder("test-adapter", "v0.1.0", registry)

	config := &config_loader.Config{
		Adapter: config_loader.AdapterInfo{Name: "test-adapter", Version: "v0.1.0"},
		Params: []config_loader.Parameter{
			{Name: "required", Source: "env.MISSING_VAR", Required: true},
		},
	}

	exec, err := NewBuilder().
		WithConfig(config).
		WithAPIClient(newMockAPIClient()).
		WithTransportClient(k8s_client.NewMockK8sClient()).
		WithLogger(logger.NewTestLogger()).
		WithMetricsRecorder(recorder).
		Build()
	require.NoError(t, err)

	handler := exec.CreateHandler()

	evt := event.New()
	evt.SetID("test-event-fail")
	evt.SetType("com.hyperfleet.test")
	evt.SetSource("test")
	eventData := map[string]interface{}{"id": "cluster-1"}
	eventBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventBytes)

	err = handler(context.Background(), &evt)
	require.NoError(t, err, "handler should always return nil even on failure")

	families, err := registry.Gather()
	require.NoError(t, err)

	// Verify failed event was recorded
	failedCount := getCounterValue(t, families, "hyperfleet_adapter_events_processed_total", "status", "failed")
	assert.Equal(t, float64(1), failedCount, "expected 1 failed event")

	// Verify error was recorded with phase label
	errorCount := getCounterValue(t, families, "hyperfleet_adapter_errors_total", "error_type", "param_extraction")
	assert.Equal(t, float64(1), errorCount, "expected 1 param_extraction error")
}

// TestCreateHandler_NilMetricsRecorder verifies handler works without a metrics recorder
func TestCreateHandler_NilMetricsRecorder(t *testing.T) {
	config := &config_loader.Config{
		Adapter: config_loader.AdapterInfo{Name: "test-adapter", Version: "v0.1.0"},
	}

	exec, err := NewBuilder().
		WithConfig(config).
		WithAPIClient(newMockAPIClient()).
		WithTransportClient(k8s_client.NewMockK8sClient()).
		WithLogger(logger.NewTestLogger()).
		Build()
	require.NoError(t, err)

	handler := exec.CreateHandler()

	evt := event.New()
	evt.SetID("test-event-nil")
	evt.SetType("com.hyperfleet.test")
	evt.SetSource("test")
	_ = evt.SetData(event.ApplicationJSON, []byte(`{"id":"cluster-1"}`))

	assert.NotPanics(t, func() {
		_ = handler(context.Background(), &evt)
	}, "handler with nil MetricsRecorder should not panic")
}

// helper functions for metrics assertions

func findFamily(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func getCounterValue(t *testing.T, families []*dto.MetricFamily, metricName, labelName, labelValue string) float64 {
	t.Helper()
	family := findFamily(families, metricName)
	if family == nil {
		t.Fatalf("metric %s not found", metricName)
	}
	for _, m := range family.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == labelName && l.GetValue() == labelValue {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}
