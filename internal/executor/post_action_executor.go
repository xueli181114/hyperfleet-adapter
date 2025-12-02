package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// PostActionExecutor executes post-processing actions
type PostActionExecutor struct {
	apiClient hyperfleet_api.Client
	log       logger.Logger
}

// NewPostActionExecutor creates a new post-action executor
func NewPostActionExecutor(apiClient hyperfleet_api.Client, log logger.Logger) *PostActionExecutor {
	return &PostActionExecutor{
		apiClient: apiClient,
		log:       log,
	}
}

// ExecuteAll executes all post-processing actions
// First builds params from post.params, then executes post.postActions
func (pae *PostActionExecutor) ExecuteAll(ctx context.Context, postConfig *config_loader.PostConfig, execCtx *ExecutionContext) ([]PostActionResult, error) {
	if postConfig == nil {
		return nil, nil
	}

	// Step 1: Build post params (like clusterStatusPayload)
	if len(postConfig.Params) > 0 {
		if err := pae.buildPostParams(postConfig.Params, execCtx); err != nil {
			return nil, NewExecutorError(PhasePostActions, "build_params", "failed to build post params", err)
		}
	}

	// Step 2: Execute post actions
	results := make([]PostActionResult, 0, len(postConfig.PostActions))
	for _, action := range postConfig.PostActions {
		result, err := pae.executePostAction(ctx, action, execCtx)
		results = append(results, result)

		if err != nil {
			// Log error but continue with other actions
			pae.log.Errorf("Post action '%s' failed: %v", action.Name, err)
		}
	}

	return results, nil
}

// buildPostParams builds parameters from post.params configuration
func (pae *PostActionExecutor) buildPostParams(params []config_loader.Parameter, execCtx *ExecutionContext) error {
	// Build evaluation context with resources and adapter metadata
	evalCtx := criteria.NewEvaluationContext()
	
	// Add all existing params
	for k, v := range execCtx.Params {
		evalCtx.Set(k, v)
	}
	
	// Add resources map for CEL evaluation
	resourcesMap, err := BuildResourcesMap(execCtx.Resources)
	if err != nil {
		return fmt.Errorf("failed to build resources map: %w", err)
	}
	evalCtx.Set("resources", resourcesMap)
	
	// Add adapter metadata
	evalCtx.Set("adapter", map[string]interface{}{
		"executionStatus": execCtx.Adapter.ExecutionStatus,
		"errorReason":     execCtx.Adapter.ErrorReason,
		"errorMessage":    execCtx.Adapter.ErrorMessage,
	})

	evaluator := criteria.NewEvaluator(evalCtx)

	for _, param := range params {
		var value interface{}
		var err error

		if param.Build != nil {
			// Build complex payload from build definition
			value, err = pae.buildPayload(param.Build, evaluator, execCtx.Params)
			if err != nil {
				return fmt.Errorf("failed to build param '%s': %w", param.Name, err)
			}
		} else if param.BuildRef != "" && len(param.BuildRefContent) > 0 {
			// Build from referenced template
			value, err = pae.buildPayload(param.BuildRefContent, evaluator, execCtx.Params)
			if err != nil {
				return fmt.Errorf("failed to build param '%s' from ref: %w", param.Name, err)
			}
		} else if param.Source != "" {
			// Extract from source (handled elsewhere)
			continue
		} else {
			continue
		}

		// Convert to JSON string for API body
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal param '%s' to JSON: %w", param.Name, err)
		}
		
		execCtx.Params[param.Name] = string(jsonBytes)
		pae.log.Infof("Built post param '%s'", param.Name)
	}

	return nil
}

// buildPayload builds a payload from a build definition
// The build definition can contain expressions that need to be evaluated
func (pae *PostActionExecutor) buildPayload(build interface{}, evaluator *criteria.Evaluator, params map[string]interface{}) (interface{}, error) {
	switch v := build.(type) {
	case map[string]interface{}:
		return pae.buildMapPayload(v, evaluator, params)
	case map[interface{}]interface{}:
		converted := convertToStringKeyMap(v)
		return pae.buildMapPayload(converted, evaluator, params)
	default:
		return build, nil
	}
}

// buildMapPayload builds a map payload, evaluating expressions as needed
func (pae *PostActionExecutor) buildMapPayload(m map[string]interface{}, evaluator *criteria.Evaluator, params map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for k, v := range m {
		// Render the key
		renderedKey, err := renderTemplate(k, params)
		if err != nil {
			return nil, fmt.Errorf("failed to render key '%s': %w", k, err)
		}

		// Process the value
		processedValue, err := pae.processValue(v, evaluator, params)
		if err != nil {
			return nil, fmt.Errorf("failed to process value for key '%s': %w", k, err)
		}

		result[renderedKey] = processedValue
	}

	return result, nil
}

// processValue processes a value, evaluating expressions as needed
func (pae *PostActionExecutor) processValue(v interface{}, evaluator *criteria.Evaluator, params map[string]interface{}) (interface{}, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		// Check if this is an expression definition
		if expr, ok := val["expression"].(string); ok {
			// Evaluate CEL expression
			result, err := evaluator.EvaluateCEL(strings.TrimSpace(expr))
			if err != nil {
				pae.log.Warningf("CEL expression evaluation failed: %v", err)
				return nil, nil // Return nil for failed expressions
			}
			return result.Value, nil
		}
		
		// Check if this is a simple value definition
		if value, ok := val["value"]; ok {
			// Render template if it's a string
			if strVal, ok := value.(string); ok {
				return renderTemplate(strVal, params)
			}
			return value, nil
		}

		// Recursively process nested maps
		return pae.buildMapPayload(val, evaluator, params)

	case map[interface{}]interface{}:
		converted := convertToStringKeyMap(val)
		return pae.processValue(converted, evaluator, params)

	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			processed, err := pae.processValue(item, evaluator, params)
			if err != nil {
				return nil, err
			}
			result[i] = processed
		}
		return result, nil

	case string:
		return renderTemplate(val, params)

	default:
		return v, nil
	}
}

// executePostAction executes a single post-action
func (pae *PostActionExecutor) executePostAction(ctx context.Context, action config_loader.PostAction, execCtx *ExecutionContext) (PostActionResult, error) {
	startTime := time.Now()
	result := PostActionResult{
		Name:   action.Name,
		Status: StatusSuccess,
	}

	pae.log.Infof("Executing post action: %s", action.Name)

	// Execute log action if configured
	if action.Log != nil {
		if err := ExecuteLogAction(action.Log, execCtx, pae.log); err != nil {
			pae.log.Warningf("Log action failed in post action '%s': %v", action.Name, err)
			// Log failures are not fatal - continue execution
		}
	}

	// Execute API call if configured
	if action.APICall != nil {
		resp, err := pae.executeAPICall(ctx, action.APICall, execCtx)
		result.APICallMade = true

		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.Duration = time.Since(startTime)
			return result, NewExecutorError(PhasePostActions, action.Name, "API call failed", err)
		}

		result.APIResponse = resp.Body
		result.HTTPStatus = resp.StatusCode

		if !resp.IsSuccess() {
			result.Status = StatusFailed
			result.Error = fmt.Errorf("API returned non-success status: %d %s", resp.StatusCode, resp.Status)
			result.Duration = time.Since(startTime)
			return result, nil
		}
	}

	result.Duration = time.Since(startTime)
	pae.log.Infof("Post action '%s' completed (duration: %v)", action.Name, result.Duration)

	return result, nil
}

// executeAPICall executes an API call and returns the response
func (pae *PostActionExecutor) executeAPICall(ctx context.Context, apiCall *config_loader.APICall, execCtx *ExecutionContext) (*hyperfleet_api.Response, error) {
	// Render URL template
	url, err := renderTemplate(apiCall.URL, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render URL template: %w", err)
	}

	pae.log.Infof("Making post-action API call: %s %s", apiCall.Method, url)

	// Build request options
	opts := make([]hyperfleet_api.RequestOption, 0)

	// Add headers
	headers := make(map[string]string)
	for _, h := range apiCall.Headers {
		headerValue, err := renderTemplate(h.Value, execCtx.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to render header '%s' template: %w", h.Name, err)
		}
		headers[h.Name] = headerValue
	}
	if len(headers) > 0 {
		opts = append(opts, hyperfleet_api.WithHeaders(headers))
	}

	// Set timeout if specified
	if apiCall.Timeout != "" {
		timeout, err := time.ParseDuration(apiCall.Timeout)
		if err == nil {
			opts = append(opts, hyperfleet_api.WithRequestTimeout(timeout))
		}
	}

	// Set retry configuration
	if apiCall.RetryAttempts > 0 {
		opts = append(opts, hyperfleet_api.WithRequestRetryAttempts(apiCall.RetryAttempts))
	}
	if apiCall.RetryBackoff != "" {
		backoff := hyperfleet_api.BackoffStrategy(apiCall.RetryBackoff)
		opts = append(opts, hyperfleet_api.WithRequestRetryBackoff(backoff))
	}

	// Execute request based on method
	var resp *hyperfleet_api.Response
	switch strings.ToUpper(apiCall.Method) {
	case http.MethodGet:
		resp, err = pae.apiClient.Get(ctx, url, opts...)
	case http.MethodPost:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pae.apiClient.Post(ctx, url, body, opts...)
	case http.MethodPut:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pae.apiClient.Put(ctx, url, body, opts...)
	case http.MethodPatch:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pae.apiClient.Patch(ctx, url, body, opts...)
	case http.MethodDelete:
		resp, err = pae.apiClient.Delete(ctx, url, opts...)
	default:
		return nil, fmt.Errorf("unsupported HTTP method: %s", apiCall.Method)
	}

	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}

	pae.log.Infof("Post-action API call completed: %d %s", resp.StatusCode, resp.Status)
	return resp, nil
}


