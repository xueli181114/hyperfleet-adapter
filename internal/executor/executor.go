package executor

import (
	"context"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// New creates a new Executor with the given configuration
func New(config *ExecutorConfig) (*Executor, error) {
	if config == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "executor config is required", nil)
	}
	if config.AdapterConfig == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "adapter config is required", nil)
	}
	if config.APIClient == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "API client is required", nil)
	}
	if config.Logger == nil {
		return nil, NewExecutorError(PhaseParamExtraction, "init", "logger is required", nil)
	}

	return &Executor{
		config: config,
	}, nil
}

// Execute processes a CloudEvent according to the adapter configuration
// This is the main entry point for event processing
func (e *Executor) Execute(ctx context.Context, evt *event.Event) *ExecutionResult {
	if evt == nil {
		return &ExecutionResult{
			Status:      StatusFailed,
			Error:       NewExecutorError(PhaseParamExtraction, "init", "event is required", nil),
			ErrorReason: "nil event received",
			Duration:    0,
		}
	}

	startTime := time.Now()

	result := &ExecutionResult{
		EventID: evt.ID(),
		Status:  StatusSuccess,
		Params:  make(map[string]interface{}),
	}

	// Create execution context
	execCtx := NewExecutionContext(ctx, evt)

	e.config.Logger.Infof("Starting event execution: id=%s type=%s source=%s",
		evt.ID(), evt.Type(), evt.Source())

	// Phase 1: Extract parameters
	result.Phase = PhaseParamExtraction
	if err := e.executeParamExtraction(execCtx); err != nil {
		result.Status = StatusFailed
		result.Error = err
		result.ErrorReason = "parameter extraction failed"
		result.Duration = time.Since(startTime)
		e.config.Logger.Errorf("Parameter extraction failed: %v", err)
		return result
	}
	result.Params = execCtx.Params
	e.config.Logger.Infof("Parameter extraction completed: extracted %d params", len(execCtx.Params))

	// Phase 2: Execute preconditions
	result.Phase = PhasePreconditions
	precondResults, err := e.executePreconditions(ctx, execCtx)
	result.PreconditionResults = precondResults
	if err != nil {
		if IsPreconditionNotMet(err) {
			// Precondition not met is a soft failure - skip resources but continue to post
			result.Status = StatusSkipped
			result.ErrorReason = err.Error()
			e.config.Logger.Warningf("Preconditions not met, skipping resource execution: %v", err)
			// Set error in adapter metadata for post actions
			execCtx.SetError("PreconditionNotMet", err.Error())
		} else {
			result.Status = StatusFailed
			result.Error = err
			result.ErrorReason = "precondition evaluation failed"
			result.Duration = time.Since(startTime)
			e.config.Logger.Errorf("Precondition execution failed: %v", err)
			execCtx.SetError("PreconditionFailed", err.Error())
			// Continue to post actions even on failure
		}
	} else {
		e.config.Logger.Infof("Preconditions completed: %d preconditions evaluated", len(precondResults))
	}

	// Phase 3: Execute resources (skip if preconditions not met or failed)
	result.Phase = PhaseResources
	if result.Status == StatusSuccess {
		resourceResults, err := e.executeResources(ctx, execCtx)
		result.ResourceResults = resourceResults
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.ErrorReason = "resource execution failed"
			e.config.Logger.Errorf("Resource execution failed: %v", err)
			execCtx.SetError("ResourceFailed", err.Error())
			// Continue to post actions even on failure
		} else {
			e.config.Logger.Infof("Resources completed: %d resources processed", len(resourceResults))
		}
	}

	// Phase 4: Execute post actions (always execute, even on failure)
	result.Phase = PhasePostActions
	postResults, err := e.executePostActions(ctx, execCtx)
	result.PostActionResults = postResults
	if err != nil {
		// Log error but don't override status if already failed
		e.config.Logger.Errorf("Post action execution failed: %v", err)
		if result.Status == StatusSuccess {
			result.Status = StatusFailed
			result.Error = err
			result.ErrorReason = "post action execution failed"
		}
	} else {
		e.config.Logger.Infof("Post actions completed: %d actions executed", len(postResults))
	}

	result.Duration = time.Since(startTime)

	// Final logging
	if result.Status == StatusSuccess {
		e.config.Logger.Infof("Event execution completed successfully: id=%s duration=%v",
			evt.ID(), result.Duration)
	} else if result.Status == StatusSkipped {
		e.config.Logger.Warningf("Event execution skipped: id=%s reason=%s duration=%v",
			evt.ID(), result.ErrorReason, result.Duration)
	} else {
		e.config.Logger.Errorf("Event execution failed: id=%s phase=%s reason=%s duration=%v",
			evt.ID(), result.Phase, result.ErrorReason, result.Duration)
	}

	return result
}

// executeParamExtraction extracts parameters from the event and environment
func (e *Executor) executeParamExtraction(execCtx *ExecutionContext) error {
	extractor := NewParamExtractor(e.config.AdapterConfig, execCtx)

	// Extract parameters from config
	params, err := extractor.ExtractAll(e.config.AdapterConfig.Spec.Params)
	if err != nil {
		return err
	}

	// Store params in context
	for k, v := range params {
		execCtx.Params[k] = v
	}

	// Add metadata params
	extractor.AddMetadataParams(execCtx.Params)

	return nil
}

// executePreconditions evaluates all preconditions
func (e *Executor) executePreconditions(ctx context.Context, execCtx *ExecutionContext) ([]PreconditionResult, error) {
	if len(e.config.AdapterConfig.Spec.Preconditions) == 0 {
		return nil, nil
	}

	precondExecutor := NewPreconditionExecutor(e.config.APIClient, e.config.Logger)
	return precondExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Preconditions, execCtx)
}

// executeResources creates/updates all Kubernetes resources
func (e *Executor) executeResources(ctx context.Context, execCtx *ExecutionContext) ([]ResourceResult, error) {
	if len(e.config.AdapterConfig.Spec.Resources) == 0 {
		return nil, nil
	}

	// Check if K8s client is available
	if e.config.K8sClient == nil && !e.config.DryRun {
		return nil, NewExecutorError(PhaseResources, "init", "kubernetes client not configured", nil)
	}

	resourceExecutor := NewResourceExecutor(e.config.K8sClient, e.config.Logger, e.config.DryRun)
	return resourceExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Resources, execCtx)
}

// executePostActions executes all post-processing actions
func (e *Executor) executePostActions(ctx context.Context, execCtx *ExecutionContext) ([]PostActionResult, error) {
	if e.config.AdapterConfig.Spec.Post == nil {
		return nil, nil
	}

	postExecutor := NewPostActionExecutor(e.config.APIClient, e.config.Logger)
	return postExecutor.ExecuteAll(ctx, e.config.AdapterConfig.Spec.Post, execCtx)
}

// CreateHandler creates an event handler function that can be used with the broker subscriber
// This is a convenience method for integrating with the broker_consumer package
func (e *Executor) CreateHandler() func(ctx context.Context, evt *event.Event) error {
	return func(ctx context.Context, evt *event.Event) error {
		result := e.Execute(ctx, evt)

		if result.Status == StatusFailed {
			return result.Error
		}

		// StatusSkipped is not an error - preconditions not met is expected behavior
		return nil
	}
}


// ExecutorBuilder provides a fluent interface for building an Executor
type ExecutorBuilder struct {
	config *ExecutorConfig
}

// NewBuilder creates a new ExecutorBuilder
func NewBuilder() *ExecutorBuilder {
	return &ExecutorBuilder{
		config: &ExecutorConfig{},
	}
}

// WithAdapterConfig sets the adapter configuration
func (b *ExecutorBuilder) WithAdapterConfig(config *config_loader.AdapterConfig) *ExecutorBuilder {
	b.config.AdapterConfig = config
	return b
}

// WithAPIClient sets the HyperFleet API client
func (b *ExecutorBuilder) WithAPIClient(client hyperfleet_api.Client) *ExecutorBuilder {
	b.config.APIClient = client
	return b
}

// WithK8sClient sets the Kubernetes client
func (b *ExecutorBuilder) WithK8sClient(client *k8s_client.Client) *ExecutorBuilder {
	b.config.K8sClient = client
	return b
}

// WithLogger sets the logger
func (b *ExecutorBuilder) WithLogger(log logger.Logger) *ExecutorBuilder {
	b.config.Logger = log
	return b
}

// WithDryRun enables dry run mode
func (b *ExecutorBuilder) WithDryRun(dryRun bool) *ExecutorBuilder {
	b.config.DryRun = dryRun
	return b
}

// Build creates the Executor
func (b *ExecutorBuilder) Build() (*Executor, error) {
	return New(b.config)
}

