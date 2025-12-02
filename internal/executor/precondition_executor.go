package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

// PreconditionExecutor evaluates preconditions
type PreconditionExecutor struct {
	apiClient hyperfleet_api.Client
	log       logger.Logger
}

// NewPreconditionExecutor creates a new precondition executor
func NewPreconditionExecutor(apiClient hyperfleet_api.Client, log logger.Logger) *PreconditionExecutor {
	return &PreconditionExecutor{
		apiClient: apiClient,
		log:       log,
	}
}

// ExecuteAll executes all preconditions in sequence
// Returns results for each precondition and updates the execution context
func (pe *PreconditionExecutor) ExecuteAll(ctx context.Context, preconditions []config_loader.Precondition, execCtx *ExecutionContext) ([]PreconditionResult, error) {
	results := make([]PreconditionResult, 0, len(preconditions))

	for _, precond := range preconditions {
		result, err := pe.executePrecondition(ctx, precond, execCtx)
		results = append(results, result)

		if err != nil {
			return results, err
		}

		if !result.Matched {
			// Precondition not met - return immediately
			return results, &PreconditionNotMetError{
				Name:    precond.Name,
				Reason:  "conditions not satisfied",
				Details: formatConditionDetails(result),
			}
		}
	}

	return results, nil
}

// executePrecondition executes a single precondition
func (pe *PreconditionExecutor) executePrecondition(ctx context.Context, precond config_loader.Precondition, execCtx *ExecutionContext) (PreconditionResult, error) {
	startTime := time.Now()
	result := PreconditionResult{
		Name:            precond.Name,
		Status:          StatusSuccess,
		ExtractedFields: make(map[string]interface{}),
	}

	pe.log.Infof("Evaluating precondition: %s", precond.Name)

	// Step 1: Execute log action if configured
	if precond.Log != nil {
		if err := ExecuteLogAction(precond.Log, execCtx, pe.log); err != nil {
			pe.log.Warningf("Log action failed in precondition '%s': %v", precond.Name, err)
			// Log failures are not fatal - continue execution
		}
	}

	// Step 2: Make API call if configured
	if precond.APICall != nil {
		apiResult, err := pe.executeAPICall(ctx, precond.APICall, execCtx)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.Duration = time.Since(startTime)
			return result, NewExecutorError(PhasePreconditions, precond.Name, "API call failed", err)
		}
		result.APICallMade = true
		result.APIResponse = apiResult

		// Parse response as JSON
		var responseData map[string]interface{}
		if err := json.Unmarshal(apiResult, &responseData); err != nil {
			result.Status = StatusFailed
			result.Error = fmt.Errorf("failed to parse API response as JSON: %w", err)
			result.Duration = time.Since(startTime)
			return result, NewExecutorError(PhasePreconditions, precond.Name, "failed to parse API response", err)
		}

		// Store response if configured
		if precond.StoreResponseAs != "" {
			execCtx.Responses[precond.StoreResponseAs] = responseData
			execCtx.Params[precond.StoreResponseAs] = responseData
		}

		// Extract fields from response
		if len(precond.Extract) > 0 {
			for _, extract := range precond.Extract {
				value, err := extractFieldFromData(responseData, extract.Field)
				if err != nil {
					pe.log.Warningf("Failed to extract field '%s' as '%s': %v", extract.Field, extract.As, err)
					continue
				}
				result.ExtractedFields[extract.As] = value
				execCtx.Params[extract.As] = value
			}
		}
	}

	// Step 3: Evaluate conditions
	// Create evaluation context with all params and responses
	evalCtx := criteria.NewEvaluationContext()
	for k, v := range execCtx.Params {
		evalCtx.Set(k, v)
	}
	for k, v := range execCtx.Responses {
		evalCtx.Set(k, v)
	}

	evaluator := criteria.NewEvaluator(evalCtx)

	// Evaluate using structured conditions or CEL expression
	evalStartTime := time.Now()
	if len(precond.Conditions) > 0 {
		condDefs := ToConditionDefs(precond.Conditions)

		condResult, err := evaluator.EvaluateConditionsWithResult(condDefs)
		evalDuration := time.Since(evalStartTime)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.Duration = time.Since(startTime)
			return result, NewExecutorError(PhasePreconditions, precond.Name, "condition evaluation failed", err)
		}

		result.Matched = condResult.Matched
		result.ConditionResults = condResult.Results

		// Record evaluation in execution context - reuse criteria.EvaluationResult directly
		fieldResults := make(map[string]criteria.EvaluationResult, len(condResult.Results))
		for _, cr := range condResult.Results {
			fieldResults[cr.Field] = cr
		}
		execCtx.AddConditionsEvaluation(PhasePreconditions, precond.Name, condResult.Matched, evalDuration, fieldResults)
	} else if precond.Expression != "" {
		// Evaluate CEL expression
		celResult, err := evaluator.EvaluateCEL(strings.TrimSpace(precond.Expression))
		evalDuration := time.Since(evalStartTime)
		if err != nil {
			result.Status = StatusFailed
			result.Error = err
			result.Duration = time.Since(startTime)
			return result, NewExecutorError(PhasePreconditions, precond.Name, "CEL expression evaluation failed", err)
		}

		result.Matched = celResult.Matched
		result.CELResult = celResult

		// Record CEL evaluation in execution context
		execCtx.AddCELEvaluation(PhasePreconditions, precond.Name, precond.Expression, celResult.Matched, evalDuration)
	} else {
		// No conditions specified - consider it matched
		result.Matched = true
	}

	result.Duration = time.Since(startTime)

	if result.Matched {
		pe.log.Infof("Precondition '%s' satisfied", precond.Name)
	} else {
		pe.log.Warningf("Precondition '%s' not satisfied", precond.Name)
	}

	return result, nil
}

// executeAPICall executes an API call and returns the response body
func (pe *PreconditionExecutor) executeAPICall(ctx context.Context, apiCall *config_loader.APICall, execCtx *ExecutionContext) ([]byte, error) {
	// Render URL template
	url, err := renderTemplate(apiCall.URL, execCtx.Params)
	if err != nil {
		return nil, fmt.Errorf("failed to render URL template: %w", err)
	}

	pe.log.Infof("Making API call: %s %s", apiCall.Method, url)

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
		resp, err = pe.apiClient.Get(ctx, url, opts...)
	case http.MethodPost:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pe.apiClient.Post(ctx, url, body, opts...)
	case http.MethodPut:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pe.apiClient.Put(ctx, url, body, opts...)
	case http.MethodPatch:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		resp, err = pe.apiClient.Patch(ctx, url, body, opts...)
	case http.MethodDelete:
		resp, err = pe.apiClient.Delete(ctx, url, opts...)
	default:
		return nil, fmt.Errorf("unsupported HTTP method: %s", apiCall.Method)
	}

	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API request returned non-success status: %d %s", resp.StatusCode, resp.Status)
	}

	pe.log.Infof("API call completed: %d %s", resp.StatusCode, resp.Status)
	return resp.Body, nil
}

// extractFieldFromData extracts a field from data using dot notation
func extractFieldFromData(data map[string]interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	var current interface{} = data

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		case map[interface{}]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field '%s' not found at path '%s'", part, strings.Join(parts[:i+1], "."))
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access field '%s': parent is not a map (got %T)", part, current)
		}
	}

	return current, nil
}

// renderTemplate renders a Go template string with the given data
func renderTemplate(templateStr string, data map[string]interface{}) (string, error) {
	// If no template delimiters, return as-is
	if !strings.Contains(templateStr, "{{") {
		return templateStr, nil
	}

	tmpl, err := template.New("template").Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// renderTemplateBytes renders a Go template string and returns bytes
func renderTemplateBytes(templateStr string, data map[string]interface{}) ([]byte, error) {
	result, err := renderTemplate(templateStr, data)
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// formatConditionDetails formats condition evaluation details for error messages
func formatConditionDetails(result PreconditionResult) string {
	var details []string

	if result.CELResult != nil && result.CELResult.HasError() {
		details = append(details, fmt.Sprintf("CEL error: %s", result.CELResult.ErrorReason))
	}

	for _, condResult := range result.ConditionResults {
		if !condResult.Matched {
			details = append(details, fmt.Sprintf("%s %s %v (actual: %v)",
				condResult.Field, condResult.Operator, condResult.ExpectedValue, condResult.FieldValue))
		}
	}

	if len(details) == 0 {
		return "no specific details available"
	}

	return strings.Join(details, "; ")
}


