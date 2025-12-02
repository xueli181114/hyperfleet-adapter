package executor_integration_test

import (
	"context"
	"os"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	// Import the testcontainers helper from k8s_client integration tests
	k8s_integration "github.com/openshift-hyperfleet/hyperfleet-adapter/test/integration/k8s_client"
)

// K8sTestEnv wraps the K8s test environment for executor tests
type K8sTestEnv struct {
	Client  *k8s_client.Client
	Config  *rest.Config
	Ctx     context.Context
	Log     logger.Logger
	cleanup func()
}

// SetupK8sTestEnv creates a K8s test environment based on INTEGRATION_STRATEGY
func SetupK8sTestEnv(t *testing.T) *K8sTestEnv {
	t.Helper()

	// Check if K8s integration tests are enabled
	if os.Getenv("INTEGRATION_ENVTEST_IMAGE") == "" && os.Getenv("INTEGRATION_STRATEGY") != "k3s" {
		t.Skip("K8s integration tests require INTEGRATION_ENVTEST_IMAGE or INTEGRATION_STRATEGY=k3s")
	}

	// Use the shared test environment from k8s_client tests
	env := k8s_integration.SetupTestEnv(t)

	return &K8sTestEnv{
		Client: env.GetClient(),
		Config: env.GetConfig(),
		Ctx:    env.GetContext(),
		Log:    env.GetLogger(),
		cleanup: func() {
			env.Cleanup(t)
		},
	}
}

// Cleanup cleans up the test environment
func (e *K8sTestEnv) Cleanup(t *testing.T) {
	if e.cleanup != nil {
		e.cleanup()
	}
}

// CreateTestNamespace creates a namespace for test isolation
func (e *K8sTestEnv) CreateTestNamespace(t *testing.T, name string) {
	t.Helper()

	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": name,
				"labels": map[string]interface{}{
					"test":                         "executor-integration",
					"hyperfleet.io/test-namespace": "true",
				},
			},
		},
	}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"})

	_, err := e.Client.CreateResource(e.Ctx, ns)
	if err != nil && !isAlreadyExistsError(err) {
		require.NoError(t, err, "Failed to create test namespace %s", name)
	}
}

// CleanupTestNamespace deletes a test namespace
func (e *K8sTestEnv) CleanupTestNamespace(t *testing.T, name string) {
	t.Helper()

	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}
	err := e.Client.DeleteResource(e.Ctx, gvk, "", name)
	if err != nil {
		t.Logf("Warning: failed to cleanup namespace %s: %v", name, err)
	}
}

// isAlreadyExistsError checks if the error indicates the resource already exists
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "already exists") || contains(errStr, "AlreadyExists")
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

