// This file contains helper functions for setting up a test environment using testcontainers and K3s.

package k8s_client_integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	k8s_client "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// TestEnvTestcontainers holds the test environment for integration tests using testcontainers
type TestEnvTestcontainers struct {
	Container *k3s.K3sContainer
	Client    *k8s_client.Client
	Ctx       context.Context
	Log       logger.Logger
}

// SetupTestEnvTestcontainers creates a new test environment with testcontainers K3s
func SetupTestEnvTestcontainers(t *testing.T) *TestEnvTestcontainers {
	t.Helper()

	ctx := context.Background()
	log := logger.NewLogger(ctx)

	// Configure testcontainers to use Podman if DOCKER_HOST is set for Podman
	// This allows testcontainers to work with Podman on macOS
	// The DOCKER_HOST env var should be set to the Podman socket

	log.Infof("Starting K3s container for integration tests...")
	log.Infof("Note: K3s startup may take 30-60 seconds...")

	// Configure proxy settings if available
	// This is crucial for corporate networks where containerd needs proxy to reach registries
	var opts []testcontainers.ContainerCustomizer
	
	// Build environment map for proxy settings
	envMap := make(map[string]string)
	
	// Pass through proxy environment variables to K3s container
	// This allows K3s's containerd to use the proxy for image pulls
	if httpProxy := os.Getenv("HTTP_PROXY"); httpProxy != "" {
		log.Infof("Configuring HTTP_PROXY: %s", httpProxy)
		envMap["HTTP_PROXY"] = httpProxy
		envMap["http_proxy"] = httpProxy // K3s containerd checks lowercase too
	}
	if httpsProxy := os.Getenv("HTTPS_PROXY"); httpsProxy != "" {
		log.Infof("Configuring HTTPS_PROXY: %s", httpsProxy)
		envMap["HTTPS_PROXY"] = httpsProxy
		envMap["https_proxy"] = httpsProxy // K3s containerd checks lowercase too
	}
	if noProxy := os.Getenv("NO_PROXY"); noProxy != "" {
		log.Infof("Configuring NO_PROXY: %s", noProxy)
		envMap["NO_PROXY"] = noProxy
		envMap["no_proxy"] = noProxy // K3s containerd checks lowercase too
	}
	
	// Add environment variables if any proxy is configured
	if len(envMap) > 0 {
		opts = append(opts, testcontainers.WithEnv(envMap))
		log.Infof("K3s container will use proxy for image pulls")
	}

	// Override wait strategy to avoid Podman log access issues
	// Use HTTP readiness check on port 6443 instead of log parsing
	opts = append(opts, testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			WaitingFor: wait.ForHTTP("/readyz").
				WithPort("6443/tcp").
				WithStatusCodeMatcher(func(status int) bool {
					// K3s API server returns various status codes during startup
					// Accept any response (including 401 Unauthorized) as "ready"
					return status > 0
				}).
				WithStartupTimeout(3 * time.Minute).
				WithPollInterval(2 * time.Second),
		},
	}))

	// Create K3s container with proxy configuration and HTTP wait strategy
	k3sContainer, err := k3s.Run(ctx, "rancher/k3s:v1.27.1-k3s1", opts...)
	
	// Register cleanup immediately after creation to prevent leaks if assertions fail
	if k3sContainer != nil {
		t.Cleanup(func() {
			if k3sContainer != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = k3sContainer.Terminate(ctx)
			}
		})
	}

	require.NoError(t, err, "Failed to start K3s container")
	require.NotNil(t, k3sContainer, "K3s container is nil")

	log.Infof("K3s container started, waiting for API server to be ready...")

	// Get kubeconfig from container
	kubeConfigYaml, err := k3sContainer.GetKubeConfig(ctx)
	require.NoError(t, err, "Failed to get kubeconfig from K3s container")

	// Create rest.Config from kubeconfig
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeConfigYaml)
	require.NoError(t, err, "Failed to parse kubeconfig")

	restConfig, err := clientConfig.ClientConfig()
	require.NoError(t, err, "Failed to create rest.Config")

	// Wait for Kubernetes API to be responsive
	// This replaces the log-based wait strategy which fails on Podman
	log.Infof("Verifying Kubernetes API connectivity...")
	if !waitForK8sAPI(ctx, restConfig, 2*time.Minute, log) {
		require.Fail(t, "Kubernetes API did not become ready within timeout")
	}

	// Create k8s client using the K3s config
	client, err := k8s_client.NewClientFromConfig(ctx, restConfig, log)
	require.NoError(t, err, "Failed to create k8s client")
	require.NotNil(t, client, "Client is nil")

	// Additional wait for K3s admission controllers to be fully ready
	// K3s can pass readiness checks but admission controllers take longer
	log.Infof("Waiting for K3s admission controllers to be ready...")
	if !waitForAdmissionControllers(ctx, restConfig, 30*time.Second, log) {
		log.Infof("Warning: Admission controllers may not be fully ready, but continuing...")
	}

	// Create default namespace (envtest doesn't create it automatically)
	log.Infof("Creating default namespace...")
	createDefaultNamespace(t, client, ctx)
	log.Infof("Default namespace ready")

	log.Infof("K3s test environment ready")

	return &TestEnvTestcontainers{
		Container: k3sContainer,
		Client:    client,
		Ctx:       ctx,
		Log:       log,
	}
}

// Cleanup stops and removes the K3s container
func (e *TestEnvTestcontainers) Cleanup(t *testing.T) {
	t.Helper()
	if e.Container != nil {
		e.Log.Infof("Stopping K3s container...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		
		err := e.Container.Terminate(ctx)
		if err != nil {
			t.Logf("Warning: Failed to stop K3s container: %v", err)
		}
	}
}

// GetKubeconfig returns the kubeconfig YAML from the K3s container
func (e *TestEnvTestcontainers) GetKubeconfig(t *testing.T) []byte {
	t.Helper()
	
	kubeConfigYaml, err := e.Container.GetKubeConfig(e.Ctx)
	require.NoError(t, err, "Failed to get kubeconfig from K3s container")
	
	return kubeConfigYaml
}

// waitForK8sAPI waits for the Kubernetes API server to become responsive
// This avoids Podman log access issues by using direct API calls
func waitForK8sAPI(ctx context.Context, restConfig *rest.Config, timeout time.Duration, log logger.Logger) bool {
	deadline := time.Now().Add(timeout)
	attempt := 0
	
	for time.Now().Before(deadline) {
		attempt++
		
		// Try to create a clientset and list nodes
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			if attempt%10 == 0 { // Log every 10th attempt (~20 seconds)
				log.Infof("Attempt %d: Failed to create clientset: %v (retrying...)", attempt, err)
			}
			time.Sleep(2 * time.Second)
			continue
		}
		
		// Try to list nodes - this verifies the API is actually working
		_, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err == nil {
			log.Infof("Kubernetes API is ready after %d attempts", attempt)
			return true
		}
		
		if attempt%10 == 0 { // Log every 10th attempt
			log.Infof("Attempt %d: API not ready yet: %v (retrying...)", attempt, err)
		}
		
		time.Sleep(2 * time.Second)
	}
	
	log.Infof("Kubernetes API did not become ready within %v", timeout)
	return false
}

// waitForAdmissionControllers waits for K3s admission controllers to be fully initialized
// K3s can report /readyz as ready but admission controllers take longer to initialize
// This tests actual write operations to ensure admission controllers are working
func waitForAdmissionControllers(ctx context.Context, restConfig *rest.Config, timeout time.Duration, log logger.Logger) bool {
	deadline := time.Now().Add(timeout)
	attempt := 0
	testNamespace := "test-admission-check"
	
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Infof("Failed to create clientset for admission check: %v", err)
		return false
	}
	
	for time.Now().Before(deadline) {
		attempt++
		
		// Try to create a test namespace - this requires admission controllers
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		
		_, err := clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err == nil {
			// Success! Clean up the test namespace
			_ = clientset.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
			log.Infof("Admission controllers are ready after %d attempts", attempt)
			return true
		}
		
		// Check if it's the "not yet ready" error
		if strings.Contains(err.Error(), "not yet ready to handle request") || 
		   strings.Contains(err.Error(), "connection refused") ||
		   strings.Contains(err.Error(), "the server is currently unable to handle the request") {
			if attempt%3 == 0 { // Log every 3rd attempt
				log.Infof("Attempt %d: Admission controllers not ready yet (retrying...)", attempt)
			}
			time.Sleep(1 * time.Second)
			continue
		}
		
		// If it's already exists, admission controllers are working (leftover from previous run)
		if strings.Contains(err.Error(), "already exists") {
			// Clean up and return success
			_ = clientset.CoreV1().Namespaces().Delete(ctx, testNamespace, metav1.DeleteOptions{})
			log.Infof("Admission controllers are ready (namespace existed from previous run)")
			return true
		}
		
		// Some other error - log and retry
		log.Infof("Unexpected error checking admission controllers: %v (retrying...)", err)
		time.Sleep(1 * time.Second)
	}
	
	log.Infof("Admission controllers did not become fully ready within %v", timeout)
	return false
}

