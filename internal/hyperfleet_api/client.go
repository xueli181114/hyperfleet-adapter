package hyperfleet_api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/golang/glog"
	apierrors "github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
)

// Environment variables for API configuration
const (
	EnvBaseURL    = "HYPERFLEET_API_BASE_URL"
	EnvAPIVersion = "HYPERFLEET_API_VERSION"
)

// -----------------------------------------------------------------------------
// HTTP Client Implementation
// -----------------------------------------------------------------------------

// httpClient implements the Client interface
type httpClient struct {
	client *http.Client
	config *ClientConfig
}

// ClientOption is a functional option for configuring the client
type ClientOption func(*httpClient)

// WithHTTPClient sets a custom http.Client
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *httpClient) {
		c.client = client
	}
}

// WithConfig sets the client configuration
func WithConfig(config *ClientConfig) ClientOption {
	return func(c *httpClient) {
		if config != nil {
			c.config = config
		}
	}
}

// WithDefaultHeader adds a default header to all requests
func WithDefaultHeader(key, value string) ClientOption {
	return func(c *httpClient) {
		if c.config.DefaultHeaders == nil {
			c.config.DefaultHeaders = make(map[string]string)
		}
		c.config.DefaultHeaders[key] = value
	}
}

// WithTimeout sets the client timeout
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *httpClient) {
		c.config.Timeout = timeout
	}
}

// WithRetryAttempts sets the number of retry attempts
func WithRetryAttempts(attempts int) ClientOption {
	return func(c *httpClient) {
		c.config.RetryAttempts = attempts
	}
}

// WithRetryBackoff sets the retry backoff strategy
func WithRetryBackoff(backoff BackoffStrategy) ClientOption {
	return func(c *httpClient) {
		c.config.RetryBackoff = backoff
	}
}

// WithBaseDelay sets the base delay for retry backoff
func WithBaseDelay(delay time.Duration) ClientOption {
	return func(c *httpClient) {
		c.config.BaseDelay = delay
	}
}

// WithMaxDelay sets the maximum delay for retry backoff
func WithMaxDelay(delay time.Duration) ClientOption {
	return func(c *httpClient) {
		c.config.MaxDelay = delay
	}
}

// NewClient creates a new HyperFleet API client
func NewClient(opts ...ClientOption) Client {
	c := &httpClient{
		config: DefaultClientConfig(),
	}

	// Apply options
	for _, opt := range opts {
		opt(c)
	}

	// Create HTTP client if not provided
	if c.client == nil {
		c.client = &http.Client{
			Timeout: c.config.Timeout,
		}
	}

	return c
}

// BaseURLFromEnv returns the base URL from environment variable
func BaseURLFromEnv() string {
	return os.Getenv(EnvBaseURL)
}

// APIVersionFromEnv returns the API version from environment variable
func APIVersionFromEnv() string {
	version := os.Getenv(EnvAPIVersion)
	if version == "" {
		return "v1"
	}
	return version
}

// -----------------------------------------------------------------------------
// Client Interface Implementation
// -----------------------------------------------------------------------------

// Do executes an HTTP request with retry logic
func (c *httpClient) Do(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("request cannot be nil")
	}

	// Determine retry configuration
	retryAttempts := c.config.RetryAttempts
	if req.RetryAttempts != nil {
		retryAttempts = *req.RetryAttempts
	}
	// Normalize to ensure at least 1 attempt - zero or negative values would skip the loop entirely
	if retryAttempts < 1 {
		retryAttempts = 1
	}

	backoffStrategy := c.config.RetryBackoff
	if req.RetryBackoff != nil {
		backoffStrategy = *req.RetryBackoff
	}

	var lastErr error
	var lastResp *Response
	startTime := time.Now()

	for attempt := 1; attempt <= retryAttempts; attempt++ {
		// Check context before each attempt
		if err := ctx.Err(); err != nil {
			return nil, apierrors.NewAPIError(req.Method, req.URL, 0, "", nil, attempt, time.Since(startTime), fmt.Errorf("context cancelled: %w", err))
		}

		resp, err := c.doRequest(ctx, req)
		if err != nil {
			lastErr = err
			glog.Warningf("HyperFleet API request failed (attempt %d/%d): %v", attempt, retryAttempts, err)
		} else {
			resp.Attempts = attempt
			resp.Duration = time.Since(startTime)

			// Success or non-retryable error
			if resp.IsSuccess() || !resp.IsRetryable() {
				return resp, nil
			}

			lastResp = resp
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			glog.Warningf("HyperFleet API request returned retryable status %d (attempt %d/%d)",
				resp.StatusCode, attempt, retryAttempts)
		}

		// Don't sleep after the last attempt
		if attempt < retryAttempts {
			delay := c.calculateBackoff(attempt, backoffStrategy)
			glog.Infof("Retrying in %v...", delay)

			select {
			case <-ctx.Done():
				return nil, apierrors.NewAPIError(req.Method, req.URL, 0, "", nil, attempt, time.Since(startTime), fmt.Errorf("context cancelled during retry: %w", ctx.Err()))
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// All retries exhausted - return APIError with full details
	duration := time.Since(startTime)
	if lastResp != nil {
		lastResp.Duration = duration
		return lastResp, apierrors.NewAPIError(
			req.Method,
			req.URL,
			lastResp.StatusCode,
			lastResp.Status,
			lastResp.Body,
			retryAttempts,
			duration,
			lastErr,
		)
	}

	return nil, apierrors.NewAPIError(req.Method, req.URL, 0, "", nil, retryAttempts, duration, lastErr)
}

// doRequest performs a single HTTP request without retry logic
func (c *httpClient) doRequest(ctx context.Context, req *Request) (*Response, error) {
	// Determine timeout
	timeout := c.config.Timeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	// Create context with timeout
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create HTTP request
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, req.Method, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Add default headers
	for k, v := range c.config.DefaultHeaders {
		httpReq.Header.Set(k, v)
	}

	// Add request-specific headers (override defaults)
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Set default Content-Type for requests with body
	if len(req.Body) > 0 && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Execute request
	glog.V(2).Infof("HyperFleet API request: %s %s", req.Method, req.URL)
	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	response := &Response{
		StatusCode: httpResp.StatusCode,
		Status:     httpResp.Status,
		Headers:    httpResp.Header,
		Body:       respBody,
	}

	glog.V(2).Infof("HyperFleet API response: %d %s", response.StatusCode, response.Status)

	return response, nil
}

// calculateBackoff calculates the delay before the next retry attempt
func (c *httpClient) calculateBackoff(attempt int, strategy BackoffStrategy) time.Duration {
	baseDelay := c.config.BaseDelay
	maxDelay := c.config.MaxDelay

	var delay time.Duration

	switch strategy {
	case BackoffExponential:
		// Exponential backoff: baseDelay * 2^(attempt-1)
		delay = time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt-1)))
	case BackoffLinear:
		// Linear backoff: baseDelay * attempt
		delay = baseDelay * time.Duration(attempt)
	case BackoffConstant:
		// Constant backoff: always baseDelay
		delay = baseDelay
	default:
		delay = baseDelay
	}

	// Add jitter (±10%) to prevent thundering herd
	// Using package-level rand.Float64() which is concurrency-safe (uses locked source)
	jitter := time.Duration(rand.Float64()*0.2*float64(delay) - 0.1*float64(delay))
	delay += jitter

	// Cap at max delay
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

// -----------------------------------------------------------------------------
// Convenience Methods
// -----------------------------------------------------------------------------

// Get performs a GET request
func (c *httpClient) Get(ctx context.Context, url string, opts ...RequestOption) (*Response, error) {
	req := &Request{
		Method: http.MethodGet,
		URL:    url,
	}
	for _, opt := range opts {
		opt(req)
	}
	return c.Do(ctx, req)
}

// Post performs a POST request
func (c *httpClient) Post(ctx context.Context, url string, body []byte, opts ...RequestOption) (*Response, error) {
	req := &Request{
		Method: http.MethodPost,
		URL:    url,
		Body:   body,
	}
	for _, opt := range opts {
		opt(req)
	}
	return c.Do(ctx, req)
}

// Put performs a PUT request
func (c *httpClient) Put(ctx context.Context, url string, body []byte, opts ...RequestOption) (*Response, error) {
	req := &Request{
		Method: http.MethodPut,
		URL:    url,
		Body:   body,
	}
	for _, opt := range opts {
		opt(req)
	}
	return c.Do(ctx, req)
}

// Patch performs a PATCH request
func (c *httpClient) Patch(ctx context.Context, url string, body []byte, opts ...RequestOption) (*Response, error) {
	req := &Request{
		Method: http.MethodPatch,
		URL:    url,
		Body:   body,
	}
	for _, opt := range opts {
		opt(req)
	}
	return c.Do(ctx, req)
}

// Delete performs a DELETE request
func (c *httpClient) Delete(ctx context.Context, url string, opts ...RequestOption) (*Response, error) {
	req := &Request{
		Method: http.MethodDelete,
		URL:    url,
	}
	for _, opt := range opts {
		opt(req)
	}
	return c.Do(ctx, req)
}

