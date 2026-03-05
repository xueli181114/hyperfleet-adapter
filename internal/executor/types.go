package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/manifest"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/transport_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/metrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ExecutionPhase represents which phase of execution
type ExecutionPhase string

const (
	// PhaseParamExtraction is the parameter extraction phase
	PhaseParamExtraction ExecutionPhase = "param_extraction"
	// PhasePreconditions is the precondition evaluation phase
	PhasePreconditions ExecutionPhase = "preconditions"
	// PhaseResources is the resource creation/update phase
	PhaseResources ExecutionPhase = "resources"
	// PhasePostActions is the post-action execution phase
	PhasePostActions ExecutionPhase = "post_actions"
)

// ExecutionStatus represents the status of execution (runtime perspective)
type ExecutionStatus string

const (
	// StatusSuccess indicates successful execution (adapter ran successfully)
	StatusSuccess ExecutionStatus = "success"
	// StatusFailed indicates failed execution (process execution error: API timeout, parse error, K8s error, etc.)
	StatusFailed ExecutionStatus = "failed"
)

// ResourceRef represents a reference to a HyperFleet resource
type ResourceRef struct {
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"`
	Href string `json:"href,omitempty"`
}

// EventData represents the data payload of a HyperFleet CloudEvent
type EventData struct {
	ID              string       `json:"id,omitempty"`
	Kind            string       `json:"kind,omitempty"`
	Href            string       `json:"href,omitempty"`
	Generation      int64        `json:"generation,omitempty"`
	OwnerReferences *ResourceRef `json:"owner_references,omitempty"`
}

// ExecutorConfig holds configuration for the executor
type ExecutorConfig struct {
	// Config is the unified configuration (merged from deployment and task configs)
	Config *config_loader.Config
	// APIClient is the HyperFleet API client
	APIClient hyperfleet_api.Client
	// TransportClient is the transport client for applying resources (kubernetes or maestro)
	TransportClient transport_client.TransportClient
	// Logger is the logger instance
	Logger logger.Logger
	// MetricsRecorder records adapter-level Prometheus metrics (nil disables recording)
	MetricsRecorder *metrics.Recorder
}

// Executor processes CloudEvents according to the adapter configuration
type Executor struct {
	config             *ExecutorConfig
	precondExecutor    *PreconditionExecutor
	resourceExecutor   *ResourceExecutor
	postActionExecutor *PostActionExecutor
	log                logger.Logger
}

// ExecutionResult contains the result of processing an event
type ExecutionResult struct {
	// Status is the overall execution status (runtime perspective)
	Status ExecutionStatus
	// CurrentPhase is the phase where execution ended (or is currently)
	CurrentPhase ExecutionPhase
	// Params contains the extracted parameters
	Params map[string]interface{}
	// PreconditionResults contains results of precondition evaluations
	PreconditionResults []PreconditionResult
	// ResourceResults contains results of resource operations
	ResourceResults []ResourceResult
	// PostActionResults contains results of post-action executions
	PostActionResults []PostActionResult
	// Errors contains errors keyed by the phase where they occurred
	Errors map[ExecutionPhase]error
	// ResourcesSkipped indicates if resources were skipped (business outcome)
	ResourcesSkipped bool
	// SkipReason is why resources were skipped (e.g., "precondition not met")
	SkipReason string
	// ExecutionContext contains the full execution context (for testing and debugging)
	ExecutionContext *ExecutionContext
}

// PreconditionResult contains the result of a single precondition evaluation
type PreconditionResult struct {
	// Name is the precondition name
	Name string
	// Status is the result status
	Status ExecutionStatus
	// Matched indicates if conditions were satisfied
	Matched bool
	// APICallMade indicates if an API call was made
	APICallMade bool
	// APIResponse contains the raw API response (if APICallMade)
	APIResponse []byte
	// CapturedFields contains fields captured from the API response
	CapturedFields map[string]interface{}
	// ConditionResults contains individual condition evaluation results
	ConditionResults []criteria.EvaluationResult
	// CELResult contains CEL evaluation result (if expression was used)
	CELResult *criteria.CELResult
	// Error is the error if Status is StatusFailed
	Error error
}

// ResourceResult contains the result of a single resource operation
type ResourceResult struct {
	// Name is the resource name from config
	Name string
	// Kind is the Kubernetes resource kind
	Kind string
	// Namespace is the resource namespace
	Namespace string
	// ResourceName is the actual K8s resource name
	ResourceName string
	// Status is the result status
	Status ExecutionStatus
	// Operation is the operation performed (create, update, recreate, skip)
	Operation manifest.Operation
	// OperationReason explains why this operation was performed
	// Examples: "resource not found", "generation changed from 1 to 2", "generation 1 unchanged", "recreate_on_change=true"
	OperationReason string
	// Error is the error if Status is StatusFailed
	Error error
}

// PostActionResult contains the result of a single post-action execution
type PostActionResult struct {
	// Name is the post-action name
	Name string
	// Status is the result status
	Status ExecutionStatus
	// Skipped indicates if the action was skipped due to when condition
	Skipped bool
	// SkipReason is the reason for skipping
	SkipReason string
	// APICallMade indicates if an API call was made
	APICallMade bool
	// APIResponse contains the raw API response (if APICallMade)
	APIResponse []byte
	// HTTPStatus is the HTTP status code of the API response
	HTTPStatus int
	// Error is the error if Status is StatusFailed
	Error error
}

// ExecutionContext holds runtime context during execution
type ExecutionContext struct {
	// Ctx is the Go context
	Ctx context.Context
	// Config is the unified adapter configuration
	Config *config_loader.Config
	// EventData is the parsed event data payload
	EventData map[string]interface{}
	// Params holds extracted parameters and captured fields
	// - Populated during param extraction phase with event/env data
	// - Populated during precondition phase with captured API response fields
	Params map[string]interface{}
	// Resources holds discovered resources keyed by resource name.
	// Nested discoveries are also added as top-level entries keyed by nested discovery name.
	// Values are expected to be *unstructured.Unstructured.
	Resources map[string]interface{}
	// Adapter holds adapter execution metadata
	Adapter AdapterMetadata
	// Evaluations tracks all condition evaluations for debugging/auditing
	Evaluations []EvaluationRecord
}

// EvaluationRecord tracks a single condition evaluation during execution
type EvaluationRecord struct {
	// Phase is the execution phase where this evaluation occurred
	Phase ExecutionPhase
	// Name is the name of the precondition/resource/action being evaluated
	Name string
	// EvaluationType indicates what kind of evaluation was performed
	EvaluationType EvaluationType
	// Expression is the CEL expression or condition description
	Expression string
	// Matched indicates whether the evaluation succeeded
	Matched bool
	// FieldResults contains individual field evaluation results keyed by field path (for structured conditions)
	// Reuses criteria.EvaluationResult to avoid duplication
	FieldResults map[string]criteria.EvaluationResult
	// Timestamp is when the evaluation occurred
	Timestamp time.Time
}

// EvaluationType indicates the type of evaluation performed
type EvaluationType string

const (
	// EvaluationTypeCEL indicates a CEL expression evaluation
	EvaluationTypeCEL EvaluationType = "cel"
	// EvaluationTypeConditions indicates structured conditions evaluation
	EvaluationTypeConditions EvaluationType = "conditions"
)

// AdapterMetadata holds adapter execution metadata for CEL expressions
type AdapterMetadata struct {
	// ExecutionStatus is the overall execution status (runtime perspective: "success", "failed")
	ExecutionStatus string
	// ErrorReason is the error reason if failed (process execution errors only)
	ErrorReason string
	// ErrorMessage is the error message if failed (process execution errors only)
	ErrorMessage string
	// ExecutionError contains detailed error information if execution failed
	ExecutionError *ExecutionError `json:"executionError,omitempty"`
	// ResourcesSkipped indicates if resources were skipped (business outcome)
	ResourcesSkipped bool `json:"resourcesSkipped,omitempty"`
	// SkipReason is why resources were skipped (e.g., "precondition not met")
	SkipReason string `json:"skipReason,omitempty"`
}

// ExecutionError represents a structured execution error
type ExecutionError struct {
	// Phase is the execution phase where the error occurred
	Phase string `json:"phase"`
	// Step is the specific step (precondition/resource/action name) that failed
	Step string `json:"step"`
	// Message is the error message (includes all relevant details)
	Message string `json:"message"`
}

// NewExecutionContext creates a new execution context
func NewExecutionContext(ctx context.Context, eventData map[string]interface{}, config *config_loader.Config) *ExecutionContext {
	return &ExecutionContext{
		Ctx:         ctx,
		Config:      config,
		EventData:   eventData,
		Params:      make(map[string]interface{}),
		Resources:   make(map[string]interface{}),
		Evaluations: make([]EvaluationRecord, 0),
		Adapter: AdapterMetadata{
			ExecutionStatus: string(StatusSuccess),
		},
	}
}

// AddEvaluation records a condition evaluation result
func (ec *ExecutionContext) AddEvaluation(phase ExecutionPhase, name string, evalType EvaluationType, expression string, matched bool, fieldResults map[string]criteria.EvaluationResult) {
	ec.Evaluations = append(ec.Evaluations, EvaluationRecord{
		Phase:          phase,
		Name:           name,
		EvaluationType: evalType,
		Expression:     expression,
		Matched:        matched,
		FieldResults:   fieldResults,
		Timestamp:      time.Now(),
	})
}

// AddCELEvaluation is a convenience method for recording CEL expression evaluations
func (ec *ExecutionContext) AddCELEvaluation(phase ExecutionPhase, name, expression string, matched bool) {
	ec.AddEvaluation(phase, name, EvaluationTypeCEL, expression, matched, nil)
}

// AddConditionsEvaluation is a convenience method for recording structured conditions evaluations
func (ec *ExecutionContext) AddConditionsEvaluation(phase ExecutionPhase, name string, matched bool, fieldResults map[string]criteria.EvaluationResult) {
	ec.AddEvaluation(phase, name, EvaluationTypeConditions, "", matched, fieldResults)
}

// GetEvaluationsByPhase returns all evaluations for a specific phase
func (ec *ExecutionContext) GetEvaluationsByPhase(phase ExecutionPhase) []EvaluationRecord {
	var results []EvaluationRecord
	for _, eval := range ec.Evaluations {
		if eval.Phase == phase {
			results = append(results, eval)
		}
	}
	return results
}

// GetFailedEvaluations returns all evaluations that did not match
func (ec *ExecutionContext) GetFailedEvaluations() []EvaluationRecord {
	var results []EvaluationRecord
	for _, eval := range ec.Evaluations {
		if !eval.Matched {
			results = append(results, eval)
		}
	}
	return results
}

// SetError sets the error status in adapter metadata (for runtime failures)
func (ec *ExecutionContext) SetError(reason, message string) {
	ec.Adapter.ExecutionStatus = string(StatusFailed)
	ec.Adapter.ErrorReason = reason
	ec.Adapter.ErrorMessage = message
}

// SetSkipped sets the status to indicate execution was skipped (not an error)
func (ec *ExecutionContext) SetSkipped(reason, message string) {
	// Execution was successful, but resources were skipped due to business logic
	ec.Adapter.ExecutionStatus = string(StatusSuccess)
	ec.Adapter.ResourcesSkipped = true
	ec.Adapter.SkipReason = reason
	if message != "" {
		ec.Adapter.SkipReason = message // Use message if provided for more detail
	}
}

// GetCELVariables returns all variables for CEL evaluation.
// This includes Params, adapter metadata, and resources.
func (ec *ExecutionContext) GetCELVariables() map[string]interface{} {
	result := make(map[string]interface{})

	// Copy all params
	for k, v := range ec.Params {
		result[k] = v
	}

	// Add adapter metadata (use helper from utils.go)
	result["adapter"] = adapterMetadataToMap(&ec.Adapter)

	// Add resources (convert unstructured to maps for CEL evaluation)
	resources := make(map[string]interface{})
	for name, val := range ec.Resources {
		switch v := val.(type) {
		case *unstructured.Unstructured:
			if v != nil {
				resources[name] = v.Object
			}
		case map[string]*unstructured.Unstructured:
			nested := make(map[string]interface{})
			for nestedName, nestedRes := range v {
				if nestedRes != nil {
					nested[nestedName] = nestedRes.Object
				}
			}
			resources[name] = nested
		}
	}
	result["resources"] = resources

	return result
}

// ExecutorError represents an error during execution
type ExecutorError struct {
	Phase   ExecutionPhase
	Step    string
	Message string
	Err     error
}

func (e *ExecutorError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %s: %v", e.Phase, e.Step, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Phase, e.Step, e.Message)
}

func (e *ExecutorError) Unwrap() error {
	return e.Err
}

// NewExecutorError creates a new executor error
func NewExecutorError(phase ExecutionPhase, step, message string, err error) *ExecutorError {
	return &ExecutorError{
		Phase:   phase,
		Step:    step,
		Message: message,
		Err:     err,
	}
}

// PreconditionsOutcome represents the high-level result of precondition evaluation
type PreconditionsOutcome struct {
	// AllMatched indicates whether all preconditions were satisfied (business outcome)
	AllMatched bool
	// Results contains individual precondition results
	Results []PreconditionResult
	// Error contains execution errors (API failures, parse errors, etc.)
	// nil if preconditions were evaluated successfully, even if not matched
	Error error
	// NotMetReason provides details when AllMatched is false
	NotMetReason string
}
