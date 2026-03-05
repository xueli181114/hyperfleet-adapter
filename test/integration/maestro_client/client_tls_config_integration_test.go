package maestro_client_integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/maestro_client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMaestroClientConfigFromLoaded reproduces the same mapping logic as
// createMaestroClient in cmd/adapter/main.go. This is intentionally duplicated
// so the test catches drift between main.go and the config structs.
func buildMaestroClientConfigFromLoaded(maestroConfig *config_loader.MaestroClientConfig) (*maestro_client.Config, error) {
	config := &maestro_client.Config{
		MaestroServerAddr: maestroConfig.HTTPServerAddress,
		GRPCServerAddr:    maestroConfig.GRPCServerAddress,
		SourceID:          maestroConfig.SourceID,
		Insecure:          maestroConfig.Insecure,
	}

	if maestroConfig.Timeout != "" {
		d, err := time.ParseDuration(maestroConfig.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid maestro timeout %q: %w", maestroConfig.Timeout, err)
		}
		config.HTTPTimeout = d
	}

	if maestroConfig.ServerHealthinessTimeout != "" {
		d, err := time.ParseDuration(maestroConfig.ServerHealthinessTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid maestro serverHealthinessTimeout %q: %w", maestroConfig.ServerHealthinessTimeout, err)
		}
		config.ServerHealthinessTimeout = d
	}

	if maestroConfig.Auth.TLSConfig != nil {
		config.CAFile = maestroConfig.Auth.TLSConfig.CAFile
		config.ClientCertFile = maestroConfig.Auth.TLSConfig.CertFile
		config.ClientKeyFile = maestroConfig.Auth.TLSConfig.KeyFile
		config.HTTPCAFile = maestroConfig.Auth.TLSConfig.HTTPCAFile
	}

	return config, nil
}

// writeTestAdapterConfig writes a minimal AdapterConfig YAML that references
// the given TLS cert paths and Maestro addresses.
func writeTestAdapterConfig(t *testing.T, dir string, opts map[string]string) string {
	t.Helper()

	tlsBlock := ""
	if opts["caFile"] != "" {
		tlsBlock = fmt.Sprintf(`    auth:
      type: "tls"
      tls_config:
        ca_file: %q
        cert_file: %q
        key_file: %q
        http_ca_file: %q`, opts["caFile"], opts["certFile"], opts["keyFile"], opts["httpCaFile"])
	}

	insecure := "false"
	if opts["insecure"] == "true" {
		insecure = "true"
	}

	yaml := fmt.Sprintf(`adapter:
  name: tls-integration-test
  version: "0.1.0"
clients:
  maestro:
    grpc_server_address: %q
    http_server_address: %q
    source_id: %q
    insecure: %s
    timeout: "15s"
    server_healthiness_timeout: "25s"
%s
  hyperfleet_api:
    base_url: http://localhost:8000
    version: v1
    timeout: 2s
    retry_attempts: 1
  broker:
    subscription_id: test
    topic: test
  kubernetes:
    api_version: v1
`, opts["grpcAddr"], opts["httpAddr"], opts["sourceId"], insecure, tlsBlock)

	path := filepath.Join(dir, "adapter-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))
	return path
}

// writeMinimalTaskConfig writes the smallest valid AdapterTaskConfig.
func writeMinimalTaskConfig(t *testing.T, dir string) string {
	t.Helper()
	yaml := `params: []
`
	path := filepath.Join(dir, "task-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))
	return path
}

// TestTLSConfigLoadAndConnect_MutualTLS loads an AdapterConfig YAML with mTLS
// settings, parses it through the config loader, maps it to maestro_client.Config
// (same logic as main.go), creates a real client, and connects to the TLS Maestro.
func TestTLSConfigLoadAndConnect_MutualTLS(t *testing.T) {
	env := GetSharedEnv(t)
	requireTLSEnv(t, env)

	tmpDir := t.TempDir()

	adapterPath := writeTestAdapterConfig(t, tmpDir, map[string]string{
		"grpcAddr":   env.TLSMaestroGRPCAddr,
		"httpAddr":   env.TLSMaestroServerAddr,
		"sourceId":   "config-tls-mtls",
		"caFile":     env.TLSCerts.CAFilePath(),
		"certFile":   env.TLSCerts.ClientCertFilePath(),
		"keyFile":    env.TLSCerts.ClientKeyFilePath(),
		"httpCaFile": env.TLSCerts.CAFilePath(),
	})
	taskPath := writeMinimalTaskConfig(t, tmpDir)

	cfg, err := config_loader.LoadConfig(
		config_loader.WithAdapterConfigPath(adapterPath),
		config_loader.WithTaskConfigPath(taskPath),
		config_loader.WithSkipSemanticValidation(),
	)
	require.NoError(t, err, "Config loading should succeed")
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Clients, "Clients config should not be nil")
	require.NotNil(t, cfg.Clients.Maestro, "Maestro config should be present")

	maestroCfg := cfg.Clients.Maestro
	assert.Equal(t, env.TLSMaestroGRPCAddr, maestroCfg.GRPCServerAddress)
	assert.Equal(t, env.TLSMaestroServerAddr, maestroCfg.HTTPServerAddress)
	assert.Equal(t, "config-tls-mtls", maestroCfg.SourceID)
	assert.False(t, maestroCfg.Insecure)
	assert.Equal(t, "15s", maestroCfg.Timeout)
	assert.Equal(t, "25s", maestroCfg.ServerHealthinessTimeout)
	require.NotNil(t, maestroCfg.Auth.TLSConfig)
	assert.Equal(t, env.TLSCerts.CAFilePath(), maestroCfg.Auth.TLSConfig.CAFile)
	assert.Equal(t, env.TLSCerts.ClientCertFilePath(), maestroCfg.Auth.TLSConfig.CertFile)
	assert.Equal(t, env.TLSCerts.ClientKeyFilePath(), maestroCfg.Auth.TLSConfig.KeyFile)
	assert.Equal(t, env.TLSCerts.CAFilePath(), maestroCfg.Auth.TLSConfig.HTTPCAFile)

	// Map config → client config (same as main.go createMaestroClient)
	clientCfg, err := buildMaestroClientConfigFromLoaded(maestroCfg)
	require.NoError(t, err)

	assert.Equal(t, 15*time.Second, clientCfg.HTTPTimeout)
	assert.Equal(t, 25*time.Second, clientCfg.ServerHealthinessTimeout)
	assert.Equal(t, env.TLSCerts.CAFilePath(), clientCfg.CAFile)
	assert.Equal(t, env.TLSCerts.ClientCertFilePath(), clientCfg.ClientCertFile)
	assert.Equal(t, env.TLSCerts.ClientKeyFilePath(), clientCfg.ClientKeyFile)
	assert.Equal(t, env.TLSCerts.CAFilePath(), clientCfg.HTTPCAFile)

	// Create a real client and connect
	tc := createTLSTestClient(t, clientCfg, 30*time.Second)
	defer tc.Close()

	list, err := tc.Client.ListManifestWorks(tc.Ctx, "test-cluster-list", "")
	require.NoError(t, err, "ListManifestWorks over TLS (config-loaded mTLS) should succeed")
	t.Logf("Config-loaded mTLS: listed %d ManifestWorks", len(list.Items))
}

// TestTLSConfigLoadAndConnect_CAOnly loads config with only caFile (no client certs),
// verifying the CA-only TLS path works end-to-end from config file.
func TestTLSConfigLoadAndConnect_CAOnly(t *testing.T) {
	env := GetSharedEnv(t)
	requireTLSEnv(t, env)

	tmpDir := t.TempDir()

	adapterPath := writeTestAdapterConfig(t, tmpDir, map[string]string{
		"grpcAddr":   env.TLSMaestroGRPCAddr,
		"httpAddr":   env.TLSMaestroServerAddr,
		"sourceId":   "config-tls-ca-only",
		"caFile":     env.TLSCerts.CAFilePath(),
		"certFile":   "",
		"keyFile":    "",
		"httpCaFile": "",
	})
	taskPath := writeMinimalTaskConfig(t, tmpDir)

	cfg, err := config_loader.LoadConfig(
		config_loader.WithAdapterConfigPath(adapterPath),
		config_loader.WithTaskConfigPath(taskPath),
		config_loader.WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Clients, "Clients config should not be nil")

	clientCfg, err := buildMaestroClientConfigFromLoaded(cfg.Clients.Maestro)
	require.NoError(t, err)

	assert.Equal(t, env.TLSCerts.CAFilePath(), clientCfg.CAFile)
	assert.Empty(t, clientCfg.ClientCertFile, "No client cert for CA-only")
	assert.Empty(t, clientCfg.ClientKeyFile, "No client key for CA-only")

	tc := createTLSTestClient(t, clientCfg, 30*time.Second)
	defer tc.Close()

	list, err := tc.Client.ListManifestWorks(tc.Ctx, "test-cluster-list", "")
	require.NoError(t, err, "ListManifestWorks over TLS (config-loaded CA-only) should succeed")
	t.Logf("Config-loaded CA-only: listed %d ManifestWorks", len(list.Items))
}

// TestTLSConfigLoadAndConnect_Insecure loads an insecure config (no TLS)
// and connects to the plaintext Maestro, verifying the insecure path from config.
func TestTLSConfigLoadAndConnect_Insecure(t *testing.T) {
	env := GetSharedEnv(t)

	tmpDir := t.TempDir()

	adapterPath := writeTestAdapterConfig(t, tmpDir, map[string]string{
		"grpcAddr": env.MaestroGRPCAddr,
		"httpAddr": env.MaestroServerAddr,
		"sourceId": "config-insecure",
		"insecure": "true",
	})
	taskPath := writeMinimalTaskConfig(t, tmpDir)

	cfg, err := config_loader.LoadConfig(
		config_loader.WithAdapterConfigPath(adapterPath),
		config_loader.WithTaskConfigPath(taskPath),
		config_loader.WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Clients, "Clients config should not be nil")

	maestroCfg := cfg.Clients.Maestro
	assert.True(t, maestroCfg.Insecure)

	clientCfg, err := buildMaestroClientConfigFromLoaded(maestroCfg)
	require.NoError(t, err)
	assert.True(t, clientCfg.Insecure)

	tc := createTLSTestClient(t, clientCfg, 30*time.Second)
	defer tc.Close()

	list, err := tc.Client.ListManifestWorks(tc.Ctx, "test-cluster-list", "")
	require.NoError(t, err, "ListManifestWorks (config-loaded insecure) should succeed")
	t.Logf("Config-loaded insecure: listed %d ManifestWorks", len(list.Items))
}

// TestTLSConfigLoadAndConnect_EnvOverride verifies that environment variables
// override YAML config values, simulating the production Viper override path.
func TestTLSConfigLoadAndConnect_EnvOverride(t *testing.T) {
	env := GetSharedEnv(t)
	requireTLSEnv(t, env)

	tmpDir := t.TempDir()

	// Write config with placeholder values that will be overridden by env vars
	adapterPath := writeTestAdapterConfig(t, tmpDir, map[string]string{
		"grpcAddr":   "placeholder:9999",
		"httpAddr":   "https://placeholder:9999",
		"sourceId":   "will-be-overridden",
		"caFile":     env.TLSCerts.CAFilePath(),
		"certFile":   env.TLSCerts.ClientCertFilePath(),
		"keyFile":    env.TLSCerts.ClientKeyFilePath(),
		"httpCaFile": env.TLSCerts.CAFilePath(),
	})
	taskPath := writeMinimalTaskConfig(t, tmpDir)

	// Override addresses via environment variables
	t.Setenv("HYPERFLEET_MAESTRO_GRPC_SERVER_ADDRESS", env.TLSMaestroGRPCAddr)
	t.Setenv("HYPERFLEET_MAESTRO_HTTP_SERVER_ADDRESS", env.TLSMaestroServerAddr)
	t.Setenv("HYPERFLEET_MAESTRO_SOURCE_ID", "config-tls-env-override")
	t.Setenv("HYPERFLEET_MAESTRO_INSECURE", "false")

	cfg, err := config_loader.LoadConfig(
		config_loader.WithAdapterConfigPath(adapterPath),
		config_loader.WithTaskConfigPath(taskPath),
		config_loader.WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.Clients, "Clients config should not be nil")

	maestroCfg := cfg.Clients.Maestro
	assert.Equal(t, env.TLSMaestroGRPCAddr, maestroCfg.GRPCServerAddress, "Env should override YAML")
	assert.Equal(t, env.TLSMaestroServerAddr, maestroCfg.HTTPServerAddress, "Env should override YAML")
	assert.Equal(t, "config-tls-env-override", maestroCfg.SourceID, "Env should override YAML")

	clientCfg, err := buildMaestroClientConfigFromLoaded(maestroCfg)
	require.NoError(t, err)

	tc := createTLSTestClient(t, clientCfg, 30*time.Second)
	defer tc.Close()

	list, err := tc.Client.ListManifestWorks(tc.Ctx, "test-cluster-list", "")
	require.NoError(t, err, "ListManifestWorks with env-overridden TLS config should succeed")
	t.Logf("Config-loaded with env override: listed %d ManifestWorks", len(list.Items))
}
