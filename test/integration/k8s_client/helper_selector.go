// This file contains helper functions for selecting the appropriate integration test environment.

package k8s_client_integration

import (
	"context"
	"os"
	"testing"

	k8s_client "github.com/openshift-hyperfleet/hyperfleet-adapter/internal/k8s_client"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

// TestEnv is a common interface for all integration test environments
type TestEnv interface {
	GetClient() *k8s_client.Client
	GetConfig() *rest.Config
	GetContext() context.Context
	GetLogger() logger.Logger
	Cleanup(t *testing.T)
}

// Ensure both implementations satisfy the interface
var _ TestEnv = (*TestEnvPrebuilt)(nil)
var _ TestEnv = (*TestEnvK3s)(nil)

// TestEnvK3s wraps TestEnvTestcontainers to implement TestEnv interface
type TestEnvK3s struct {
	*TestEnvTestcontainers
}

func (e *TestEnvK3s) GetClient() *k8s_client.Client {
	return e.Client
}

func (e *TestEnvK3s) GetConfig() *rest.Config {
	return nil // K3s doesn't expose Config in current implementation
}

func (e *TestEnvK3s) GetContext() context.Context {
	return e.Ctx
}

func (e *TestEnvK3s) GetLogger() logger.Logger {
	return e.Log
}

// GetClient returns the k8s client
func (e *TestEnvPrebuilt) GetClient() *k8s_client.Client {
	return e.Client
}

// GetConfig returns the rest config
func (e *TestEnvPrebuilt) GetConfig() *rest.Config {
	return e.Config
}

// GetContext returns the context
func (e *TestEnvPrebuilt) GetContext() context.Context {
	return e.Ctx
}

// GetLogger returns the logger
func (e *TestEnvPrebuilt) GetLogger() logger.Logger {
	return e.Log
}

// createDefaultNamespace creates a "default" namespace in the test environment
// This is needed because envtest doesn't create the default namespace automatically
func createDefaultNamespace(t *testing.T, client *k8s_client.Client, ctx context.Context) {
	t.Helper()

	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "default",
			},
		},
	}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"})

	_, err := client.CreateResource(ctx, ns)
	// Ignore error if namespace already exists
	if err != nil && !isAlreadyExistsError(err) {
		require.NoError(t, err, "Failed to create default namespace")
	}
}

// isAlreadyExistsError checks if the error is an "already exists" error
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "already exists") || contains(err.Error(), "AlreadyExists")
}

// contains checks if a string contains a substring (case-insensitive helper)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > len(substr) && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// SetupTestEnv creates a test environment based on INTEGRATION_STRATEGY env var
// - If INTEGRATION_STRATEGY=k3s, uses K3s testcontainers (privileged, slower, more realistic)
// - Otherwise, uses pre-built envtest image (unprivileged, faster, suitable for CI/CD)
func SetupTestEnv(t *testing.T) TestEnv {
	t.Helper()

	strategy := os.Getenv("INTEGRATION_STRATEGY")
	
	switch strategy {
	case "k3s":
		t.Logf("Using K3s integration test strategy")
		tcEnv := SetupTestEnvTestcontainers(t)
		return &TestEnvK3s{TestEnvTestcontainers: tcEnv}
	default:
		t.Logf("Using pre-built envtest integration test strategy")
		return SetupTestEnvPrebuilt(t)
	}
}

