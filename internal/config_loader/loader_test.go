package config_loader

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// createTestConfigFiles creates temporary adapter and task config files for testing
func createTestConfigFiles(t *testing.T, tmpDir string, adapterYAML, taskYAML string) (adapterPath, taskPath string) {
	t.Helper()

	adapterPath = filepath.Join(tmpDir, "adapter-config.yaml")
	taskPath = filepath.Join(tmpDir, "task-config.yaml")

	err := os.WriteFile(adapterPath, []byte(adapterYAML), 0644)
	require.NoError(t, err)

	err = os.WriteFile(taskPath, []byte(taskYAML), 0644)
	require.NoError(t, err)

	return adapterPath, taskPath
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()

	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: deployment-config
  namespace: hyperfleet-system
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
      retryAttempts: 3
      retryBackoff: exponential
    kubernetes:
      apiVersion: "v1"
`

	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
  namespace: hyperfleet-system
  labels:
    hyperfleet.io/adapter-type: test
spec:
  params:
    - name: "clusterId"
      source: "event.id"
      type: "string"
      required: true
  preconditions:
    - name: "clusterStatus"
      apiCall:
        method: "GET"
        url: "https://api.example.com/clusters/{{ .clusterId }}"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        namespace: "*"
        byName: "test-ns"
`

	adapterPath, taskPath := createTestConfigFiles(t, tmpDir, adapterYAML, taskYAML)

	// Test loading
	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify merged config fields
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", config.APIVersion)
	assert.Equal(t, "Config", config.Kind)
	// Metadata comes from adapter config (takes precedence)
	assert.Equal(t, "deployment-config", config.Metadata.Name)
	// Adapter info comes from adapter config
	assert.Equal(t, "0.1.0", config.Spec.Adapter.Version)
	// Clients config comes from adapter config
	assert.Equal(t, "https://test.example.com", config.Spec.Clients.HyperfleetAPI.BaseURL)
	assert.Equal(t, 2*time.Second, config.Spec.Clients.HyperfleetAPI.Timeout)
	// Task fields come from task config
	require.Len(t, config.Spec.Params, 1)
	assert.Equal(t, "clusterId", config.Spec.Params[0].Name)
	require.Len(t, config.Spec.Preconditions, 1)
	assert.Equal(t, "clusterStatus", config.Spec.Preconditions[0].Name)
	require.Len(t, config.Spec.Resources, 1)
	assert.Equal(t, "testNamespace", config.Spec.Resources[0].Name)
}

func TestLoadConfigMissingAdapterConfig(t *testing.T) {
	tmpDir := t.TempDir()
	taskPath := filepath.Join(tmpDir, "task-config.yaml")
	err := os.WriteFile(taskPath, []byte(`
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec: {}
`), 0644)
	require.NoError(t, err)

	config, err := LoadConfig(
		WithAdapterConfigPath("/nonexistent/adapter-config.yaml"),
		WithTaskConfigPath(taskPath),
	)
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to load adapter config")
}

func TestLoadConfigMissingTaskConfig(t *testing.T) {
	tmpDir := t.TempDir()
	adapterPath := filepath.Join(tmpDir, "adapter-config.yaml")
	err := os.WriteFile(adapterPath, []byte(`
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  clients:
    hyperfleetApi:
      timeout: 5s
    kubernetes:
      apiVersion: v1
`), 0644)
	require.NoError(t, err)

	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath("/nonexistent/task-config.yaml"),
	)
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to load task config")
}

func TestAdapterConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid minimal adapter config",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
  clients:
    hyperfleetApi:
      timeout: 5s
    kubernetes:
      apiVersion: "v1"
`,
			wantError: false,
		},
		{
			name: "missing apiVersion",
			yaml: `
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "apiVersion is required",
		},
		{
			name: "missing kind",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "kind is required",
		},
		{
			name: "missing metadata.name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  namespace: test
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "metadata.name is required",
		},
		{
			name: "missing adapter.version",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter: {}
`,
			wantError: true,
			errorMsg:  "spec.adapter.version is required",
		},
		{
			name: "unsupported apiVersion",
			yaml: `
apiVersion: hyperfleet.redhat.com/v2
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "1.0.0"
`,
			wantError: true,
			errorMsg:  "unsupported apiVersion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config AdapterConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			require.NoError(t, err)

			validator := NewAdapterConfigValidator(&config, "")
			err = validator.ValidateStructure()

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTaskConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid minimal task config",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec: {}
`,
			wantError: false,
		},
		{
			name: "valid task config with params",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterId"
      source: "event.id"
      required: true
`,
			wantError: false,
		},
		{
			name: "missing apiVersion",
			yaml: `
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec: {}
`,
			wantError: true,
			errorMsg:  "apiVersion is required",
		},
		{
			name: "missing kind",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
metadata:
  name: test-adapter
spec: {}
`,
			wantError: true,
			errorMsg:  "kind is required",
		},
		{
			name: "parameter without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - source: "event.id"
`,
			wantError: true,
			errorMsg:  "spec.params[0].name is required",
		},
		{
			name: "parameter without source",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterId"
      required: true
`,
			wantError: true,
			errorMsg:  "source is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config AdapterTaskConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			require.NoError(t, err)

			validator := NewTaskConfigValidator(&config, "")
			err = validator.ValidateStructure()

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePreconditionsInTaskConfig(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid precondition with API call",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "GET"
        url: "https://api.example.com/clusters"
`,
			wantError: false,
		},
		{
			name: "precondition without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  preconditions:
    - apiCall:
        method: "GET"
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "spec.preconditions[0].name is required",
		},
		{
			name: "precondition without apiCall or expression",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  preconditions:
    - name: "checkCluster"
`,
			wantError: true,
			errorMsg:  "spec.preconditions[0]: must specify apiCall, conditions",
		},
		{
			name: "API call without method",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  preconditions:
    - name: "checkCluster"
      apiCall:
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "method is required",
		},
		{
			name: "API call with invalid method",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  preconditions:
    - name: "checkCluster"
      apiCall:
        method: "INVALID"
        url: "https://api.example.com/clusters"
`,
			wantError: true,
			errorMsg:  "is invalid (allowed:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config AdapterTaskConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			require.NoError(t, err)

			validator := NewTaskConfigValidator(&config, "")
			err = validator.ValidateStructure()

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateResourcesInTaskConfig(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid resource with manifest",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        namespace: "*"
        byName: "test-ns"
`,
			wantError: false,
		},
		{
			name: "resource without name",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - manifest:
        apiVersion: v1
        kind: Namespace
`,
			wantError: true,
			errorMsg:  "spec.resources[0].name is required",
		},
		{
			name: "resource without manifest - kubernetes transport requires manifest in semantic validation",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testNamespace"
      discovery:
        byName: "test-ns"
`,
			wantError: false, // Manifest is no longer structurally required (validated semantically based on transport type)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config AdapterTaskConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			require.NoError(t, err)

			validator := NewTaskConfigValidator(&config, "")
			err = validator.ValidateStructure()

			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMergeConfigs(t *testing.T) {
	adapterCfg := &AdapterConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterConfig",
		Metadata: Metadata{
			Name: "adapter-deployment",
		},
		Spec: AdapterConfigSpec{
			Adapter: AdapterInfo{
				Version: "1.0.0",
			},
			Clients: ClientsConfig{
				HyperfleetAPI: HyperfleetAPIConfig{
					BaseURL:       "https://api.example.com",
					Timeout:       5 * time.Second,
					RetryAttempts: 3,
				},
				Kubernetes: KubernetesConfig{
					APIVersion: "v1",
				},
			},
		},
	}

	taskCfg := &AdapterTaskConfig{
		APIVersion: "hyperfleet.redhat.com/v1alpha1",
		Kind:       "AdapterTaskConfig",
		Metadata: Metadata{
			Name: "task-processor",
		},
		Spec: AdapterTaskSpec{
			Params: []Parameter{
				{Name: "clusterId", Source: "event.id", Required: true},
			},
			Preconditions: []Precondition{
				{ActionBase: ActionBase{Name: "checkStatus"}},
			},
			Resources: []Resource{
				{Name: "namespace"},
			},
		},
	}

	merged := Merge(adapterCfg, taskCfg)

	// Verify merged config
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", merged.APIVersion)
	assert.Equal(t, "Config", merged.Kind)
	// Metadata comes from adapter config
	assert.Equal(t, "adapter-deployment", merged.Metadata.Name)
	// Adapter info from adapter config
	assert.Equal(t, "1.0.0", merged.Spec.Adapter.Version)
	// Clients from adapter config
	assert.Equal(t, "https://api.example.com", merged.Spec.Clients.HyperfleetAPI.BaseURL)
	assert.Equal(t, 5*time.Second, merged.Spec.Clients.HyperfleetAPI.Timeout)
	// Task fields from task config
	require.Len(t, merged.Spec.Params, 1)
	assert.Equal(t, "clusterId", merged.Spec.Params[0].Name)
	require.Len(t, merged.Spec.Preconditions, 1)
	assert.Equal(t, "checkStatus", merged.Spec.Preconditions[0].Name)
	require.Len(t, merged.Spec.Resources, 1)
	assert.Equal(t, "namespace", merged.Spec.Resources[0].Name)
}

func TestGetRequiredParams(t *testing.T) {
	config := &Config{
		Spec: ConfigSpec{
			Params: []Parameter{
				{Name: "clusterId", Source: "event.id", Required: true},
				{Name: "optional", Source: "event.optional", Required: false},
			},
		},
	}

	requiredParams := config.GetRequiredParams()
	assert.Len(t, requiredParams, 1)
	assert.Equal(t, "clusterId", requiredParams[0].Name)
}

func TestGetResourceByName(t *testing.T) {
	config := &Config{
		Spec: ConfigSpec{
			Resources: []Resource{
				{Name: "namespace1"},
				{Name: "namespace2"},
			},
		},
	}

	resource := config.GetResourceByName("namespace1")
	assert.NotNil(t, resource)
	assert.Equal(t, "namespace1", resource.Name)

	resource = config.GetResourceByName("nonexistent")
	assert.Nil(t, resource)
}

func TestGetPreconditionByName(t *testing.T) {
	config := &Config{
		Spec: ConfigSpec{
			Preconditions: []Precondition{
				{ActionBase: ActionBase{Name: "precond1"}},
				{ActionBase: ActionBase{Name: "precond2"}},
			},
		},
	}

	precond := config.GetPreconditionByName("precond1")
	assert.NotNil(t, precond)
	assert.Equal(t, "precond1", precond.Name)

	precond = config.GetPreconditionByName("nonexistent")
	assert.Nil(t, precond)
}

func TestValidateAdapterVersion(t *testing.T) {
	config := &AdapterConfig{
		Spec: AdapterConfigSpec{
			Adapter: AdapterInfo{
				Version: "1.0.0",
			},
		},
	}

	// Exact match
	err := ValidateAdapterVersion(config, "1.0.0")
	assert.NoError(t, err)

	// Patch version differs - should pass (bug fix release)
	err = ValidateAdapterVersion(config, "1.0.5")
	assert.NoError(t, err)

	// Minor version differs - should fail
	err = ValidateAdapterVersion(config, "1.1.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adapter version mismatch")

	// Major version differs - should fail
	err = ValidateAdapterVersion(config, "2.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adapter version mismatch")

	// Empty expected version (skip validation)
	err = ValidateAdapterVersion(config, "")
	assert.NoError(t, err)

	// Dev build versions (0.0.0-* skip validation)
	err = ValidateAdapterVersion(config, "0.0.0-dev")
	assert.NoError(t, err)

	err = ValidateAdapterVersion(config, "0.0.0-master")
	assert.NoError(t, err)

	err = ValidateAdapterVersion(config, "v0.0.0-dev")
	assert.NoError(t, err)

	// Pre-release version with same major.minor - should pass
	err = ValidateAdapterVersion(config, "1.0.1-rc.1")
	assert.NoError(t, err)

	// Invalid config version
	invalidConfig := &AdapterConfig{
		Spec: AdapterConfigSpec{
			Adapter: AdapterInfo{
				Version: "not-a-version",
			},
		},
	}
	err = ValidateAdapterVersion(invalidConfig, "1.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid config adapter version")

	// Invalid expected version
	err = ValidateAdapterVersion(config, "not-a-version")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid expected adapter version")
}

func TestIsSupportedAPIVersion(t *testing.T) {
	// Supported version
	assert.True(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v1alpha1"))

	// Unsupported versions
	assert.False(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v1"))
	assert.False(t, IsSupportedAPIVersion("hyperfleet.redhat.com/v2"))
	assert.False(t, IsSupportedAPIVersion("other.io/v1alpha1"))
	assert.False(t, IsSupportedAPIVersion(""))
}

func TestSupportedAPIVersions(t *testing.T) {
	// Verify the constant is in the supported list
	assert.Contains(t, SupportedAPIVersions, APIVersionV1Alpha1)
	assert.Equal(t, "hyperfleet.redhat.com/v1alpha1", APIVersionV1Alpha1)
}

func TestValidateFileReferencesInTaskConfig(t *testing.T) {
	// Create temporary directory with test files
	tmpDir := t.TempDir()

	// Create a test template file
	templatePath := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templatePath, 0755))
	templateFile := filepath.Join(templatePath, "test-template.yaml")
	require.NoError(t, os.WriteFile(templateFile, []byte("test: value"), 0644))

	tests := []struct {
		name    string
		config  *AdapterTaskConfig
		baseDir string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid payload buildRef",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Post: &PostConfig{
						Payloads: []Payload{
							{Name: "test", BuildRef: "templates/test-template.yaml"},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "invalid payload buildRef - file not found",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Post: &PostConfig{
						Payloads: []Payload{
							{Name: "test", BuildRef: "templates/nonexistent.yaml"},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "does not exist",
		},
		{
			name: "invalid payload buildRef - is a directory",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Post: &PostConfig{
						Payloads: []Payload{
							{Name: "test", BuildRef: "templates"},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "is a directory",
		},
		{
			name: "valid manifest.ref",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"ref": "templates/test-template.yaml",
							},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "invalid manifest.ref - file not found",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Resources: []Resource{
						{
							Name: "test",
							Manifest: map[string]interface{}{
								"ref": "templates/nonexistent.yaml",
							},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: true,
			errMsg:  "does not exist",
		},
		{
			name: "valid multiple payloads with buildRef",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Post: &PostConfig{
						Payloads: []Payload{
							{Name: "payload1", BuildRef: "templates/test-template.yaml"},
							{Name: "payload2", BuildRef: "templates/test-template.yaml"},
						},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
		{
			name: "no file references - should pass",
			config: &AdapterTaskConfig{
				APIVersion: "hyperfleet.redhat.com/v1alpha1",
				Kind:       "AdapterTaskConfig",
				Metadata:   Metadata{Name: "test"},
				Spec: AdapterTaskSpec{
					Params: []Parameter{
						{Name: "test", Source: "event.test"},
					},
				},
			},
			baseDir: tmpDir,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewTaskConfigValidator(tt.config, tt.baseDir)
			err := validator.ValidateFileReferences()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfigWithFileReferences(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()

	// Create a template file
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0755))
	templateFile := filepath.Join(templateDir, "status-payload.yaml")
	require.NoError(t, os.WriteFile(templateFile, []byte(`
status: "{{ .status }}"
`), 0644))

	// Create adapter config file
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
    kubernetes:
      apiVersion: "v1"
`
	adapterPath := filepath.Join(tmpDir, "adapter-config.yaml")
	require.NoError(t, os.WriteFile(adapterPath, []byte(adapterYAML), 0644))

	// Create task config file with buildRef
	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterId"
      source: "event.id"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: test
      discovery:
        namespace: "*"
        byName: "test"
  post:
    payloads:
      - name: "statusPayload"
        buildRef: "templates/status-payload.yaml"
`
	taskPath := filepath.Join(tmpDir, "task-config.yaml")
	require.NoError(t, os.WriteFile(taskPath, []byte(taskYAML), 0644))

	// Load should succeed because template file exists
	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "test-adapter", config.Metadata.Name)

	// Verify buildRef content was loaded
	require.NotNil(t, config.Spec.Post)
	require.Len(t, config.Spec.Post.Payloads, 1)
	assert.NotNil(t, config.Spec.Post.Payloads[0].BuildRefContent)

	// Now test with non-existent buildRef
	taskYAMLBad := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterId"
      source: "event.id"
  resources:
    - name: "testNamespace"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: test
      discovery:
        namespace: "*"
        byName: "test"
  post:
    payloads:
      - name: "statusPayload"
        buildRef: "templates/nonexistent.yaml"
`
	taskPathBad := filepath.Join(tmpDir, "task-config-bad.yaml")
	require.NoError(t, os.WriteFile(taskPathBad, []byte(taskYAMLBad), 0644))

	// Load should fail because template file doesn't exist
	config, err = LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPathBad),
		WithSkipSemanticValidation(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Nil(t, config)
}

func TestLoadFileReferencesContent(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templateDir, 0755))

	// Create a buildRef template file
	buildRefFile := filepath.Join(templateDir, "status-payload.yaml")
	require.NoError(t, os.WriteFile(buildRefFile, []byte(`
status: "{{ .status }}"
message: "Operation completed"
`), 0644))

	// Create a manifest.ref template file
	manifestRefFile := filepath.Join(templateDir, "deployment.yaml")
	require.NoError(t, os.WriteFile(manifestRefFile, []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: "{{ .name }}"
  namespace: "{{ .namespace }}"
spec:
  replicas: 1
`), 0644))

	// Create adapter config
	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
    kubernetes:
      apiVersion: "v1"
`
	adapterPath := filepath.Join(tmpDir, "adapter-config.yaml")
	require.NoError(t, os.WriteFile(adapterPath, []byte(adapterYAML), 0644))

	// Create task config file with both buildRef and manifest.ref
	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterId"
      source: "event.id"
  resources:
    - name: "deployment"
      manifest:
        ref: "templates/deployment.yaml"
      discovery:
        namespace: "*"
        bySelectors:
          labelSelector:
            app: "test"
  post:
    payloads:
      - name: "statusPayload"
        buildRef: "templates/status-payload.yaml"
`
	taskPath := filepath.Join(tmpDir, "task-config.yaml")
	require.NoError(t, os.WriteFile(taskPath, []byte(taskYAML), 0644))

	// Load config
	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify manifest.ref was loaded and replaced
	require.Len(t, config.Spec.Resources, 1)
	manifest, ok := config.Spec.Resources[0].Manifest.(map[string]interface{})
	require.True(t, ok, "Manifest should be a map after loading ref")
	assert.Equal(t, "apps/v1", manifest["apiVersion"])
	assert.Equal(t, "Deployment", manifest["kind"])
	// Verify ref is no longer present (replaced with actual content)
	_, hasRef := manifest["ref"]
	assert.False(t, hasRef, "ref should be replaced with actual content")

	// Verify buildRef content was loaded into BuildRefContent
	require.NotNil(t, config.Spec.Post)
	require.Len(t, config.Spec.Post.Payloads, 1)
	assert.NotNil(t, config.Spec.Post.Payloads[0].BuildRefContent)
	assert.Equal(t, "{{ .status }}", config.Spec.Post.Payloads[0].BuildRefContent["status"])
	assert.Equal(t, "Operation completed", config.Spec.Post.Payloads[0].BuildRefContent["message"])
	// Original BuildRef path should still be preserved
	assert.Equal(t, "templates/status-payload.yaml", config.Spec.Post.Payloads[0].BuildRef)
}

func TestValidateResourceDiscoveryInTaskConfig(t *testing.T) {
	// Helper to create a valid task config with given resources
	configWithResources := func(resources []Resource) *AdapterTaskConfig {
		return &AdapterTaskConfig{
			APIVersion: "hyperfleet.redhat.com/v1alpha1",
			Kind:       "AdapterTaskConfig",
			Metadata:   Metadata{Name: "test-adapter"},
			Spec: AdapterTaskSpec{
				Resources: resources,
			},
		}
	}

	tests := []struct {
		name      string
		resources []Resource
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid - manifest.ref with discovery bySelectors",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					Discovery: &DiscoveryConfig{
						Namespace: "test-ns",
						BySelectors: &SelectorConfig{
							LabelSelector: map[string]string{"app": "test"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - manifest.ref with discovery byName",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					Discovery: &DiscoveryConfig{
						Namespace: "*",
						ByName:    "my-resource",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid - inline manifest with discovery",
			resources: []Resource{
				{
					Name: "test",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
					},
					Discovery: &DiscoveryConfig{
						Namespace: "test-ns",
						ByName:    "my-configmap",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - inline manifest missing discovery",
			resources: []Resource{
				{
					Name: "test",
					Manifest: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
					},
					// Missing discovery - required for all resources
				},
			},
			wantErr: true,
			errMsg:  "discovery is required",
		},
		{
			name: "invalid - manifest.ref missing discovery",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					// Missing discovery
				},
			},
			wantErr: true,
			errMsg:  "discovery is required",
		},
		{
			name: "valid - manifest.ref with discovery missing namespace (all namespaces)",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					Discovery: &DiscoveryConfig{
						// Empty namespace means all namespaces
						ByName: "my-resource",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - manifest.ref missing byName or bySelectors",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					Discovery: &DiscoveryConfig{
						Namespace: "test-ns",
						// Missing byName and bySelectors
					},
				},
			},
			wantErr: true,
			errMsg:  "spec.resources[0].discovery: must have either 'byName' or 'bySelectors' set",
		},
		{
			name: "invalid - bySelectors without labelSelector defined",
			resources: []Resource{
				{
					Name:     "test",
					Manifest: map[string]interface{}{"ref": "templates/test.yaml"},
					Discovery: &DiscoveryConfig{
						Namespace:   "test-ns",
						BySelectors: &SelectorConfig{
							// Empty selectors
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "spec.resources[0].discovery.bySelectors.labelSelector is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := configWithResources(tt.resources)
			errs := ValidateStruct(config)
			if tt.wantErr {
				require.True(t, errs != nil && errs.HasErrors(), "expected error but got none")
				assert.Contains(t, errs.Error(), tt.errMsg)
			} else {
				assert.True(t, errs == nil || !errs.HasErrors(), "unexpected error: %v", errs)
			}
		})
	}
}

func TestConditionValuesAlias(t *testing.T) {
	// Test that both "value" and "values" YAML keys are supported
	tests := []struct {
		name     string
		yaml     string
		expected interface{}
	}{
		{
			name: "value with single item",
			yaml: `
field: status
operator: equals
value: "Ready"
`,
			expected: "Ready",
		},
		{
			name: "value with list",
			yaml: `
field: status
operator: in
value:
  - "Ready"
  - "Running"
`,
			expected: []interface{}{"Ready", "Running"},
		},
		{
			name: "values with list (alias)",
			yaml: `
field: status
operator: in
values:
  - "Ready"
  - "Running"
`,
			expected: []interface{}{"Ready", "Running"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cond Condition
			err := yaml.Unmarshal([]byte(tt.yaml), &cond)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cond.Value)
		})
	}
}

// TestConditionValueAndValuesError verifies that specifying both value and values is an error
func TestConditionValueAndValuesError(t *testing.T) {
	yamlContent := `
field: status
operator: in
value: "ignored"
values:
  - "Used"
`
	var cond Condition
	err := yaml.Unmarshal([]byte(yamlContent), &cond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "condition has both 'value' and 'values' keys")
}

// =============================================================================
// Transport Config Tests
// =============================================================================

func TestTransportConfigYAMLParsing(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string
		wantError      bool
		wantClient     string
		wantTarget     string
		wantMaestroNil bool
	}{
		{
			name: "resource with kubernetes transport",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testResource"
      transport:
        client: "kubernetes"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        byName: "test-ns"
`,
			wantError:      false,
			wantClient:     "kubernetes",
			wantMaestroNil: true,
		},
		{
			name: "resource with maestro transport",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testResource"
      transport:
        client: "maestro"
        maestro:
          targetCluster: "cluster1"
          manifestWork:
            apiVersion: work.open-cluster-management.io/v1
            kind: ManifestWork
            metadata:
              name: "test-mw"
      discovery:
        byName: "test-mw"
`,
			wantError:      false,
			wantClient:     "maestro",
			wantTarget:     "cluster1",
			wantMaestroNil: false,
		},
		{
			name: "resource with maestro transport and manifestWork ref",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testResource"
      transport:
        client: "maestro"
        maestro:
          targetCluster: "{{ .clusterName }}"
          manifestWork:
            ref: "/path/to/manifestwork.yaml"
      discovery:
        byName: "test-mw"
`,
			wantError:      false,
			wantClient:     "maestro",
			wantTarget:     "{{ .clusterName }}",
			wantMaestroNil: false,
		},
		{
			name: "resource without transport (defaults to kubernetes)",
			yaml: `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testResource"
      manifest:
        apiVersion: v1
        kind: Namespace
        metadata:
          name: "test-ns"
      discovery:
        byName: "test-ns"
`,
			wantError:      false,
			wantClient:     "kubernetes",
			wantMaestroNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config AdapterTaskConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &config)

			if tt.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, config.Spec.Resources, 1)

			resource := config.Spec.Resources[0]
			assert.Equal(t, tt.wantClient, resource.GetTransportClient())

			if tt.wantMaestroNil {
				if resource.Transport != nil {
					assert.Nil(t, resource.Transport.Maestro)
				}
			} else {
				require.NotNil(t, resource.Transport)
				require.NotNil(t, resource.Transport.Maestro)
				assert.Equal(t, tt.wantTarget, resource.Transport.Maestro.TargetCluster)
			}
		})
	}
}

func TestGetTransportClient(t *testing.T) {
	tests := []struct {
		name     string
		resource Resource
		want     string
	}{
		{
			name:     "nil transport defaults to kubernetes",
			resource: Resource{Name: "test"},
			want:     TransportClientKubernetes,
		},
		{
			name:     "empty client defaults to kubernetes",
			resource: Resource{Name: "test", Transport: &TransportConfig{Client: ""}},
			want:     TransportClientKubernetes,
		},
		{
			name:     "explicit kubernetes",
			resource: Resource{Name: "test", Transport: &TransportConfig{Client: "kubernetes"}},
			want:     TransportClientKubernetes,
		},
		{
			name:     "explicit maestro",
			resource: Resource{Name: "test", Transport: &TransportConfig{Client: "maestro"}},
			want:     TransportClientMaestro,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.resource.GetTransportClient())
		})
	}
}

func TestIsMaestroTransport(t *testing.T) {
	tests := []struct {
		name     string
		resource Resource
		want     bool
	}{
		{
			name:     "nil transport is not maestro",
			resource: Resource{Name: "test"},
			want:     false,
		},
		{
			name:     "kubernetes transport is not maestro",
			resource: Resource{Name: "test", Transport: &TransportConfig{Client: "kubernetes"}},
			want:     false,
		},
		{
			name:     "maestro transport",
			resource: Resource{Name: "test", Transport: &TransportConfig{Client: "maestro"}},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.resource.IsMaestroTransport())
		})
	}
}

func TestLoadConfigWithManifestWorkRef(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a manifestWork template file
	manifestWorkFile := filepath.Join(tmpDir, "manifestwork.yaml")
	require.NoError(t, os.WriteFile(manifestWorkFile, []byte(`
apiVersion: work.open-cluster-management.io/v1
kind: ManifestWork
metadata:
  name: "test-manifestwork"
spec:
  workload:
    manifests: []
`), 0644))

	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
    kubernetes:
      apiVersion: "v1"
`

	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterName"
      source: "event.name"
  resources:
    - name: "testManifestWork"
      transport:
        client: "maestro"
        maestro:
          targetCluster: "{{ .clusterName }}"
      manifest:
        ref: "manifestwork.yaml"
      discovery:
        bySelectors:
          labelSelector:
            app: "test"
`

	adapterPath, taskPath := createTestConfigFiles(t, tmpDir, adapterYAML, taskYAML)

	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify manifest ref was loaded and replaced with ManifestWork content
	require.Len(t, config.Spec.Resources, 1)
	resource := config.Spec.Resources[0]

	mw, ok := resource.Manifest.(map[string]interface{})
	require.True(t, ok, "Manifest should be a map after loading ref")
	assert.Equal(t, "work.open-cluster-management.io/v1", mw["apiVersion"])
	assert.Equal(t, "ManifestWork", mw["kind"])

	// Verify ref is no longer present
	_, hasRef := mw["ref"]
	assert.False(t, hasRef, "ref should be replaced with actual content")
}

func TestLoadConfigWithManifestWorkRefNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
    kubernetes:
      apiVersion: "v1"
`

	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  resources:
    - name: "testManifestWork"
      transport:
        client: "maestro"
        maestro:
          targetCluster: "cluster1"
      manifest:
        ref: "nonexistent-manifestwork.yaml"
      discovery:
        bySelectors:
          labelSelector:
            app: "test"
`

	adapterPath, taskPath := createTestConfigFiles(t, tmpDir, adapterYAML, taskYAML)

	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestLoadConfigWithInlineManifestWork(t *testing.T) {
	tmpDir := t.TempDir()

	adapterYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterConfig
metadata:
  name: test-adapter
spec:
  adapter:
    version: "0.1.0"
  clients:
    hyperfleetApi:
      baseUrl: "https://test.example.com"
      timeout: 2s
    kubernetes:
      apiVersion: "v1"
`

	taskYAML := `
apiVersion: hyperfleet.redhat.com/v1alpha1
kind: AdapterTaskConfig
metadata:
  name: test-adapter
spec:
  params:
    - name: "clusterName"
      source: "event.name"
  resources:
    - name: "testManifestWork"
      transport:
        client: "maestro"
        maestro:
          targetCluster: "{{ .clusterName }}"
      manifest:
        apiVersion: work.open-cluster-management.io/v1
        kind: ManifestWork
        metadata:
          name: "inline-mw"
        spec:
          workload:
            manifests: []
      discovery:
        bySelectors:
          labelSelector:
            app: "test"
`

	adapterPath, taskPath := createTestConfigFiles(t, tmpDir, adapterYAML, taskYAML)

	config, err := LoadConfig(
		WithAdapterConfigPath(adapterPath),
		WithTaskConfigPath(taskPath),
		WithSkipSemanticValidation(),
	)
	require.NoError(t, err)
	require.NotNil(t, config)

	// Verify inline manifest (ManifestWork) is preserved as-is
	require.Len(t, config.Spec.Resources, 1)
	resource := config.Spec.Resources[0]

	mw, ok := resource.Manifest.(map[string]interface{})
	require.True(t, ok, "Manifest should be a map")
	assert.Equal(t, "work.open-cluster-management.io/v1", mw["apiVersion"])
	assert.Equal(t, "ManifestWork", mw["kind"])
}
