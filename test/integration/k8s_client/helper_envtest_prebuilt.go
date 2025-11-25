// This file contains helper functions for setting up a pre-built image integration test environment.

package k8s_client_integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"k8s.io/client-go/rest"

	k8s_client "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/test/integration/testutil"
)

const (
	// EnvtestAPIServerPort is the port the kube-apiserver listens on
	EnvtestAPIServerPort = "6443/tcp"

	// EnvtestReadyLog is the log message indicating envtest is ready
	EnvtestReadyLog = "Envtest is running"
)

// TestEnvPrebuilt holds the test environment for pre-built image integration tests
type TestEnvPrebuilt struct {
	Container testcontainers.Container
	Client    *k8s_client.Client
	Config    *rest.Config
	Ctx       context.Context
	Log       logger.Logger
}

// Cleanup terminates the container and cleans up resources
func (e *TestEnvPrebuilt) Cleanup(t *testing.T) {
	t.Helper()
	if e.Container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Use Terminate which is idempotent and safe to call multiple times
		// (e.g. here AND in t.Cleanup)
		if err := e.Container.Terminate(ctx); err != nil {
			// Just log, don't fail, as it might already be stopped
			t.Logf("Container cleanup: %v", err)
		}
	}
}

// SetupTestEnvPrebuilt sets up integration tests using a pre-built image with envtest.
// This approach avoids Exec() calls and works reliably with both Docker and Podman.
//
// IMPORTANT: INTEGRATION_ENVTEST_IMAGE environment variable must be set.
// Do not call this function directly. Instead, use:
//
//	make test-integration
//
// The Makefile will automatically:
// - Build the image if needed (using test/Dockerfile.integration)
// - Set INTEGRATION_ENVTEST_IMAGE to the appropriate value
// - Run the integration tests
//
// For CI/CD, set INTEGRATION_ENVTEST_IMAGE to your pre-built image:
//
//	INTEGRATION_ENVTEST_IMAGE=quay.io/your-org/integration-test:v1 make test-integration
func SetupTestEnvPrebuilt(t *testing.T) *TestEnvPrebuilt {
	t.Helper()

	ctx := context.Background()
	log := logger.NewLogger(ctx)

	// Check that INTEGRATION_ENVTEST_IMAGE is set
	imageName := os.Getenv("INTEGRATION_ENVTEST_IMAGE")
	if imageName == "" {
		t.Fatalf(`INTEGRATION_ENVTEST_IMAGE environment variable is not set.

Please run integration tests using:
  make test-integration

The Makefile will automatically build the image if needed and set INTEGRATION_ENVTEST_IMAGE.

For CI/CD environments, set INTEGRATION_ENVTEST_IMAGE to your pre-built image:
  INTEGRATION_ENVTEST_IMAGE=quay.io/your-org/integration-test:v1 make test-integration`)
	}
	log.Infof("Using integration image: %s", imageName)

	// Configure proxy settings from environment
	httpProxy := os.Getenv("HTTP_PROXY")
	httpsProxy := os.Getenv("HTTPS_PROXY")
	noProxy := os.Getenv("NO_PROXY")

	if httpProxy != "" {
		log.Infof("Configuring HTTP_PROXY: %s", httpProxy)
	}
	if httpsProxy != "" {
		log.Infof("Configuring HTTPS_PROXY: %s", httpsProxy)
	}

	// Configure container using shared utility
	config := testutil.DefaultContainerConfig()
	config.Name = "envtest"
	config.Image = imageName
	config.ExposedPorts = []string{EnvtestAPIServerPort}
	config.Env = map[string]string{
		"HTTP_PROXY":  httpProxy,
		"HTTPS_PROXY": httpsProxy,
		"NO_PROXY":    noProxy,
	}
	config.MaxRetries = 3
	config.StartupTimeout = 3 * time.Minute
	// Use TCP wait + log-based wait since health endpoints require auth
	config.WaitStrategy = wait.ForAll(
		wait.ForListeningPort(EnvtestAPIServerPort).WithPollInterval(500 * time.Millisecond),
		wait.ForLog(EnvtestReadyLog).WithPollInterval(500 * time.Millisecond),
	).WithDeadline(120 * time.Second)

	log.Infof("Creating container from image: %s", imageName)
	log.Infof("This should be fast since binaries are pre-installed...")

	result, err := testutil.StartContainer(t, config)
	if err != nil {
		t.Fatalf(`Failed to start container: %v

Image: %s

Possible causes:
1. Image pull is stuck (check network/proxy configuration)
2. Container runtime is slow or unresponsive
3. Image does not exist (ensure 'make test-integration' was used)`, err, imageName)
	}

	// Register explicit cleanup for setup failure cases
	// While StartContainer registers t.Cleanup, this ensures we catch setup failures immediately
	// and fits the pattern of "cleanup before assertions"
	setupSuccess := false
	defer func() {
		if !setupSuccess && result.Container != nil {
			log.Infof("Setup failed, force terminating container...")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = result.Container.Terminate(ctx)
		}
	}()

	require.NotNil(t, result.Container, "Container is nil")

	log.Infof("Container started successfully")

	// Get the kube-apiserver endpoint
	kubeAPIServer := fmt.Sprintf("https://%s", result.GetEndpoint(EnvtestAPIServerPort))
	log.Infof("Kube-apiserver available at: %s", kubeAPIServer)

	// Give API server a moment to fully initialize
	log.Infof("Waiting for API server to be fully ready...")
	time.Sleep(5 * time.Second)
	log.Infof("API server is ready!")

	// Create Kubernetes client using the k8s_client package
	log.Infof("Creating Kubernetes client...")

	// Create rest.Config for the client with bearer token authentication
	restConfig := &rest.Config{
		Host:        kubeAPIServer,
		BearerToken: "test-token", // Matches token in /tmp/envtest/certs/token-auth-file
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, // Skip TLS verification for testing
		},
	}

	// Create client using the config
	client, err := k8s_client.NewClientFromConfig(ctx, restConfig, log)
	require.NoError(t, err)
	require.NotNil(t, client)

	log.Infof("Kubernetes client created successfully")

	// Create default namespace (envtest doesn't create it automatically)
	log.Infof("Creating default namespace...")
	createDefaultNamespace(t, client, ctx)
	log.Infof("Default namespace ready")

	setupSuccess = true

	return &TestEnvPrebuilt{
		Container: result.Container,
		Client:    client,
		Config:    restConfig,
		Ctx:       ctx,
		Log:       log,
	}
}

