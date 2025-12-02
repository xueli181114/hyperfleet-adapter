package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// mockLogger implements logger.Logger for testing
type mockLogger struct{}

func (m *mockLogger) V(level int32) logger.Logger                       { return m }
func (m *mockLogger) Infof(format string, args ...interface{})          {}
func (m *mockLogger) Warningf(format string, args ...interface{})       {}
func (m *mockLogger) Errorf(format string, args ...interface{})         {}
func (m *mockLogger) Extra(key string, value interface{}) logger.Logger { return m }
func (m *mockLogger) Info(message string)                               {}
func (m *mockLogger) Warning(message string)                            {}
func (m *mockLogger) Error(message string)                              {}
func (m *mockLogger) Fatal(message string)                              {}

// mockAPIClient implements hyperfleet_api.Client for testing
type mockAPIClient struct {
	getResponse *hyperfleet_api.Response
	getError    error
}

func (m *mockAPIClient) Do(ctx context.Context, req *hyperfleet_api.Request) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

func (m *mockAPIClient) Get(ctx context.Context, url string, opts ...hyperfleet_api.RequestOption) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

func (m *mockAPIClient) Post(ctx context.Context, url string, body []byte, opts ...hyperfleet_api.RequestOption) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

func (m *mockAPIClient) Put(ctx context.Context, url string, body []byte, opts ...hyperfleet_api.RequestOption) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

func (m *mockAPIClient) Patch(ctx context.Context, url string, body []byte, opts ...hyperfleet_api.RequestOption) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

func (m *mockAPIClient) Delete(ctx context.Context, url string, opts ...hyperfleet_api.RequestOption) (*hyperfleet_api.Response, error) {
	return m.getResponse, m.getError
}

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
				APIClient: &mockAPIClient{},
				Logger:    &mockLogger{},
			},
			expectError: true,
		},
		{
			name: "missing API client",
			config: &ExecutorConfig{
				AdapterConfig: &config_loader.AdapterConfig{},
				Logger:        &mockLogger{},
			},
			expectError: true,
		},
		{
			name: "missing logger",
			config: &ExecutorConfig{
				AdapterConfig: &config_loader.AdapterConfig{},
				APIClient:     &mockAPIClient{},
			},
			expectError: true,
		},
		{
			name: "valid config",
			config: &ExecutorConfig{
				AdapterConfig: &config_loader.AdapterConfig{},
				APIClient:     &mockAPIClient{},
				Logger:        &mockLogger{},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if tt.expectError && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExecutorBuilder(t *testing.T) {
	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name:      "test-adapter",
			Namespace: "test-ns",
		},
	}

	exec, err := NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(&mockAPIClient{}).
		WithLogger(&mockLogger{}).
		WithDryRun(true).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if exec == nil {
		t.Fatal("expected executor, got nil")
	}

	if !exec.config.DryRun {
		t.Error("expected DryRun to be true")
	}
}

func TestExecutionContext(t *testing.T) {
	ctx := context.Background()
	evt := event.New()
	evt.SetID("test-123")

	execCtx := NewExecutionContext(ctx, &evt)

	if execCtx.Event.ID() != "test-123" {
		t.Errorf("expected event ID 'test-123', got '%s'", execCtx.Event.ID())
	}

	if len(execCtx.Params) != 0 {
		t.Error("expected empty params")
	}

	if len(execCtx.Responses) != 0 {
		t.Error("expected empty responses")
	}

	if len(execCtx.Resources) != 0 {
		t.Error("expected empty resources")
	}

	if execCtx.Adapter.ExecutionStatus != string(StatusSuccess) {
		t.Errorf("expected initial status to be success, got %s", execCtx.Adapter.ExecutionStatus)
	}
}

func TestExecutionContext_SetError(t *testing.T) {
	ctx := context.Background()
	evt := event.New()

	execCtx := NewExecutionContext(ctx, &evt)
	execCtx.SetError("TestReason", "Test message")

	if execCtx.Adapter.ExecutionStatus != string(StatusFailed) {
		t.Errorf("expected status to be failed, got %s", execCtx.Adapter.ExecutionStatus)
	}

	if execCtx.Adapter.ErrorReason != "TestReason" {
		t.Errorf("expected error reason 'TestReason', got '%s'", execCtx.Adapter.ErrorReason)
	}

	if execCtx.Adapter.ErrorMessage != "Test message" {
		t.Errorf("expected error message 'Test message', got '%s'", execCtx.Adapter.ErrorMessage)
	}
}

func TestExecutionContext_EvaluationTracking(t *testing.T) {
	ctx := context.Background()
	evt := event.New()
	evt.SetID("test-123")

	execCtx := NewExecutionContext(ctx, &evt)

	// Verify evaluations are empty initially
	if len(execCtx.Evaluations) != 0 {
		t.Error("expected empty evaluations initially")
	}

	// Add a CEL evaluation
	execCtx.AddCELEvaluation(PhasePreconditions, "check-status", "status == 'active'", true, 100)

	if len(execCtx.Evaluations) != 1 {
		t.Fatalf("expected 1 evaluation, got %d", len(execCtx.Evaluations))
	}

	eval := execCtx.Evaluations[0]
	if eval.Phase != PhasePreconditions {
		t.Errorf("expected phase PhasePreconditions, got %s", eval.Phase)
	}
	if eval.Name != "check-status" {
		t.Errorf("expected name 'check-status', got '%s'", eval.Name)
	}
	if eval.EvaluationType != EvaluationTypeCEL {
		t.Errorf("expected type CEL, got %s", eval.EvaluationType)
	}
	if eval.Expression != "status == 'active'" {
		t.Errorf("expected expression \"status == 'active'\", got '%s'", eval.Expression)
	}
	if !eval.Matched {
		t.Error("expected matched to be true")
	}

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
	execCtx.AddConditionsEvaluation(PhasePreconditions, "check-replicas", true, 50, fieldResults)

	if len(execCtx.Evaluations) != 2 {
		t.Fatalf("expected 2 evaluations, got %d", len(execCtx.Evaluations))
	}

	condEval := execCtx.Evaluations[1]
	if condEval.EvaluationType != EvaluationTypeConditions {
		t.Errorf("expected type Conditions, got %s", condEval.EvaluationType)
	}
	if len(condEval.FieldResults) != 2 {
		t.Errorf("expected 2 field results, got %d", len(condEval.FieldResults))
	}

	// Verify lookup by field name works
	if result, ok := condEval.FieldResults["status.phase"]; !ok {
		t.Error("expected to find 'status.phase' in field results")
	} else if result.FieldValue != "Running" {
		t.Errorf("expected FieldValue 'Running', got '%v'", result.FieldValue)
	}

	if result, ok := condEval.FieldResults["replicas"]; !ok {
		t.Error("expected to find 'replicas' in field results")
	} else if result.FieldValue != 3 {
		t.Errorf("expected FieldValue 3, got '%v'", result.FieldValue)
	}
}

func TestExecutionContext_GetEvaluationsByPhase(t *testing.T) {
	ctx := context.Background()
	evt := event.New()

	execCtx := NewExecutionContext(ctx, &evt)

	// Add evaluations in different phases
	execCtx.AddCELEvaluation(PhasePreconditions, "precond-1", "true", true, 10)
	execCtx.AddCELEvaluation(PhasePreconditions, "precond-2", "false", false, 20)
	execCtx.AddCELEvaluation(PhasePostActions, "post-1", "true", true, 30)

	// Get preconditions evaluations
	precondEvals := execCtx.GetEvaluationsByPhase(PhasePreconditions)
	if len(precondEvals) != 2 {
		t.Fatalf("expected 2 precondition evaluations, got %d", len(precondEvals))
	}

	// Get post actions evaluations
	postEvals := execCtx.GetEvaluationsByPhase(PhasePostActions)
	if len(postEvals) != 1 {
		t.Fatalf("expected 1 post action evaluation, got %d", len(postEvals))
	}

	// Get resources evaluations (none)
	resourceEvals := execCtx.GetEvaluationsByPhase(PhaseResources)
	if len(resourceEvals) != 0 {
		t.Fatalf("expected 0 resource evaluations, got %d", len(resourceEvals))
	}
}

func TestExecutionContext_GetFailedEvaluations(t *testing.T) {
	ctx := context.Background()
	evt := event.New()

	execCtx := NewExecutionContext(ctx, &evt)

	// Add mixed evaluations
	execCtx.AddCELEvaluation(PhasePreconditions, "passed-1", "true", true, 10)
	execCtx.AddCELEvaluation(PhasePreconditions, "failed-1", "false", false, 20)
	execCtx.AddCELEvaluation(PhasePreconditions, "passed-2", "true", true, 30)
	execCtx.AddCELEvaluation(PhasePostActions, "failed-2", "false", false, 40)

	failedEvals := execCtx.GetFailedEvaluations()
	if len(failedEvals) != 2 {
		t.Fatalf("expected 2 failed evaluations, got %d", len(failedEvals))
	}

	// Verify the failed ones are correct
	names := make(map[string]bool)
	for _, eval := range failedEvals {
		names[eval.Name] = true
	}
	if !names["failed-1"] || !names["failed-2"] {
		t.Error("expected failed-1 and failed-2 in failed evaluations")
	}
}

func TestExecutorError(t *testing.T) {
	err := NewExecutorError(PhasePreconditions, "test-step", "test message", nil)

	expected := "[preconditions] test-step: test message"
	if err.Error() != expected {
		t.Errorf("expected '%s', got '%s'", expected, err.Error())
	}

	// With wrapped error
	wrappedErr := NewExecutorError(PhaseResources, "create", "failed to create", context.Canceled)
	if wrappedErr.Unwrap() != context.Canceled {
		t.Error("Unwrap() should return the wrapped error")
	}
}

func TestPreconditionNotMetError(t *testing.T) {
	err := &PreconditionNotMetError{
		Name:    "test-precond",
		Reason:  "condition failed",
		Details: "field != expected",
	}

	if !IsPreconditionNotMet(err) {
		t.Error("IsPreconditionNotMet should return true")
	}

	otherErr := NewExecutorError(PhasePreconditions, "test", "error", nil)
	if IsPreconditionNotMet(otherErr) {
		t.Error("IsPreconditionNotMet should return false for other errors")
	}
}

func TestExecute_ParamExtraction(t *testing.T) {
	// Set up environment variable for test
	t.Setenv("TEST_VAR", "test-value")

	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name:      "test-adapter",
			Namespace: "test-ns",
		},
		Spec: config_loader.AdapterConfigSpec{
			Params: []config_loader.Parameter{
				{
					Name:     "testParam",
					Source:   "env.TEST_VAR",
					Required: true,
				},
				{
					Name:     "eventParam",
					Source:   "event.cluster_id",
					Required: true,
				},
			},
		},
	}

	exec, err := NewBuilder().
		WithAdapterConfig(config).
		WithAPIClient(&mockAPIClient{}).
		WithLogger(&mockLogger{}).
		WithDryRun(true).
		Build()

	if err != nil {
		t.Fatalf("unexpected error creating executor: %v", err)
	}

	// Create event with data
	evt := event.New()
	evt.SetID("test-event-123")
	eventData := map[string]interface{}{
		"cluster_id": "cluster-456",
	}
	eventDataBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventDataBytes)

	// Execute
	result := exec.Execute(context.Background(), &evt)

	// Check result
	if result.EventID != "test-event-123" {
		t.Errorf("expected event ID 'test-event-123', got '%s'", result.EventID)
	}

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

	config := &config_loader.AdapterConfig{
		Metadata: config_loader.Metadata{
			Name:      "test",
			Namespace: "default",
		},
	}

	evt := event.New()
	eventData := map[string]interface{}{
		"cluster_id": "test-cluster",
		"nested": map[string]interface{}{
			"value": "nested-value",
		},
	}
	eventDataBytes, _ := json.Marshal(eventData)
	_ = evt.SetData(event.ApplicationJSON, eventDataBytes)

	execCtx := NewExecutionContext(context.Background(), &evt)
	extractor := NewParamExtractor(config, execCtx)

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
				{Name: "clusterId", Source: "event.cluster_id"},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.ExtractAll(tt.params)

			if tt.expectError {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectKey != "" {
				if result[tt.expectKey] != tt.expectValue {
					t.Errorf("expected %s=%v, got %v", tt.expectKey, tt.expectValue, result[tt.expectKey])
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
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

