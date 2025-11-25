# HyperFleet API Client

Pure HTTP client for communicating with the HyperFleet API. Supports configurable timeouts, retry logic with multiple backoff strategies, and a clean functional options pattern.

## Features

- **Pure HTTP client**: No dependencies on config_loader or other internal packages
- **Configurable timeout**: Set HTTP request timeout per-client or per-request
- **Retry logic**: Automatic retry with configurable attempts
- **Backoff strategies**: Exponential, linear, or constant backoff with jitter
- **Functional options**: Clean configuration pattern for both client and requests
- **Response helpers**: Methods to check success, error status, and retryability

## Usage

### Basic Usage

```go
import "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/hyperfleet_api"

// Create a client with defaults
client := hyperfleet_api.NewClient()

// Make a GET request
ctx := context.Background()
resp, err := client.Get(ctx, "https://api.hyperfleet.io/v1/clusters/123")
if err != nil {
    log.Fatal(err)
}

if resp.IsSuccess() {
    fmt.Printf("Response: %s\n", resp.Body)
}
```

### With Client Options

```go
// Create client with custom configuration
client := hyperfleet_api.NewClient(
    hyperfleet_api.WithTimeout(30*time.Second),
    hyperfleet_api.WithRetryAttempts(5),
    hyperfleet_api.WithRetryBackoff(hyperfleet_api.BackoffExponential),
    hyperfleet_api.WithBaseDelay(500*time.Millisecond),
    hyperfleet_api.WithMaxDelay(30*time.Second),
    hyperfleet_api.WithDefaultHeader("X-Custom", "value"),
)
```

### Request Options

```go
// Override per-request settings
resp, err := client.Get(ctx, url,
    hyperfleet_api.WithRequestTimeout(60*time.Second),
    hyperfleet_api.WithRequestRetryAttempts(10),
    hyperfleet_api.WithHeader("X-Request-ID", requestID),
)

// POST with JSON body - use WithJSONBody to set Content-Type header automatically
body, _ := json.Marshal(payload)
resp, err := client.Post(ctx, url, nil,
    hyperfleet_api.WithJSONBody(body),
    hyperfleet_api.WithHeader("X-Request-ID", requestID),
)

// Alternatively, pass body directly (caller must set Content-Type if needed)
resp, err := client.Post(ctx, url, body,
    hyperfleet_api.WithHeader("Content-Type", "application/json"),
)
```

### Using with Adapter Config (in message handler)

```go
// In your message handler, parse config and create client:
func createAPIClient(apiConfig config_loader.HyperfleetAPIConfig) (hyperfleet_api.Client, error) {
    var opts []hyperfleet_api.ClientOption

    // Parse and set timeout using the accessor method
    timeout, err := apiConfig.ParseTimeout()
    if err != nil {
        return nil, fmt.Errorf("invalid timeout %q: %w", apiConfig.Timeout, err)
    }
    if timeout > 0 {
        opts = append(opts, hyperfleet_api.WithTimeout(timeout))
    }

    // Set retry attempts
    if apiConfig.RetryAttempts > 0 {
        opts = append(opts, hyperfleet_api.WithRetryAttempts(apiConfig.RetryAttempts))
    }

    // Parse and validate retry backoff strategy
    if apiConfig.RetryBackoff != "" {
        backoff := hyperfleet_api.BackoffStrategy(apiConfig.RetryBackoff)
        switch backoff {
        case hyperfleet_api.BackoffExponential, hyperfleet_api.BackoffLinear, hyperfleet_api.BackoffConstant:
            opts = append(opts, hyperfleet_api.WithRetryBackoff(backoff))
        default:
            return nil, fmt.Errorf("invalid retry backoff strategy %q (supported: exponential, linear, constant)", apiConfig.RetryBackoff)
        }
    }

    return hyperfleet_api.NewClient(opts...), nil
}
```

## Client Options

| Option | Description |
|--------|-------------|
| `WithTimeout(d)` | Set HTTP client timeout |
| `WithRetryAttempts(n)` | Set number of retry attempts |
| `WithRetryBackoff(b)` | Set backoff strategy |
| `WithBaseDelay(d)` | Set initial retry delay |
| `WithMaxDelay(d)` | Set maximum retry delay |
| `WithDefaultHeader(k, v)` | Add default header to all requests |
| `WithConfig(c)` | Set full ClientConfig |
| `WithHTTPClient(c)` | Use custom http.Client |

## Request Options

| Option | Description |
|--------|-------------|
| `WithHeader(k, v)` | Add header to request |
| `WithHeaders(m)` | Add multiple headers |
| `WithBody(b)` | Set request body |
| `WithJSONBody(b)` | Set body with JSON Content-Type |
| `WithRequestTimeout(d)` | Override timeout for this request |
| `WithRequestRetryAttempts(n)` | Override retry attempts |
| `WithRequestRetryBackoff(b)` | Override backoff strategy |

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `HYPERFLEET_API_BASE_URL` | Base URL for the HyperFleet API | - |
| `HYPERFLEET_API_VERSION` | API version | `v1` |

## Backoff Strategies

| Strategy | Description | Example (base=1s) |
|----------|-------------|-------------------|
| `BackoffExponential` | Doubles delay each retry | 1s, 2s, 4s, 8s... |
| `BackoffLinear` | Increases delay linearly | 1s, 2s, 3s, 4s... |
| `BackoffConstant` | Same delay between retries | 1s, 1s, 1s, 1s... |

All strategies include ±10% jitter to prevent thundering herd problems.

## Retryable Status Codes

The client treats **all 5xx responses** plus the following client errors as retryable:
- `408` Request Timeout
- `429` Too Many Requests  

Common retryable server errors include:
- `500` Internal Server Error
- `502` Bad Gateway
- `503` Service Unavailable
- `504` Gateway Timeout

Other 4xx status codes are not retried.

## Response Helpers

```go
resp.IsSuccess()     // true for 2xx status codes
resp.IsClientError() // true for 4xx status codes
resp.IsServerError() // true for 5xx status codes
resp.IsRetryable()   // true for retryable status codes

resp.StatusCode      // HTTP status code
resp.Body            // Response body as []byte
resp.Duration        // Total request duration including retries
resp.Attempts        // Number of attempts made
```

