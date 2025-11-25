package hyperfleet_api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/errors"
)

func TestNewClient(t *testing.T) {
	client := NewClient()
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestNewClientWithOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []ClientOption
	}{
		{
			name: "with timeout",
			opts: []ClientOption{
				WithTimeout(5 * time.Second),
			},
		},
		{
			name: "with retry attempts",
			opts: []ClientOption{
				WithRetryAttempts(5),
			},
		},
		{
			name: "with exponential backoff",
			opts: []ClientOption{
				WithRetryBackoff(BackoffExponential),
			},
		},
		{
			name: "with linear backoff",
			opts: []ClientOption{
				WithRetryBackoff(BackoffLinear),
			},
		},
		{
			name: "with constant backoff",
			opts: []ClientOption{
				WithRetryBackoff(BackoffConstant),
			},
		},
		{
			name: "with all options",
			opts: []ClientOption{
				WithTimeout(10 * time.Second),
				WithRetryAttempts(3),
				WithRetryBackoff(BackoffExponential),
				WithBaseDelay(500 * time.Millisecond),
				WithMaxDelay(30 * time.Second),
				WithDefaultHeader("X-Custom", "value"),
			},
		},
		{
			name: "with custom config",
			opts: []ClientOption{
				WithConfig(&ClientConfig{
					Timeout:       5 * time.Second,
					RetryAttempts: 2,
					RetryBackoff:  BackoffConstant,
					BaseDelay:     100 * time.Millisecond,
					MaxDelay:      10 * time.Second,
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.opts...)
			if client == nil {
				t.Error("client is nil")
			}
		})
	}
}

func TestClientGet(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()

	client := NewClient()
	ctx := context.Background()

	resp, err := client.Get(ctx, server.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if !resp.IsSuccess() {
		t.Error("expected IsSuccess to return true")
	}
}

func TestClientPost(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		receivedContentType = r.Header.Get("Content-Type")

		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewClient()
	ctx := context.Background()
	body := []byte(`{"key":"value"}`)

	resp, err := client.Post(ctx, server.URL+"/test", body, WithHeader("Content-Type", "application/json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}

	if string(receivedBody) != string(body) {
		t.Errorf("expected body %q, got %q", body, receivedBody)
	}

	if receivedContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", receivedContentType)
	}
}

func TestClientWithHeaders(t *testing.T) {
	var receivedAuth string
	var receivedCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom-Header")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(WithDefaultHeader("Authorization", "Bearer default-token"))
	ctx := context.Background()

	// Test with additional header
	_, err := client.Get(ctx, server.URL+"/test",
		WithHeader("X-Custom-Header", "custom-value"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "Bearer default-token" {
		t.Errorf("expected Authorization header 'Bearer default-token', got %q", receivedAuth)
	}

	if receivedCustom != "custom-value" {
		t.Errorf("expected X-Custom-Header 'custom-value', got %q", receivedCustom)
	}
}

func TestClientRetry(t *testing.T) {
	var attemptCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 3 {
			// First two attempts fail with 503
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Third attempt succeeds
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.RetryAttempts = 3
	config.BaseDelay = 10 * time.Millisecond // Short delay for tests

	client := NewClient(WithConfig(config))
	ctx := context.Background()

	resp, err := client.Get(ctx, server.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", resp.Attempts)
	}

	if atomic.LoadInt32(&attemptCount) != 3 {
		t.Errorf("expected server to receive 3 requests, got %d", attemptCount)
	}
}

func TestClientRetryExhausted(t *testing.T) {
	var attemptCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attemptCount, 1)
		// Always fail
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.RetryAttempts = 3
	config.BaseDelay = 10 * time.Millisecond

	client := NewClient(WithConfig(config))
	ctx := context.Background()

	resp, err := client.Get(ctx, server.URL+"/test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if resp == nil {
		t.Fatal("expected response even on error")
	}

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", resp.StatusCode)
	}

	if atomic.LoadInt32(&attemptCount) != 3 {
		t.Errorf("expected 3 attempts, got %d", attemptCount)
	}
}

func TestClientNoRetryOn4xx(t *testing.T) {
	var attemptCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attemptCount, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.RetryAttempts = 3

	client := NewClient(WithConfig(config))
	ctx := context.Background()

	resp, err := client.Get(ctx, server.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not retry on 400
	if atomic.LoadInt32(&attemptCount) != 1 {
		t.Errorf("expected 1 attempt (no retry on 4xx), got %d", attemptCount)
	}

	if !resp.IsClientError() {
		t.Error("expected IsClientError to return true")
	}
}

func TestClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.Timeout = 100 * time.Millisecond
	config.RetryAttempts = 1

	client := NewClient(WithConfig(config))
	ctx := context.Background()

	_, err := client.Get(ctx, server.URL+"/test")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestClientContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Get(ctx, server.URL+"/test")
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
}

func TestResponseHelpers(t *testing.T) {
	tests := []struct {
		statusCode      int
		isSuccess       bool
		isClientError   bool
		isServerError   bool
		isRetryable     bool
	}{
		{200, true, false, false, false},
		{201, true, false, false, false},
		{204, true, false, false, false},
		{400, false, true, false, false},
		{401, false, true, false, false},
		{403, false, true, false, false},
		{404, false, true, false, false},
		{408, false, true, false, true},  // Request Timeout is retryable
		{429, false, true, false, true},  // Too Many Requests is retryable
		{500, false, false, true, true},
		{502, false, false, true, true},
		{503, false, false, true, true},
		{504, false, false, true, true},
	}

	for _, tt := range tests {
		resp := &Response{StatusCode: tt.statusCode}

		if got := resp.IsSuccess(); got != tt.isSuccess {
			t.Errorf("StatusCode %d: IsSuccess() = %v, want %v", tt.statusCode, got, tt.isSuccess)
		}
		if got := resp.IsClientError(); got != tt.isClientError {
			t.Errorf("StatusCode %d: IsClientError() = %v, want %v", tt.statusCode, got, tt.isClientError)
		}
		if got := resp.IsServerError(); got != tt.isServerError {
			t.Errorf("StatusCode %d: IsServerError() = %v, want %v", tt.statusCode, got, tt.isServerError)
		}
		if got := resp.IsRetryable(); got != tt.isRetryable {
			t.Errorf("StatusCode %d: IsRetryable() = %v, want %v", tt.statusCode, got, tt.isRetryable)
		}
	}
}

func TestBackoffCalculation(t *testing.T) {
	config := DefaultClientConfig()
	config.BaseDelay = 100 * time.Millisecond
	config.MaxDelay = 10 * time.Second

	c := &httpClient{
		config: config,
	}

	// Test exponential backoff (with some tolerance for jitter)
	delay1 := c.calculateBackoff(1, BackoffExponential)
	if delay1 < 80*time.Millisecond || delay1 > 120*time.Millisecond {
		t.Errorf("exponential backoff attempt 1: expected ~100ms, got %v", delay1)
	}

	delay2 := c.calculateBackoff(2, BackoffExponential)
	if delay2 < 160*time.Millisecond || delay2 > 240*time.Millisecond {
		t.Errorf("exponential backoff attempt 2: expected ~200ms, got %v", delay2)
	}

	// Test linear backoff
	delay1 = c.calculateBackoff(1, BackoffLinear)
	if delay1 < 80*time.Millisecond || delay1 > 120*time.Millisecond {
		t.Errorf("linear backoff attempt 1: expected ~100ms, got %v", delay1)
	}

	delay2 = c.calculateBackoff(2, BackoffLinear)
	if delay2 < 160*time.Millisecond || delay2 > 240*time.Millisecond {
		t.Errorf("linear backoff attempt 2: expected ~200ms, got %v", delay2)
	}

	// Test constant backoff
	delay1 = c.calculateBackoff(1, BackoffConstant)
	delay2 = c.calculateBackoff(2, BackoffConstant)
	delay3 := c.calculateBackoff(3, BackoffConstant)

	// All should be around the base delay
	for i, d := range []time.Duration{delay1, delay2, delay3} {
		if d < 80*time.Millisecond || d > 120*time.Millisecond {
			t.Errorf("constant backoff attempt %d: expected ~100ms, got %v", i+1, d)
		}
	}
}

func TestClientPut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient()
	ctx := context.Background()

	resp, err := client.Put(ctx, server.URL+"/test", []byte(`{"id":"123"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestClientPatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient()
	ctx := context.Background()

	resp, err := client.Patch(ctx, server.URL+"/test", []byte(`{"field":"value"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestClientDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient()
	ctx := context.Background()

	resp, err := client.Delete(ctx, server.URL+"/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", resp.StatusCode)
	}
}

func TestAPIError(t *testing.T) {
	err := errors.NewAPIError(
		"POST",
		"https://api.example.com/v1/clusters/123",
		503,
		"503 Service Unavailable",
		[]byte(`{"error":"service unavailable","message":"backend is down"}`),
		3,
		5*time.Second,
		fmt.Errorf("connection refused"),
	)

	// Test Error() method
	errStr := err.Error()
	if !strings.Contains(errStr, "POST") {
		t.Errorf("error string should contain method, got: %s", errStr)
	}
	if !strings.Contains(errStr, "503") {
		t.Errorf("error string should contain status code, got: %s", errStr)
	}
	if !strings.Contains(errStr, "3 attempt") {
		t.Errorf("error string should contain attempts, got: %s", errStr)
	}

	// Test helper methods
	if !err.IsServerError() {
		t.Error("expected IsServerError to return true for 503")
	}
	if err.IsClientError() {
		t.Error("expected IsClientError to return false for 503")
	}
	if err.IsNotFound() {
		t.Error("expected IsNotFound to return false for 503")
	}

	// Test ResponseBodyString
	bodyStr := err.ResponseBodyString()
	if !strings.Contains(bodyStr, "backend is down") {
		t.Errorf("expected response body string to contain error message, got: %s", bodyStr)
	}
}

func TestAPIErrorStatusHelpers(t *testing.T) {
	tests := []struct {
		statusCode     int
		isServerError  bool
		isClientError  bool
		isNotFound     bool
		isUnauthorized bool
		isForbidden    bool
		isRateLimited  bool
	}{
		{200, false, false, false, false, false, false},
		{401, false, true, false, true, false, false},
		{403, false, true, false, false, true, false},
		{404, false, true, true, false, false, false},
		{429, false, true, false, false, false, true},
		{500, true, false, false, false, false, false},
		{502, true, false, false, false, false, false},
		{503, true, false, false, false, false, false},
	}

	for _, tt := range tests {
		err := errors.NewAPIError("GET", "http://test", tt.statusCode, "", nil, 1, time.Second, nil)

		if got := err.IsServerError(); got != tt.isServerError {
			t.Errorf("StatusCode %d: IsServerError() = %v, want %v", tt.statusCode, got, tt.isServerError)
		}
		if got := err.IsClientError(); got != tt.isClientError {
			t.Errorf("StatusCode %d: IsClientError() = %v, want %v", tt.statusCode, got, tt.isClientError)
		}
		if got := err.IsNotFound(); got != tt.isNotFound {
			t.Errorf("StatusCode %d: IsNotFound() = %v, want %v", tt.statusCode, got, tt.isNotFound)
		}
		if got := err.IsUnauthorized(); got != tt.isUnauthorized {
			t.Errorf("StatusCode %d: IsUnauthorized() = %v, want %v", tt.statusCode, got, tt.isUnauthorized)
		}
		if got := err.IsForbidden(); got != tt.isForbidden {
			t.Errorf("StatusCode %d: IsForbidden() = %v, want %v", tt.statusCode, got, tt.isForbidden)
		}
		if got := err.IsRateLimited(); got != tt.isRateLimited {
			t.Errorf("StatusCode %d: IsRateLimited() = %v, want %v", tt.statusCode, got, tt.isRateLimited)
		}
	}
}

func TestIsAPIError(t *testing.T) {
	// Test with APIError
	apiErr := errors.NewAPIError("GET", "http://test", 500, "500 Internal Server Error", nil, 1, time.Second, nil)
	if extracted, ok := errors.IsAPIError(apiErr); !ok {
		t.Error("IsAPIError should return true for APIError")
	} else if extracted.StatusCode != 500 {
		t.Errorf("extracted error should have status 500, got %d", extracted.StatusCode)
	}

	// Test with wrapped APIError
	wrappedErr := fmt.Errorf("wrapped: %w", apiErr)
	if extracted, ok := errors.IsAPIError(wrappedErr); !ok {
		t.Error("IsAPIError should return true for wrapped APIError")
	} else if extracted.StatusCode != 500 {
		t.Errorf("extracted error should have status 500, got %d", extracted.StatusCode)
	}

	// Test with non-APIError
	regularErr := fmt.Errorf("regular error")
	if _, ok := errors.IsAPIError(regularErr); ok {
		t.Error("IsAPIError should return false for regular error")
	}
}

func TestAPIErrorInRetryExhausted(t *testing.T) {
	var attemptCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attemptCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"service_unavailable","message":"backend overloaded"}`))
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.RetryAttempts = 2
	config.BaseDelay = 10 * time.Millisecond

	client := NewClient(WithConfig(config))
	ctx := context.Background()

	_, err := client.Get(ctx, server.URL+"/test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Check if it's an APIError with proper details
	apiErr, ok := errors.IsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}

	if apiErr.Method != "GET" {
		t.Errorf("expected method GET, got %s", apiErr.Method)
	}
	if apiErr.StatusCode != 503 {
		t.Errorf("expected status 503, got %d", apiErr.StatusCode)
	}
	if apiErr.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", apiErr.Attempts)
	}
	if !strings.Contains(apiErr.ResponseBodyString(), "backend overloaded") {
		t.Errorf("expected response body to contain error message, got: %s", apiErr.ResponseBodyString())
	}
	if !apiErr.IsServerError() {
		t.Error("expected IsServerError to return true")
	}
}