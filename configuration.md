# Adapter Configuration Reference

This document describes the deployment-level `AdapterConfig` options and how to set them
in three formats: YAML, command-line flags, and environment variables.

Overrides are applied in this order: CLI flags > environment variables > YAML file > defaults.

## Config file location

You can point the adapter at a deployment config file with either:

- CLI: `--config` (or `-c`)
- Env: `HYPERFLEET_ADAPTER_CONFIG`

Task config is separate (`--task-config` / `HYPERFLEET_TASK_CONFIG`) and not covered here.

## YAML options (AdapterConfig)

All configuration is nested under `apiVersion`, `kind`, `metadata`, and `spec`.

```yaml
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: example-adapter
  namespace: hyperfleet-system
  labels:
    hyperfleet.io/component: adapter
spec:
  adapter:
    version: "0.1.0"
  debugConfig: false
  clients:
    maestro:
      grpcServerAddress: "maestro-grpc.maestro.svc.cluster.local:8090"
      httpServerAddress: "https://maestro-api.maestro.svc.cluster.local"
      sourceId: "hyperfleet-adapter"
      clientId: "hyperfleet-adapter-client"
      auth:
        type: "tls"
        tlsConfig:
          caFile: "/etc/maestro/certs/grpc/ca.crt"
          certFile: "/etc/maestro/certs/grpc/client.crt"
          keyFile: "/etc/maestro/certs/grpc/client.key"
      timeout: "30s"
      retryAttempts: 3
      keepalive:
        time: "30s"
        timeout: "10s"
      insecure: false
    hyperfleetApi:
      baseUrl: "http://hyperfleet-api:8000"
      version: "v1"
      timeout: "10s"
      retryAttempts: 3
      retryBackoff: "exponential"
      baseDelay: "1s"
      maxDelay: "30s"
      defaultHeaders:
        X-Example: "value"
    broker:
      subscriptionId: "example-subscription"
      topic: "example-topic"
    kubernetes:
      apiVersion: "v1"
      kubeConfigPath: "/path/to/kubeconfig"
      qps: 100
      burst: 200
```

### Top-level fields

- `apiVersion` (string, required): Must be `hyperfleet.redhat.com/v1alpha1`.
- `kind` (string, required): Must be `AdapterConfig`.
- `metadata.name` (string, required): Adapter name.
- `metadata.labels` (map[string]string, optional): Labels for the adapter metadata.

### Spec fields

- `spec.adapter.version` (string, required): Adapter version expected by the binary.
- `spec.debugConfig` (bool, optional): Log the merged config after load. Default: `false`.

### Maestro client (`spec.clients.maestro`)

- `grpcServerAddress` (string): Maestro gRPC endpoint.
- `httpServerAddress` (string): Maestro HTTP API endpoint.
- `sourceId` (string): CloudEvents source identifier.
- `clientId` (string): Maestro client identifier.
- `auth.type` (string): Authentication type (`tls` or `none`).
- `auth.tlsConfig.caFile` (string): CA certificate path.
- `auth.tlsConfig.certFile` (string): Client certificate path.
- `auth.tlsConfig.keyFile` (string): Client key path.
- `timeout` (duration string): Request timeout (e.g. `30s`).
- `retryAttempts` (int): Number of retry attempts.
- `keepalive.time` (duration string): gRPC keepalive time.
- `keepalive.timeout` (duration string): gRPC keepalive timeout.
- `insecure` (bool): Allow insecure connection.

### HyperFleet API client (`spec.clients.hyperfleetApi`)

- `baseUrl` (string): Base URL for HyperFleet API requests.
- `version` (string): API version. Default: `v1`.
- `timeout` (duration string): HTTP client timeout. Default: `10s`.
- `retryAttempts` (int): Retry attempts. Default: `3`.
- `retryBackoff` (string): Backoff strategy (`exponential`, `linear`, `constant`). Default: `exponential`.
- `baseDelay` (duration string): Initial retry delay. Default: `1s`.
- `maxDelay` (duration string): Maximum retry delay. Default: `30s`.
- `defaultHeaders` (map[string]string): Headers added to all API requests.

### Broker (`spec.clients.broker`)

- `subscriptionId` (string): Broker subscription ID (required at runtime).
- `topic` (string): Broker topic (required at runtime).

#### Broker Metrics

The adapter automatically registers Prometheus metrics from the broker library on the `/metrics` endpoint (port 9090).
These metrics use the `hyperfleet_broker_` prefix and include the adapter's `component` and `version` labels:

- `hyperfleet_broker_messages_consumed_total` — Total messages consumed from the broker.
- `hyperfleet_broker_messages_published_total` — Total messages published to the broker.
- `hyperfleet_broker_errors_total` — Total message processing errors (labels: `topic`, `error_type`).
- `hyperfleet_broker_message_duration_seconds` — Histogram of message processing duration.

### Kubernetes (`spec.clients.kubernetes`)

- `apiVersion` (string): Kubernetes API version.
- `kubeConfigPath` (string): Path to kubeconfig (empty uses in-cluster auth).
- `qps` (float): Client-side QPS limit (0 uses defaults).
- `burst` (int): Client-side burst limit (0 uses defaults).

## Command-line parameters

The following CLI flags override YAML values:

- `--debug-config` -> `spec.debugConfig`
- `--maestro-grpc-server-address` -> `spec.clients.maestro.grpcServerAddress`
- `--maestro-http-server-address` -> `spec.clients.maestro.httpServerAddress`
- `--maestro-source-id` -> `spec.clients.maestro.sourceId`
- `--maestro-client-id` -> `spec.clients.maestro.clientId`
- `--maestro-ca-file` -> `spec.clients.maestro.auth.tlsConfig.caFile`
- `--maestro-cert-file` -> `spec.clients.maestro.auth.tlsConfig.certFile`
- `--maestro-key-file` -> `spec.clients.maestro.auth.tlsConfig.keyFile`
- `--maestro-timeout` -> `spec.clients.maestro.timeout`
- `--maestro-insecure` -> `spec.clients.maestro.insecure`
- `--hyperfleet-api-timeout` -> `spec.clients.hyperfleetApi.timeout`
- `--hyperfleet-api-retry` -> `spec.clients.hyperfleetApi.retryAttempts`

## Environment variables

All deployment overrides use the `HYPERFLEET_` prefix unless noted.

- `HYPERFLEET_DEBUG_CONFIG` -> `spec.debugConfig`
- `HYPERFLEET_MAESTRO_GRPC_SERVER_ADDRESS` -> `spec.clients.maestro.grpcServerAddress`
- `HYPERFLEET_MAESTRO_HTTP_SERVER_ADDRESS` -> `spec.clients.maestro.httpServerAddress`
- `HYPERFLEET_MAESTRO_SOURCE_ID` -> `spec.clients.maestro.sourceId`
- `HYPERFLEET_MAESTRO_CLIENT_ID` -> `spec.clients.maestro.clientId`
- `HYPERFLEET_MAESTRO_CA_FILE` -> `spec.clients.maestro.auth.tlsConfig.caFile`
- `HYPERFLEET_MAESTRO_CERT_FILE` -> `spec.clients.maestro.auth.tlsConfig.certFile`
- `HYPERFLEET_MAESTRO_KEY_FILE` -> `spec.clients.maestro.auth.tlsConfig.keyFile`
- `HYPERFLEET_MAESTRO_TIMEOUT` -> `spec.clients.maestro.timeout`
- `HYPERFLEET_MAESTRO_RETRY_ATTEMPTS` -> `spec.clients.maestro.retryAttempts`
- `HYPERFLEET_MAESTRO_INSECURE` -> `spec.clients.maestro.insecure`
- `HYPERFLEET_API_BASE_URL` -> `spec.clients.hyperfleetApi.baseUrl`
- `HYPERFLEET_API_VERSION` -> `spec.clients.hyperfleetApi.version`
- `HYPERFLEET_API_TIMEOUT` -> `spec.clients.hyperfleetApi.timeout`
- `HYPERFLEET_API_RETRY_ATTEMPTS` -> `spec.clients.hyperfleetApi.retryAttempts`
- `HYPERFLEET_API_RETRY_BACKOFF` -> `spec.clients.hyperfleetApi.retryBackoff`
- `HYPERFLEET_BROKER_SUBSCRIPTION_ID` -> `spec.clients.broker.subscriptionId`
- `HYPERFLEET_BROKER_TOPIC` -> `spec.clients.broker.topic`

Legacy broker environment variables (used only if the prefixed version is unset):

- `BROKER_SUBSCRIPTION_ID` -> `spec.clients.broker.subscriptionId`
- `BROKER_TOPIC` -> `spec.clients.broker.topic`
