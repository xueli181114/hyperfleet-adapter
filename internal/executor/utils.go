package executor

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/criteria"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"
	apierrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ToConditionDefs converts config_loader.Condition slice to criteria.ConditionDef slice.
// This centralizes the conversion logic that was previously repeated in multiple places.
func ToConditionDefs(conditions []config_loader.Condition) []criteria.ConditionDef {
	defs := make([]criteria.ConditionDef, len(conditions))
	for i, cond := range conditions {
		defs[i] = criteria.ConditionDef{
			Field:    cond.Field,
			Operator: criteria.Operator(cond.Operator),
			Value:    cond.Value,
		}
	}
	return defs
}

// ExecuteLogAction executes a log action with the given context
// The message is rendered as a Go template with access to all params
// This is a shared utility function used by both PreconditionExecutor and PostActionExecutor
func ExecuteLogAction(ctx context.Context, logAction *config_loader.LogAction, execCtx *ExecutionContext, log logger.Logger) {
	if logAction == nil || logAction.Message == "" {
		return
	}

	// Render the message template
	message, err := renderTemplate(logAction.Message, execCtx.Params)
	if err != nil {
		errCtx := logger.WithErrorField(ctx, err)
		log.Errorf(errCtx, "failed to render log message")
		return
	}

	// Log at the specified level (default: info)
	level := strings.ToLower(logAction.Level)
	if level == "" {
		level = "info"
	}

	switch level {
	case "debug":
		log.Debugf(ctx, "[config] %s", message)
	case "info":
		log.Infof(ctx, "[config] %s", message)
	case "warning", "warn":
		log.Warnf(ctx, "[config] %s", message)
	case "error":
		log.Errorf(ctx, "[config] %s", message)
	default:
		log.Infof(ctx, "[config] %s", message)
	}

}

// ExecuteAPICall executes an API call with the given configuration and returns the response and rendered URL
// This is a shared utility function used by both PreconditionExecutor and PostActionExecutor
// On error, it returns an APIError with full context (method, URL, status, body, attempts, duration)
// Returns: response, renderedURL, error
func ExecuteAPICall(ctx context.Context, apiCall *config_loader.APICall, execCtx *ExecutionContext, apiClient hyperfleet_api.Client, log logger.Logger) (*hyperfleet_api.Response, string, error) {
	if apiCall == nil {
		return nil, "", fmt.Errorf("apiCall is nil")
	}

	// First render the URL template to resolve variables like {{ .hyperfleetApiBaseUrl }}
	renderedURL, err := renderTemplate(apiCall.URL, execCtx.Params)
	if err != nil {
		return nil, "", fmt.Errorf("failed to render URL template: %w", err)
	}

	// Then build the final URL - this handles absolute URLs vs relative paths
	url := buildHyperfleetAPICallURL(renderedURL, execCtx)

	log.Infof(ctx, "Making API call: %s %s", apiCall.Method, url)

	// Build request options
	opts := make([]hyperfleet_api.RequestOption, 0)

	// Add headers
	headers := make(map[string]string)
	for _, h := range apiCall.Headers {
		headerValue, err := renderTemplate(h.Value, execCtx.Params)
		if err != nil {
			return nil, url, fmt.Errorf("failed to render header '%s' template: %w", h.Name, err)
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
		} else {
			log.Warnf(ctx, "failed to parse timeout '%s': %v, using default timeout", apiCall.Timeout, err)
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
		resp, err = apiClient.Get(ctx, url, opts...)
	case http.MethodPost:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, url, fmt.Errorf("failed to render body template: %w", err)
			}
		}
		log.Debugf(ctx, "API call payload: %s %s payload=%s", apiCall.Method, url, string(body))
		resp, err = apiClient.Post(ctx, url, body, opts...)
		// Log body on failure for debugging
		if err != nil || (resp != nil && !resp.IsSuccess()) {
			var logErr error
			if err != nil {
				logErr = err
			} else {
				logErr = fmt.Errorf("POST %s returned non-success status: %d", url, resp.StatusCode)
			}
			errCtx := logger.WithErrorField(ctx, logErr)
			log.Error(errCtx, "Request failed")
		}
	case http.MethodPut:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, "", fmt.Errorf("failed to render body template: %w", err)
			}
		}
		log.Debugf(ctx, "API call payload: %s %s payload=%s", apiCall.Method, url, string(body))
		resp, err = apiClient.Put(ctx, url, body, opts...)
	case http.MethodPatch:
		body := []byte(apiCall.Body)
		if apiCall.Body != "" {
			body, err = renderTemplateBytes(apiCall.Body, execCtx.Params)
			if err != nil {
				return nil, "", fmt.Errorf("failed to render body template: %w", err)
			}
		}
		log.Debugf(ctx, "API call payload: %s %s payload=%s", apiCall.Method, url, string(body))
		resp, err = apiClient.Patch(ctx, url, body, opts...)
	case http.MethodDelete:
		resp, err = apiClient.Delete(ctx, url, opts...)
	default:
		return nil, url, fmt.Errorf("unsupported HTTP method: %s", apiCall.Method)
	}

	if err != nil {
		// Return response AND error - response may contain useful details even on error
		// (e.g., HTTP status code, response body)
		if resp != nil {
			log.Warnf(ctx, "API call failed: %d %s, error: %v", resp.StatusCode, resp.Status, err)
			// Wrap as APIError with full context
			apiErr := apierrors.NewAPIError(
				apiCall.Method,
				url,
				resp.StatusCode,
				resp.Status,
				resp.Body,
				resp.Attempts,
				resp.Duration,
				err,
			)
			return resp, url, apiErr
		} else {
			log.Warnf(ctx, "API call failed: %v", err)
			// No response - create APIError with minimal context
			apiErr := apierrors.NewAPIError(
				apiCall.Method,
				url,
				0,
				"",
				nil,
				0,
				0,
				err,
			)
			return resp, url, apiErr
		}
	}
	if resp == nil {
		nilErr := fmt.Errorf("API client returned nil response without error")
		return nil, url, apierrors.NewAPIError(apiCall.Method, url, 0, "", nil, 0, 0, nilErr)
	}

	log.Infof(ctx, "API call completed: %d %s", resp.StatusCode, resp.Status)
	return resp, url, nil
}

// buildHyperfleetAPICallURL builds a full HyperFleet API URL when a relative path is provided.
// It uses hyperfleet API client settings from execution context config.
// Since the hyperfleet_api.Client always prepends its baseURL to the path,
// this function returns a relative path that the client can use correctly.
// If the URL is absolute and contains the baseURL, the relative path is extracted.
func buildHyperfleetAPICallURL(apiCallURL string, execCtx *ExecutionContext) string {
	if apiCallURL == "" {
		return apiCallURL
	}
	if execCtx == nil || execCtx.Config == nil {
		return apiCallURL
	}

	// Parse the input URL to check if it's absolute
	parsedURL, err := url.Parse(apiCallURL)
	if err != nil {
		return apiCallURL
	}

	// If the URL is absolute (has a scheme like http:// or https://)
	if parsedURL.Scheme != "" {
		// Parse the baseURL to extract its path for comparison
		baseURLStr := execCtx.Config.Clients.HyperfleetAPI.BaseURL
		if baseURLStr == "" {
			return apiCallURL
		}

		baseURL, err := url.Parse(baseURLStr)
		if err != nil {
			return apiCallURL
		}

		// Check if the absolute URL starts with our baseURL (same scheme, host, and path prefix)
		if parsedURL.Scheme == baseURL.Scheme && parsedURL.Host == baseURL.Host {
			// Extract the relative path by removing the baseURL's path prefix
			basePath := strings.TrimSuffix(baseURL.Path, "/")
			relativePath := parsedURL.Path
			if basePath != "" && strings.HasPrefix(relativePath, basePath) {
				relativePath = strings.TrimPrefix(relativePath, basePath)
			}
			// Ensure the path starts with /
			if !strings.HasPrefix(relativePath, "/") {
				relativePath = "/" + relativePath
			}
			return relativePath
		}

		// For absolute URLs not matching our baseURL, return as-is
		return apiCallURL
	}

	// For relative URLs, ensure proper formatting
	baseURLStr := execCtx.Config.Clients.HyperfleetAPI.BaseURL
	if baseURLStr == "" {
		return apiCallURL
	}

	// Clean the path and check if it already has the api/ prefix
	cleanPath := path.Clean(parsedURL.Path)
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	if strings.HasPrefix(cleanPath, "api/") {
		// Already has api/ prefix, return with leading slash
		return "/" + cleanPath
	}

	// Build the full API path using path.Join for clean path handling
	version := execCtx.Config.Clients.HyperfleetAPI.Version
	if version == "" {
		version = "v1"
	}
	return path.Join("/api/hyperfleet", version, cleanPath)
}

// ValidateAPIResponse checks if an API response is valid and successful
// Returns an APIError with full context if response is nil or unsuccessful
// method and url are used to construct APIError with proper context
func ValidateAPIResponse(resp *hyperfleet_api.Response, err error, method, url string) error {
	if err != nil {
		// If it's already an APIError, return it as-is
		if _, ok := apierrors.IsAPIError(err); ok { //nolint:errcheck // checking type only, not using the value
			return err
		}
		// Otherwise wrap it as APIError
		return apierrors.NewAPIError(method, url, 0, "", nil, 0, 0, err)
	}

	if resp == nil {
		nilErr := fmt.Errorf("API response is nil")
		return apierrors.NewAPIError(method, url, 0, "", nil, 0, 0, nilErr)
	}

	if !resp.IsSuccess() {
		errMsg := fmt.Sprintf("API returned non-success status: %d %s", resp.StatusCode, resp.Status)
		if len(resp.Body) > 0 {
			errMsg = fmt.Sprintf("%s, response body: %s", errMsg, string(resp.Body))
		}
		baseErr := fmt.Errorf("%s", errMsg)
		return apierrors.NewAPIError(
			method,
			url,
			resp.StatusCode,
			resp.Status,
			resp.Body,
			resp.Attempts,
			resp.Duration,
			baseErr,
		)
	}

	return nil
}

// renderTemplate renders a Go template string with the given data
// templateFuncs provides common functions for Go templates
var templateFuncs = template.FuncMap{
	// Time functions
	"now": time.Now,
	"date": func(layout string, t time.Time) string {
		return t.Format(layout)
	},
	"dateFormat": func(layout string, t time.Time) string {
		return t.Format(layout)
	},
	// String functions
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"title": func(s string) string {
		return cases.Title(language.English).String(s)
	},
	"trim":      strings.TrimSpace,
	"replace":   strings.ReplaceAll,
	"contains":  strings.Contains,
	"hasPrefix": strings.HasPrefix,
	"hasSuffix": strings.HasSuffix,
	// Default value function
	"default": func(defaultVal, val interface{}) interface{} {
		if val == nil || val == "" {
			return defaultVal
		}
		return val
	},
	// Quote function
	"quote": func(s string) string {
		return fmt.Sprintf("%q", s)
	},
	// Type conversion functions
	"int": func(v interface{}) int {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			i, _ := strconv.Atoi(val) //nolint:errcheck // returns 0 on error, which is acceptable
			return i
		default:
			return 0
		}
	},
	"int64": func(v interface{}) int64 {
		switch val := v.(type) {
		case int:
			return int64(val)
		case int64:
			return val
		case float64:
			return int64(val)
		case string:
			i, _ := strconv.ParseInt(val, 10, 64) //nolint:errcheck // returns 0 on error, which is acceptable
			return i
		default:
			return 0
		}
	},
	"float64": func(v interface{}) float64 {
		switch val := v.(type) {
		case int:
			return float64(val)
		case int64:
			return float64(val)
		case float64:
			return val
		case string:
			f, _ := strconv.ParseFloat(val, 64) //nolint:errcheck // returns 0 on error, which is acceptable
			return f
		default:
			return 0
		}
	},
	"string": func(v interface{}) string {
		return fmt.Sprintf("%v", v)
	},
}

// This is a shared utility used across preconditions, resources, and post-actions
func renderTemplate(templateStr string, data map[string]interface{}) (string, error) {
	// If no template delimiters, return as-is
	if !strings.Contains(templateStr, "{{") {
		return templateStr, nil
	}

	tmpl, err := template.New("template").Funcs(templateFuncs).Option("missingkey=error").Parse(templateStr)
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

// executionErrorToMap converts an ExecutionError struct to a map for CEL evaluation
// Returns nil if the ExecutionError pointer is nil
func executionErrorToMap(execErr *ExecutionError) interface{} {
	if execErr == nil {
		return nil
	}

	return map[string]interface{}{
		"phase":   execErr.Phase,
		"step":    execErr.Step,
		"message": execErr.Message,
	}
}

// adapterMetadataToMap converts AdapterMetadata struct to a map for CEL evaluation
func adapterMetadataToMap(adapter *AdapterMetadata) map[string]interface{} {
	if adapter == nil {
		return map[string]interface{}{}
	}

	return map[string]interface{}{
		"executionStatus":  adapter.ExecutionStatus,
		"resourcesSkipped": adapter.ResourcesSkipped,
		"skipReason":       adapter.SkipReason,
		"errorReason":      adapter.ErrorReason,
		"errorMessage":     adapter.ErrorMessage,
		"executionError":   executionErrorToMap(adapter.ExecutionError),
	}
}
