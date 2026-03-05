package maestro_client_integration

import (
	"context"
	crypto_tls "crypto/tls"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/openshift-online/maestro/pkg/api/openapi"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Database configuration
	dbName     = "maestro"
	dbUser     = "maestro"
	dbPassword = "maestro-test-password"
)

// setupMaestroTestEnv starts PostgreSQL and Maestro containers
func setupMaestroTestEnv() (*MaestroTestEnv, error) {
	ctx := context.Background()
	env := &MaestroTestEnv{}

	// Step 1: Start PostgreSQL
	println("   📦 Starting PostgreSQL container...")
	pgContainer, err := startPostgresContainer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start PostgreSQL: %w", err)
	}
	env.PostgresContainer = pgContainer

	// Get PostgreSQL connection info
	host, err := pgContainer.Host(ctx)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("failed to get PostgreSQL host: %w", err)
	}
	env.PostgresHost = host

	port, err := pgContainer.MappedPort(ctx, nat.Port(PostgresPort))
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("failed to get PostgreSQL port: %w", err)
	}
	env.PostgresPort = port.Port()
	println(fmt.Sprintf("   ✅ PostgreSQL ready at %s:%s", env.PostgresHost, env.PostgresPort))

	// Step 2: Run Maestro migration
	println("   🔄 Running Maestro database migration...")
	if err := runMaestroMigration(ctx, env); err != nil {
		_ = pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("failed to run Maestro migration: %w", err)
	}
	println("   ✅ Database migration complete")

	// Step 3: Start Maestro server
	println("   📦 Starting Maestro server container...")
	maestroContainer, err := startMaestroServer(ctx, env)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		return nil, fmt.Errorf("failed to start Maestro server: %w", err)
	}
	env.MaestroContainer = maestroContainer

	// Get Maestro connection info
	env.MaestroHost, err = maestroContainer.Host(ctx)
	if err != nil {
		cleanupMaestroTestEnv(env)
		return nil, fmt.Errorf("failed to get Maestro host: %w", err)
	}

	httpPort, err := maestroContainer.MappedPort(ctx, nat.Port(MaestroHTTPPort))
	if err != nil {
		cleanupMaestroTestEnv(env)
		return nil, fmt.Errorf("failed to get Maestro HTTP port: %w", err)
	}
	env.MaestroHTTPPort = httpPort.Port()

	grpcPort, err := maestroContainer.MappedPort(ctx, nat.Port(MaestroGRPCPort))
	if err != nil {
		cleanupMaestroTestEnv(env)
		return nil, fmt.Errorf("failed to get Maestro gRPC port: %w", err)
	}
	env.MaestroGRPCPort = grpcPort.Port()

	healthPort, err := maestroContainer.MappedPort(ctx, nat.Port(MaestroHealthPort))
	if err != nil {
		cleanupMaestroTestEnv(env)
		return nil, fmt.Errorf("failed to get Maestro health port: %w", err)
	}
	env.MaestroHealthPort = healthPort.Port()

	// Build connection strings - use 127.0.0.1 to avoid IPv6 issues
	env.MaestroServerAddr = fmt.Sprintf("http://127.0.0.1:%s", env.MaestroHTTPPort)
	env.MaestroGRPCAddr = fmt.Sprintf("127.0.0.1:%s", env.MaestroGRPCPort)

	println("   ✅ Maestro server ready")

	// Step 4: Register test consumers (waitForMaestroAPI handles initialization delay)
	println("   📝 Registering test consumers...")
	if err := registerTestConsumers(ctx, env); err != nil {
		cleanupMaestroTestEnv(env)
		return nil, fmt.Errorf("failed to register test consumers: %w", err)
	}
	println("   ✅ Test consumers registered")

	return env, nil
}

// startPostgresContainer starts a PostgreSQL container
func startPostgresContainer(ctx context.Context) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        PostgresImage,
		ExposedPorts: []string{PostgresPort},
		Env: map[string]string{
			"POSTGRESQL_DATABASE": dbName,
			"POSTGRESQL_USER":     dbUser,
			"POSTGRESQL_PASSWORD": dbPassword,
		},
		WaitingFor: wait.ForListeningPort(nat.Port(PostgresPort)).
			WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// getPostgresIP returns the PostgreSQL container's IP address for inter-container communication
func getPostgresIP(ctx context.Context, container testcontainers.Container) (string, error) {
	pgInspect, err := container.Inspect(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to inspect PostgreSQL container: %w", err)
	}

	// Try to get the container IP from any network
	for _, network := range pgInspect.NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress, nil
		}
	}

	// Fallback to host.docker.internal for Docker Desktop
	return "host.docker.internal", nil
}

// runMaestroMigration runs the Maestro database migration
func runMaestroMigration(ctx context.Context, env *MaestroTestEnv) error {
	pgIP, err := getPostgresIP(ctx, env.PostgresContainer)
	if err != nil {
		return err
	}

	// Maestro now uses file-based database configuration
	// Create files via shell script in entrypoint
	setupScript := fmt.Sprintf(`#!/bin/sh
mkdir -p /secrets
echo -n '%s' > /secrets/db.host
echo -n '5432' > /secrets/db.port
echo -n '%s' > /secrets/db.user
echo -n '%s' > /secrets/db.password
echo -n '%s' > /secrets/db.name
exec /usr/local/bin/maestro migration \
  --db-host-file=/secrets/db.host \
  --db-port-file=/secrets/db.port \
  --db-user-file=/secrets/db.user \
  --db-password-file=/secrets/db.password \
  --db-name-file=/secrets/db.name \
  --db-sslmode=disable \
  --alsologtostderr \
  -v=2
`, pgIP, dbUser, dbPassword, dbName)

	req := testcontainers.ContainerRequest{
		Image:      MaestroImage,
		Entrypoint: []string{"/bin/sh", "-c", setupScript},
		WaitingFor: wait.ForExit().WithExitTimeout(120 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("failed to run migration container: %w", err)
	}
	defer func() {
		_ = container.Terminate(ctx)
	}()

	// Check exit code
	state, err := container.State(ctx)
	if err != nil {
		return fmt.Errorf("failed to get migration container state: %w", err)
	}

	if state.ExitCode != 0 {
		// Get logs for debugging (read full output to avoid truncation)
		logs, _ := container.Logs(ctx)
		if logs != nil {
			defer logs.Close() //nolint:errcheck
			logBytes, _ := io.ReadAll(logs)
			println(fmt.Sprintf("      Migration logs: %s", string(logBytes)))
		}
		return fmt.Errorf("migration failed with exit code %d", state.ExitCode)
	}

	return nil
}

// startMaestroServer starts the Maestro server container
func startMaestroServer(ctx context.Context, env *MaestroTestEnv) (testcontainers.Container, error) {
	pgIP, err := getPostgresIP(ctx, env.PostgresContainer)
	if err != nil {
		return nil, err
	}

	// Maestro now uses file-based database configuration
	// Create files via shell script in entrypoint
	setupScript := fmt.Sprintf(`#!/bin/sh
mkdir -p /secrets
echo -n '%s' > /secrets/db.host
echo -n '5432' > /secrets/db.port
echo -n '%s' > /secrets/db.user
echo -n '%s' > /secrets/db.password
echo -n '%s' > /secrets/db.name
exec /usr/local/bin/maestro server \
  --db-host-file=/secrets/db.host \
  --db-port-file=/secrets/db.port \
  --db-user-file=/secrets/db.user \
  --db-password-file=/secrets/db.password \
  --db-name-file=/secrets/db.name \
  --db-sslmode=disable \
  --server-hostname=0.0.0.0 \
  --enable-grpc-server=true \
  --grpc-server-bindport=8090 \
  --http-server-bindport=8000 \
  --health-check-server-bindport=8083 \
  --message-broker-type=grpc \
  --alsologtostderr \
  -v=2
`, pgIP, dbUser, dbPassword, dbName)

	req := testcontainers.ContainerRequest{
		Image:        MaestroImage,
		ExposedPorts: []string{MaestroHTTPPort, MaestroGRPCPort, MaestroHealthPort},
		Entrypoint:   []string{"/bin/sh", "-c", setupScript},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort(nat.Port(MaestroHTTPPort)).WithStartupTimeout(120*time.Second),
			wait.ForListeningPort(nat.Port(MaestroGRPCPort)).WithStartupTimeout(120*time.Second),
		),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// cleanupMaestroTestEnv cleans up all containers
func cleanupMaestroTestEnv(env *MaestroTestEnv) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if env.TLSMaestroContainer != nil {
		println("   Stopping TLS Maestro server...")
		if err := env.TLSMaestroContainer.Terminate(ctx); err != nil {
			println(fmt.Sprintf("   ⚠️  Warning: Failed to terminate TLS Maestro: %v", err))
		}
	}

	if env.TLSCerts != nil {
		env.TLSCerts.Cleanup()
	}

	if env.MaestroContainer != nil {
		println("   Stopping Maestro server...")
		if err := env.MaestroContainer.Terminate(ctx); err != nil {
			println(fmt.Sprintf("   ⚠️  Warning: Failed to terminate Maestro: %v", err))
		}
	}

	if env.PostgresContainer != nil {
		println("   Stopping PostgreSQL...")
		if err := env.PostgresContainer.Terminate(ctx); err != nil {
			println(fmt.Sprintf("   ⚠️  Warning: Failed to terminate PostgreSQL: %v", err))
		}
	}

	println("   ✅ Cleanup complete")
}

// setupTLSMaestroEnv generates TLS certs and starts a TLS-enabled Maestro server
// that shares the same PostgreSQL database as the insecure instance.
func setupTLSMaestroEnv(env *MaestroTestEnv) error {
	ctx := context.Background()

	// Generate test certificates
	certs, err := generateTestCerts()
	if err != nil {
		return fmt.Errorf("failed to generate test certs: %w", err)
	}
	if err := certs.WriteToTempDir(); err != nil {
		return fmt.Errorf("failed to write certs to temp dir: %w", err)
	}
	env.TLSCerts = certs

	// Start TLS Maestro container
	container, err := startTLSMaestroServer(ctx, env)
	if err != nil {
		certs.Cleanup()
		return fmt.Errorf("failed to start TLS Maestro: %w", err)
	}
	env.TLSMaestroContainer = container

	host, err := container.Host(ctx)
	if err != nil {
		return fmt.Errorf("failed to get TLS Maestro host: %w", err)
	}

	httpPort, err := container.MappedPort(ctx, nat.Port(MaestroHTTPPort))
	if err != nil {
		return fmt.Errorf("failed to get TLS Maestro HTTP port: %w", err)
	}
	env.TLSMaestroHTTPPort = httpPort.Port()

	grpcPort, err := container.MappedPort(ctx, nat.Port(MaestroGRPCPort))
	if err != nil {
		return fmt.Errorf("failed to get TLS Maestro gRPC port: %w", err)
	}
	env.TLSMaestroGRPCPort = grpcPort.Port()

	_ = host
	env.TLSMaestroServerAddr = fmt.Sprintf("https://127.0.0.1:%s", env.TLSMaestroHTTPPort)
	env.TLSMaestroGRPCAddr = fmt.Sprintf("127.0.0.1:%s", env.TLSMaestroGRPCPort)

	// Wait for API readiness via HTTPS (skip verify for health check only)
	if err := waitForTLSMaestroAPI(ctx, env); err != nil {
		return fmt.Errorf("TLS Maestro health check failed: %w", err)
	}

	return nil
}

// startTLSMaestroServer starts a Maestro server with TLS enabled for both HTTP and gRPC.
// Uses --grpc-authn-type=mock so client certs are accepted but not required,
// allowing us to test both CA-only and mTLS client configurations.
func startTLSMaestroServer(ctx context.Context, env *MaestroTestEnv) (testcontainers.Container, error) {
	pgIP, err := getPostgresIP(ctx, env.PostgresContainer)
	if err != nil {
		return nil, err
	}

	setupScript := fmt.Sprintf(`#!/bin/sh
mkdir -p /secrets /certs
echo -n '%s' > /secrets/db.host
echo -n '5432' > /secrets/db.port
echo -n '%s' > /secrets/db.user
echo -n '%s' > /secrets/db.password
echo -n '%s' > /secrets/db.name
exec /usr/local/bin/maestro server \
  --db-host-file=/secrets/db.host \
  --db-port-file=/secrets/db.port \
  --db-user-file=/secrets/db.user \
  --db-password-file=/secrets/db.password \
  --db-name-file=/secrets/db.name \
  --db-sslmode=disable \
  --server-hostname=0.0.0.0 \
  --enable-grpc-server=true \
  --grpc-server-bindport=8090 \
  --http-server-bindport=8000 \
  --health-check-server-bindport=8083 \
  --message-broker-type=grpc \
  --enable-https=true \
  --https-cert-file=/certs/server.crt \
  --https-key-file=/certs/server.key \
  --grpc-tls-cert-file=/certs/server.crt \
  --grpc-tls-key-file=/certs/server.key \
  --grpc-authn-type=mock \
  --alsologtostderr \
  -v=2
`, pgIP, dbUser, dbPassword, dbName)

	certsDir := env.TLSCerts.TempDir

	req := testcontainers.ContainerRequest{
		Image:        MaestroImage,
		ExposedPorts: []string{MaestroHTTPPort, MaestroGRPCPort, MaestroHealthPort},
		Entrypoint:   []string{"/bin/sh", "-c", setupScript},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      filepath.Join(certsDir, "ca.crt"),
				ContainerFilePath: "/certs/ca.crt",
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(certsDir, "server.crt"),
				ContainerFilePath: "/certs/server.crt",
				FileMode:          0o644,
			},
			{
				HostFilePath:      filepath.Join(certsDir, "server.key"),
				ContainerFilePath: "/certs/server.key",
				FileMode:          0o600,
			},
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort(nat.Port(MaestroHTTPPort)).WithStartupTimeout(120*time.Second),
			wait.ForListeningPort(nat.Port(MaestroGRPCPort)).WithStartupTimeout(120*time.Second),
		),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// waitForTLSMaestroAPI waits for the TLS Maestro API to respond.
// Uses InsecureSkipVerify only for the health probe; actual tests use proper CA verification.
func waitForTLSMaestroAPI(ctx context.Context, env *MaestroTestEnv) error {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &crypto_tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // health probe only
			},
		},
	}

	apiURL := fmt.Sprintf("%s/api/maestro/v1/consumers", env.TLSMaestroServerAddr)
	println(fmt.Sprintf("      TLS API URL: %s", apiURL))

	maxRetries := 20
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				println(fmt.Sprintf("      TLS API check succeeded on attempt %d (HTTP %d)", i+1, resp.StatusCode))
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
			if i < 3 || i%5 == 0 {
				println(fmt.Sprintf("      TLS API check attempt %d: %v", i+1, err))
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	return fmt.Errorf("TLS Maestro API check failed after %d retries, last error: %v", maxRetries, lastErr)
}

// testConsumerNames lists all consumer names used by integration tests
var testConsumerNames = []string{
	"test-cluster-create",
	"test-cluster-list",
	"test-cluster-apply",
	"test-cluster-skip",
}

// waitForMaestroAPI waits for Maestro API to be fully ready
func waitForMaestroAPI(ctx context.Context, env *MaestroTestEnv) error {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Use the consumers endpoint to verify API readiness
	apiURL := fmt.Sprintf("%s/api/maestro/v1/consumers", env.MaestroServerAddr)
	println(fmt.Sprintf("      API URL: %s", apiURL))

	// More retries to handle slow startup
	maxRetries := 20
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			// Accept any response (2xx-4xx means API is responding)
			if resp.StatusCode < 500 {
				println(fmt.Sprintf("      API check succeeded on attempt %d (HTTP %d)", i+1, resp.StatusCode))
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			println(fmt.Sprintf("      API check attempt %d: HTTP %d", i+1, resp.StatusCode))
		} else {
			lastErr = err
			if i < 3 || i%5 == 0 {
				println(fmt.Sprintf("      API check attempt %d: %v", i+1, err))
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			// Retry
		}
	}

	return fmt.Errorf("Maestro API check failed after %d retries, last error: %v", maxRetries, lastErr)
}

// registerTestConsumers registers fake consumers for integration testing
func registerTestConsumers(ctx context.Context, env *MaestroTestEnv) error {
	// Wait for Maestro API to be fully ready (not just ports listening)
	println("      Waiting for Maestro health check...")
	if err := waitForMaestroAPI(ctx, env); err != nil {
		return fmt.Errorf("Maestro health check failed: %w", err)
	}
	println("      Maestro API is ready")

	// Create an openapi client for the Maestro API
	apiConfig := openapi.NewConfiguration()
	apiConfig.Servers = openapi.ServerConfigurations{
		{URL: env.MaestroServerAddr},
	}
	apiConfig.HTTPClient = &http.Client{
		Timeout: 10 * time.Second,
	}
	apiClient := openapi.NewAPIClient(apiConfig)

	// Register each test consumer
	for _, consumerName := range testConsumerNames {
		consumer := openapi.NewConsumer()
		consumer.SetName(consumerName)

		_, resp, err := apiClient.DefaultAPI.ApiMaestroV1ConsumersPost(ctx).Consumer(*consumer).Execute()
		if err != nil {
			// Check if it's a conflict (consumer already exists) - that's OK
			if resp != nil && resp.StatusCode == http.StatusConflict {
				println(fmt.Sprintf("      Consumer %s already exists (OK)", consumerName))
				continue
			}
			return fmt.Errorf("failed to register consumer %s: %w", consumerName, err)
		}
		println(fmt.Sprintf("      Registered consumer: %s", consumerName))
	}

	return nil
}
