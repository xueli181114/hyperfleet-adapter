package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
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

// Kubernetes annotation keys
const (
	// AnnotationGeneration is the annotation key for tracking resource generation
	AnnotationGeneration = "hyperfleet.io/generation"
)

// ExecutionStatus represents the status of execution
type ExecutionStatus string

const (
	// StatusSuccess indicates successful execution
	StatusSuccess ExecutionStatus = "success"
	// StatusFailed indicates failed execution
	StatusFailed ExecutionStatus = "failed"
	// StatusSkipped indicates execution was skipped (e.g., precondition not met)
	StatusSkipped ExecutionStatus = "skipped"
)

// ExecutorConfig holds configuration for the executor
type ExecutorConfig struct {
	// AdapterConfig is the loaded adapter configuration
	AdapterConfig *config_loader.AdapterConfig
	// APIClient is the HyperFleet API client
	APIClient hyperfleet_api.Client
	// K8sClient is the Kubernetes client (optional, can be nil if not needed)
	K8sClient *k8s_client.Client
	// Logger is the logger instance
	Logger logger.Logger
	// DryRun if true, don't actually create K8s resources
	DryRun bool
}

// Executor processes CloudEvents according to the adapter configuration
type Executor struct {
	config *ExecutorConfig
}

// ExecutionResult contains the result of processing an event
type ExecutionResult struct {
	// EventID is the ID of the processed event
	EventID string
	// Status is the overall execution status
	Status ExecutionStatus
	// Phase is the phase where execution ended
	Phase ExecutionPhase
	// Duration is how long the execution took
	Duration time.Duration
	// Params contains the extracted parameters
	Params map[string]interface{}
	// PreconditionResults contains results of precondition evaluations
	PreconditionResults []PreconditionResult
	// ResourceResults contains results of resource operations
	ResourceResults []ResourceResult
	// PostActionResults contains results of post-action executions
	PostActionResults []PostActionResult
	// Error is the error if Status is StatusFailed
	Error error
	// ErrorReason is a human-readable error reason
	ErrorReason string
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
	// ExtractedFields contains fields extracted from the response
	ExtractedFields map[string]interface{}
	// ConditionResults contains individual condition evaluation results
	ConditionResults []criteria.EvaluationResult
	// CELResult contains CEL evaluation result (if expression was used)
	CELResult *criteria.CELResult
	// Error is the error if Status is StatusFailed
	Error error
	// Duration is how long this precondition took
	Duration time.Duration
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
	// Operation is the operation performed (create, update, skip)
	Operation ResourceOperation
	// Resource is the created/updated resource (if successful)
	Resource *unstructured.Unstructured
	// Error is the error if Status is StatusFailed
	Error error
	// Duration is how long this operation took
	Duration time.Duration
}

// ResourceOperation represents the operation performed on a resource
type ResourceOperation string

const (
	// OperationCreate indicates a resource was created
	OperationCreate ResourceOperation = "create"
	// OperationUpdate indicates a resource was updated
	OperationUpdate ResourceOperation = "update"
	// OperationRecreate indicates a resource was deleted and recreated
	OperationRecreate ResourceOperation = "recreate"
	// OperationSkip indicates no operation was needed
	OperationSkip ResourceOperation = "skip"
	// OperationDryRun indicates dry run mode (no actual operation)
	OperationDryRun ResourceOperation = "dry_run"
)

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
	// Duration is how long this action took
	Duration time.Duration
}

// ExecutionContext holds runtime context during execution
type ExecutionContext struct {
	// Ctx is the Go context
	Ctx context.Context
	// Event is the CloudEvent being processed
	Event *event.Event
	// Params holds extracted parameters (populated during param extraction)
	Params map[string]interface{}
	// Responses holds API responses keyed by storeResponseAs name
	Responses map[string]interface{}
	// Resources holds created/updated K8s resources keyed by resource name
	Resources map[string]*unstructured.Unstructured
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
	// Duration is how long the evaluation took
	Duration time.Duration
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
	// ExecutionStatus is the overall execution status
	ExecutionStatus string
	// ErrorReason is the error reason if failed
	ErrorReason string
	// ErrorMessage is the error message if failed
	ErrorMessage string
}

// NewExecutionContext creates a new execution context
func NewExecutionContext(ctx context.Context, evt *event.Event) *ExecutionContext {
	return &ExecutionContext{
		Ctx:         ctx,
		Event:       evt,
		Params:      make(map[string]interface{}),
		Responses:   make(map[string]interface{}),
		Resources:   make(map[string]*unstructured.Unstructured),
		Evaluations: make([]EvaluationRecord, 0),
		Adapter: AdapterMetadata{
			ExecutionStatus: string(StatusSuccess),
		},
	}
}

// AddEvaluation records a condition evaluation result
func (ec *ExecutionContext) AddEvaluation(phase ExecutionPhase, name string, evalType EvaluationType, expression string, matched bool, duration time.Duration, fieldResults map[string]criteria.EvaluationResult) {
	ec.Evaluations = append(ec.Evaluations, EvaluationRecord{
		Phase:          phase,
		Name:           name,
		EvaluationType: evalType,
		Expression:     expression,
		Matched:        matched,
		FieldResults:   fieldResults,
		Timestamp:      time.Now(),
		Duration:       duration,
	})
}

// AddCELEvaluation is a convenience method for recording CEL expression evaluations
func (ec *ExecutionContext) AddCELEvaluation(phase ExecutionPhase, name, expression string, matched bool, duration time.Duration) {
	ec.AddEvaluation(phase, name, EvaluationTypeCEL, expression, matched, duration, nil)
}

// AddConditionsEvaluation is a convenience method for recording structured conditions evaluations
func (ec *ExecutionContext) AddConditionsEvaluation(phase ExecutionPhase, name string, matched bool, duration time.Duration, fieldResults map[string]criteria.EvaluationResult) {
	ec.AddEvaluation(phase, name, EvaluationTypeConditions, "", matched, duration, fieldResults)
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

// SetError sets the error status in adapter metadata
func (ec *ExecutionContext) SetError(reason, message string) {
	ec.Adapter.ExecutionStatus = string(StatusFailed)
	ec.Adapter.ErrorReason = reason
	ec.Adapter.ErrorMessage = message
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

// PreconditionNotMetError indicates a precondition was not satisfied
type PreconditionNotMetError struct {
	Name    string
	Reason  string
	Details string
}

func (e *PreconditionNotMetError) Error() string {
	return fmt.Sprintf("precondition '%s' not met: %s (details: %s)", e.Name, e.Reason, e.Details)
}

// IsPreconditionNotMet checks if an error is a PreconditionNotMetError
func IsPreconditionNotMet(err error) bool {
	_, ok := err.(*PreconditionNotMetError)
	return ok
}

