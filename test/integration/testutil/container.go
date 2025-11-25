// Package testutil provides common utilities for integration tests using testcontainers.
// This package centralizes container lifecycle management to reduce code duplication
// across different integration test suites.
package testutil

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ContainerConfig holds configuration for starting a container
type ContainerConfig struct {
	// Image is the container image to use (required)
	Image string

	// ExposedPorts is a list of ports to expose (e.g., "8080/tcp")
	ExposedPorts []string

	// Cmd is the command to run in the container
	Cmd []string

	// Env is a map of environment variables to set
	Env map[string]string

	// WaitStrategy is the strategy to wait for container readiness
	WaitStrategy wait.Strategy

	// StartupTimeout is the maximum time to wait for container to start (default: 180s)
	StartupTimeout time.Duration

	// CleanupTimeout is the maximum time to wait for container cleanup (default: 60s)
	// Note: The cleanup path enforces a minimum of 60s to ensure containers have time to stop gracefully.
	CleanupTimeout time.Duration

	// MaxRetries is the number of times to retry container creation (default: 1, no retries)
	MaxRetries int

	// RetryDelay is the base delay between retries (default: 1s, increases with attempt number)
	RetryDelay time.Duration

	// Name is a human-readable name for logging purposes
	Name string
}

// ContainerResult holds the result of starting a container
type ContainerResult struct {
	// Container is the testcontainers container instance
	Container testcontainers.Container

	// Host is the container host
	Host string

	// Ports maps exposed port specs (e.g., "8080/tcp") to their mapped ports
	Ports map[string]string
}

// GetEndpoint returns the host:port endpoint for the given port spec
func (r *ContainerResult) GetEndpoint(portSpec string) string {
	if port, ok := r.Ports[portSpec]; ok {
		return fmt.Sprintf("%s:%s", r.Host, port)
	}
	return ""
}

// DefaultContainerConfig returns a ContainerConfig with sensible defaults
func DefaultContainerConfig() ContainerConfig {
	return ContainerConfig{
		StartupTimeout: 180 * time.Second,
		CleanupTimeout: 60 * time.Second,
		MaxRetries:     1,
		RetryDelay:     time.Second,
		Name:           "container",
	}
}

// StartContainer starts a container with the given configuration.
// It automatically registers cleanup with t.Cleanup() to ensure the container
// is stopped and removed when the test completes (even on failure or panic).
//
// Example:
//
//	config := testutil.DefaultContainerConfig()
//	config.Image = "redis:latest"
//	config.ExposedPorts = []string{"6379/tcp"}
//	config.WaitStrategy = wait.ForListeningPort("6379/tcp")
//
//	result, err := testutil.StartContainer(t, config)
//	require.NoError(t, err)
//
//	endpoint := result.GetEndpoint("6379/tcp") // e.g., "localhost:32768"
func StartContainer(t *testing.T, config ContainerConfig) (*ContainerResult, error) {
	t.Helper()

	// Apply defaults for zero values
	if config.StartupTimeout == 0 {
		config.StartupTimeout = 180 * time.Second
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = 60 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 1
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Second
	}
	if config.Name == "" {
		config.Name = "container"
	}

	ctx := context.Background()

	t.Logf("Starting %s container (image: %s)...", config.Name, config.Image)

	// Build container request
	req := testcontainers.ContainerRequest{
		Image:        config.Image,
		ExposedPorts: config.ExposedPorts,
		Cmd:          config.Cmd,
		Env:          config.Env,
		WaitingFor:   config.WaitStrategy,
	}

	// Create container with retries
	var container testcontainers.Container
	var err error

	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		if attempt > 1 {
			delay := config.RetryDelay * time.Duration(attempt)
			t.Logf("Retry attempt %d/%d for %s container (waiting %v)...", attempt, config.MaxRetries, config.Name, delay)
			time.Sleep(delay)
		}

		// Create context with timeout for this attempt
		attemptCtx, cancel := context.WithTimeout(ctx, config.StartupTimeout)

		container, err = testcontainers.GenericContainer(attemptCtx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})

		cancel() // Clean up the context

		if err == nil {
			break // Success!
		}

		// If container was created but failed to start fully (e.g. wait strategy timeout),
		// ensure we terminate it before retrying to avoid leaks
		if container != nil {
			t.Logf("Attempt %d failed but container was created. Terminating...", attempt)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if termErr := container.Terminate(ctx); termErr != nil {
				t.Logf("Warning: Failed to terminate failed container from attempt %d: %v", attempt, termErr)
				// Try force cleanup
				if cid := container.GetContainerID(); cid != "" {
					forceCleanupContainer(t, cid)
				}
			}
			cancel()
		}

		if attempt < config.MaxRetries {
			t.Logf("Attempt %d failed for %s container: %v", attempt, config.Name, err)
		}
	}

	// Register cleanup BEFORE checking error to ensure container cleanup even on partial failure
	if container != nil {
		// Capture container ID for cleanup - this is the specific container we started
		containerID := container.GetContainerID()
		containerName := config.Name

		t.Cleanup(func() {
			t.Logf("Stopping and removing %s container (ID: %s)...", containerName, containerID)

			// Use longer timeout for cleanup to prevent stuck containers
			cleanupTimeout := config.CleanupTimeout
			if cleanupTimeout < 60*time.Second {
				cleanupTimeout = 60 * time.Second
			}

			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()

			if termErr := container.Terminate(cleanupCtx); termErr != nil {
				t.Logf("Warning: Failed to terminate %s container gracefully: %v", containerName, termErr)

				// Try force cleanup by specific container ID
				if containerID != "" {
					forceCleanupContainer(t, containerID)
				}
			} else {
				t.Logf("%s container stopped and removed successfully", containerName)
			}
		})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to start %s container after %d attempts: %w", config.Name, config.MaxRetries, err)
	}

	// Get container host
	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s container host: %w", config.Name, err)
	}

	// Get all mapped ports
	ports := make(map[string]string)
	for _, portSpec := range config.ExposedPorts {
		port, err := container.MappedPort(ctx, nat.Port(portSpec))
		if err != nil {
			return nil, fmt.Errorf("failed to get mapped port %s for %s container: %w", portSpec, config.Name, err)
		}
		ports[portSpec] = port.Port()
	}

	t.Logf("%s container started successfully (host: %s)", config.Name, host)

	return &ContainerResult{
		Container: container,
		Host:      host,
		Ports:     ports,
	}, nil
}

// forceCleanupContainer attempts to force remove a specific container using docker/podman CLI.
// This is a fallback when testcontainers' Terminate() fails.
//
// Note: This function requires either 'docker' or 'podman' CLI to be available in PATH.
// If neither is available, cleanup will fail with a warning message suggesting manual cleanup.
func forceCleanupContainer(t *testing.T, containerID string) {
	t.Helper()

	if containerID == "" {
		return
	}

	// Try docker first, then podman
	runtimes := []string{"docker", "podman"}

	for _, runtime := range runtimes {
		rmCmd := exec.Command(runtime, "rm", "-f", containerID)
		if output, err := rmCmd.CombinedOutput(); err == nil {
			t.Logf("Force-removed container %s using %s", containerID, runtime)
			return
		} else {
			// Log the error; some "not found" noise is acceptable for cleanup
			t.Logf("Failed to force-remove with %s: %v (output: %s)", runtime, err, string(output))
		}
	}

	t.Logf("WARNING: Could not force-remove container %s. It may already be removed or manual cleanup required.", containerID)
	t.Logf("Run: docker rm -f %s  OR  podman rm -f %s", containerID, containerID)
}

// CleanupLeakedContainers removes any containers matching the given image pattern.
// This can be called to clean up containers from previous failed test runs.
//
// Note: This function requires either 'docker' or 'podman' CLI to be available in PATH.
// If neither is available, cleanup will silently skip (no containers found with either runtime).
func CleanupLeakedContainers(t *testing.T, imagePattern string) {
	t.Helper()

	runtimes := []string{"docker", "podman"}

	for _, runtime := range runtimes {
		// List containers matching the image
		listCmd := exec.Command(runtime, "ps", "-a", "-q", "--filter", fmt.Sprintf("ancestor=%s", imagePattern))
		output, err := listCmd.Output()
		if err != nil {
			continue // Try next runtime
		}

		containers := strings.TrimSpace(string(output))
		if containers == "" {
			continue
		}

		// Remove found containers
		containerIDs := strings.Split(containers, "\n")
		for _, id := range containerIDs {
			if id == "" {
				continue
			}
			rmCmd := exec.Command(runtime, "rm", "-f", id)
			if rmErr := rmCmd.Run(); rmErr != nil {
				t.Logf("Warning: Failed to remove container %s: %v", id, rmErr)
			} else {
				t.Logf("Cleaned up leaked container: %s", id)
			}
		}
		return // Success with this runtime
	}
}

// WaitStrategies provides common wait strategy builders
var WaitStrategies = struct {
	// ForLogAndPort creates a wait strategy that waits for both a log message and a listening port
	ForLogAndPort func(logMessage string, portSpec string, timeout time.Duration) wait.Strategy

	// ForPort creates a wait strategy that waits for a listening port
	ForPort func(portSpec string, timeout time.Duration) wait.Strategy

	// ForLog creates a wait strategy that waits for a log message
	ForLog func(logMessage string, timeout time.Duration) wait.Strategy

	// ForHTTP creates a wait strategy that waits for an HTTP endpoint
	ForHTTP func(path string, portSpec string, timeout time.Duration) wait.Strategy
}{
	ForLogAndPort: func(logMessage string, portSpec string, timeout time.Duration) wait.Strategy {
		return wait.ForAll(
			wait.ForLog(logMessage).WithStartupTimeout(timeout),
			wait.ForListeningPort(nat.Port(portSpec)).WithStartupTimeout(timeout),
		)
	},
	ForPort: func(portSpec string, timeout time.Duration) wait.Strategy {
		return wait.ForListeningPort(nat.Port(portSpec)).WithStartupTimeout(timeout)
	},
	ForLog: func(logMessage string, timeout time.Duration) wait.Strategy {
		return wait.ForLog(logMessage).WithStartupTimeout(timeout)
	},
	ForHTTP: func(path string, portSpec string, timeout time.Duration) wait.Strategy {
		return wait.ForHTTP(path).
			WithPort(nat.Port(portSpec)).
			WithStartupTimeout(timeout)
	},
}

