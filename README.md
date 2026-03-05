# HyperFleet Adapter

HyperFleet Adapter Framework - configuration driven framework to run tasks for cluster provisioning.

An instance of an adapter targets an specific resource such as a Cluster or NodePool. And provides a clear workflow:

- **Listens to events**: A CloudEvent informs what resource to process.
  - Supports different types of brokers via hyperfleet-broker lib.
- **Param phase**: Gets parameters from environment, event and current resource status (by querying HyperFleet API)
- **Decision phase**: Computes where an action has to be performed to a resource using the params
- **Resource phase**: Creates resources using a configured client
  - Kubernetes client: local or remote cluster
  - Maestro client: remote cluster via Maestro server
- **Status reporting**: Reports result of task execution to HyperFleet API
  - Builds the payload evaluating the status of the resources created in the resource phase

## Prerequisites

- Go 1.24.6 or later
- Docker (for building Docker images)
- Kubernetes 1.19+ (for deployment)
- Helm 3.0+ (for Helm chart deployment)
- `golangci-lint` (for linting, optional)

## Getting Started

### Clone the Repository

```bash
git clone https://github.com/openshift-hyperfleet/hyperfleet-adapter.git
cd hyperfleet-adapter
```

### Install Dependencies

```bash
make mod-tidy
```

### Build

```bash
# Build the binary
make build

# The binary will be created at: bin/hyperfleet-adapter
```

### Run Tests

```bash
# Run unit tests
make test

# Run integration tests (pre-built envtest - unprivileged, CI/CD friendly)
make test-integration

# Run integration tests with K3s (faster, may need privileges)
make test-integration-k3s

# Run all tests
make test-all
```

### Linting

```bash
# Run linter
make lint

# Format code
make fmt
```

## Development

### Project Structure

```
hyperfleet-adapter/
├── cmd/
│   └── adapter/            # Main application entry point
├── pkg/
│   ├── constants/          # Shared constants (annotations, labels)
│   ├── errors/             # Error handling utilities
│   ├── health/             # Health and metrics servers
│   ├── logger/             # Structured logging with context support
│   ├── otel/               # OpenTelemetry tracing utilities
│   ├── utils/              # General utility functions
│   └── version/            # Version information
├── internal/
│   ├── config_loader/      # Configuration loading and validation
│   ├── criteria/           # Precondition and CEL evaluation
│   ├── executor/           # Event execution engine (phases pipeline)
│   ├── hyperfleet_api/     # HyperFleet API client
│   ├── k8s_client/         # Kubernetes client wrapper
│   ├── maestro_client/     # Maestro/OCM ManifestWork client
│   ├── manifest/           # Manifest utilities (generation, rendering)
│   └── transport_client/   # TransportClient interface (unified apply)
├── test/
│   └── integration/        # Integration tests
│       ├── config-loader/  # Config loader integration tests
│       ├── executor/       # Executor integration tests
│       ├── k8s_client/     # K8s client integration tests
│       ├── maestro_client/ # Maestro client integration tests
│       └── testutil/       # Test utilities
├── charts/                 # Helm chart for Kubernetes deployment
├── configs/                # Configuration templates and examples
├── scripts/                # Build and test scripts
├── Dockerfile              # Multi-stage Docker build
├── Makefile                # Build and test automation
├── go.mod                  # Go module dependencies
└── README.md               # This file
```

### Available Make Targets

| Target | Description |
|--------|-------------|
| `make build` | Build binary |
| `make test` | Run unit tests |
| `make test-integration` | Run integration tests with pre-built envtest (unprivileged, CI/CD friendly) |
| `make test-integration-k3s` | Run integration tests with K3s (faster, may need privileges) |
| `make test-all` | Run all tests (unit + integration) |
| `make test-coverage` | Generate test coverage report |
| `make lint` | Run golangci-lint |
| `make image` | Build container image |
| `make image-push` | Build and push container image to registry |
| `make image-dev` | Build and push to personal Quay registry (requires QUAY_USER) |
| `make fmt` | Format code |
| `make mod-tidy` | Tidy Go module dependencies |
| `make clean` | Clean build artifacts |
| `make verify` | Run lint and test |

💡 **Tip:** Use `make help` to see all available targets with descriptions

### Tool Dependency Management (Bingo)

HyperFleet Adapter uses [bingo](https://github.com/bwplotka/bingo) to manage Go tool dependencies with pinned versions.

**Managed tools**:

- `goimports` - Code formatting and import organization
- `golangci-lint` - Code linting

**Common operations**:

```bash
# Install all tools
bingo get

# Install a specific tool
bingo get <tool>

# Update a tool to latest version
bingo get <tool>@latest

# List all managed tools
bingo list
```

Tool versions are tracked in `.bingo/*.mod` files and loaded automatically via `include .bingo/Variables.mk` in the Makefile.

### Configuration

A  HyperFleet Adapter requires several files for configuration:

- **Adapter config**: Configures the adapter framework application
- **Adapter Task config**: Configures the adapter task steps that will create resources
- **Broker configuration**: Configures the specific broker to use by the adapter framework to receive CloudEvents

To see all configuration options read [configuration.md](docs/configuration.md) file

#### Adapter configuration

The adapter deployment configuration (`AdapterConfig`) controls runtime and infrastructure
settings for the adapter process, such as client connections, retries, and broker
subscription details. It is loaded with Viper, so values can be overridden by CLI flags
and environment variables in this priority order: CLI flags > env vars > file > defaults.

Fields use **snake_case** naming.

- **Path**: `HYPERFLEET_ADAPTER_CONFIG` (required)
- **Common fields**: `adapter.name`, `adapter.version`, `debug_config`, `clients.*`
  (HyperFleet API: `clients.hyperfleet_api`, Maestro: `clients.maestro`, broker: `clients.broker`, Kubernetes: `clients.kubernetes`)

Reference examples:

- `configs/adapter-deployment-config.yaml` (full reference with env/flag notes)
- `charts/examples/kubernetes/adapter-config.yaml` (minimal deployment example)

#### Adapter task configuration

The adapter task configuration (`AdapterTaskConfig`) defines the **business logic** for
processing events: parameters, preconditions, resources to create, and post-actions.
This file is loaded as **static YAML** (no Viper overrides) and is required at runtime.

- **Path**: `HYPERFLEET_TASK_CONFIG` (required)
- **Key sections**: `params`, `preconditions`, `resources`, `post`
- **Resource manifests**: inline YAML or external file via `manifest.ref`

Reference examples:

- `charts/examples/kubernetes/adapter-task-config.yaml` (worked example)
- `configs/adapter-task-config-template.yaml` (complete schema reference)

### Broker Configuration

Broker configuration is particular since responsibility is split between:

- **Hyperfleet broker library**: configures the connection to a concrete broker (google pubsub, rabbitmq, ...)
  - Configured using a YAML file specified by the `BROKER_CONFIG_FILE` environment variable
- **Adapter**: configures which topic/subscriptions to use on the broker
  - Configure topic/subscription in the `adapter-config.yaml` but can be overridden with env variables or cli params

See the Helm chart documentation for broker configuration options.

## Dry-Run Mode

Dry-run mode lets you simulate the full adapter execution pipeline locally without connecting to any real infrastructure (no broker, no Kubernetes cluster, no HyperFleet API). It processes a single CloudEvent from a JSON file and produces a detailed trace of what the adapter would do.

### Usage

```bash
hyperfleet-adapter serve \
  --config ./adapter-config.yaml \
  --task-config ./task-config.yaml \
  --dry-run-event ./event.json
```

Dry-run mode is activated when the `--dry-run-event` flag is present. Instead of subscribing to a broker, the adapter loads the event from the specified file and runs through all phases (params, preconditions, resources, post-actions) using mock clients.

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--dry-run-event <path>` | Yes | Path to a CloudEvent JSON file to process |
| `--dry-run-api-responses <path>` | No | Path to mock API responses JSON file (defaults to 200 OK for all requests) |
| `--dry-run-discovery <path>` | No | Path to mock discovery overrides JSON file (simulates server-populated fields) |
| `--dry-run-verbose` | No | Show rendered manifests and API request/response bodies in output |
| `--dry-run-output <format>` | No | Output format: `text` (default) or `json` |

### Input Files

#### CloudEvent File (`--dry-run-event`)

A standard CloudEvents JSON file:

```json
{
  "specversion": "1.0",
  "id": "abc123",
  "type": "io.hyperfleet.cluster.updated",
  "source": "/api/clusters_mgmt/v1/clusters/abc123",
  "time": "2025-01-15T10:30:00Z",
  "datacontenttype": "application/json",
  "data": {
    "id": "abc123",
    "kind": "Cluster",
    "href": "/api/clusters_mgmt/v1/clusters/abc123",
    "generation": 5
  }
}
```

#### Mock API Responses File (`--dry-run-api-responses`)

Defines canned responses for HyperFleet API calls. Requests are matched by HTTP method and URL regex pattern. When multiple responses are defined for a match, they are returned sequentially (the last response repeats):

```json
{
  "responses": [
    {
      "match": {
        "method": "GET",
        "urlPattern": "/api/hyperfleet/v1/clusters/.*"
      },
      "responses": [
        {
          "statusCode": 200,
          "headers": { "Content-Type": "application/json" },
          "body": { "id": "abc-123", "name": "abc123", "kind": "Cluster" }
        }
      ]
    }
  ]
}
```

If no file is provided, all API requests return 200 OK by default.

#### Discovery Overrides File (`--dry-run-discovery`)

Maps rendered resource names to complete resource objects, allowing you to simulate server-populated fields (status, uid, resourceVersion, etc.) that would normally be set by the Kubernetes API server:

```json
{
  "rendered-resource-name": {
    "apiVersion": "work.open-cluster-management.io/v1",
    "kind": "ManifestWork",
    "metadata": { "name": "manifestwork-001", "namespace": "cluster1" },
    "status": { "conditions": [{ "type": "Applied", "status": "True" }] }
  }
}
```

These overrides replace applied manifests in the in-memory store, making the simulated discovery results available as `resources.*` in post-action CEL expressions.

Having the discovery mocked is useful to develop the status payload to return to the hyperfleet_api

### Output

The trace output shows the result of each execution phase:

1. **Event Info** - Event ID and type
2. **Phase 1: Parameter Extraction** - Extracted parameters and their values
3. **Phase 2: Preconditions** - Precondition evaluation results (SUCCESS/FAILED/NOT MET) with API calls made
4. **Phase 3: Resources** - Resource operations (CREATE/UPDATE/RECREATE) with kind, namespace, and name
5. **Phase 3.5: Discovery Results** - Resources available for post-action CEL evaluation
6. **Phase 4: Post Actions** - Post-action API calls and skip reasons
7. **Result** - Overall SUCCESS or FAILED

Use `--dry-run-verbose` to include rendered manifests and full API request/response bodies.

Use `--dry-run-output json` for structured JSON output suitable for programmatic consumption.

### Examples

Minimal dry-run (mock API returns 200 OK for everything):

```bash
hyperfleet-adapter serve \
  --config ./adapter-config.yaml \
  --task-config ./task-config.yaml \
  --dry-run-event ./event.json
```

Full dry-run with mock API responses, discovery overrides, and verbose JSON output:

```bash
hyperfleet-adapter serve \
  --config ./adapter-config.yaml \
  --task-config ./task-config.yaml \
  --dry-run-event ./event.json \
  --dry-run-api-responses ./api-responses.json \
  --dry-run-discovery ./discovery-overrides.json \
  --dry-run-verbose \
  --dry-run-output json
```

Example input files are available in `test/testdata/dryrun/`.

## Deployment

### Using Helm Chart

The project includes a Helm chart for Kubernetes deployment.

```bash
# Install the chart
helm install hyperfleet-adapter ./charts/

# Install with custom values
helm install hyperfleet-adapter ./charts/ -f ./charts/examples/values.yaml

# Upgrade deployment
helm upgrade hyperfleet-adapter ./charts/

# Uninstall
helm delete hyperfleet-adapter
```

For detailed Helm chart documentation, see [charts/README.md](./charts/README.md).

### Container Image

Build and push container images:

```bash
# Build container image
make image

# Build with custom tag
make image IMAGE_TAG=v1.0.0

# Build and push to default registry
make image-push

# Build and push to personal Quay registry (for development)
QUAY_USER=myuser make image-dev
```

Default image: `quay.io/openshift-hyperfleet/hyperfleet-adapter:latest`

The container build automatically embeds version metadata (version, git commit, build date) into the binary. The git commit is passed from the build machine via `--build-arg GIT_COMMIT`. To override:

```bash
make image GIT_COMMIT=abc1234
```

## Testing

### Unit Tests

```bash
# Run unit tests (fast, no dependencies)
make test
```

Unit tests include:

- Logger functionality and context handling
- Error handling and error codes
- Operation ID middleware
- Template rendering and parsing
- Kubernetes client logic

### Integration Tests

Integration tests use **Testcontainers** with **dynamically installed envtest** - works in any CI/CD platform without requiring privileged containers.

<details>
<summary>Click to expand: Setup and run integration tests</summary>

#### Prerequisites

- **Docker or Podman** must be running (both fully supported!)
  - Docker: `docker info`
  - Podman: `podman info`
- The Makefile automatically detects and configures your container runtime
- **Podman users**: Corporate proxy settings are auto-detected from Podman machine

#### Run Tests

```bash
# Run integration tests with pre-built envtest (default - unprivileged)
make test-integration

# Run integration tests with K3s (faster, may need privileges)
make test-integration-k3s

# Run all tests (unit + integration)
make test-all

# Generate coverage report
make test-coverage
```

The first run will download golang:alpine and install envtest (~20-30 seconds). Subsequent runs are faster with caching.

#### Advantages

- ✅ **Simple Setup**: Just needs Docker/Podman (no binary installation, no custom Dockerfile)
- ✅ **Unprivileged**: Works in ANY CI/CD platform (OpenShift, Tekton, restricted runners)
- ✅ **Real API**: Kubernetes API server + etcd (sufficient for most integration tests)
- ✅ **Podman Optimized**: Auto-detects proxy, works in corporate networks
- ✅ **CI/CD Ready**: No privileged mode required
- ✅ **Isolated**: Fresh environment for each test suite

**Performance**: ~30-40 seconds for complete test suite (10 suites, 24 test cases).

**Alternative**: Use K3s (`make test-integration-k3s`) for 2x faster tests if privileged containers are available.

- ⚠️ Requires Docker or rootful Podman
- ✅ Makefile automatically checks Podman mode and provides helpful instructions if incompatible

</details>

📖 **Full guide:** [`test/integration/k8s_client/README.md`](test/integration/k8s_client/README.md)

### Test Coverage

```bash
# Generate coverage report
make test-coverage

# Generate HTML coverage report
make test-coverage-html
```

**Expected Total Coverage:** ~65-75% (unit + integration tests)

📊 **Test Status:** See [`TEST_STATUS.md`](TEST_STATUS.md) for detailed coverage analysis

## Logging

The adapter uses structured logging with context-aware fields:

- **Transaction ID** (`txid`): Request transaction identifier
- **Operation ID** (`opid`): Unique operation identifier
- **Adapter ID** (`adapter_id`): Adapter instance identifier
- **Cluster ID** (`cluster_id`): Cluster identifier

Logs are formatted with prefixes like: `[opid=abc123][adapter_id=adapter-1] message`

## Error Handling

The adapter uses a structured error handling system:

- **Error Codes**: Standardized error codes with prefixes
- **Error References**: API references for error documentation
- **Error Types**: Common error types (NotFound, Validation, Conflict, etc.)

See `pkg/errors/error.go` for error handling implementation.

## Contributing

Welcome contributions! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines on:

- Code style and standards
- Testing requirements
- Pull request process
- Commit message guidelines

## Repository Access

All members of the **hyperfleet** team have write access to this repository.

### Steps to Apply for Repository Access

If you're a team member and need access to this repository:

1. **Verify Organization Membership**: Ensure you're a member of the `openshift-hyperfleet` organization
2. **Check Team Assignment**: Confirm you're added to the hyperfleet team within the organization
3. **Repository Permissions**: All hyperfleet team members automatically receive write access
4. **OWNERS File**: Code reviews and approvals are managed through the OWNERS file

For access issues, contact a repository administrator or organization owner.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](./LICENSE) file for details.

## Related Documentation

- [Helm Chart Documentation](./charts/README.md)
- [Contributing Guidelines](./CONTRIBUTING.md)

## Support

For issues, questions, or contributions, please open an issue on GitHub.
