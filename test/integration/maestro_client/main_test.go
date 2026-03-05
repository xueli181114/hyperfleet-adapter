// main_test.go provides shared test setup for Maestro integration tests.
// It starts PostgreSQL and Maestro server containers that are reused across all test functions.

package maestro_client_integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const (
	// MaestroImage is the Maestro server container image
	MaestroImage = "quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro:latest"

	// PostgresImage is the PostgreSQL container image
	PostgresImage = "quay.io/sclorg/postgresql-15-c9s:latest"

	// Default ports
	PostgresPort      = "5432/tcp"
	MaestroHTTPPort   = "8000/tcp"
	MaestroGRPCPort   = "8090/tcp"
	MaestroHealthPort = "8083/tcp"
)

// MaestroTestEnv holds the test environment configuration
type MaestroTestEnv struct {
	// PostgreSQL
	PostgresContainer testcontainers.Container
	PostgresHost      string
	PostgresPort      string

	// Maestro (insecure / plaintext)
	MaestroContainer  testcontainers.Container
	MaestroHost       string
	MaestroHTTPPort   string
	MaestroGRPCPort   string
	MaestroHealthPort string

	// Insecure connection strings
	MaestroServerAddr string // HTTP API address (e.g., "http://localhost:32000")
	MaestroGRPCAddr   string // gRPC address (e.g., "localhost:32001")

	// TLS-enabled Maestro (separate container, same DB)
	TLSCerts             *TLSTestCerts
	TLSMaestroContainer  testcontainers.Container
	TLSMaestroHTTPPort   string
	TLSMaestroGRPCPort   string
	TLSMaestroServerAddr string // HTTPS API address (e.g., "https://localhost:32100")
	TLSMaestroGRPCAddr   string // gRPC+TLS address (e.g., "localhost:32101")
}

// sharedEnv holds the shared test environment for all integration tests
var sharedEnv *MaestroTestEnv

// skipReason holds reason to skip tests (e.g., ARM64 without local image, no container runtime)
var skipReason string

// setupErr holds any error that occurred during setup (consumer registration, container start, etc.)
// These errors should cause test FAILURE, not skip.
var setupErr error

// TestMain runs before all tests to set up the shared containers
func TestMain(m *testing.M) {
	flag.Parse()

	// Check if we should skip integration tests
	if testing.Short() {
		os.Exit(m.Run())
	}

	// Check if SKIP_MAESTRO_INTEGRATION_TESTS is set
	if os.Getenv("SKIP_MAESTRO_INTEGRATION_TESTS") == "true" {
		skipReason = "SKIP_MAESTRO_INTEGRATION_TESTS is set"
		println("⚠️  SKIP_MAESTRO_INTEGRATION_TESTS is set, skipping maestro_client integration tests")
		os.Exit(m.Run())
	}

	// Skip on ARM64 Macs unless MAESTRO_ARM64_TEST is set (user has local ARM64 image)
	// To run on ARM64, build a local image from the Maestro source and tag it as:
	// quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro:latest
	if runtime.GOARCH == "arm64" && os.Getenv("MAESTRO_ARM64_TEST") != "true" {
		skipReason = "ARM64 architecture without MAESTRO_ARM64_TEST=true (set this env if you have a local ARM64 Maestro image)"
		println("⚠️  Skipping Maestro integration tests on ARM64")
		println("   The official Maestro image is amd64 only.")
		println("   To run locally, build from source and set MAESTRO_ARM64_TEST=true:")
		println("   cd /path/to/maestro && podman build -t quay.io/redhat-user-workloads/maestro-rhtap-tenant/maestro/maestro:latest .")
		os.Exit(m.Run())
	}

	// Quick check if testcontainers can work
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		skipReason = fmt.Sprintf("container runtime not available: %v", err)
		println("⚠️  Warning: Could not connect to container runtime:", err.Error())
		println("   Tests will be skipped")
	} else {
		info, err := provider.DaemonHost(ctx)
		_ = provider.Close()

		if err != nil {
			skipReason = fmt.Sprintf("container runtime info not available: %v", err)
			println("⚠️  Warning: Could not get container runtime info:", err.Error())
			println("   Tests will be skipped")
		} else {
			println("✅ Container runtime available:", info)
			println("🚀 Starting Maestro test environment...")

			// Set up the shared environment
			env, err := setupMaestroTestEnv()
			if err != nil {
				// Setup failures (including consumer registration) should FAIL tests, not skip
				setupErr = err
				println("❌ Failed to set up Maestro environment:", err.Error())
				println("   Tests will FAIL")
			} else {
				sharedEnv = env
				println("✅ Maestro test environment ready!")
				println(fmt.Sprintf("   HTTP API: %s", env.MaestroServerAddr))
				println(fmt.Sprintf("   gRPC:     %s", env.MaestroGRPCAddr))

				// Set up TLS-enabled Maestro (shares the same PostgreSQL)
				println("🔒 Setting up TLS Maestro server...")
				if err := setupTLSMaestroEnv(env); err != nil {
					setupErr = fmt.Errorf("TLS setup failed: %w", err)
					println("❌ Failed to set up TLS Maestro:", err.Error())
					println("   Tests will FAIL")
				} else {
					println("✅ TLS Maestro ready!")
					println(fmt.Sprintf("   HTTPS API: %s", env.TLSMaestroServerAddr))
					println(fmt.Sprintf("   gRPC+TLS:  %s", env.TLSMaestroGRPCAddr))
				}
			}
		}
	}
	println()

	// Run tests
	exitCode := m.Run()

	// Cleanup after all tests
	if sharedEnv != nil {
		println()
		println("🧹 Cleaning up Maestro test environment...")
		cleanupMaestroTestEnv(sharedEnv)
	}

	os.Exit(exitCode)
}

// GetSharedEnv returns the shared test environment.
// - If there's a skipReason (ARM64, no container runtime), tests are skipped
// - If there's a setupErr (consumer registration, container start failed), tests FAIL
// - If in short mode, tests are skipped
func GetSharedEnv(t *testing.T) *MaestroTestEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if skipReason != "" {
		t.Skipf("Skipping: %s", skipReason)
	}
	if setupErr != nil {
		t.Fatalf("Maestro environment setup failed: %v", setupErr)
	}
	if sharedEnv == nil {
		t.Fatal("Shared test environment is not initialized")
	}
	return sharedEnv
}
